package sqlite

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
// that paging is deterministic: LIMIT/OFFSET over an incompletely ordered
// result can repeat or skip rows between pages.
func (s *sqliteSource) Browse(ctx context.Context, ref model.ObjectRef, opt source.BrowseOptions) (model.RowStream, error) {
	if !browsableKinds[ref.Kind] {
		return nil, fmt.Errorf("sqlite: %s is not browsable", ref)
	}
	id := model.RowIdentity{Kind: model.IdentityNone}
	if ref.Kind == model.KindTable {
		var err error
		if id, err = s.identity(ctx, ref); err != nil {
			return nil, statementError(err)
		}
		opt.Sorts = tiebreak(opt.Sorts, id.Columns)
		if id.Kind == model.IdentityRowID && len(opt.Columns) == 0 {
			// The rowid is none of the table's columns, so it is selected
			// first: a row with no other key is written by it.
			cols, err := s.columns(ctx, ref.Name())
			if err != nil {
				return nil, statementError(err)
			}
			opt.Columns = []string{"rowid"}
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
		return nil, statementError(err)
	}
	return newRowStream(rows, ref, id)
}

// identity is how a table's rows are told apart: its primary key when it
// has one, as a WITHOUT ROWID table always does, or else its rowid, which
// Browse then selects (FR-4.7, ADR-0034).
func (s *sqliteSource) identity(ctx context.Context, ref model.ObjectRef) (model.RowIdentity, error) {
	key := ref.Name()
	s.mu.Lock()
	id, ok := s.identities[key]
	s.mu.Unlock()
	if ok {
		return id, nil
	}
	pk, err := s.primaryKey(ctx, ref.Name())
	if err != nil {
		return id, err
	}
	id = model.RowIdentity{Kind: model.IdentityRowID, Columns: []string{"rowid"}, Target: ref}
	if len(pk) > 0 {
		id = model.RowIdentity{Kind: model.IdentityPrimaryKey, Columns: pk, Target: ref}
	}
	s.mu.Lock()
	s.identities[key] = id
	s.mu.Unlock()
	return id, nil
}

func (s *sqliteSource) primaryKey(ctx context.Context, table string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name FROM pragma_table_info(?) WHERE pk > 0 ORDER BY pk`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var pk []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		pk = append(pk, n)
	}
	return pk, rows.Err()
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
		r.cols[i] = model.ColumnDef{Name: ct.Name(), Type: dataType(ct.DatabaseTypeName()), Origin: origin}
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
			return nil, statementError(err)
		}
		return nil, io.EOF
	}
	if err := r.rows.Scan(r.ptrs...); err != nil {
		return nil, statementError(err)
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
func normalize(v any, dt model.DataType) any {
	switch x := v.(type) {
	case []byte:
		b := append([]byte(nil), x...) // database/sql may reuse the buffer
		if dt.Class == model.TypeJSON && json.Valid(b) {
			return model.JSON(b)
		}
		if dt.Class == model.TypeString {
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
	case int32:
		return int64(x)
	case float32:
		return float64(x)
	}
	return v
}

// dataType maps a declared column type through SQLite's own affinity rules
// (sqlite.org/datatype3.html §3.1), then names what a person means by the
// NUMERIC-affinity ones: a DATE, a BOOLEAN, JSON. A column declared with no
// type holds whatever it is given.
func dataType(decl string) model.DataType {
	u := strings.ToUpper(strings.TrimSpace(decl))
	dt := model.DataType{Native: strings.ToLower(strings.TrimSpace(decl)), Length: -1, Nullable: true}
	switch {
	case u == "":
		dt.Class, dt.Native = model.TypeUnknown, "any"
	case strings.Contains(u, "INT"):
		dt.Class = model.TypeInteger
	case strings.Contains(u, "CHAR") || strings.Contains(u, "CLOB") || strings.Contains(u, "TEXT"):
		dt.Class = model.TypeString
	case strings.Contains(u, "BLOB"):
		dt.Class = model.TypeBytes
	case strings.Contains(u, "REAL") || strings.Contains(u, "FLOA") || strings.Contains(u, "DOUB"):
		dt.Class = model.TypeFloat
	case strings.HasPrefix(u, "BOOL"):
		dt.Class = model.TypeBool
	case u == "DATE":
		dt.Class = model.TypeDate
	case strings.Contains(u, "DATETIME") || strings.Contains(u, "TIMESTAMP"):
		dt.Class = model.TypeTimestamp
	case u == "TIME":
		dt.Class = model.TypeTime
	case strings.Contains(u, "JSON"):
		dt.Class = model.TypeJSON
	default:
		dt.Class = model.TypeDecimal // NUMERIC affinity
	}
	return dt
}
