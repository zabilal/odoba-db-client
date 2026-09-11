package mysql

import (
	"context"
	"fmt"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Distinct lists a column's values for the grid's filter picklist (FR-3.4).
// The rows are decoded as a browse decodes them, so a picked value filters
// exactly the rows it was counted from.
func (s *mysqlSource) Distinct(ctx context.Context, ref model.ObjectRef, column string, opt source.BrowseOptions, limit int) ([]source.DistinctValue, error) {
	if !browsableKinds[ref.Kind] || len(ref.Path) < 2 {
		return nil, fmt.Errorf("mysql: %s is not browsable", ref)
	}
	stmt, err := s.buildDistinct(ref, column, opt, limit)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, stmt.SQL, stmt.Args...)
	if err != nil {
		return nil, statementError(err, ctx)
	}
	st, err := newRowStream(rows, ref, model.RowIdentity{Kind: model.IdentityNone})
	if err != nil {
		return nil, statementError(err, ctx)
	}
	st.ctx = ctx
	defer st.Close()
	return source.ReadDistinct(ctx, st)
}
