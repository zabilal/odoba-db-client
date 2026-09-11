package postgres

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Browse opens a table, view or materialized view as rows.
func (s *pgSource) Browse(ctx context.Context, ref model.ObjectRef, opt source.BrowseOptions) (model.RowStream, error) {
	if !browsableKinds[ref.Kind] {
		return nil, fmt.Errorf("postgres: %s is not browsable", ref)
	}
	if len(ref.Path) < 3 {
		return nil, fmt.Errorf("postgres: incomplete reference %s", ref)
	}
	p, err := s.poolFor(ctx, ref)
	if err != nil {
		return nil, err
	}
	pk, err := s.primaryKey(ctx, p, ref)
	if err != nil {
		return nil, err
	}
	opt.Sorts = tiebreak(opt.Sorts, pk, ref.Kind)

	st, err := s.BuildBrowse(ref, opt)
	if err != nil {
		return nil, err
	}
	rows, err := p.Query(ctx, st.SQL, append([]any{resultFormats}, st.Args...)...)
	if err != nil {
		return nil, statementError(err)
	}
	return s.newRowStream(ctx, rows, ref.Path[0], ref, identityFor(ref, pk, opt.Columns)), nil
}

// tiebreak appends a unique key to the sort, so paging is deterministic.
//
// LIMIT/OFFSET over an incompletely ordered result may return the same row on
// two pages and skip another entirely, because PostgreSQL is free to order
// ties differently on each execution. The grid fetches in pages, so without
// this a user scrolling a table sorted by status sees rows repeat.
//
// Views have no key at all, so paging one stays best-effort. Tables without a
// primary key fall back to ctid, the physical row address: stable enough for
// scrolling a table nobody is rewriting.
func tiebreak(sorts []source.Sort, pk []string, kind model.ObjectKind) []source.Sort {
	keys := pk
	if len(keys) == 0 {
		if kind == model.KindView {
			return sorts
		}
		keys = []string{"ctid"}
	}
	have := make(map[string]bool, len(sorts))
	for _, s := range sorts {
		have[s.Column] = true
	}
	out := append([]source.Sort(nil), sorts...)
	for _, k := range keys {
		if !have[k] {
			out = append(out, source.Sort{Column: k})
		}
	}
	return out
}

// identityFor decides whether browsed rows can be addressed for editing
// (FR-4.7). A primary key is required, and every key column must be in the
// projection: a row that cannot be addressed cannot be safely updated.
func identityFor(ref model.ObjectRef, pk, cols []string) model.RowIdentity {
	if ref.Kind != model.KindTable || len(pk) == 0 {
		return model.RowIdentity{Kind: model.IdentityNone}
	}
	if len(cols) > 0 {
		in := make(map[string]bool, len(cols))
		for _, c := range cols {
			in[c] = true
		}
		for _, k := range pk {
			if !in[k] {
				return model.RowIdentity{Kind: model.IdentityNone}
			}
		}
	}
	return model.RowIdentity{Kind: model.IdentityPrimaryKey, Columns: pk, Target: ref}
}

func (s *pgSource) primaryKey(ctx context.Context, p *pgxpool.Pool, ref model.ObjectRef) ([]string, error) {
	if ref.Kind != model.KindTable {
		return nil, nil // views and matviews cannot have one
	}
	key := ref.Path[0] + "\x00" + ref.Path[1] + "\x00" + ref.Path[2]
	if v, ok := s.pkCache.Load(key); ok {
		return v.([]string), nil
	}
	rows, err := p.Query(ctx, `
		SELECT a.attname::text FROM pg_index i
		JOIN pg_class c ON c.oid = i.indrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		CROSS JOIN LATERAL unnest(i.indkey::int2[]) WITH ORDINALITY AS k(attnum, ord)
		JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum = k.attnum
		WHERE n.nspname = $1 AND c.relname = $2 AND i.indisprimary
		ORDER BY k.ord`, ref.Path[1], ref.Path[2])
	if err != nil {
		return nil, err
	}
	pk, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	if pk == nil {
		pk = []string{}
	}
	s.pkCache.Store(key, pk)
	return pk, nil
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

func (s *pgSource) newRowStream(ctx context.Context, rows pgx.Rows, db string, origin model.ObjectRef, id model.RowIdentity) *rowStream {
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

// lookupType names a type the OID table does not know — an enum, composite or
// extension type — so a column reads "order_status" rather than "oid 16394".
// It runs on a separate pooled connection, with a short timeout, because the
// result being described is still open on this one; if it fails, the column
// keeps its OID label rather than delaying the rows.
func (s *pgSource) lookupType(ctx context.Context, db string, oid uint32) (typeInfo, bool) {
	key := db + "\x00" + strconv.FormatUint(uint64(oid), 10)
	if v, ok := s.typeNames.Load(key); ok {
		return v.(typeInfo), true
	}
	p, err := s.pool(ctx, db)
	if err != nil {
		return typeInfo{}, false
	}
	lctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	var name, typtype string
	if err := p.QueryRow(lctx, `SELECT format_type(oid, NULL), typtype::text FROM pg_type WHERE oid = $1`,
		oid).Scan(&name, &typtype); err != nil {
		return typeInfo{}, false
	}
	info := typeInfo{class: classOf(name, typtype), native: name}
	s.typeNames.Store(key, info)
	return info, true
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
			return nil, statementError(err)
		}
		return nil, io.EOF
	}
	vals, err := r.rows.Values()
	if err != nil {
		return nil, statementError(err)
	}
	raw := r.rows.RawValues()
	for i := range vals {
		if r.cols[i].Type.Class == model.TypeGeometry && raw[i] != nil {
			if g, err := pgGeometry(raw[i]); err == nil {
				vals[i] = g
				continue
			}
		}
		if !rawText(r.oids[i]) {
			vals[i] = normalize(vals[i])
			continue
		}
		// Taken from the text bytes, not the decoded value; see resultFormats.
		switch {
		case raw[i] == nil:
			vals[i] = nil
		case r.oids[i] == pgtype.NumericOID:
			vals[i] = model.Decimal(raw[i])
		default:
			// pgx reuses this buffer on the next row, so it must be copied.
			vals[i] = model.JSON(append([]byte(nil), raw[i]...))
		}
	}
	return model.Row(vals), nil
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
