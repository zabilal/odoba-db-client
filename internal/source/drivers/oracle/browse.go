package oracle

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

var browsableKinds = map[model.ObjectKind]bool{
	model.KindTable: true, model.KindView: true, model.KindMaterializedView: true,
}

// identity is how a table's rows are addressed and ordered.
type identity struct {
	row  model.RowIdentity
	sort []string // what paging ends its ORDER BY with
}

// Browse opens an object's rows, addressed by the table's key where it has
// one and by ROWID where it has not.
//
// ROWID says where a row is, and every table has one: it is the row's
// address until the row is moved, and a grid holds it only for as long as
// it holds the row it read (ADR-0144).
func (s *oracleSource) Browse(ctx context.Context, ref model.ObjectRef, opt source.BrowseOptions) (model.RowStream, error) {
	if !browsableKinds[ref.Kind] || len(ref.Path) < 2 {
		return nil, fmt.Errorf("oracle: %s is not browsable", ref)
	}
	id := identity{row: model.RowIdentity{Kind: model.IdentityNone}}
	if ref.Kind == model.KindTable {
		var err error
		if id, err = s.identity(ctx, ref); err != nil {
			return nil, err
		}
		opt.Sorts = tiebreak(opt.Sorts, id.sort)
		if id.row.Kind == model.IdentityRowID && len(opt.Columns) == 0 {
			// A row's address is none of its columns, so it is selected
			// first: a row with no key of its own is written by it.
			cols, err := s.columns(ctx, ref)
			if err != nil {
				return nil, err
			}
			opt.Columns = []string{rowIDName}
			for _, c := range cols {
				opt.Columns = append(opt.Columns, c.Name)
			}
		}
	}
	stmt, err := s.BuildBrowse(ref, opt)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, stmt.SQL, stmt.Args...)
	if err != nil {
		return nil, statementError(err, ctx)
	}
	st, err := newRowStream(rows, ref, id.row)
	if err != nil {
		return nil, statementError(err, ctx)
	}
	st.ctx = ctx
	return st, nil
}

// identity finds how a table's rows are addressed, once: the answer does
// not change while a connection is open, and paging asks on every page.
func (s *oracleSource) identity(ctx context.Context, ref model.ObjectRef) (identity, error) {
	key := ref.Path[0] + "\x00" + ref.Path[1]
	s.mu.Lock()
	id, ok := s.identities[key]
	s.mu.Unlock()
	if ok {
		return id, nil
	}
	pk, err := s.keyColumns(ctx, ref)
	if err != nil {
		return id, err
	}
	if len(pk) > 0 {
		id = identity{row: model.RowIdentity{Kind: model.IdentityPrimaryKey, Columns: pk, Target: ref}, sort: pk}
	} else {
		// Every table has a ROWID, so every table's rows can be told apart.
		id = identity{row: model.RowIdentity{Kind: model.IdentityRowID, Columns: []string{rowIDName}, Target: ref},
			sort: []string{rowIDName}}
	}
	s.mu.Lock()
	s.identities[key] = id
	s.mu.Unlock()
	return id, nil
}

// keyColumns is the table's primary key, in the order it was declared.
func (s *oracleSource) keyColumns(ctx context.Context, ref model.ObjectRef) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT c.column_name
		FROM all_constraints k
		JOIN all_cons_columns c ON c.owner = k.owner AND c.constraint_name = k.constraint_name
		WHERE k.owner = :1 AND k.table_name = :2 AND k.constraint_type = 'P'
		ORDER BY c.position`, ref.Path[0], ref.Path[1])
	if err != nil {
		return nil, statementError(err, ctx)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
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
	id      model.RowIdentity
	ctx     context.Context // the statement's, to tell a stop from a failure
	onClose func()
	closed  bool
}

var (
	_ model.RowStream  = (*rowStream)(nil)
	_ model.Identified = (*rowStream)(nil)
)

func newRowStream(rows *sql.Rows, origin model.ObjectRef, id model.RowIdentity) (*rowStream, error) {
	cts, err := rows.ColumnTypes()
	if err != nil {
		rows.Close()
		return nil, err
	}
	r := &rowStream{rows: rows, id: id, cols: make([]model.ColumnDef, len(cts)),
		vals: make([]any, len(cts)), ptrs: make([]any, len(cts))}
	for i, ct := range cts {
		dt := wireType(ct.DatabaseTypeName())
		if p, sc, ok := ct.DecimalSize(); ok && dt.Class != model.TypeFloat {
			dt = numberType(p, sc)
		}
		if n, ok := ct.Nullable(); ok {
			dt.Nullable = n
		}
		r.cols[i] = model.ColumnDef{Name: ct.Name(), Type: dt, Origin: origin}
		r.ptrs[i] = &r.vals[i]
	}
	return r, nil
}

func (r *rowStream) Columns() []model.ColumnDef  { return r.cols }
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
// go-ora hands a NUMBER over as its own text, whatever its size, which is
// what lets a column of thirty digits arrive without being rounded to a
// float on the way.
func normalize(v any, dt model.DataType) any {
	switch x := v.(type) {
	case nil:
		return nil
	case bool, float64, model.Decimal, model.JSON:
		return x
	case string:
		return textOf(x, dt)
	case []byte:
		b := append([]byte(nil), x...) // the driver reuses its buffer
		if dt.Class == model.TypeJSON && json.Valid(b) {
			return model.JSON(b)
		}
		return b
	case int64:
		return x
	case int32:
		return int64(x)
	case float32:
		return float64(x)
	}
	return v
}

// textOf reads a value that arrived as its own text as what its column says
// it is: a whole number that fits is a number, and one that does not keeps
// every digit it came with.
func textOf(s string, dt model.DataType) any {
	switch dt.Class {
	case model.TypeInteger:
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return n
		}
		return model.Decimal(s)
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
// Oracle gives a NUMBER back without the zeros at the end of it, so a
// column of money would show 1.5 where the row holds 1.50. The column says
// how many places there are, and a number shown with fewer would be a
// different number to anybody reading it.
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

// wireType classifies a column by the type name the protocol gives it.
//
// The wire says less than the catalogue: a VARCHAR2 and an NVARCHAR2 both
// arrive as NCHAR, and a BOOLEAN arrives as a NUMBER. What a table's
// structure says is read from the catalogue instead (see columns).
func wireType(name string) model.DataType {
	dt := model.DataType{Native: name, Length: -1, Nullable: true}
	switch name {
	case "NUMBER":
		dt.Class = model.TypeDecimal
	case "IBFloat", "IBDouble":
		dt.Class = model.TypeFloat
	case "NCHAR", "CHAR", "LongVarChar", "LONG":
		dt.Class = model.TypeString
	case "RAW", "LongRaw":
		dt.Class = model.TypeBytes
	case "DATE", "TimeStampDTY":
		dt.Class = model.TypeTimestamp
	case "TimeStampTZ_DTY", "TimeStampLTZ_DTY":
		dt.Class, dt.TimeZone = model.TypeTimestamp, true
	case "IntervalDS_DTY", "IntervalYM_DTY":
		dt.Class = model.TypeInterval
	case "ROWID":
		dt.Class = model.TypeString
	case "OCIBlobLocator":
		// What a JSON column arrives as. A BLOB comes over as LongRaw, so
		// this locator is the document type rather than binary data.
		dt.Class = model.TypeJSON
	default:
		dt.Class = model.TypeUnknown
	}
	return dt
}

// numberType says what a NUMBER of this precision and scale holds. Oracle
// reports a scale of 255 for a NUMBER declared without one, which is a
// number with a fractional part rather than a whole one.
func numberType(precision, scale int64) model.DataType {
	dt := model.DataType{Native: "NUMBER", Length: -1, Nullable: true,
		Class: model.TypeDecimal, Precision: int32(precision), Scale: int32(scale)}
	if scale == 0 && precision > 0 && precision <= 18 {
		dt.Class, dt.Scale = model.TypeInteger, 0
	}
	if scale == 255 {
		dt.Scale = 0
	}
	return dt
}
