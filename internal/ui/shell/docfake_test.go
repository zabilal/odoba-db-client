package shell

import (
	"context"
	"errors"
	"strings"
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
	return &docSource{declared: cfg.Host == "declared"}, nil
}

// sampled counts the documents the latest inference was asked for.
var sampled atomic.Int64

type docSource struct{ declared bool }

var peopleRef = model.NewRef(model.KindCollection, "main", "people")

func (d *docSource) Capabilities() capability.Capabilities {
	return capability.Capabilities{
		Paradigm:  model.ParadigmDocument,
		Structure: capability.Structure{MultipleDatabases: true, InferredShape: !d.declared},
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
	return &model.Collection{Name: ref.Name(), DocumentsEstimate: 12,
		Indexes: []model.DocumentIndex{{Name: "_id_", Keys: []model.IndexColumn{{Name: "_id"}}}}}, nil
}

func (*docSource) Badge(context.Context, model.ObjectRef) (model.Badge, bool, error) {
	return model.Badge{}, false, nil
}

func (*docSource) Browse(context.Context, model.ObjectRef, source.BrowseOptions) (model.RowStream, error) {
	return nil, errors.New("docfake: no documents to browse")
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
	c, err := fx.conns.Create(store.SavedConnection{Name: host, Driver: "docfake", Host: host}, nil)
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
