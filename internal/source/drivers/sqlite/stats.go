package sqlite

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
// table when they have narrowed it. A local file counts quickly, so there
// is nothing to estimate and nothing to sample.

var _ source.Statistician = (*sqliteSource)(nil)

func (s *sqliteSource) ColumnStats(ctx context.Context, ref model.ObjectRef, def model.ColumnDef,
	opt source.BrowseOptions) (*source.ColumnStats, error) {
	stmt, q, err := s.buildStats(ref, def, opt)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	rows, err := s.db.QueryContext(ctx, stmt.SQL, stmt.Args...)
	if err != nil {
		return nil, statementError(err)
	}
	st, err := newRowStream(rows, ref, model.RowIdentity{Kind: model.IdentityNone})
	if err != nil {
		return nil, statementError(err)
	}
	defer st.Close()

	row, err := st.Next(ctx)
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
		return source.Statement{}, source.StatsQuery{}, fmt.Errorf("sqlite: %s is not browsable", ref)
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
