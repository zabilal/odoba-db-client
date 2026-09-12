package shell

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/capability"
	"github.com/ikigai-db/ikigai-db/internal/store"
)

// docFake is a document store: a database of collections whose structure is
// not declared but sampled, as MongoDB's is. It is what the parts of the UI
// that must work without columns are tested against.
type docFake struct{}

func init() { source.Register(docFake{}) }

func (docFake) Describe() source.Descriptor {
	return source.Descriptor{ID: "docfake", Name: "Doc Fake", Paradigm: model.ParadigmDocument,
		Fields: []source.Field{{Key: "host", Label: "Host", Kind: source.FieldText, Required: true}}}
}

func (docFake) Open(_ context.Context, cfg source.ConnectionConfig) (source.Source, error) {
	// A host of "declared" is a document store whose structure the server
	// holds: it has collections, and nothing to sample.
	return &docSource{declared: cfg.Host == "declared",
		production: cfg.Guard.Environment == source.EnvProduction}, nil
}

// sampled counts the documents the latest inference was asked for.
var sampled atomic.Int64

type docSource struct{ declared, production bool }

var peopleRef = model.NewRef(model.KindCollection, "main", "people")

func (d *docSource) Capabilities() capability.Capabilities {
	return capability.Capabilities{
		Paradigm:  model.ParadigmDocument,
		Structure: capability.Structure{MultipleDatabases: true, InferredShape: !d.declared},
		Data:      capability.Data{Pipeline: true},
		Schema:    capability.Schema{Indexes: !d.declared},
		Objects:   map[model.ObjectKind]bool{model.KindDatabase: true, model.KindCollection: true},
	}
}

func (*docSource) Info(context.Context) (source.ServerInfo, error) {
	return source.ServerInfo{Product: "DocFake", Version: "1.0"}, nil
}
func (*docSource) Ping(context.Context) error { return nil }
func (*docSource) Close() error               { return nil }

func (*docSource) Root(context.Context) ([]model.Node, error) {
	return []model.Node{{Ref: model.NewRef(model.KindDatabase, "main"), Label: "main", HasChildren: true}}, nil
}

func (*docSource) Children(_ context.Context, ref model.ObjectRef) ([]model.Node, error) {
	if ref.Kind == model.KindDatabase {
		return []model.Node{{Ref: peopleRef, Label: "people", Browsable: true}}, nil
	}
	return nil, nil
}

func (*docSource) Describe(_ context.Context, ref model.ObjectRef) (any, error) {
	if ref.Kind != model.KindCollection {
		return nil, errors.New("docfake: nothing to describe")
	}
	docIndexes.Lock()
	defer docIndexes.Unlock()
	idx := []model.DocumentIndex{{Name: "_id_", Keys: []model.IndexColumn{{Name: "_id"}}}}
	idx = append(idx, docIndexes.made...)
	return &model.Collection{Name: ref.Name(), DocumentsEstimate: 12, Indexes: idx}, nil
}

// docIndexes are the indexes the fake has made, and the plans it ran.
var docIndexes struct {
	sync.Mutex
	made  []model.DocumentIndex
	plans []*source.WritePlan
	fail  bool
}

func indexPlans() []*source.WritePlan {
	docIndexes.Lock()
	defer docIndexes.Unlock()
	return append([]*source.WritePlan(nil), docIndexes.plans...)
}

func madeIndexes() []model.DocumentIndex {
	docIndexes.Lock()
	defer docIndexes.Unlock()
	return append([]model.DocumentIndex(nil), docIndexes.made...)
}

func forgetIndexes() {
	docIndexes.Lock()
	defer docIndexes.Unlock()
	docIndexes.made, docIndexes.plans, docIndexes.fail = nil, nil, false
}

// indexOp is what a planned index change would do.
type indexOp struct {
	idx  model.DocumentIndex
	drop string
}

func (d *docSource) PlanIndex(_ context.Context, ref model.ObjectRef, idx model.DocumentIndex,
	confirmed bool) (*source.WritePlan, error) {
	if len(idx.Keys) == 0 {
		return nil, errors.New("docfake: an index is on at least one field")
	}
	return d.plan(ref, "createIndex("+idx.Name+")", "Make an index on "+idx.Keys[0].Name,
		&indexOp{idx: idx}, confirmed), nil
}

func (d *docSource) PlanDropIndex(_ context.Context, ref model.ObjectRef, name string,
	confirmed bool) (*source.WritePlan, error) {
	return d.plan(ref, "dropIndex("+name+")", "Drop the index "+name, &indexOp{drop: name}, confirmed), nil
}

func (d *docSource) plan(ref model.ObjectRef, call, desc string, op *indexOp, confirmed bool) *source.WritePlan {
	guard := source.Guard{}
	if d.production {
		guard.Environment = source.EnvProduction
	}
	return &source.WritePlan{Target: ref, Statements: []source.Statement{{SQL: call, Op: op, Confirmed: confirmed}},
		Descriptions: []string{desc}, Guarded: guard.RequiresConfirmation(source.AccessDDL)}
}

func (d *docSource) ApplyIndex(_ context.Context, plan *source.WritePlan) (*source.WriteOutcome, error) {
	for _, st := range plan.Statements {
		if d.production && !st.Confirmed {
			return nil, source.ErrConfirmationRequired
		}
	}
	docIndexes.Lock()
	defer docIndexes.Unlock()
	docIndexes.plans = append(docIndexes.plans, plan)
	if docIndexes.fail {
		return &source.WriteOutcome{FailedAt: 0, Err: errors.New("docfake: the server said no")}, nil
	}
	for _, st := range plan.Statements {
		op, ok := st.Op.(*indexOp)
		if !ok {
			return &source.WriteOutcome{FailedAt: 0, Err: errors.New("docfake: not planned here")}, nil
		}
		if op.drop != "" {
			kept := docIndexes.made[:0]
			for _, idx := range docIndexes.made {
				if idx.Name != op.drop {
					kept = append(kept, idx)
				}
			}
			docIndexes.made = kept
			continue
		}
		docIndexes.made = append(docIndexes.made, op.idx)
	}
	return &source.WriteOutcome{Applied: len(plan.Statements), FailedAt: -1}, nil
}

func (*docSource) Badge(context.Context, model.ObjectRef) (model.Badge, bool, error) {
	return model.Badge{}, false, nil
}

// docRows are the documents the fake holds, and docCols their fields.
var (
	docCols = []model.ColumnDef{{Name: "_id"}, {Name: "name"}, {Name: "score"}}
	docRows = []model.Row{
		{"d1", "Ada", int64(42)},
		{"d2", "Grace", int64(7)},
		{"d3", "Edsger", int64(3)},
	}
)

func (*docSource) Browse(_ context.Context, ref model.ObjectRef, opt source.BrowseOptions) (model.RowStream, error) {
	if ref.Kind != model.KindCollection {
		return nil, errors.New("docfake: no documents to browse")
	}
	return &docStream{cols: docCols, rows: page(docRows, opt)}, nil
}

// page is the rows a browse's offset and limit ask for.
func page(rows []model.Row, opt source.BrowseOptions) []model.Row {
	if opt.Offset >= int64(len(rows)) {
		return nil
	}
	rows = rows[opt.Offset:]
	if opt.Limit > 0 && opt.Limit < int64(len(rows)) {
		rows = rows[:opt.Limit]
	}
	return rows
}

// ran records what the latest pipeline was, and whether it was consented to.
var ran struct {
	sync.Mutex
	pipeline  string
	confirmed bool
}

func lastPipeline() (string, bool) {
	ran.Lock()
	defer ran.Unlock()
	return ran.pipeline, ran.confirmed
}

// Aggregate answers a pipeline: "$bad" is one the server refuses, "$out" one
// that writes, and anything else produces two documents of its own shape.
func (d *docSource) Aggregate(_ context.Context, ref model.ObjectRef, pipeline string,
	opt source.BrowseOptions, confirmed bool) (model.RowStream, error) {
	ran.Lock()
	ran.pipeline, ran.confirmed = pipeline, confirmed
	ran.Unlock()
	if ref.Kind != model.KindCollection {
		return nil, errors.New("docfake: nothing to aggregate")
	}
	if strings.Contains(pipeline, "$bad") {
		return nil, errors.New("docfake: $bad is not a stage")
	}
	if strings.Contains(pipeline, "$out") && d.production && !confirmed {
		return nil, source.ErrConfirmationRequired
	}
	cols := []model.ColumnDef{{Name: "_id"}, {Name: "n"}}
	rows := []model.Row{{"Ada", int64(1)}, {"Grace", int64(2)}}
	return &docStream{cols: cols, rows: page(rows, opt)}, nil
}

// docStream is a stream over rows already in hand.
type docStream struct {
	cols []model.ColumnDef
	rows []model.Row
	at   int
}

func (s *docStream) Columns() []model.ColumnDef { return s.cols }
func (s *docStream) Close() error               { return nil }
func (s *docStream) Next(context.Context) (model.Row, error) {
	if s.at >= len(s.rows) {
		return nil, io.EOF
	}
	s.at++
	return s.rows[s.at-1], nil
}

func (*docSource) InferShape(_ context.Context, ref model.ObjectRef, n int) (*model.DocumentShape, error) {
	if ref.Kind != model.KindCollection {
		return nil, errors.New("docfake: nothing to sample")
	}
	sampled.Store(int64(n))
	return &model.DocumentShape{Sampled: int64(n), Fields: []model.InferredField{
		{Name: "_id", Presence: 1, Types: []model.ObservedType{{Type: model.DataType{Native: "objectId"}, Count: int64(n)}}},
		{Name: "nickname", Presence: 0.25, Types: []model.ObservedType{{Type: model.DataType{Native: "string"}}}},
	}}, nil
}

// openCollection opens the structure of a document store's collection.
func openCollection(t *testing.T, fx *fixture, host string) *tab {
	t.Helper()
	conn := store.SavedConnection{Name: host, Driver: "docfake", Host: host}
	if host == "prod" {
		conn.Environment = "production"
	}
	c, err := fx.conns.Create(conn, nil)
	if err != nil {
		t.Fatal(err)
	}
	tb := fx.s.OpenStructure(c.ID, model.Node{Ref: peopleRef, Label: "people", Browsable: true})
	pump(t, fx.q, func() bool { return showsAll(tb, "Indexes", "_id_") })
	return tb
}

func TestStructureSamplesACollectionsDocuments(t *testing.T) {
	fx := newFixture(t)
	sampled.Store(0)
	tb := openCollection(t, fx, "docs")

	// Nothing is sampled unasked: the structure shows what the server
	// declares, and offers to read documents for the rest.
	if showsAll(tb, "Fields") {
		t.Error("documents were sampled without being asked for")
	}
	if sampled.Load() != 0 {
		t.Errorf("%d documents read unasked", sampled.Load())
	}
	b := button(t, tb, "Sample")
	if b.Text != "Sample 200 documents" {
		t.Errorf("the button says %q, want how many it would read", b.Text)
	}

	test.Tap(b)
	pump(t, fx.q, func() bool { return showsAll(tb, "Fields", "nickname", "25% of those sampled") })
	if sampled.Load() != 200 {
		t.Errorf("%d documents read, want 200", sampled.Load())
	}
	if !showsAll(tb, "Fields seen in 200 documents sampled") {
		t.Error("the panel does not say the fields were sampled")
	}
	// Asked again, it says so rather than repeating the offer.
	if got := button(t, tb, "Sample").Text; got != "Sample again" {
		t.Errorf("the button says %q after sampling", got)
	}
}

func TestStructureOffersNoSamplingWhereTheServerDeclaresTheStructure(t *testing.T) {
	fx := newFixture(t)
	selectItems(t, fx)
	fx.s.run(cmdStructure)
	tb := fx.onlyTab(t)
	pump(t, fx.q, func() bool { return showsAll(tb, "Columns") })
	if b := sampleButton(tb); b != nil {
		t.Errorf("a table offers %q, though its columns are the server's own", b.Text)
	}
	// Nor does a collection whose server holds its structure: what is
	// offered follows the capability, not the kind of object.
	tb = openCollection(t, fx, "declared")
	if b := sampleButton(tb); b != nil {
		t.Errorf("a collection offers %q, though its source says nothing is sampled", b.Text)
	}
}

// sampleButton is the tab's sampling button, or nil.
func sampleButton(tb *tab) *widget.Button {
	for _, b := range buttons(tb.item.Content) {
		if strings.HasPrefix(b.Text, "Sample") {
			return b
		}
	}
	return nil
}

// button finds the one button whose text begins with a prefix.
func button(t *testing.T, tb *tab, prefix string) *widget.Button {
	t.Helper()
	if b := sampleButton(tb); b != nil && strings.HasPrefix(b.Text, prefix) {
		return b
	}
	t.Fatalf("no %q button in the tab", prefix)
	return nil
}
