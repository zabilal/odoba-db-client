package mysql

import (
	"context"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/sqlscript"
)

var _ source.Writer = (*mysqlSource)(nil)

// Plan renders a changeset as the statements that would write it, each
// value bound (FR-4.4, ADR-0031).
func (s *mysqlSource) Plan(_ context.Context, cs source.Changeset) (*source.WritePlan, error) {
	return sqlscript.PlanWrites(s, s.cfg.Guard, cs, "() VALUES ()")
}

// Apply runs a plan in one transaction: all of it, or on the first failure
// none of it (FR-4.5).
func (s *mysqlSource) Apply(ctx context.Context, plan *source.WritePlan) (*source.WriteOutcome, error) {
	return sqlscript.ApplySQL(ctx, s.db, s.cfg.Guard, plan)
}

var _ source.BulkLoader = (*mysqlSource)(nil)

// LoadRows imports rows into a table, a batch a transaction, or emptying it
// first in one (FR-10.6, ADR-0050).
func (s *mysqlSource) LoadRows(ctx context.Context, target model.ObjectRef, columns []string, rows model.RowStream, opt source.LoadOptions) (int64, error) {
	return sqlscript.LoadSQL(ctx, s.db, s, s.cfg.Guard, target, columns, rows, opt)
}
