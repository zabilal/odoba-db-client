package sqlite

import (
	"context"

	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/sqlscript"
)

var _ source.Writer = (*sqliteSource)(nil)

// Plan renders a changeset as the statements that would write it, each
// value bound (FR-4.4, ADR-0031).
func (s *sqliteSource) Plan(_ context.Context, cs source.Changeset) (*source.WritePlan, error) {
	return sqlscript.PlanWrites(s, s.cfg.Guard, cs, "DEFAULT VALUES")
}

// Apply runs a plan in one transaction: all of it, or on the first failure
// none of it (FR-4.5).
func (s *sqliteSource) Apply(ctx context.Context, plan *source.WritePlan) (*source.WriteOutcome, error) {
	return sqlscript.ApplySQL(ctx, s.db, s.cfg.Guard, plan)
}
