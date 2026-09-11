package sqlite

import (
	"context"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Distinct lists a column's values for the grid's filter picklist (FR-3.4).
// The rows are decoded as a browse decodes them, so a picked value filters
// exactly the rows it was counted from.
func (s *sqliteSource) Distinct(ctx context.Context, ref model.ObjectRef, column string, opt source.BrowseOptions, limit int) ([]source.DistinctValue, error) {
	stmt, err := s.buildDistinct(ref, column, opt, limit)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, stmt.SQL, stmt.Args...)
	if err != nil {
		return nil, statementError(err)
	}
	st, err := newRowStream(rows, ref, model.RowIdentity{Kind: model.IdentityNone})
	if err != nil {
		return nil, statementError(err)
	}
	defer st.Close()
	return source.ReadDistinct(ctx, st)
}
