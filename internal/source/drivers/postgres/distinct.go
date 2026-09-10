package postgres

import (
	"context"
	"fmt"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Distinct lists a column's values for the grid's filter picklist (FR-3.4).
// The rows are decoded as a browse decodes them, so a picked value filters
// exactly the rows it was counted from.
func (s *pgSource) Distinct(ctx context.Context, ref model.ObjectRef, column string, filters []source.Filter, limit int) ([]source.DistinctValue, error) {
	if len(ref.Path) < 3 {
		return nil, fmt.Errorf("postgres: incomplete reference %s", ref)
	}
	st, err := s.buildDistinct(ref, column, filters, limit)
	if err != nil {
		return nil, err
	}
	p, err := s.poolFor(ctx, ref)
	if err != nil {
		return nil, err
	}
	rows, err := p.Query(ctx, st.SQL, append([]any{resultFormats}, st.Args...)...)
	if err != nil {
		return nil, statementError(err)
	}
	stream := s.newRowStream(ctx, rows, ref.Path[0], ref, model.RowIdentity{Kind: model.IdentityNone})
	defer stream.Close()
	return source.ReadDistinct(ctx, stream)
}
