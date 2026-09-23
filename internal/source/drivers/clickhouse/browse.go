package clickhouse

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

var browsableKinds = map[model.ObjectKind]bool{
	model.KindTable: true, model.KindView: true, model.KindMaterializedView: true,
}

// Browse opens an object's rows, ordered by the table's sorting key so that
// paging is as stable as a column store can make it.
//
// There is no key that addresses a row here: a MergeTree's ORDER BY is the
// order its parts are written in and nothing enforces that it is unique, and
// the virtual columns saying where a row sits on disk move when parts merge.
// So the rows are known by nothing (model.IdentityNone), the grid reads and
// does not edit, and two rows equal on the sorting key may change places
// between one page and the next. Ordering by every column instead would be a
// total order and a sort of the whole table, which on the tables this engine
// is for is not a page of rows (ADR-0142).
func (s *clickhouseSource) Browse(ctx context.Context, ref model.ObjectRef, opt source.BrowseOptions) (model.RowStream, error) {
	if !browsableKinds[ref.Kind] || len(ref.Path) < 2 {
		return nil, fmt.Errorf("clickhouse: %s is not browsable", ref)
	}
	key, err := s.sortKey(ctx, ref)
	if err != nil {
		return nil, err
	}
	opt.Sorts = tiebreak(opt.Sorts, key)
	stmt, err := s.BuildBrowse(ref, opt)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, stmt.SQL, stmt.Args...)
	if err != nil {
		return nil, statementError(err, ctx)
	}
	st, err := newRowStream(rows, ref)
	if err != nil {
		return nil, statementError(err, ctx)
	}
	st.ctx = ctx
	return st, nil
}

// sortKey is the columns a browse ends its ORDER BY with, read once and
// remembered: the answer does not change while a connection is open, and
// paging asks for it on every page.
func (s *clickhouseSource) sortKey(ctx context.Context, ref model.ObjectRef) ([]string, error) {
	at := ref.Path[0] + "\x00" + ref.Path[1]
	s.mu.Lock()
	key, ok := s.keys[at]
	s.mu.Unlock()
	if ok {
		return key, nil
	}
	var sorting string
	err := s.db.QueryRowContext(ctx, `SELECT sorting_key FROM system.tables
		WHERE database = ? AND name = ?`, ref.Path[0], ref.Path[1]).Scan(&sorting)
	if err != nil && err != sql.ErrNoRows {
		return nil, statementError(err, ctx)
	}
	key = plainNames(sorting)
	if len(key) == 0 {
		// No sorting key: a Memory, Log or View table, which is small by
		// what it is for. Every column is a total order, and the best there
		// is. ClickHouse compares almost everything — a map, a tuple, a
		// point and a JSON document all have an order — and the one type it
		// will not order by, an aggregate state, is one this driver cannot
		// read at all, so a table holding one is unreadable long before it
		// is unsortable.
		cols, err := s.columns(ctx, ref)
		if err != nil {
			return nil, err
		}
		for _, c := range cols {
			key = append(key, c.Name)
		}
	}
	s.mu.Lock()
	s.keys[at] = key
	s.mu.Unlock()
	return key, nil
}

// plainNames keeps the parts of a sorting key that are a column and not an
// expression over one: toYYYYMM(d) orders rows, but it is not a name this
// program may write into a statement (NFR-S6), and dropping it costs only
// the ordering it would have added.
//
// A part in backquotes is a name whatever is in it, which is what the
// quotes are for; one without them has to look like a name.
func plainNames(key string) []string {
	var out []string
	for _, part := range splitArgs(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(key), "("), ")")) {
		switch {
		case len(part) > 1 && part[0] == '`' && part[len(part)-1] == '`':
			out = append(out, strings.ReplaceAll(part[1:len(part)-1], "``", "`"))
		case part != "" && isPlainName(part):
			out = append(out, part)
		}
	}
	return out
}

// isPlainName reports whether a name is one word: what a quoted name may
// hold is anything, but a sorting key's part that is not one word is an
// expression.
func isPlainName(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '_':
		case c >= '0' && c <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

func tiebreak(sorts []source.Sort, key []string) []source.Sort {
	have := map[string]bool{}
	for _, s := range sorts {
		have[strings.ToLower(s.Column)] = true
	}
	out := append([]source.Sort(nil), sorts...)
	for _, k := range key {
		if !have[strings.ToLower(k)] {
			out = append(out, source.Sort{Column: k})
		}
	}
	return out
}

// rowStream adapts database/sql rows to the row contract.
type rowStream struct {
	rows    *sql.Rows
	cols    []model.ColumnDef
	vals    []any
	ptrs    []any
	ctx     context.Context // the statement's, to tell a stop from a failure
	onClose func()
	closed  bool
}

var (
	_ model.RowStream  = (*rowStream)(nil)
	_ model.Identified = (*rowStream)(nil)
)

func newRowStream(rows *sql.Rows, origin model.ObjectRef) (*rowStream, error) {
	cts, err := rows.ColumnTypes()
	if err != nil {
		rows.Close()
		return nil, err
	}
	r := &rowStream{rows: rows, cols: make([]model.ColumnDef, len(cts)),
		vals: make([]any, len(cts)), ptrs: make([]any, len(cts))}
	for i, ct := range cts {
		dt := dataType(ct.DatabaseTypeName())
		r.cols[i] = model.ColumnDef{Name: ct.Name(), Type: dt, Origin: origin}
		r.ptrs[i] = &r.vals[i]
	}
	return r, nil
}

func (r *rowStream) Columns() []model.ColumnDef { return r.cols }

// Identity says that these rows are addressed by nothing: ClickHouse has no
// key that tells two of them apart, so an edit could not say which row it
// was for (FR-4.7).
func (r *rowStream) Identity() model.RowIdentity {
	return model.RowIdentity{Kind: model.IdentityNone}
}

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
			return nil, statementError(err, r.ctx)
		}
		if r.ctx != nil && r.ctx.Err() != nil {
			return nil, r.ctx.Err() // cut short by a stop: not the end of the data
		}
		return nil, io.EOF
	}
	if err := r.rows.Scan(r.ptrs...); err != nil {
		return nil, statementError(err, r.ctx)
	}
	row := make(model.Row, len(r.vals))
	for i, v := range r.vals {
		row[i] = normalize(v, r.cols[i].Type)
	}
	return row, nil
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

// normalize narrows a scanned value to the model's closed set.
//
// clickhouse-go hands over the Go type nearest each column's own: a uint8
// for a UInt8, a decimal for a Decimal, a uuid for a UUID, a slice for an
// Array, a map for a Map. The narrow ones widen, the ones that carry their
// own text are read from it, and anything made of other values is walked.
func normalize(v any, dt model.DataType) any {
	switch x := v.(type) {
	case nil:
		return nil
	case bool, float64, model.Decimal, model.JSON:
		return x
	case time.Time:
		return x
	case string:
		// An exact number, a UUID and an address all arrive as their own
		// text through database/sql; what the column is says which.
		return textOf(x, dt)
	case []byte:
		// An Array(UInt8) is a []byte in Go as much as a String is, and
		// only the column says which: one is a list of numbers to walk and
		// the other is text.
		if dt.Class == model.TypeArray {
			break
		}
		return string(x)
	case int64:
		return x
	case int8:
		return int64(x)
	case int16:
		return int64(x)
	case int32:
		return int64(x)
	case uint8:
		return int64(x)
	case uint16:
		return int64(x)
	case uint32:
		return int64(x)
	case uint64:
		if x <= math.MaxInt64 {
			return int64(x)
		}
		return model.Decimal(strconv.FormatUint(x, 10)) // past what an int64 holds
	case int:
		return int64(x)
	case float32:
		return float64(x)
	}
	return compound(v, dt)
}

// stringer is what a value that carries its own text implements: a decimal,
// a UUID, an address, a big integer.
type stringer interface{ String() string }

// compound narrows what is left: a value that writes itself, and a value
// made of other values.
func compound(v any, dt model.DataType) any {
	rv := reflect.ValueOf(v)
	if k := rv.Kind(); k == reflect.Pointer || k == reflect.Interface {
		// A Nullable column arrives as a pointer to what it holds.
		if rv.IsNil() {
			return nil
		}
		return normalize(rv.Elem().Interface(), dt)
	}
	// Before the walk, not after it: an address is four bytes and a UUID is
	// sixteen, and either walked would come out as a list of numbers.
	if s, ok := written(v); ok {
		return textOf(s, dt)
	}
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		el := model.DataType{Class: model.TypeUnknown, Length: -1}
		if dt.Element != nil {
			el = *dt.Element
		}
		out := make([]any, rv.Len())
		for i := range out {
			out[i] = normalize(rv.Index(i).Interface(), el)
		}
		return out
	case reflect.Map:
		unknown := model.DataType{Class: model.TypeUnknown, Length: -1}
		out := make(map[string]any, rv.Len())
		iter := rv.MapRange()
		for iter.Next() {
			out[fmt.Sprint(iter.Key().Interface())] = normalize(iter.Value().Interface(), unknown)
		}
		return out
	}
	return fmt.Sprint(v)
}

// written is the text a value carries, where it carries one.
//
// A decimal writes itself; so do a UUID, an address and a big integer —
// except that math/big writes itself through a pointer, so a value that
// arrived as one is given an address to be asked through.
func written(v any) (string, bool) {
	if s, ok := v.(stringer); ok {
		return s.String(), true
	}
	rv := reflect.ValueOf(v)
	if !rv.IsValid() || rv.Kind() == reflect.Pointer {
		return "", false
	}
	p := reflect.New(rv.Type())
	p.Elem().Set(rv)
	if s, ok := p.Interface().(stringer); ok {
		return s.String(), true
	}
	return "", false
}

// textOf is the text a value wrote itself as, read as what its column says
// it is.
func textOf(s string, dt model.DataType) any {
	switch dt.Class {
	case model.TypeDecimal:
		return model.Decimal(scaled(s, dt.Scale))
	case model.TypeJSON:
		if json.Valid([]byte(s)) {
			return model.JSON(s)
		}
	}
	return s
}

// scaled writes an exact number with the places its column has.
//
// The driver's decimal drops the zeros at the end of one, so a column of
// money would show 1.5 where the row holds 1.50. The column says how many
// places there are, and a number shown with fewer would be a different
// number to anybody reading it.
func scaled(s string, places int32) string {
	if places <= 0 || s == "" {
		return s
	}
	whole, frac, _ := strings.Cut(s, ".")
	if len(frac) >= int(places) {
		return s
	}
	return whole + "." + frac + strings.Repeat("0", int(places)-len(frac))
}
