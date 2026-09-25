package firebird

import (
	"context"
	"fmt"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/sqlscript"
)

var _ source.Writer = (*firebirdSource)(nil)

// Plan renders a changeset as the statements that would write it, each value
// bound (FR-4.4, ADR-0031).
func (s *firebirdSource) Plan(_ context.Context, cs source.Changeset) (*source.WritePlan, error) {
	row := s.defaultRow()
	if row == "" {
		for _, c := range cs.Changes {
			if c.Kind == source.ChangeInsert && len(c.Values) == 0 {
				return nil, fmt.Errorf("firebird: this server is version %s, and a row of nothing "+
					"but defaults needs Firebird 4 or later; give a value in at least one column", s.version)
			}
		}
	}
	return sqlscript.PlanWrites(s, s.cfg.Guard, cs, row)
}

// defaultRow writes a row of nothing but defaults, or nothing where this
// server has no way to ask for one.
//
// DEFAULT VALUES arrived in Firebird 4. Before that a column had to be named
// before its default could be asked for, and a table whose only column is an
// identity could not be given a row at all — so on an older server this
// refuses rather than writing something that will fail at the server with a
// syntax error nobody can act on.
func (s *firebirdSource) defaultRow() string {
	if s.major() < 4 {
		return ""
	}
	return "DEFAULT VALUES"
}

// Apply runs a plan in one transaction: all of it, or on the first failure
// none of it (FR-4.5).
func (s *firebirdSource) Apply(ctx context.Context, plan *source.WritePlan) (*source.WriteOutcome, error) {
	return sqlscript.ApplySQL(ctx, s.db, s.cfg.Guard, plan)
}

var _ source.BulkLoader = (*firebirdSource)(nil)

// LoadRows imports rows into a table, a batch a transaction, or emptying it
// first in one (FR-10.6, ADR-0050).
func (s *firebirdSource) LoadRows(ctx context.Context, target model.ObjectRef, columns []string,
	rows model.RowStream, opt source.LoadOptions) (int64, error) {
	return sqlscript.LoadSQL(ctx, s.db, s, s.cfg.Guard, target, columns, rows, opt)
}
