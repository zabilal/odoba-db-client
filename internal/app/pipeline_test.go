package app

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/capability"
)

// aggregating is a source read by a pipeline: it records what it was asked
// and answers with rows of its own.
type aggregating struct {
	fakeSource
	opts    []source.BrowseOptions
	rows    []model.Row
	tooMany bool
	noClaim bool
}

func (a *aggregating) Capabilities() capability.Capabilities {
	return capability.Capabilities{Paradigm: model.ParadigmDocument,
		Data: capability.Data{Pipeline: !a.noClaim}}
}

func (a *aggregating) Aggregate(_ context.Context, _ model.ObjectRef, _ string,
	opt source.BrowseOptions, _ bool) (model.RowStream, error) {
	a.opts = append(a.opts, opt)
	rows := a.rows
	if opt.Offset < int64(len(rows)) {
		rows = rows[opt.Offset:]
	} else {
		rows = nil
	}
	if !a.tooMany && opt.Limit > 0 && opt.Limit < int64(len(rows)) {
		rows = rows[:opt.Limit]
	}
	return &rowsOver{cols: []model.ColumnDef{{Name: "_id"}, {Name: "n"}}, rows: rows}, nil
}

type rowsOver struct {
	cols []model.ColumnDef
	rows []model.Row
	at   int
}

func (r *rowsOver) Columns() []model.ColumnDef { return r.cols }
func (r *rowsOver) Close() error               { return nil }
func (r *rowsOver) Next(context.Context) (model.Row, error) {
	if r.at >= len(r.rows) {
		return nil, io.EOF
	}
	r.at++
	return r.rows[r.at-1], nil
}

func TestAPipelineSourceReadsAPageAtATime(t *testing.T) {
	ctx := context.Background()
	ref := model.NewRef(model.KindCollection, "shop", "people")
	src := &aggregating{rows: []model.Row{{"Ada", int64(1)}, {"Grace", int64(2)}, {"Edsger", int64(3)}}}
	p, err := NewPipelineSource(ctx, src, ref, `[{"$group": {"_id": "$name"}}]`, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Columns(); len(got) != 2 || got[1].Name != "n" {
		t.Errorf("columns %v, want what the pipeline produced", got)
	}
	if p.Pipeline() != `[{"$group": {"_id": "$name"}}]` {
		t.Errorf("it runs %q", p.Pipeline())
	}
	rows, err := p.Fetch(ctx, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0][0] != "Grace" {
		t.Errorf("the page is %v, want the documents after the first", rows)
	}
	if last := src.opts[len(src.opts)-1]; last.Offset != 1 || last.Limit != 2 {
		t.Errorf("the page asked for was %+v", last)
	}
	// Nothing of a pipeline's making is written back: its documents are
	// computed, and the collection may not hold them in that shape.
	if p.Identity().Editable() {
		t.Error("a pipeline's rows are editable")
	}
	// Counting them means running it, which the grid is already doing.
	if n, err := p.Count(ctx); n != -1 || err != nil {
		t.Errorf("counted %d (%v), want it unknown", n, err)
	}
	// A source that ignores the limit does not fill memory quietly.
	src.tooMany = true
	if _, err := p.Fetch(ctx, 0, 1); err == nil {
		t.Error("a source that returned more than it was asked for was taken at its word")
	}
	// A source that does not claim pipelines is not asked for one.
	if _, err := NewPipelineSource(ctx, &aggregating{noClaim: true}, ref, "[]", false); !errors.Is(err, ErrNoPipelines) {
		t.Errorf("a source that reads no pipeline: %v", err)
	}
}
