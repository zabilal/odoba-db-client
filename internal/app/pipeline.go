package app

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// PipelineSource feeds the grid from a pipeline a person wrote (FR-12.1),
// the way BrowseSource feeds it from an object's rows. It has the same
// Columns / Fetch / Count shape, so the grid takes one for the other and
// knows nothing about either.
//
// Nothing it produces can be written back: a pipeline's documents are
// computed, and the collection they came from may not hold them in that
// shape at all (FR-4.7).
type PipelineSource struct {
	agg      source.Aggregator
	ref      model.ObjectRef
	pipeline string
	cols     []model.ColumnDef
}

// ErrNoPipelines is a source that does not read by a pipeline.
var ErrNoPipelines = errors.New("app: this source does not read by a pipeline")

// NewPipelineSource runs a pipeline once to learn what it produces: the
// columns are the fields its documents hold, and the grid needs them before
// it asks for a row.
//
// confirmed carries the person's consent for a pipeline that writes, which
// the source's guard asks for on a production connection (FR-4.9).
func NewPipelineSource(ctx context.Context, src source.Source, ref model.ObjectRef,
	pipeline string, confirmed bool) (_ *PipelineSource, err error) {
	defer panics.Recover(&err, "running the pipeline")
	agg, ok := src.(source.Aggregator)
	if !ok || !src.Capabilities().Data.Pipeline {
		return nil, ErrNoPipelines
	}
	rs, err := agg.Aggregate(ctx, ref, pipeline, source.BrowseOptions{Limit: 1}, confirmed)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	return &PipelineSource{agg: agg, ref: ref, pipeline: pipeline, cols: rs.Columns()}, nil
}

// Pipeline is the text this source runs.
func (p *PipelineSource) Pipeline() string { return p.pipeline }

// Columns are the fields the pipeline's documents held when it was opened.
func (p *PipelineSource) Columns() []model.ColumnDef { return p.cols }

// Identity is none: a pipeline's documents are computed, so nothing here is
// written back (FR-4.7).
func (p *PipelineSource) Identity() model.RowIdentity {
	return model.RowIdentity{Kind: model.IdentityNone}
}

// Fetch runs the pipeline again for one page of it. A pipeline is not a
// cursor a page can be taken from: the stages run each time, and the page is
// a $skip and a $limit the source adds.
func (p *PipelineSource) Fetch(ctx context.Context, offset, limit int64) (_ []model.Row, err error) {
	defer panics.Recover(&err, "reading the pipeline's documents")
	rs, err := p.agg.Aggregate(ctx, p.ref, p.pipeline, source.BrowseOptions{Offset: offset, Limit: limit}, false)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	out := make([]model.Row, 0, limit)
	for {
		row, err := rs.Next(ctx)
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		if int64(len(out)) >= limit {
			return nil, fmt.Errorf("app: the pipeline returned more than the %d documents asked for", limit)
		}
		out = append(out, row)
	}
}

// Count is unknown: counting a pipeline's documents means running it, which
// is what the grid is already doing a page at a time.
func (p *PipelineSource) Count(context.Context) (int64, error) { return -1, nil }
