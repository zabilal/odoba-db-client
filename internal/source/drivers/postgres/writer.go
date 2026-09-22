package postgres

import (
	"context"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/sqlscript"
)

var (
	_ source.Writer     = (*pgSource)(nil)
	_ source.BulkLoader = (*pgSource)(nil)
)

// Plan renders a changeset as the statements that would write it, each
// value bound (FR-4.4, ADR-0031).
func (s *pgSource) Plan(_ context.Context, cs source.Changeset) (*source.WritePlan, error) {
	return sqlscript.PlanWrites(s, s.cfg.Guard, cs, "DEFAULT VALUES")
}

// Apply runs a plan in one transaction on its target's database: all of it,
// or on the first failure none of it (FR-4.5).
func (s *pgSource) Apply(ctx context.Context, plan *source.WritePlan) (*source.WriteOutcome, error) {
	if err := sqlscript.AllowWrites(s.cfg.Guard, plan); err != nil {
		return nil, err
	}
	pool, err := s.poolFor(ctx, plan.Target)
	if err != nil {
		return nil, err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return sqlscript.ApplyWith(plan, func(st source.Statement) (int64, error) {
		tag, err := tx.Exec(ctx, st.SQL, st.Args...)
		return tag.RowsAffected(), err
	}, func() error { return tx.Commit(ctx) }, func() error { return tx.Rollback(ctx) }), nil
}

// LoadRows imports rows into a table on its database's pool, a batch a
// transaction, or emptying it first in one (FR-10.6, ADR-0050).
func (s *pgSource) LoadRows(ctx context.Context, target model.ObjectRef, columns []string, rows model.RowStream, opt source.LoadOptions) (int64, error) {
	// Asked before anything is opened. LoadWith asks again, and that is the
	// check that counts; this one is here so that a connection nobody may
	// write to does not have a pool opened for a load that cannot happen —
	// every other write path in this driver refuses before it dials.
	if err := s.cfg.Guard.Allow(source.AccessWrite, opt.Confirmed); err != nil {
		return 0, err
	}
	pool, err := s.poolFor(ctx, target)
	if err != nil {
		return 0, err
	}
	return sqlscript.LoadWith(ctx, s, s.cfg.Guard, target, columns, rows, opt, func() (sqlscript.Tx, error) {
		tx, err := pool.Begin(ctx)
		if err != nil {
			return sqlscript.Tx{}, err
		}
		return sqlscript.Tx{
			Exec: func(st source.Statement) (int64, error) {
				tag, err := tx.Exec(ctx, st.SQL, st.Args...)
				return tag.RowsAffected(), err
			},
			Commit:   func() error { return tx.Commit(ctx) },
			Rollback: func() error { return tx.Rollback(ctx) },
		}, nil
	})
}

// UpsertClause writes a row whose key is taken over the row there, as ON
// CONFLICT … DO UPDATE (ADR-0052).
func (d dialect) UpsertClause(keys, cols []string) string { return sqlscript.OnConflict(d, keys, cols) }
