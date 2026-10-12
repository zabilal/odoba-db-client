package trino

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
	"time"

	tr "github.com/trinodb/trino-go-client/trino"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Browse opens an object's rows, ordered so that one page follows another.
//
// Trino promises no order it was not asked for: a table is read by many
// workers at once and its rows come back as they finish, so the second page of
// an unordered read is not what follows the first. And no row has an address —
// there is no key a query engine over other people's stores could vouch for.
// So every browse ends its ORDER BY with every column that has an order, which
// is as total as an order here can be, and the rows are known by nothing
// (model.IdentityNone): the grid reads and does not edit.
//
// That is a sort, and it is not free. It is also not what makes a page cost
// what it costs: an OFFSET is a read of everything before it either way, with
// no index to skip by, and what the sort adds to that is keeping the first
// offset-and-limit rows in order as they pass (ADR-0174).
func (s *trinoSource) Browse(ctx context.Context, ref model.ObjectRef, opt source.BrowseOptions) (model.RowStream, error) {
	if !browsable(ref) {
		return nil, fmt.Errorf("trino: %s is not browsable", ref)
	}
	cols, err := s.columns(ctx, ref)
	if err != nil {
		return nil, err
	}
	opt.Sorts = tiebreak(opt.Sorts, cols)
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

// tiebreak ends a sort with every column that has an order and is not in it
// already.
func tiebreak(sorts []source.Sort, cols []model.Column) []source.Sort {
	have := map[string]bool{}
	for _, s := range sorts {
		have[s.Column] = true
	}
	out := append([]source.Sort(nil), sorts...)
	for _, c := range cols {
		if !have[c.Name] && orderable(c.Type) {
			out = append(out, source.Sort{Column: c.Name})
		}
	}
	return out
}

// Distinct lists a column's values among the rows the filters select, most
// frequent first (source.DistinctLister, FR-3.4).
func (s *trinoSource) Distinct(ctx context.Context, ref model.ObjectRef, column string, opt source.BrowseOptions, limit int) ([]source.DistinctValue, error) {
	stmt, err := s.buildDistinct(ref, column, opt, limit)
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
	defer st.Close()
	st.ctx = ctx
	var out []source.DistinctValue
	for {
		row, err := st.Next(ctx)
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		n, _ := row[1].(int64)
		out = append(out, source.DistinctValue{Value: row[0], Count: n})
	}
}

// rowStream adapts database/sql rows to the row contract.
type rowStream struct {
	rows *sql.Rows
	cols []model.ColumnDef
	vals []any
	ptrs []any
	ctx  context.Context // the statement's, to tell a stop from a failure
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
		// A result names a type without its size and says the size beside
		// it: DECIMAL and 12, 2; VARCHAR and 25.
		switch dt.Class {
		case model.TypeDecimal:
			if p, sc, ok := ct.DecimalSize(); ok {
				dt.Precision, dt.Scale = int32(p), int32(sc)
			}
		case model.TypeString:
			if n, ok := ct.Length(); ok && n > 0 && n < 1<<31-1 {
				dt.Length = n
			}
		}
		if n, ok := ct.Nullable(); ok {
			dt.Nullable = n
		}
		r.cols[i] = model.ColumnDef{Name: ct.Name(), Type: dt, Origin: origin}
		r.ptrs[i] = &r.vals[i]
	}
	return r, nil
}

func (r *rowStream) Columns() []model.ColumnDef { return r.cols }

// Identity says that these rows are addressed by nothing: Trino vouches for
// no key that tells two of them apart, so an edit could not say which row it
// was for (FR-4.7).
func (r *rowStream) Identity() model.RowIdentity {
	return model.RowIdentity{Kind: model.IdentityNone}
}

func (r *rowStream) Next(ctx context.Context) (model.Row, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// A result that has been closed has no next row, and says so the way one
	// that has ended does.
	if !r.rows.Next() {
		err := r.rows.Err()
		_ = r.Close()
		if err != nil {
			// A stop arrives here too: database/sql ends a result whose
			// context has ended and says that is why.
			return nil, statementError(err, r.ctx)
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

// Close releases the result. Safe to call more than once: a result that is
// closed is closed.
func (r *rowStream) Close() error { return r.rows.Close() }

// normalize narrows a scanned value to the model's closed set.
//
// The client hands over a whole number as an int64, a fraction as a float64,
// a time as a time, and nearly everything else as the text the server wrote —
// an exact number, a UUID, an interval, a document. What the column is says
// which of those a piece of text is. An array, a map and a row arrive as
// what their JSON decoded to, numbers as their digits, and are walked.
func normalize(v any, dt model.DataType) any {
	switch x := v.(type) {
	case nil:
		return nil
	case bool, int64, float64:
		return x
	case []byte:
		return x
	case string:
		switch dt.Class {
		case model.TypeDecimal:
			return model.Decimal(x)
		case model.TypeJSON:
			if json.Valid([]byte(x)) {
				return model.JSON(x)
			}
		}
		return x
	case time.Time:
		if dt.Class == model.TypeTime {
			// A time of day is not a moment, and a time.Time for one is a
			// moment on the first of January in the year nought.
			return x.Format("15:04:05.999999999")
		}
		return x.UTC()
	case json.Number:
		return numberOf(x)
	case tr.Row:
		if !x.Valid {
			return nil
		}
		out := make(map[string]any, x.Len())
		for i := 0; i < x.Len(); i++ {
			out[x.Name(i)] = normalize(x.Value(i), model.DataType{})
		}
		return out
	case []any:
		el := model.DataType{}
		if dt.Element != nil {
			el = *dt.Element
		}
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = normalize(item, el)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, item := range x {
			out[k] = normalize(item, model.DataType{})
		}
		return out
	}
	return reflected(v, dt)
}

// numberOf is a number that arrived as its digits, as the narrowest thing
// that holds it exactly.
func numberOf(n json.Number) any {
	text := n.String()
	if !strings.ContainsAny(text, ".eE") {
		if i, err := strconv.ParseInt(text, 10, 64); err == nil {
			return i
		}
		return model.Decimal(text)
	}
	if f, err := strconv.ParseFloat(text, 64); err == nil {
		return f
	}
	return model.Decimal(text)
}

// reflected narrows what the client typed more closely than this names: a
// narrower integer, a slice or a map of something in particular. Anything
// left after that is shown as it writes itself, which is always something a
// grid can draw.
func reflected(v any, dt model.DataType) any {
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer, reflect.Interface:
		if rv.IsNil() {
			return nil
		}
		return normalize(rv.Elem().Interface(), dt)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32:
		return rv.Int()
	case reflect.Float32:
		return rv.Float()
	case reflect.Slice, reflect.Array:
		el := model.DataType{}
		if dt.Element != nil {
			el = *dt.Element
		}
		out := make([]any, rv.Len())
		for i := range out {
			out[i] = normalize(rv.Index(i).Interface(), el)
		}
		return out
	case reflect.Map:
		out := make(map[string]any, rv.Len())
		iter := rv.MapRange()
		for iter.Next() {
			out[fmt.Sprint(iter.Key().Interface())] = normalize(iter.Value().Interface(), model.DataType{})
		}
		return out
	}
	return fmt.Sprint(v)
}
