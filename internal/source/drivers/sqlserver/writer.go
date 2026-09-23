package sqlserver

import (
	"context"

	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/sqlscript"
)

var _ source.Writer = (*sqlServerSource)(nil)

// Plan renders a changeset as the statements that would write it, each
// value bound (FR-4.4, ADR-0031).
//
// A row of nothing but defaults is "INSERT INTO t DEFAULT VALUES": T-SQL has
// no empty column list, and an INSERT naming no column would be a syntax
// error rather than a row.
func (s *sqlServerSource) Plan(_ context.Context, cs source.Changeset) (*source.WritePlan, error) {
	return sqlscript.PlanWrites(s, s.cfg.Guard, cs, "DEFAULT VALUES")
}

// Apply runs a plan in one transaction, on the database its target names:
// all of it, or on the first failure none of it (FR-4.5).
func (s *sqlServerSource) Apply(ctx context.Context, plan *source.WritePlan) (*source.WriteOutcome, error) {
	db, err := s.pool(ctx, plan.Target)
	if err != nil {
		return nil, err
	}
	return sqlscript.ApplySQL(ctx, db, s.cfg.Guard, plan)
}
