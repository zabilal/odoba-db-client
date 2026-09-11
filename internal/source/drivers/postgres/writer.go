package postgres

import (
	"context"

	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/sqlscript"
)

var _ source.Writer = (*pgSource)(nil)

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
