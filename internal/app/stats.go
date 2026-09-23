package app

import (
	"context"
	"errors"
	"io"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// What a column holds (FR-3.14).
//
// Two questions, asked separately because they cost differently. How many
// rows, how many nulls, how many different values and the extremes are one
// statement over the whole of what the grid is showing; how the values are
// spread is a sample, because drawing a hundred thousand of them says no
// more than drawing ten thousand and takes ten times as long.

// ErrNoStats is a connection that will not measure a column.
var ErrNoStats = errNoStats{}

type errNoStats struct{}

func (errNoStats) Error() string { return "this connection cannot measure a column" }

// CanMeasure reports whether a connection will measure a column.
func CanMeasure(src source.Source) bool {
	if _, ok := src.(source.Statistician); !ok {
		return false
	}
	return src.Capabilities().Data.ColumnStats
}

// MeasureColumn asks what a column holds, over the rows opt selects.
func MeasureColumn(ctx context.Context, src source.Source, ref model.ObjectRef,
	def model.ColumnDef, opt source.BrowseOptions) (_ *source.ColumnStats, err error) {
	defer panics.Recover(&err, "measuring a column")
	s, ok := src.(source.Statistician)
	if !ok {
		return nil, ErrNoStats
	}
	// Only the filters: what is being looked at is what the figures are
	// about, and how it is sorted or paged says nothing about the whole of
	// it.
	return s.ColumnStats(ctx, ref, def, source.BrowseOptions{Filters: opt.Filters, Where: opt.Where})
}

// SampleLimit is how many values a spread is worked out from.
//
// Enough that a shape is the data's rather than the sample's, and few
// enough that asking is quick on a table nobody would wait for. A sample
// rather than the whole column because the answer is a picture, and a
// picture of ten thousand values is the picture of a hundred thousand.
const SampleLimit = 10_000

// SampleColumn reads a sample of one column's values, for drawing how they
// are spread.
//
// The first rows the source gives, which is the order it stores them in
// rather than a random draw: a random sample would cost a sort of the whole
// table, and what this is for is a shape rather than an estimate.
func SampleColumn(ctx context.Context, src source.Source, ref model.ObjectRef,
	def model.ColumnDef, opt source.BrowseOptions, limit int64) (_ []model.Row, err error) {
	defer panics.Recover(&err, "sampling a column")
	if limit <= 0 {
		limit = SampleLimit
	}
	rs, err := src.Browse(ctx, ref, source.BrowseOptions{
		Columns: []string{def.Name},
		Filters: opt.Filters,
		Where:   opt.Where,
		Limit:   limit,
	})
	if err != nil {
		return nil, err
	}
	defer rs.Close()

	out := make([]model.Row, 0, min(limit, 1024))
	for int64(len(out)) < limit {
		row, err := rs.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, nil
}

// FilledOf is what proportion of the rows hold something, from nought to
// one. A column of no rows is neither full nor empty, and answers nought.
func FilledOf(st *source.ColumnStats) float64 {
	if st == nil || st.Rows <= 0 {
		return 0
	}
	return float64(st.Rows-st.Nulls) / float64(st.Rows)
}
