package firebird

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

var browsableKinds = map[model.ObjectKind]bool{model.KindTable: true, model.KindView: true}

// Browse opens an object's rows. Tables are ordered by a unique key last, so
// that paging is deterministic: a window over an incompletely ordered result
// can repeat or skip rows between pages.
func (s *firebirdSource) Browse(ctx context.Context, ref model.ObjectRef, opt source.BrowseOptions) (model.RowStream, error) {
	if !browsableKinds[ref.Kind] {
		return nil, fmt.Errorf("firebird: %s is not browsable", ref)
	}
	id := model.RowIdentity{Kind: model.IdentityNone}
	if ref.Kind == model.KindTable {
		var err error
		if id, err = s.identity(ctx, ref); err != nil {
			return nil, err
		}
		opt.Sorts = tiebreak(opt.Sorts, id.Columns)
	}
	stmt, err := s.BuildBrowse(ref, opt)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, stmt.SQL, stmt.Args...)
	if err != nil {
		return nil, statementError(err, ctx, stmt.SQL)
	}
	stream, err := newRowStream(rows, ref, id)
	if err != nil {
		return nil, statementError(err, ctx, stmt.SQL)
	}
	// What a value is comes from the object rather than from the wire. The
	// library reports the wire type — LONG, INT64, BLOB — and several of
	// Firebird's types share one: a NUMERIC(12,2) and a BIGINT are both
	// INT64, and a binary blob and a text blob are both BLOB. The object
	// declared which, and a browse knows the object.
	if decl, err := s.columns(ctx, ref.Name()); err == nil {
		applyDeclared(stream.cols, decl)
	}
	return stream, nil
}

// applyDeclared gives each column the type the object declares for it. What
// the wire says is kept for anything the object does not declare — a computed
// column of a view, an expression — because that is all there is for those.
func applyDeclared(cols []model.ColumnDef, decl []model.Column) {
	by := make(map[string]model.DataType, len(decl))
	for _, d := range decl {
		by[d.Name] = d.Type
	}
	for i, c := range cols {
		if t, ok := by[c.Name]; ok {
			cols[i].Type = t
		}
	}
}

// identity is how a table's rows are told apart: its primary key, or else a
// unique constraint over columns that cannot be empty (FR-4.7).
//
// Firebird has RDB$DB_KEY, an eight-byte address of the row, and it is
// deliberately not used. It is only stable within one transaction: a row
// updated gets a new one, and a browse and the edit that follows it are two
// transactions. A key that silently stops addressing the row it was read for
// is worse than saying the rows cannot be told apart, which is what the grid
// says when there is no identity and is the truth here.
func (s *firebirdSource) identity(ctx context.Context, ref model.ObjectRef) (model.RowIdentity, error) {
	key := ref.Name()
	s.mu.Lock()
	id, ok := s.identities[key]
	s.mu.Unlock()
	if ok {
		return id, nil
	}
	pk, uniq, err := s.keys(ctx, key)
	if err != nil {
		return model.RowIdentity{}, err
	}
	id = model.RowIdentity{Kind: model.IdentityNone, Target: ref}
	switch {
	case pk != nil && len(pk.Columns) > 0:
		id = model.RowIdentity{Kind: model.IdentityPrimaryKey, Columns: pk.Columns, Target: ref}
	case len(uniq) > 0:
		cols, err := s.columns(ctx, key)
		if err != nil {
			return model.RowIdentity{}, err
		}
		notNull := map[string]bool{}
		for _, c := range cols {
			notNull[c.Name] = !c.Type.Nullable
		}
		for _, u := range uniq {
			// A unique constraint over a column that may be empty does not
			// address a row: Firebird allows any number of rows whose key is
			// NULL, so two of them would be the same row to an update.
			if allTrue(notNull, u.Columns) {
				id = model.RowIdentity{Kind: model.IdentityUniqueIndex, Columns: u.Columns, Target: ref}
				break
			}
		}
	}
	s.mu.Lock()
	s.identities[key] = id
	s.mu.Unlock()
	return id, nil
}

func allTrue(m map[string]bool, keys []string) bool {
	for _, k := range keys {
		if !m[k] {
			return false
		}
	}
	return len(keys) > 0
}

// tiebreak appends the unique key to the sort, unless already sorted by it.
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

// Count is the number of rows a browse would return. Firebird has no estimate
// to give instead, so this is a scan, and the capability says as much.
func (s *firebirdSource) Count(ctx context.Context, ref model.ObjectRef, opt source.BrowseOptions) (int64, error) {
	stmt, err := s.buildCount(ref, opt)
	if err != nil {
		return 0, err
	}
	var n int64
	if err := s.db.QueryRowContext(ctx, stmt.SQL, stmt.Args...).Scan(&n); err != nil {
		return 0, statementError(err, ctx, stmt.SQL)
	}
	return n, nil
}

// Distinct lists a column's values for the filter picklist (FR-3.4).
func (s *firebirdSource) Distinct(ctx context.Context, ref model.ObjectRef, column string,
	opt source.BrowseOptions, limit int) ([]source.DistinctValue, error) {
	stmt, err := s.buildDistinct(ref, column, opt, limit)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, stmt.SQL, stmt.Args...)
	if err != nil {
		return nil, statementError(err, ctx, stmt.SQL)
	}
	st, err := newRowStream(rows, ref, model.RowIdentity{Kind: model.IdentityNone})
	if err != nil {
		return nil, statementError(err, ctx, stmt.SQL)
	}
	defer st.Close()
	return source.ReadDistinct(ctx, st)
}

// rowStream adapts database/sql rows to the row contract. One goroutine reads
// it.
type rowStream struct {
	rows    *sql.Rows
	cols    []model.ColumnDef
	vals    []any
	ptrs    []any
	id      model.RowIdentity
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
		r.cols[i] = model.ColumnDef{Name: ct.Name(), Type: wireType(ct.DatabaseTypeName()), Origin: origin}
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
			return nil, statementError(err, ctx, "")
		}
		return nil, io.EOF
	}
	if err := r.rows.Scan(r.ptrs...); err != nil {
		return nil, statementError(err, ctx, "")
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

// normalize narrows a scanned value to the model's set (model.Row).
//
// The library hands back a string for every exact decimal, which is right and
// is kept: a NUMERIC(18,4) does not fit a float64 without losing the last of
// it, and a number shown wrong in its last place is worse than one shown as
// the text the server sent.
func normalize(v any, dt model.DataType) any {
	switch x := v.(type) {
	case []byte:
		b := append([]byte(nil), x...) // database/sql may reuse the buffer
		switch {
		case dt.Class == model.TypeJSON && json.Valid(b):
			return model.JSON(b)
		case dt.Class == model.TypeString:
			return string(b)
		}
		return b
	case string:
		if dt.Class == model.TypeJSON && json.Valid([]byte(x)) {
			return model.JSON(x)
		}
		return x
	case int:
		return int64(x)
	case int16:
		return int64(x)
	case int32:
		return int64(x)
	case float32:
		return float64(x)
	}
	return v
}
