package mongo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// The aggregation pipeline (FR-12.1, T2.35).
//
// A pipeline is a person's own text: stages the server runs in order, each
// making the documents the next one sees. It is not a filter and not a sort —
// it says its own matching and its own order — so a browse's filters and
// sorts are refused rather than quietly added to it.

// maxPipelineRows bounds what one run holds in memory. The columns are the
// fields the documents produced hold, so they are read before the first row
// is given out, and a pipeline can produce more documents than memory has
// (NFR-P11).
const maxPipelineRows = 5000

// writeStages are the stages that write. A pipeline holding one changes the
// database, and the guard is asked before it runs (FR-4.9, NFR-S4).
var writeStages = map[string]bool{"$out": true, "$merge": true}

// Aggregate runs a pipeline over a collection and streams what it produces.
func (s *mongoSource) Aggregate(ctx context.Context, ref model.ObjectRef, pipeline string,
	opt source.BrowseOptions, confirmed bool) (_ model.RowStream, err error) {
	defer panics.Recover(&err, "running a pipeline")
	if ref.Kind != model.KindCollection || len(ref.Path) < 2 {
		return nil, fmt.Errorf("mongodb: %s holds no documents", ref)
	}
	if len(opt.Filters) > 0 || len(opt.Sorts) > 0 || strings.TrimSpace(opt.Where) != "" {
		return nil, errors.New("mongodb: a pipeline says its own matching and its own order; add a stage rather than a filter")
	}
	stages, err := parsePipeline(pipeline)
	if err != nil {
		return nil, err
	}
	writing := writes(stages)
	if writing {
		// $out and $merge write a collection. Nothing here runs on a
		// read-only connection, and a production connection asks first.
		if err := s.cfg.Guard.Allow(source.AccessWrite, confirmed); err != nil {
			return nil, err
		}
	}
	if !writing {
		// A writing pipeline ends in the stage that writes — the server
		// insists on it — and produces no documents to page through.
		limit := opt.Limit
		if limit <= 0 || limit > maxPipelineRows {
			limit = maxPipelineRows
		}
		if opt.Offset > 0 {
			stages = append(stages, bson.D{{Key: "$skip", Value: opt.Offset}})
		}
		stages = append(stages, bson.D{{Key: "$limit", Value: limit}})
	}

	cur, err := s.collection(ref).Aggregate(ctx, stages)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)

	var docs []bson.Raw
	for cur.Next(ctx) {
		docs = append(docs, append(bson.Raw(nil), cur.Current...))
	}
	if err := cur.Err(); err != nil {
		return nil, err
	}
	return &produced{cols: pipelineColumns(docs), docs: docs}, nil
}

// parsePipeline reads the text a person wrote: an array of stages, or one
// stage on its own, in the extended JSON MongoDB writes and reads.
func parsePipeline(text string) ([]bson.D, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil, errors.New("mongodb: the pipeline is empty")
	}
	if strings.HasPrefix(trimmed, "{") {
		// One stage, written without its brackets: a common way to start.
		var stage bson.D
		if err := bson.UnmarshalExtJSON([]byte(trimmed), false, &stage); err != nil {
			return nil, fmt.Errorf("mongodb: this stage is not a document: %w", err)
		}
		return []bson.D{stage}, nil
	}
	if !strings.HasPrefix(trimmed, "[") {
		return nil, errors.New("mongodb: a pipeline is an array of stages, [{…}, {…}]")
	}
	// Extended JSON parses a document, not an array, so the array is read as
	// one document's field.
	var wrapper struct {
		Stages []bson.D `bson:"p"`
	}
	if err := bson.UnmarshalExtJSON([]byte(`{"p":`+trimmed+`}`), false, &wrapper); err != nil {
		return nil, fmt.Errorf("mongodb: this pipeline is not an array of documents: %w", err)
	}
	if len(wrapper.Stages) == 0 {
		return nil, errors.New("mongodb: the pipeline has no stages")
	}
	for i, st := range wrapper.Stages {
		if len(st) == 0 {
			return nil, fmt.Errorf("mongodb: stage %d is empty", i+1)
		}
	}
	return wrapper.Stages, nil
}

// writes reports whether a pipeline changes the database.
func writes(stages []bson.D) bool {
	for _, st := range stages {
		for _, e := range st {
			if writeStages[e.Key] {
				return true
			}
		}
	}
	return false
}

// pipelineColumns are the fields the documents produced hold, in the order
// the first document to hold each gave it: a pipeline's output has no
// declared shape, and its own order is the one a person wrote.
func pipelineColumns(docs []bson.Raw) []model.ColumnDef {
	var order []string
	seen := map[string]bool{}
	for _, doc := range docs {
		elems, _ := doc.Elements()
		for _, e := range elems {
			if !seen[e.Key()] {
				seen[e.Key()] = true
				order = append(order, e.Key())
			}
		}
	}
	if len(order) == 0 {
		return []model.ColumnDef{{Name: "_id", Type: model.DataType{Class: model.TypeUnknown, Nullable: true}}}
	}
	// _id leads where a stage kept it, as it does everywhere else.
	sort.SliceStable(order, func(i, j int) bool {
		return order[i] == "_id" && order[j] != "_id"
	})
	out := make([]model.ColumnDef, 0, len(order))
	for _, name := range order {
		out = append(out, model.ColumnDef{Name: name, Type: model.DataType{Class: model.TypeUnknown, Nullable: true}})
	}
	return out
}

// produced is a stream over what a pipeline made: documents already read, so
// that the columns could be known before the first row was given out.
//
// It has no identity: a pipeline's documents are computed, and writing one
// back would write to a collection it may not have come from (FR-4.7).
type produced struct {
	cols []model.ColumnDef
	docs []bson.Raw
	at   int
}

var _ model.RowStream = (*produced)(nil)

func (p *produced) Columns() []model.ColumnDef { return p.cols }

func (p *produced) Next(ctx context.Context) (model.Row, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.at >= len(p.docs) {
		return nil, io.EOF
	}
	doc := p.docs[p.at]
	p.at++
	return rowOf(doc, p.cols), nil
}

func (p *produced) Close() error { return nil }
