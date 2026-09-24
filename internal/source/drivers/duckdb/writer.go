//go:build duckdb

package duckdb

import (
	"context"

	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/sqlscript"
)

// Writing rows back from the grid (FR-4.4, FR-4.5, ADR-0031).
//
// DuckDB has transactions, so a changeset is written in one and is undone
// whole if any part of it fails. What is written is addressed by the
// table's key, or by rowid where it has none: every table here has an
// address for its rows (ADR-0146).

var _ source.Writer = (*duckSource)(nil)

// Plan renders a changeset as the statements that would write it, each
// value bound.
func (s *duckSource) Plan(_ context.Context, cs source.Changeset) (*source.WritePlan, error) {
	return sqlscript.PlanWrites(s, s.cfg.Guard, cs, "DEFAULT VALUES")
}

// Apply runs a plan in one transaction: all of it, or on the first
// failure none of it (FR-4.5).
//
// What may write is decided by the shared planner, which is handed the
// guard: asking it here as well would be the same question twice, and
// then neither asking could be shown to matter.
func (s *duckSource) Apply(ctx context.Context, plan *source.WritePlan) (*source.WriteOutcome, error) {
	db, err := s.conn()
	if err != nil {
		return nil, err
	}
	return sqlscript.ApplySQL(ctx, db, s.cfg.Guard, plan)
}

// UpsertClause writes a row whose key is taken over the row there, as ON
// CONFLICT … DO UPDATE (ADR-0052).
func (d dialect) UpsertClause(keys, cols []string) string { return sqlscript.OnConflict(d, keys, cols) }
