package mysql

import (
	"context"
	"slices"
	"strings"

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

// UpsertClause writes a row whose key is taken over the row there, as ON
// DUPLICATE KEY UPDATE from VALUES(), which MariaDB reads too (ADR-0052). It
// fires on any of the table's unique keys, not only the one named. A row of
// its key alone is left as it is.
func (d dialect) UpsertClause(keys, cols []string) string {
	var sets []string
	for _, c := range cols {
		if !slices.Contains(keys, c) {
			q := d.QuoteIdentifier(c)
			sets = append(sets, q+" = VALUES("+q+")")
		}
	}
	if len(sets) == 0 {
		q := d.QuoteIdentifier(keys[0])
		sets = []string{q + " = " + q}
	}
	return " ON DUPLICATE KEY UPDATE " + strings.Join(sets, ", ")
}
