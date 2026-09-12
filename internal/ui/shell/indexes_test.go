package shell

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

func TestIndexKeysAreReadAsTheyAreWritten(t *testing.T) {
	got, err := parseIndexKeys("name, score:-1, body:text, when:1, other:desc")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 {
		t.Fatalf("%d fields, want five", len(got))
	}
	if got[0].Name != "name" || got[0].Descending || got[0].Expression != "" {
		t.Errorf("a name on its own is %+v, want it ascending", got[0])
	}
	if !got[1].Descending || got[1].Name != "score" {
		t.Errorf("name:-1 is %+v, want it descending", got[1])
	}
	if got[2].Expression != "text" {
		t.Errorf("name:text is %+v, want a kind of index", got[2])
	}
	if got[3].Descending {
		t.Errorf("name:1 is %+v, want it ascending", got[3])
	}
	if !got[4].Descending {
		t.Errorf("name:desc is %+v, want it descending", got[4])
	}
	// Spaces and a trailing comma are a person typing, not an error.
	if got, err := parseIndexKeys("  a ,  b:-1 , "); err != nil || len(got) != 2 {
		t.Errorf("read %v (%v)", got, err)
	}
}

func TestIndexKeysThatSayNothingAreRefused(t *testing.T) {
	for _, text := range []string{"", "   ", ",,", ":1", "a:", " : "} {
		if got, err := parseIndexKeys(text); err == nil {
			t.Errorf("%q was read as %v", text, got)
		}
	}
}

func TestAnIndexIsWhatWasTypedIntoTheForm(t *testing.T) {
	idx, err := indexFrom("name, score:-1", "name_score", "3600", true, true)
	if err != nil {
		t.Fatal(err)
	}
	if idx.Name != "name_score" || !idx.Unique || !idx.Sparse || idx.TTL != 3600 {
		t.Errorf("the index is %+v", idx)
	}
	if len(idx.Keys) != 2 || !idx.Keys[1].Descending {
		t.Errorf("its fields are %+v", idx.Keys)
	}
	// What was left out is left out.
	idx, err = indexFrom("name", "  ", "  ", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if idx.Name != "" || idx.TTL != 0 || idx.Unique || idx.Sparse {
		t.Errorf("the index is %+v, want nothing claimed that was not typed", idx)
	}
	// A time that is not one is refused, and says so.
	if _, err := indexFrom("name", "", "soon", false, false); err == nil {
		t.Error("a number of seconds that is not a number was taken")
	}
	if _, err := indexFrom("name", "", "-5", false, false); err == nil {
		t.Error("a negative number of seconds was taken")
	}
}

func TestTheIndexesThatCanBeDropped(t *testing.T) {
	coll := &model.Collection{Indexes: []model.DocumentIndex{
		{Name: "_id_"}, {Name: "name_1"}, {Name: "score_-1"},
	}}
	got := indexNames(coll)
	if len(got) != 2 || got[0] != "name_1" {
		t.Errorf("names %v, want every index but the identifier's", got)
	}
	if got := indexNames(&model.Collection{Indexes: []model.DocumentIndex{{Name: "_id_"}}}); len(got) != 0 {
		t.Errorf("names %v, want none: only the identifier's is there", got)
	}
}

// openCollectionStructure opens a collection's structure tab on a connection.
func openCollectionStructure(t *testing.T, fx *fixture, host string) *tab {
	t.Helper()
	forgetIndexes()
	return openCollection(t, fx, host)
}

func TestACollectionOffersItsIndexes(t *testing.T) {
	fx := newFixture(t)
	tb := openCollectionStructure(t, fx, "docs")
	if b := buttonNamed(tb, "Add Index…"); b == nil {
		t.Fatal("a collection does not offer to add an index")
	}
	// With only the identifier's index there is nothing to drop.
	if b := buttonNamed(tb, "Drop Index…"); b == nil || !b.Disabled() {
		t.Error("dropping is offered with nothing to drop")
	}
	// A source whose structure the server holds offers neither.
	tb2 := openCollection(t, fx, "declared")
	if b := buttonNamed(tb2, "Add Index…"); b != nil {
		t.Error("a source that does not manage indexes offers to")
	}
}

func TestMakingAnIndexShowsTheCallFirst(t *testing.T) {
	fx := newFixture(t)
	tb := openCollectionStructure(t, fx, "docs")

	fx.s.addIndex(tb)
	typeIndexForm(t, fx, "name, score:-1", "name_score")
	tapOnTop(t, fx, "Continue")
	// The call is shown before anything is sent.
	if text := labelText(fx.s.win.Canvas().Overlays().Top()); !strings.Contains(text, "createIndex(name_score)") {
		t.Fatalf("the call is not shown: %q", text)
	}
	if len(indexPlans()) != 0 {
		t.Fatal("the index was made before it was agreed to")
	}
	tapOnTop(t, fx, "Run")
	pump(t, fx.q, func() bool { return len(indexPlans()) == 1 })
	made := madeIndexes()
	if len(made) != 1 || made[0].Name != "name_score" || len(made[0].Keys) != 2 {
		t.Fatalf("the index made is %+v", made)
	}
	// The structure is read again, so the tab shows what is there now.
	pump(t, fx.q, func() bool { return showsAll(tb, "name_score") })
	// And now there is something to drop.
	pump(t, fx.q, func() bool { b := buttonNamed(tb, "Drop Index…"); return b != nil && !b.Disabled() })
}

func TestSayingNoToAnIndexChangesNothing(t *testing.T) {
	fx := newFixture(t)
	tb := openCollectionStructure(t, fx, "docs")
	fx.s.addIndex(tb)
	typeIndexForm(t, fx, "name", "name_1")
	tapOnTop(t, fx, "Continue")
	tapOnTop(t, fx, "Cancel")
	if len(indexPlans()) != 0 || len(madeIndexes()) != 0 {
		t.Error("saying no made the index")
	}
	// And an index of no fields never reaches a plan.
	fx.s.addIndex(tb)
	typeIndexForm(t, fx, "  ", "")
	tapOnTop(t, fx, "Continue")
	if len(indexPlans()) != 0 {
		t.Error("an index on nothing was planned")
	}
}

func TestDroppingAnIndexAsksWhichAndShowsTheCall(t *testing.T) {
	fx := newFixture(t)
	tb := openCollectionStructure(t, fx, "docs")
	fx.s.dropIndex(tb, []string{"name_1", "score_-1"})
	tapOnTop(t, fx, "Continue")
	if text := labelText(fx.s.win.Canvas().Overlays().Top()); !strings.Contains(text, "dropIndex(name_1)") {
		t.Fatalf("the call is not shown: %q", text)
	}
	tapOnTop(t, fx, "Run")
	pump(t, fx.q, func() bool { return len(indexPlans()) == 1 })
	if got := indexPlans()[0].Descriptions[0]; got != "Drop the index name_1" {
		t.Errorf("what was run is %q", got)
	}
}

func TestChangingAnIndexOnProductionAsksAgain(t *testing.T) {
	fx := newFixture(t)
	tb := openCollectionStructure(t, fx, "prod")
	fx.s.addIndex(tb)
	typeIndexForm(t, fx, "name", "name_1")
	tapOnTop(t, fx, "Continue")
	tapOnTop(t, fx, "Run")
	if text := labelText(fx.s.win.Canvas().Overlays().Top()); !strings.Contains(text, "marked Production") {
		t.Fatalf("production is not asked about: %q", text)
	}
	if len(indexPlans()) != 0 {
		t.Fatal("it ran before consent was given")
	}
	tapOnTop(t, fx, "Run")
	pump(t, fx.q, func() bool { return len(indexPlans()) == 1 })
	if !indexPlans()[0].Statements[0].Confirmed {
		t.Error("what ran does not carry the consent")
	}
}

// typeIndexForm fills the Add Index form on top of the window.
func typeIndexForm(t *testing.T, fx *fixture, fields, name string) {
	t.Helper()
	entries := entriesIn(fx.s.win.Canvas().Overlays().Top())
	if len(entries) < 2 {
		t.Fatalf("the form has %d entries", len(entries))
	}
	entries[0].SetText(fields)
	entries[1].SetText(name)
}

// entriesIn is every entry in a canvas object, in order.
func entriesIn(o fyne.CanvasObject) []*widget.Entry {
	var out []*widget.Entry
	var walk func(fyne.CanvasObject)
	walk = func(o fyne.CanvasObject) {
		switch v := o.(type) {
		case nil:
			return
		case *widget.Entry:
			out = append(out, v)
			return
		case *fyne.Container:
			for _, c := range v.Objects {
				walk(c)
			}
			return
		}
		if w, ok := o.(fyne.Widget); ok {
			for _, c := range test.WidgetRenderer(w).Objects() {
				walk(c)
			}
		}
	}
	walk(o)
	return out
}

// buttonNamed is a tab's button with that text, or nil.
func buttonNamed(tb *tab, text string) *widget.Button {
	for _, b := range buttons(tb.item.Content) {
		if b.Text == text {
			return b
		}
	}
	return nil
}
