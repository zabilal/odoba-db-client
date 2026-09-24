package cockroach

import (
	"context"
	"fmt"
	"io"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// tableKey is how a table's rows are addressed.
//
// Every table in this engine has a primary key, because it makes one where
// nobody declared one: a hidden rowid, written with unique_rowid(). So
// there is no table here whose rows cannot be told apart, and no Choose a
// Key… dialog to fall back on (ADR-0145).
type tableKey struct {
	columns []string
	// hidden is true when the key is the one the engine made. It is no part
	// of the table's structure and SELECT * does not return it, so a browse
	// has to ask for it by name.
	hidden bool
}

// Browse opens a table, view or materialized view as rows.
func (s *crdbSource) Browse(ctx context.Context, ref model.ObjectRef, opt source.BrowseOptions) (model.RowStream, error) {
	if !browsableKinds[ref.Kind] {
		return nil, fmt.Errorf("cockroach: %s is not browsable", ref)
	}
	if len(ref.Path) < 3 {
		return nil, fmt.Errorf("cockroach: incomplete reference %s", ref)
	}
	p, err := s.conn()
	if err != nil {
		return nil, err
	}
	var key tableKey
	if ref.Kind == model.KindTable {
		if key, err = s.tableKey(ctx, ref); err != nil {
			return nil, err
		}
		opt.Sorts = tiebreak(opt.Sorts, key.columns)
		if key.hidden && len(opt.Columns) == 0 {
			// The engine's key comes last: the columns before it are the
			// table as its owner wrote it, and this one is the engine's
			// addition to the end of them.
			cols, err := s.columns(ctx, ref)
			if err != nil {
				return nil, err
			}
			for _, c := range cols {
				opt.Columns = append(opt.Columns, c.Name)
			}
			opt.Columns = append(opt.Columns, key.columns...)
		}
	}
	st, err := s.BuildBrowse(ref, opt)
	if err != nil {
		return nil, err
	}
	rows, err := p.Query(ctx, st.SQL, append([]any{resultFormats}, st.Args...)...)
	if err != nil {
		return nil, statementError(err, ctx)
	}
	return s.newRowStream(ctx, rows, ref.Path[0], ref, identityFor(ref, key, opt.Columns)), nil
}

// tiebreak appends the key to the sort, so that paging is deterministic.
//
// LIMIT/OFFSET over an incompletely ordered result may return the same row
// on two pages and skip another entirely, because the engine is free to
// order ties differently on each run. The grid fetches in pages, so without
// this somebody scrolling a table sorted by status sees rows repeat.
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
func identityFor(ref model.ObjectRef, key tableKey, cols []string) model.RowIdentity {
	if ref.Kind != model.KindTable || len(key.columns) == 0 {
		return model.RowIdentity{Kind: model.IdentityNone}
	}
	if len(cols) > 0 {
		in := make(map[string]bool, len(cols))
		for _, c := range cols {
			in[c] = true
		}
		for _, k := range key.columns {
			if !in[k] {
				return model.RowIdentity{Kind: model.IdentityNone}
			}
		}
	}
	return model.RowIdentity{Kind: model.IdentityPrimaryKey, Columns: key.columns, Target: ref}
}

// tableKey reads a table's key, once: the answer does not change while a
// connection is open, and paging asks on every page.
func (s *crdbSource) tableKey(ctx context.Context, ref model.ObjectRef) (tableKey, error) {
	cacheKey := ref.Path[0] + "\x00" + ref.Path[1] + "\x00" + ref.Path[2]
	if v, ok := s.keys.Load(cacheKey); ok {
		return v.(tableKey), nil
	}
	p, err := s.conn()
	if err != nil {
		return tableKey{}, err
	}
	db := ref.Path[0]
	rows, err := p.Query(ctx, `
		SELECT a.attname, a.attishidden IS TRUE
		FROM `+s.catalog(db, "pg_index")+` i
		JOIN `+s.catalog(db, "pg_class")+` c ON c.oid = i.indrelid
		JOIN `+s.catalog(db, "pg_namespace")+` n ON n.oid = c.relnamespace
		CROSS JOIN LATERAL unnest(i.indkey::int2[]) WITH ORDINALITY AS k(attnum, ord)
		JOIN `+s.catalog(db, "pg_attribute")+` a
		     ON a.attrelid = c.oid AND a.attnum = k.attnum
		WHERE n.nspname = $1 AND c.relname = $2 AND i.indisprimary
		ORDER BY k.ord`, ref.Path[1], ref.Path[2])
	if err != nil {
		return tableKey{}, statementError(err, ctx)
	}
	defer rows.Close()
	var key tableKey
	for rows.Next() {
		var name string
		var hidden bool
		if err := rows.Scan(&name, &hidden); err != nil {
			return tableKey{}, statementError(err, ctx)
		}
		key.columns = append(key.columns, name)
		if hidden {
			key.hidden = true
		}
	}
	if err := rows.Err(); err != nil {
		return tableKey{}, statementError(err, ctx)
	}
	s.keys.Store(cacheKey, key)
	return key, nil
}

// Distinct lists a column's values among the rows the filters select, most
// frequent first (FR-3.7).
func (s *crdbSource) Distinct(ctx context.Context, ref model.ObjectRef, column string, opt source.BrowseOptions, limit int) ([]source.DistinctValue, error) {
	p, err := s.conn()
	if err != nil {
		return nil, err
	}
	st, err := s.buildDistinct(ref, column, opt, limit)
	if err != nil {
		return nil, err
	}
	rows, err := p.Query(ctx, st.SQL, append([]any{resultFormats}, st.Args...)...)
	if err != nil {
		return nil, statementError(err, ctx)
	}
	defer rows.Close()

	oid := uint32(0)
	if fds := rows.FieldDescriptions(); len(fds) > 0 {
		oid = fds[0].DataTypeOID
	}
	var out []source.DistinctValue
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, statementError(err, ctx)
		}
		raw := rows.RawValues()
		v := decode(vals[0], raw[0], oid)
		count, _ := vals[1].(int64)
		out = append(out, source.DistinctValue{Value: v, Count: count})
	}
	return out, statementError(rows.Err(), ctx)
}

// rowStream adapts pgx rows to the grid's row contract. Not safe for
// concurrent use; a stream is read by one goroutine.
type rowStream struct {
	rows    pgx.Rows
	cols    []model.ColumnDef
	oids    []uint32
	id      model.RowIdentity
	onClose func()
	closed  bool
}

var (
	_ model.RowStream  = (*rowStream)(nil)
	_ model.Identified = (*rowStream)(nil)
)

func (s *crdbSource) newRowStream(ctx context.Context, rows pgx.Rows, db string, origin model.ObjectRef, id model.RowIdentity) *rowStream {
	fds := rows.FieldDescriptions()
	r := &rowStream{rows: rows, id: id,
		cols: make([]model.ColumnDef, len(fds)), oids: make([]uint32, len(fds))}
	for i, fd := range fds {
		r.oids[i] = fd.DataTypeOID
		dt := dataType(fd.DataTypeOID, fd.TypeModifier)
		if dt.Class == model.TypeUnknown {
			if info, ok := s.lookupType(ctx, db, fd.DataTypeOID); ok {
				dt.Class, dt.Native = info.class, info.native
			}
		}
		r.cols[i] = model.ColumnDef{Name: fd.Name, Type: dt, Origin: origin}
	}
	return r
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
	vals, err := r.rows.Values()
	if err != nil {
		return nil, statementError(err, ctx)
	}
	raw := r.rows.RawValues()
	for i := range vals {
		vals[i] = decode(vals[i], raw[i], r.oids[i])
	}
	return model.Row(vals), nil
}

// decode is one value as the grid holds it.
//
// An exact number and a document are taken from the text bytes rather than
// from what pgx decoded: the text is what the server holds, and decoding
// either loses something somebody can see. See resultFormats.
func decode(v any, raw []byte, oid uint32) any {
	if !rawText(oid) {
		return normalize(v)
	}
	switch {
	case raw == nil:
		return nil
	case oid == pgtype.NumericOID:
		return model.Decimal(append([]byte(nil), raw...))
	default:
		// pgx reuses this buffer on the next row, so it must be copied.
		return model.JSON(append([]byte(nil), raw...))
	}
}

// Close releases the result. Safe to call more than once.
func (r *rowStream) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	r.rows.Close()
	if r.onClose != nil {
		r.onClose()
	}
	return nil
}
