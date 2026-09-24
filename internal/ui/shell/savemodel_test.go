package shell

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/schemafile"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer/view"
)

// Saving a model (FR-7.6). A comparison is against one, and until this
// there was no way in the window to make one.

// A model is saved where somebody said to put it, and what is written is
// a model a comparison can read back.
func TestAModelIsSavedAndReadsBack(t *testing.T) {
	fx := newFixture(t)
	c := selectItems(t, fx)
	fx.s.Explorer.Tree.Select(view.NodeID(c.ID, model.NewRef(model.KindDatabase, "main")))
	fx.s.sync()

	if err := fx.s.Commands().Run(cmdSaveModel); err != nil {
		t.Fatalf("Save as a Model: %v", err)
	}
	if len(fx.files.saves) != 1 {
		t.Fatalf("it asked %d times where to save", len(fx.files.saves))
	}
	// A model is a tree of files, so what is asked for is where its own
	// file goes: the rest is written beside it.
	if got := fx.files.saves[0].Name; got != "database.json" {
		t.Errorf("it suggests the name %q", got)
	}
	if fx.files.saves[0].Message == "" {
		t.Error("the dialog says nothing about what it is for")
	}

	dir := t.TempDir()
	fx.q.Run(func() { fx.files.answer(filepath.Join(dir, "database.json"), nil) })
	pump(t, fx.q, func() bool {
		_, err := os.Stat(filepath.Join(dir, "database.json"))
		return err == nil
	})

	// What it wrote is a model, which is to say a comparison reads it.
	db, err := schemafile.Read(dir)
	if err != nil {
		t.Fatalf("the model does not read back: %v", err)
	}
	if db.Name != "main" {
		t.Errorf("the model is of %q", db.Name)
	}
	if !slices.ContainsFunc(db.Schemas, func(s model.Schema) bool {
		return slices.ContainsFunc(s.Tables, func(tb model.Table) bool { return tb.Name == "items" })
	}) {
		t.Errorf("the model holds no items table: %+v", db.Schemas)
	}
}

// Saying no is an answer. Nothing is written, and nothing is reported as
// having gone wrong.
func TestAModelNobodyChoseAPlaceForIsNotSaved(t *testing.T) {
	fx := newFixture(t)
	c := selectItems(t, fx)
	fx.s.Explorer.Tree.Select(view.NodeID(c.ID, model.NewRef(model.KindDatabase, "main")))
	fx.s.sync()

	if err := fx.s.Commands().Run(cmdSaveModel); err != nil {
		t.Fatal(err)
	}
	fx.q.Run(func() { fx.files.answer("", nil) })
	if fx.s.errors != nil && fx.s.errors.text != "" {
		t.Errorf("cancelling was reported as a failure: %s", fx.s.errors.text)
	}
	if len(fx.s.tasks) != 0 {
		t.Errorf("cancelling started %d tasks", len(fx.s.tasks))
	}
}

// The command is offered for what a model can be made of, and for nothing
// else: it is the same question a comparison asks, because a model is
// what a comparison reads back.
func TestWhatCanBeSavedAsAModel(t *testing.T) {
	fx := newFixture(t)
	c := selectItems(t, fx)

	// A table is not a model: a model is of a database.
	if fx.s.canSaveModelSelected() {
		t.Error("a table was offered as a model")
	}
	fx.s.Explorer.Tree.Select(view.NodeID(c.ID, model.NewRef(model.KindDatabase, "main")))
	fx.s.sync()
	if !fx.s.canSaveModelSelected() {
		t.Error("a database was not offered as a model")
	}
	// And it is offered exactly where a comparison is.
	if fx.s.canSaveModelSelected() != fx.s.canCompareSelected() {
		t.Error("a model can be saved where none could be compared, or the other way about")
	}
}
