package app

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/diff"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/capability"
)

// Comparing two live databases (FR-7.1).

// snapper reads a whole database in one pass, the way a driver that can
// does.
type snapper struct {
	source.Source
	db    *model.Database
	err   error
	calls int
}

func (*snapper) Capabilities() capability.Capabilities {
	return capability.Capabilities{Paradigm: model.ParadigmRelational}
}

func (s *snapper) Snapshot(context.Context, string) (*model.Database, error) {
	s.calls++
	return s.db, s.err
}

// A driver that can read a database whole is asked to, once, and nothing is
// walked.
func TestAConnectionThatCanBeReadWholeIsReadWhole(t *testing.T) {
	src := &snapper{db: &model.Database{Name: "sales"}}
	got, err := Snapshot(context.Background(), src, "sales")
	if err != nil || got.Name != "sales" {
		t.Fatalf("it read %+v, %v", got, err)
	}
	if src.calls != 1 {
		t.Errorf("it asked %d times", src.calls)
	}
	if !CanCompare(src) {
		t.Error("a source that can be read whole says it cannot be compared")
	}
}

func TestASnapshotThatFails(t *testing.T) {
	src := &snapper{err: errors.New("the server hung up")}
	if _, err := Snapshot(context.Background(), src, "sales"); err == nil ||
		!strings.Contains(err.Error(), "hung up") {
		t.Errorf("it said %v", err)
	}
}

// walker is a driver with no Snapshot: it has a tree and it can describe one
// object at a time, which is every driver except PostgreSQL today.
type walker struct {
	source.Source
	schemas  []string // empty where the database is the schema
	describe int
	fails    string
}

func (*walker) Capabilities() capability.Capabilities {
	return capability.Capabilities{Paradigm: model.ParadigmRelational}
}

func (w *walker) Children(_ context.Context, ref model.ObjectRef) ([]model.Node, error) {
	switch {
	case ref.Kind == model.KindDatabase && len(w.schemas) > 0:
		var out []model.Node
		for _, s := range w.schemas {
			out = append(out, model.Node{Ref: model.NewRef(model.KindSchema, ref.Name(), s), Label: s})
		}
		return out, nil
	case ref.Kind == model.KindDatabase, ref.Kind == model.KindSchema:
		// The classes, the way every driver lists them.
		var out []model.Node
		for _, k := range []model.ObjectKind{
			model.KindTable, model.KindView, model.KindSequence, model.KindTrigger, model.KindIndex,
		} {
			out = append(out, model.ClassNode(ref, k, 1))
		}
		return out, nil
	}
	kind, ok := model.ClassOf(ref)
	if !ok {
		return nil, nil
	}
	name := map[model.ObjectKind]string{
		model.KindTable: "people", model.KindView: "recent", model.KindSequence: "people_id_seq",
		model.KindTrigger: "audit", model.KindIndex: "people_name_ix",
	}[kind]
	return []model.Node{{Ref: model.NewRef(kind, "db", "public", name), Label: name}}, nil
}

func (w *walker) Describe(_ context.Context, ref model.ObjectRef) (any, error) {
	w.describe++
	if ref.Name() == w.fails {
		return nil, errors.New("that one fell over")
	}
	switch ref.Kind {
	case model.KindTable:
		return &model.Table{Name: ref.Name(), RowsEstimate: -1,
			Columns:  []model.Column{{Name: "id", Position: 1, Type: model.DataType{Native: "integer", Length: -1}}},
			Triggers: []model.Trigger{{Name: "audit", Definition: "CREATE TRIGGER audit"}}}, nil
	case model.KindView:
		return &model.View{Name: ref.Name(), Definition: "SELECT 1"}, nil
	case model.KindSequence:
		return &model.Sequence{Name: ref.Name(), Start: 1, Increment: 1}, nil
	}
	return nil, errors.New("nothing of that kind")
}

// A driver with no Snapshot is walked, and fills the same model.
func TestAConnectionWithNoSnapshotIsWalked(t *testing.T) {
	src := &walker{schemas: []string{"public"}}
	got, err := Snapshot(context.Background(), src, "sales")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Schemas) != 1 || got.Schemas[0].Name != "public" {
		t.Fatalf("it read %+v", got.Schemas)
	}
	s := got.Schemas[0]
	if len(s.Tables) != 1 || len(s.Views) != 1 || len(s.Sequences) != 1 {
		t.Errorf("it read %d tables, %d views, %d sequences", len(s.Tables), len(s.Views), len(s.Sequences))
	}
	if !CanCompare(src) {
		t.Error("a source with a tree says it cannot be compared")
	}
}

// A trigger arrives with the table it is on and an index with it too, so
// listing them again from their own folders would double them.
func TestWhatArrivesWithATableIsNotReadTwice(t *testing.T) {
	src := &walker{schemas: []string{"public"}}
	got, err := Snapshot(context.Background(), src, "sales")
	if err != nil {
		t.Fatal(err)
	}
	if n := len(got.Schemas[0].Tables[0].Triggers); n != 1 {
		t.Errorf("the table holds %d triggers", n)
	}
	// Three objects were described: the table, the view and the sequence.
	// The trigger and the index were not, because they came with the table.
	if src.describe != 3 {
		t.Errorf("it described %d objects, want 3", src.describe)
	}
}

// Not every engine has a schema between the database and its tables. Where
// there is none the database is the schema.
func TestADatabaseThatIsItsOwnSchema(t *testing.T) {
	got, err := Snapshot(context.Background(), &walker{}, "sales")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Schemas) != 1 || got.Schemas[0].Name != "sales" {
		t.Fatalf("it read %+v", got.Schemas)
	}
	if len(got.Schemas[0].Tables) != 1 {
		t.Errorf("it read %d tables", len(got.Schemas[0].Tables))
	}
}

// An object that will not describe fails the read rather than being left
// out, because a schema quietly missing a table would compare as a table
// somebody had dropped.
func TestAnObjectThatWillNotDescribeFailsTheRead(t *testing.T) {
	_, err := Snapshot(context.Background(), &walker{schemas: []string{"public"}, fails: "people"}, "sales")
	if err == nil || !strings.Contains(err.Error(), "fell over") {
		t.Errorf("it said %v", err)
	}
}

// Comparing reads both sides and hands them to the engine the right way
// round: added is what a sync script would create on the first.
func TestComparingTwoConnections(t *testing.T) {
	from := &snapper{db: &model.Database{Name: "prod", Schemas: []model.Schema{{Name: "public"}}}}
	to := &snapper{db: &model.Database{Name: "dev", Schemas: []model.Schema{{Name: "public",
		Tables: []model.Table{{Name: "people", RowsEstimate: -1}}}}}}
	got, err := Compare(context.Background(), from, "prod", to, "dev")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Differs() {
		t.Fatal("two different schemas compared as the same")
	}
	var table diff.Node
	for _, s := range got.Tree.Children {
		for _, c := range s.Children {
			if c.Name == "people" {
				table = c
			}
		}
	}
	if table.Status != diff.Added {
		t.Errorf("the table only in the target compared as %s", table.Status)
	}
	if from.calls != 1 || to.calls != 1 {
		t.Errorf("it read the two sides %d and %d times", from.calls, to.calls)
	}
}

// A side that cannot be read says which side, because "could not read the
// database" on a comparison of two is half an answer.
func TestAComparisonSaysWhichSideCouldNotBeRead(t *testing.T) {
	ok := &snapper{db: &model.Database{Name: "dev"}}
	bad := &snapper{err: errors.New("the server hung up")}
	if _, err := Compare(context.Background(), bad, "prod", ok, "dev"); err == nil ||
		!strings.Contains(err.Error(), "reading prod") {
		t.Errorf("it said %v", err)
	}
	if _, err := Compare(context.Background(), ok, "dev", bad, "prod"); err == nil ||
		!strings.Contains(err.Error(), "reading prod") {
		t.Errorf("it said %v", err)
	}
}

// A connection with no schema to speak of is not compared at all.
type schemaless struct{ source.Source }

func (*schemaless) Capabilities() capability.Capabilities {
	return capability.Capabilities{Paradigm: model.ParadigmKeyValue}
}

func TestAConnectionWithNoSchemaIsNotCompared(t *testing.T) {
	if CanCompare(&schemaless{}) {
		t.Error("a key-value store says it can be compared")
	}
	if CanCompare(nil) {
		t.Error("nothing at all says it can be compared")
	}
}

// A driver that falls over reading a schema is contained (ADR-0017).
type snapPanicker struct{ snapper }

func (*snapPanicker) Snapshot(context.Context, string) (*model.Database, error) {
	panic("fakesql: reading the schema fell over")
}

func TestADriverThatFallsOverReadingASchemaIsContained(t *testing.T) {
	_, err := Snapshot(context.Background(), &snapPanicker{}, "sales")
	if err == nil || !strings.Contains(err.Error(), "reading a schema") {
		t.Errorf("it said %v", err)
	}
}

// Comparing a live database against a model saved to disk (FR-7.1).

func TestSavingAModelAndComparingAgainstIt(t *testing.T) {
	db := &model.Database{Name: "sales", Schemas: []model.Schema{{Name: "public",
		Tables: []model.Table{{Name: "people", RowsEstimate: -1,
			Columns: []model.Column{{Name: "id", Position: 1,
				Type: model.DataType{Native: "integer", Length: -1}}}}}}}}
	src := &snapper{db: db}
	dir := filepath.Join(t.TempDir(), "model")
	if err := SaveModel(context.Background(), src, "sales", dir); err != nil {
		t.Fatal(err)
	}

	// The same database against what was saved from it: nothing differs.
	got, err := CompareWithSaved(context.Background(), src, "sales", dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Differs() {
		t.Errorf("a database compared against a model of itself as %s", got.Tree.Status)
	}

	// A database missing what the model has: the saved model is the wanted
	// state, so what it has and the database has not is Added.
	src.db = &model.Database{Name: "sales", Schemas: []model.Schema{{Name: "public"}}}
	got, err = CompareWithSaved(context.Background(), src, "sales", dir)
	if err != nil {
		t.Fatal(err)
	}
	var table diff.Node
	for _, s := range got.Tree.Children {
		for _, c := range s.Children {
			if c.Name == "people" {
				table = c
			}
		}
	}
	if table.Status != diff.Added {
		t.Errorf("a table the database is missing compared as %s", table.Status)
	}
}

func TestReadingAModelThatIsNotThere(t *testing.T) {
	if _, err := ReadModel(t.TempDir()); err == nil {
		t.Error("an empty directory read as a model")
	}
	src := &snapper{db: &model.Database{Name: "sales"}}
	if _, err := CompareWithSaved(context.Background(), src, "sales", t.TempDir()); err == nil {
		t.Error("it compared against a model that is not there")
	}
}

// A database that cannot be read says so before the model is even opened.
func TestComparingWithASavedModelWhenTheDatabaseWillNotRead(t *testing.T) {
	src := &snapper{err: errors.New("the server hung up")}
	_, err := CompareWithSaved(context.Background(), src, "sales", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "reading sales") {
		t.Errorf("it said %v", err)
	}
}
