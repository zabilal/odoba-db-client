package firebird

import (
	"context"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// What a column holds (FR-3.14).
//
// One statement, over the rows the grid's own filters select, so that the
// figures are about what somebody is looking at rather than about the whole
// table when they have narrowed it. Nothing is estimated and nothing sampled:
// Firebird has no statistic to read instead, so this is the measurement.

var _ source.Statistician = (*firebirdSource)(nil)

func (s *firebirdSource) ColumnStats(ctx context.Context, ref model.ObjectRef, def model.ColumnDef,
	opt source.BrowseOptions) (*source.ColumnStats, error) {
	stmt, q, err := s.buildStats(ref, def, opt)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	rows, err := s.db.QueryContext(ctx, stmt.SQL, stmt.Args...)
	if err != nil {
		return nil, statementError(err, ctx, stmt.SQL)
	}
	st, err := newRowStream(rows, ref, model.RowIdentity{Kind: model.IdentityNone})
	if err != nil {
		return nil, statementError(err, ctx, stmt.SQL)
	}
	defer st.Close()

	row, err := st.Next(ctx)
	if err != nil {
		return nil, statementError(err, ctx, stmt.SQL)
	}
	out, err := q.Read(row)
	if err != nil {
		return nil, err
	}
	out.Duration = time.Since(start)
	return out, nil
}
