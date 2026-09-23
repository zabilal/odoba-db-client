package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// What a column holds (FR-3.14).
//
// One statement, over the rows the grid's own filters select, so that the
// figures are about what somebody is looking at rather than about the whole
// table when they have narrowed it.
//
// Nothing here is estimated. PostgreSQL keeps its own statistics in
// pg_stats and they are a sample taken whenever the table was last
// analysed: a distinct count from there can be out by a factor of ten and
// is negative when it means a proportion. A figure somebody reads as "how
// many" and gets wrong is worse than the wait for the real one, so this
// counts.

var _ source.Statistician = (*pgSource)(nil)

func (s *pgSource) ColumnStats(ctx context.Context, ref model.ObjectRef, def model.ColumnDef,
	opt source.BrowseOptions) (*source.ColumnStats, error) {
	if len(ref.Path) < 3 {
		return nil, fmt.Errorf("postgres: incomplete reference %s", ref)
	}
	st, q, err := s.buildStats(ref, def, opt)
	if err != nil {
		return nil, err
	}
	p, err := s.poolFor(ctx, ref)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	rows, err := p.Query(ctx, st.SQL, append([]any{resultFormats}, st.Args...)...)
	if err != nil {
		return nil, statementError(err)
	}
	stream := s.newRowStream(ctx, rows, ref.Path[0], ref, model.RowIdentity{Kind: model.IdentityNone})
	defer stream.Close()

	row, err := stream.Next(ctx)
	if err != nil {
		return nil, statementError(err)
	}
	out, err := q.Read(row)
	if err != nil {
		return nil, err
	}
	out.Duration = time.Since(start)
	return out, nil
}

// buildStats renders the one statement that measures a column.
func (d dialect) buildStats(ref model.ObjectRef, def model.ColumnDef,
	opt source.BrowseOptions) (source.Statement, source.StatsQuery, error) {
	if !browsableKinds[ref.Kind] {
		return source.Statement{}, source.StatsQuery{}, fmt.Errorf("postgres: %s is not browsable", ref)
	}
	q := source.StatsFor(d.QuoteIdentifier(def.Name), def)
	b := &builder{d: d}
	var sb strings.Builder
	sb.WriteString("SELECT " + q.SQL() + " FROM " + d.QualifyRef(ref))
	if err := b.where(&sb, opt); err != nil {
		return source.Statement{}, source.StatsQuery{}, err
	}
	st, err := d.readsOnly(source.Statement{SQL: sb.String(), Args: b.args}, opt)
	return st, q, err
}
