//go:build duckdb

package duckdb

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"math/big"
	"strings"
	"time"

	duck "github.com/marcboeker/go-duckdb/v2"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// tableInfo is what a browse has to know about a table before it can
// write its own SELECT: what its columns are called and what they hold,
// which of them must be read as text, and how its rows are addressed.
type tableInfo struct {
	columns []model.Column

	// text names the columns a browse reads as their own text. Only
	// documents: everything else this library hands over can be put back
	// exactly as the row holds it (ADR-0146).
	text map[string]bool

	// key addresses a row. It is the table's own key where it has one,
	// and otherwise the row's address, which rowID says.
	key   []string
	rowID bool
}

// tableInfo reads a table's columns and key, once: neither changes while
// a connection is open, and paging asks on every page.
func (s *duckSource) tableInfo(ctx context.Context, ref model.ObjectRef) (*tableInfo, error) {
	if len(ref.Path) < 3 {
		return nil, fmt.Errorf("duckdb: incomplete reference %s", ref)
	}
	cacheKey := ref.Path[0] + "\x00" + ref.Path[1] + "\x00" + ref.Path[2]
	if v, ok := s.tables.Load(cacheKey); ok {
		return v.(*tableInfo), nil
	}
	db, err := s.conn()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `
		SELECT column_name, data_type, is_nullable, COALESCE(column_default, ''),
		       column_default IS NOT NULL, COALESCE(comment, ''), column_index
		FROM duckdb_columns()
		WHERE database_name = ? AND schema_name = ? AND table_name = ?
		ORDER BY column_index`, ref.Path[0], ref.Path[1], ref.Path[2])
	if err != nil {
		return nil, statementError(err, ctx)
	}
	info := &tableInfo{text: map[string]bool{}}
	for rows.Next() {
		var name, declared, def, comment string
		var nullable, hasDefault bool
		var position int
		if err := rows.Scan(&name, &declared, &nullable, &def, &hasDefault, &comment, &position); err != nil {
			rows.Close()
			return nil, statementError(err, ctx)
		}
		dt := dataType(declared)
		dt.Nullable = nullable
		if dt.Class == model.TypeJSON {
			info.text[name] = true
		}
		info.columns = append(info.columns, model.Column{
			Name: name, Type: dt, Position: position,
			Default: def, HasDefault: hasDefault, Comment: comment,
		})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, statementError(err, ctx)
	}
	if len(info.columns) == 0 {
		return nil, fmt.Errorf("duckdb: %s has no columns, or is not there", ref)
	}

	if ref.Kind == model.KindTable {
		if info.key, err = s.keyColumns(ctx, ref); err != nil {
			return nil, err
		}
		if len(info.key) == 0 {
			// Every table has an address even where it has no key: rowid
			// says where a row is. It is good while the grid holds the row
			// it read, which is as long as anything needs it (ADR-0146).
			info.key, info.rowID = []string{rowIDName}, true
		}
	}
	s.tables.Store(cacheKey, info)
	return info, nil
}

// keyColumns is the table's primary key, in the order it was declared.
func (s *duckSource) keyColumns(ctx context.Context, ref model.ObjectRef) ([]string, error) {
	db, err := s.conn()
	if err != nil {
		return nil, err
	}
	var names []any
	err = db.QueryRowContext(ctx, `
		SELECT constraint_column_names FROM duckdb_constraints()
		WHERE database_name = ? AND schema_name = ? AND table_name = ?
		  AND constraint_type = 'PRIMARY KEY'`,
		ref.Path[0], ref.Path[1], ref.Path[2]).Scan(&names)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, statementError(err, ctx)
	}
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, fmt.Sprint(n))
	}
	return out, nil
}

// BuildBrowse renders the SELECT behind a browse, reading the table's
// documents as text.
//
// The dialect is told which columns those are rather than finding out,
// because it holds no connection. Whatever asks the source for the
// statement gets the statement that ran, which is what the grid shows
// beside its filters (FR-3.6).
func (s *duckSource) BuildBrowse(ref model.ObjectRef, opt source.BrowseOptions) (source.Statement, error) {
	d := dialect{}
	if v, ok := s.tables.Load(ref.Path[0] + "\x00" + ref.Path[1] + "\x00" + ref.Path[2]); ok {
		d.text = v.(*tableInfo).text
	}
	return d.BuildBrowse(ref, opt)
}

// Browse opens a table or view as rows.
func (s *duckSource) Browse(ctx context.Context, ref model.ObjectRef, opt source.BrowseOptions) (model.RowStream, error) {
	if !browsableKinds[ref.Kind] {
		return nil, fmt.Errorf("duckdb: %s is not browsable", ref)
	}
	db, err := s.conn()
	if err != nil {
		return nil, err
	}
	info, err := s.tableInfo(ctx, ref)
	if err != nil {
		return nil, err
	}
	if ref.Kind == model.KindTable {
		opt.Sorts = tiebreak(opt.Sorts, info.key)
	}
	if len(opt.Columns) == 0 && (info.rowID || len(info.text) > 0) {
		// The columns are named rather than starred when there is
		// something to say about them: a row's address, which is none of
		// them and has to be asked for; or a document, which has to be
		// asked for as text.
		opt.Columns = names(info.columns)
		if info.rowID {
			// The address is read first: a row with no key of its own is
			// written by it.
			opt.Columns = append([]string{rowIDName}, opt.Columns...)
		}
	}
	st, err := s.BuildBrowse(ref, opt)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, st.SQL, st.Args...)
	if err != nil {
		return nil, statementError(err, ctx)
	}
	stream, err := newRowStream(rows, ref, identityFor(ref, info, opt.Columns), info.declaredTypes())
	if err != nil {
		return nil, statementError(err, ctx)
	}
	return stream, nil
}

func names(cols []model.Column) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = c.Name
	}
	return out
}

// tiebreak appends the key to the sort, so that paging is deterministic.
//
// LIMIT/OFFSET over an incompletely ordered result may return the same
// row on two pages and skip another entirely. The grid fetches in pages,
// so without this somebody scrolling a table sorted by status sees rows
// repeat.
func tiebreak(sorts []source.Sort, key []string) []source.Sort {
	if len(key) == 0 {
		return sorts
	}
	have := make(map[string]bool, len(sorts))
	for _, s := range sorts {
		have[s.Column] = true
	}
	out := append([]source.Sort(nil), sorts...)
	for _, k := range key {
		if !have[k] {
			out = append(out, source.Sort{Column: k})
		}
	}
	return out
}

// identityFor decides whether browsed rows can be addressed for editing
// (FR-4.7). Every key column has to be in the projection: a row that
// cannot be addressed cannot be safely written.
func identityFor(ref model.ObjectRef, info *tableInfo, cols []string) model.RowIdentity {
	if ref.Kind != model.KindTable || len(info.key) == 0 {
		return model.RowIdentity{Kind: model.IdentityNone}
	}
	if len(cols) > 0 {
		in := make(map[string]bool, len(cols))
		for _, c := range cols {
			in[c] = true
		}
		for _, k := range info.key {
			if !in[k] {
				return model.RowIdentity{Kind: model.IdentityNone}
			}
		}
	}
	kind := model.IdentityPrimaryKey
	if info.rowID {
		kind = model.IdentityRowID
	}
	return model.RowIdentity{Kind: kind, Columns: info.key, Target: ref}
}

// Distinct lists a column's values among the rows the filters select,
// most frequent first (FR-3.7).
func (s *duckSource) Distinct(ctx context.Context, ref model.ObjectRef, column string, opt source.BrowseOptions, limit int) ([]source.DistinctValue, error) {
	db, err := s.conn()
	if err != nil {
		return nil, err
	}
	info, err := s.tableInfo(ctx, ref)
	if err != nil {
		return nil, err
	}
	st, err := dialect{text: info.text}.buildDistinct(ref, column, opt, limit)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, st.SQL, st.Args...)
	if err != nil {
		return nil, statementError(err, ctx)
	}
	defer rows.Close()
	// The values are narrowed the way a browse narrows them, because the
	// picklist's job is to hand back something that filters the same
	// column: a UUID offered as its sixteen bytes would select nothing
	// (source.DistinctLister).
	dt := info.declaredTypes()[column]
	var out []source.DistinctValue
	for rows.Next() {
		var v any
		var count int64
		if err := rows.Scan(&v, &count); err != nil {
			return nil, statementError(err, ctx)
		}
		out = append(out, source.DistinctValue{Value: narrow(v, dt), Count: count})
	}
	return out, statementError(rows.Err(), ctx)
}

// rowStream adapts database/sql rows to the grid's row contract. Not safe
// for concurrent use; a stream is read by one goroutine.
type rowStream struct {
	rows    *sql.Rows
	cols    []model.ColumnDef
	id      model.RowIdentity
	onClose func()
	closed  bool
}

var (
	_ model.RowStream  = (*rowStream)(nil)
	_ model.Identified = (*rowStream)(nil)
)

// newRowStream reads the result's columns, preferring what the table
// declared over what the wire says.
//
// They differ for a column a browse cast: a document read as text arrives
// as VARCHAR, and a grid told that would show a document as a string and
// offer to edit it as one. What the wire says is right for a statement
// somebody typed, where there is no table to ask.
func newRowStream(rows *sql.Rows, origin model.ObjectRef, id model.RowIdentity, declared map[string]model.DataType) (*rowStream, error) {
	types, err := rows.ColumnTypes()
	if err != nil {
		rows.Close()
		return nil, err
	}
	r := &rowStream{rows: rows, id: id, cols: make([]model.ColumnDef, len(types))}
	for i, t := range types {
		dt, ok := declared[t.Name()]
		if !ok {
			dt = dataType(t.DatabaseTypeName())
			if nullable, known := t.Nullable(); known {
				dt.Nullable = nullable
			}
		}
		r.cols[i] = model.ColumnDef{Name: t.Name(), Type: dt, Origin: origin}
	}
	return r, nil
}

// declaredTypes is what a table says its columns hold, by name.
func (info *tableInfo) declaredTypes() map[string]model.DataType {
	out := make(map[string]model.DataType, len(info.columns))
	for _, c := range info.columns {
		out[c.Name] = c.Type
	}
	return out
}

func (r *rowStream) Columns() []model.ColumnDef { return r.cols }

func (r *rowStream) Identity() model.RowIdentity { return r.id }

func (r *rowStream) Next(ctx context.Context) (model.Row, error) {
	if r.closed {
		return nil, io.EOF
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !r.rows.Next() {
		err := r.rows.Err()
		r.Close()
		if err != nil {
			return nil, statementError(err, ctx)
		}
		return nil, io.EOF
	}
	vals := make([]any, len(r.cols))
	ptrs := make([]any, len(r.cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	if err := r.rows.Scan(ptrs...); err != nil {
		return nil, statementError(err, ctx)
	}
	for i := range vals {
		vals[i] = narrow(vals[i], r.cols[i].Type)
	}
	return model.Row(vals), nil
}

// Close releases the result. Safe to call more than once.
func (r *rowStream) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	err := r.rows.Close()
	if r.onClose != nil {
		r.onClose()
	}
	return err
}

// narrow is one value as the grid holds it, knowing what its column is.
//
// A document read as text arrives as a string and is a document; the same
// column read any other way arrives as a Go map, and is one.
func narrow(v any, dt model.DataType) any {
	switch dt.Class {
	case model.TypeJSON:
		if s, ok := v.(string); ok {
			return model.JSON(s)
		}
	case model.TypeDecimal:
		if d, ok := v.(duck.Decimal); ok {
			return model.Decimal(exact(d, dt.Scale))
		}
	case model.TypeUUID:
		// The library hands a UUID over as its sixteen bytes, which read
		// as a blob wherever a blob is shown. It is written the way
		// everything else writes one.
		if b, ok := v.([]byte); ok && len(b) == 16 {
			return uuidText(b)
		}
	}
	return normalize(v)
}

// uuidText writes a UUID the way everything else prints one.
func uuidText(b []byte) string {
	const hex = "0123456789abcdef"
	out := make([]byte, 0, 36)
	for i, c := range b {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			out = append(out, '-')
		}
		out = append(out, hex[c>>4], hex[c&0x0f])
	}
	return string(out)
}

// normalize turns what the library decoded into the closed set of values
// a row may hold (model.Row). Anything with no place in that set travels
// as its own text, which is what the grid shows and what a filter binds
// back.
func normalize(v any) any {
	switch x := v.(type) {
	case nil, bool, int64, float64, string, []byte, time.Time,
		model.Decimal, model.JSON:
		return v
	case int8:
		return int64(x)
	case int16:
		return int64(x)
	case int32:
		return int64(x)
	case int:
		return int64(x)
	case uint8:
		return int64(x)
	case uint16:
		return int64(x)
	case uint32:
		return int64(x)
	case uint64:
		// Bigger than an int64 can hold, so it keeps every digit as text.
		return model.Decimal(fmt.Sprintf("%d", x))
	case float32:
		return float64(x)
	case *big.Int:
		// A HUGEINT. Every digit of it, which no other number here holds.
		return model.Decimal(x.String())
	case big.Int:
		return model.Decimal(x.String())
	case duck.Decimal:
		return model.Decimal(exact(x, int32(x.Scale)))
	case duck.Interval:
		return intervalText(x)
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = normalize(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = normalize(x[i])
		}
		return out
	}
	return fmt.Sprint(v)
}

// exact writes a decimal with the places its column has.
//
// The library hands one over as an integer and a scale, so the point has
// to be put back. A column of money holding 1.50 would otherwise read as
// 150, and one holding 1.5 as 15.
func exact(d duck.Decimal, scale int32) string {
	if scale <= 0 {
		scale = int32(d.Scale)
	}
	digits := d.Value.String()
	neg := strings.HasPrefix(digits, "-")
	digits = strings.TrimPrefix(digits, "-")
	if scale == 0 {
		return sign(neg) + digits
	}
	for len(digits) <= int(scale) {
		digits = "0" + digits
	}
	cut := len(digits) - int(scale)
	return sign(neg) + digits[:cut] + "." + digits[cut:]
}

func sign(neg bool) string {
	if neg {
		return "-"
	}
	return ""
}

// intervalText writes an interval as the engine's own text form, which is
// what somebody reading the grid would have typed.
func intervalText(iv duck.Interval) string {
	secs := iv.Micros / 1e6
	micros := iv.Micros % 1e6
	s := fmt.Sprintf("%d mons %d days %02d:%02d:%02d",
		iv.Months, iv.Days, secs/3600, (secs/60)%60, secs%60)
	if micros != 0 {
		s = fmt.Sprintf("%s.%06d", s, micros)
	}
	return s
}
