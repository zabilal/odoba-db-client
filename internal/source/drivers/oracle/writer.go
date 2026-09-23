package oracle

import (
	"context"
	"errors"

	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/sqlscript"
)

// Writing rows back from the grid (FR-4.4, FR-4.5, ADR-0031).
//
// Oracle has transactions, so a changeset is written in one and is undone
// whole if any part of it fails — unlike the two stores before it in this
// phase. What is written is addressed by the table's key, or by the ROWID
// where it has none: every row here has an address (ADR-0144).

var _ source.Writer = (*oracleSource)(nil)

// Plan renders a changeset as the statements that would write it, each
// value bound.
func (s *oracleSource) Plan(_ context.Context, cs source.Changeset) (*source.WritePlan, error) {
	for _, c := range cs.Changes {
		if c.Kind == source.ChangeInsert && len(c.Values) == 0 {
			// Oracle has no DEFAULT VALUES clause: a column has to be named
			// before a default can be asked for in it.
			return nil, errors.New("oracle: a new row needs a value in at least one column")
		}
	}
	return sqlscript.PlanWrites(s, s.cfg.Guard, cs, "")
}

// Apply runs a plan in one transaction: all of it, or on the first failure
// none of it (FR-4.5).
func (s *oracleSource) Apply(ctx context.Context, plan *source.WritePlan) (*source.WriteOutcome, error) {
	return sqlscript.ApplySQL(ctx, s.db, s.cfg.Guard, plan)
}
