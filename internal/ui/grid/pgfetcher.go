//go:build spike

package grid

// Spike-only Postgres fetcher for W1 (T0.39–T0.42).
//
// Behind a build tag: this is NOT the PostgreSQL driver. That is T1.32 and
// implements the full source contract. This exists solely to prove the grid's
// windowed fetch works against a real server holding ten million rows, and it
// is deliberately minimal so it cannot be mistaken for the real thing.

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// PgFetcher reads pages from a Postgres table.
type PgFetcher struct {
	pool  *pgxpool.Pool
	table string
	cols  []model.ColumnDef

	// LastFetch records the most recent round-trip, for the spike's HUD.
	LastFetch time.Duration
}

var _ Fetcher = (*PgFetcher)(nil)

// NewPgFetcher connects and introspects the table's columns.
func NewPgFetcher(ctx context.Context, dsn, table string) (*PgFetcher, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping: %w", err)
	}

	f := &PgFetcher{pool: pool, table: table}
	if err := f.loadColumns(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return f, nil
}

func (f *PgFetcher) Close() { f.pool.Close() }

func (f *PgFetcher) loadColumns(ctx context.Context) error {
	// LIMIT 0 gets the result descriptor without reading any data.
	rows, err := f.pool.Query(ctx, "SELECT * FROM "+pgx.Identifier{f.table}.Sanitize()+" LIMIT 0")
	if err != nil {
		return fmt.Errorf("describe: %w", err)
	}
	defer rows.Close()

	for _, fd := range rows.FieldDescriptions() {
		f.cols = append(f.cols, model.ColumnDef{
			Name: fd.Name,
			Type: pgTypeToModel(fd.DataTypeOID),
		})
	}
	return rows.Err()
}

func (f *PgFetcher) Columns() []model.ColumnDef { return f.cols }

func (f *PgFetcher) Count(ctx context.Context) (int64, error) {
	var n int64
	err := f.pool.QueryRow(ctx,
		"SELECT count(*) FROM "+pgx.Identifier{f.table}.Sanitize()).Scan(&n)
	return n, err
}

// Fetch reads one page, ordered by primary key so that paging is stable. An
// unordered LIMIT/OFFSET can return the same row twice across pages, which
// shows up as duplicated rows while scrolling.
func (f *PgFetcher) Fetch(ctx context.Context, offset, limit int64) ([]model.Row, error) {
	start := time.Now()
	sql := fmt.Sprintf("SELECT * FROM %s ORDER BY id LIMIT $1 OFFSET $2",
		pgx.Identifier{f.table}.Sanitize())

	rows, err := f.pool.Query(ctx, sql, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]model.Row, 0, limit)
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, err
		}
		out = append(out, model.Row(normalize(vals)))
	}
	f.LastFetch = time.Since(start)
	return out, rows.Err()
}

// normalize narrows pgx's dynamic types to the closed set model.Row permits.
func normalize(vals []any) []any {
	for i, v := range vals {
		switch x := v.(type) {
		case int32:
			vals[i] = int64(x)
		case int16:
			vals[i] = int64(x)
		case [16]byte: // uuid
			vals[i] = fmt.Sprintf("%x-%x-%x-%x-%x", x[0:4], x[4:6], x[6:8], x[8:10], x[10:16])
		case map[string]any:
			// jsonb arrives decoded; re-encoding keeps the grid's structured
			// path identical to what a real driver will hand it.
			vals[i] = x
		}
	}
	return vals
}

// pgTypeToModel is a minimal OID mapping, enough for the spike table.
func pgTypeToModel(oid uint32) model.DataType {
	t := func(c model.TypeClass, native string) model.DataType {
		return model.DataType{Class: c, Native: native, Nullable: true, Length: -1}
	}
	switch oid {
	case 20:
		return t(model.TypeInteger, "int8")
	case 23:
		return t(model.TypeInteger, "int4")
	case 21:
		return t(model.TypeInteger, "int2")
	case 700, 701:
		return t(model.TypeFloat, "float8")
	case 1700:
		return t(model.TypeDecimal, "numeric")
	case 16:
		return t(model.TypeBool, "bool")
	case 1114:
		return t(model.TypeTimestamp, "timestamp")
	case 1184:
		return t(model.TypeTimestamp, "timestamptz")
	case 1082:
		return t(model.TypeDate, "date")
	case 114, 3802:
		return t(model.TypeJSON, "jsonb")
	case 2950:
		return t(model.TypeUUID, "uuid")
	case 17:
		return t(model.TypeBytes, "bytea")
	default:
		return t(model.TypeString, "text")
	}
}
