package shell

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/store"
	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
)

func cols(names ...string) []model.ColumnDef {
	out := make([]model.ColumnDef, 0, len(names))
	for _, n := range names {
		out = append(out, model.ColumnDef{Name: n})
	}
	return out
}

func TestDocumentChangesAreWhatDiffers(t *testing.T) {
	c := cols("_id", "name", "score", "nickname")
	row := model.Row{"64b1", "Ada", int64(42), "Ada L"}

	// What was not touched is not written: an edit to one field does not
	// write over another the document holds.
	got, err := documentChanges(c, row, `{"_id":"64b1","name":"Ada Lovelace","score":42,"nickname":"Ada L"}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got["name"] != "Ada Lovelace" {
		t.Errorf("changes %v, want the name alone", got)
	}
	// A field taken out of the document is removed from it.
	got, err = documentChanges(c, row, `{"_id":"64b1","name":"Ada","score":42}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["nickname"].(model.Removed); !ok || len(got) != 1 {
		t.Errorf("changes %v, want the nickname removed", got)
	}
	// A field the collection has not got yet is added like any other.
	got, err = documentChanges(c, row, `{"_id":"64b1","name":"Ada","score":42,"nickname":"Ada L","born":1815}`)
	if err != nil {
		t.Fatal(err)
	}
	if got["born"] != int64(1815) || len(got) != 1 {
		t.Errorf("changes %v, want the new field alone", got)
	}
	// A document saved as it was reads back as no change at all.
	got, err = documentChanges(c, row, `{"_id":"64b1","name":"Ada","score":42,"nickname":"Ada L"}`)
	if err != nil || len(got) != 0 {
		t.Errorf("changes %v (%v), want none", got, err)
	}
	// A field this document never had is not removed from it: the column is
	// the collection's, and there is nothing here to go.
	sparse := model.Row{"64b1", "Ada", int64(42), nil}
	got, err = documentChanges(c, sparse, `{"_id":"64b1","name":"Ada","score":42}`)
	if err != nil || len(got) != 0 {
		t.Errorf("changes %v (%v), want none: the field was never there", got, err)
	}
}

func TestDocumentChangesKeepNumbersWhole(t *testing.T) {
	c := cols("_id", "score", "rate")
	row := model.Row{"64b1", int64(42), 1.5}
	got, err := documentChanges(c, row, `{"_id":"64b1","score":43,"rate":2.5}`)
	if err != nil {
		t.Fatal(err)
	}
	// A whole number stays whole where the field held one: a document store
	// tells 43 from 43.0.
	if v, ok := got["score"].(int64); !ok || v != 43 {
		t.Errorf("score is %#v, want a whole number", got["score"])
	}
	if v, ok := got["rate"].(float64); !ok || v != 2.5 {
		t.Errorf("rate is %#v, want the number typed", got["rate"])
	}
	// A field that held a fraction keeps one even when a whole number is
	// typed: the field is the kind of number it was.
	got, err = documentChanges(c, row, `{"_id":"64b1","score":42,"rate":3}`)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := got["rate"].(float64); !ok || v != 3 {
		t.Errorf("rate is %#v, want it still a fraction's kind", got["rate"])
	}
}

func TestDocumentChangesRefuseWhatCannotBeSaved(t *testing.T) {
	c := cols("_id", "name")
	row := model.Row{"64b1", "Ada"}
	cases := []struct{ text, says string }{
		{`{"_id":"64b1","name":`, "not a JSON document"},
		{`[{"_id":"64b1"}]`, "not a JSON document"},
		{`{"_id":"64b1"} {"_id":"other"}`, "one at a time"},
		{`{"_id":"another","name":"Ada"}`, "another document"},
	}
	for _, tc := range cases {
		_, err := documentChanges(c, row, tc.text)
		if err == nil {
			t.Errorf("%s was saved", tc.text)
			continue
		}
		if !strings.Contains(err.Error(), tc.says) {
			t.Errorf("%s is refused with %q, want it to say %q", tc.text, err, tc.says)
		}
	}
}

func TestATypeUnlikeTheRestIsSaid(t *testing.T) {
	c := []model.ColumnDef{
		{Name: "score", Type: model.DataType{Class: model.TypeInteger}},
		{Name: "name", Type: model.DataType{Class: model.TypeString}},
		{Name: "notes", Type: model.DataType{Class: model.TypeUnknown}},
	}
	got := typeWarnings(c, map[string]any{"score": "high", "name": "Ada"})
	if !strings.Contains(got, "score is text here") || !strings.Contains(got, "integer") {
		t.Errorf("it says %q, want the field, what was typed and what the rest hold", got)
	}
	if strings.Contains(got, "name") {
		t.Errorf("it says %q about a field that is what it always was", got)
	}
	// A field the sample saw as two types is unknown, and nothing is said.
	if got := typeWarnings(c, map[string]any{"notes": int64(1)}); got != "" {
		t.Errorf("it says %q about a field with no one type", got)
	}
	// A field removed is not a type.
	if got := typeWarnings(c, map[string]any{"score": model.Removed{}}); got != "" {
		t.Errorf("it says %q about a field removed", got)
	}
}

func TestTheEditorSaysWhatTheDocumentHasNot(t *testing.T) {
	c := cols("_id", "name", "nickname", "born")
	if got := alsoHolds(c, model.Row{"64b1", "Ada", nil, nil}); !strings.Contains(got, "nickname, born") {
		t.Errorf("it says %q, want the empty fields the rest hold", got)
	}
	if got := alsoHolds(c, model.Row{"64b1", "Ada", "Ada L", int64(1815)}); got != "" {
		t.Errorf("it says %q about a document holding everything", got)
	}
}

func TestTheJSONViewEditsOneDocument(t *testing.T) {
	fx, tb := loadedItems(t)
	j := jsonOn(t, fx, tb, tb.grid)
	// Nothing is edited until a document is chosen and Edit is pressed.
	if !j.edit.Disabled() {
		t.Error("Edit is offered with no document chosen")
	}
	tb.grid.Select(grid.CellID{Row: 2, Col: 0}, grid.CellID{Row: 2, Col: 0})
	j.buttons()
	if j.edit.Disabled() {
		t.Fatal("Edit is not offered with a document chosen")
	}
	test.Tap(j.edit)
	pump(t, fx.q, func() bool { return j.editing })
	if got := j.text.Text; !strings.HasPrefix(got, "{") || !strings.Contains(got, `"name": "item 2"`) {
		t.Errorf("the document being edited is\n%s", got)
	}
	if j.where.Text != "Document 3" {
		t.Errorf("it says %q", j.where.Text)
	}
	if !j.save.Visible() || j.prev.Visible() {
		t.Error("editing shows Save, and not the paging")
	}
	// Cancel puts the page back, keeping nothing.
	test.Tap(j.cancel)
	pump(t, fx.q, func() bool { return !j.editing && strings.HasPrefix(j.text.Text, "[") })
	if j.save.Visible() || !j.prev.Visible() {
		t.Error("the paging is back, and Save is not")
	}

	// Left mid-edit, the view opens on the page again rather than on what
	// was being typed.
	test.Tap(j.edit)
	pump(t, fx.q, func() bool { return j.editing })
	j.hide()
	fx.s.run(cmdJSONView)
	pump(t, fx.q, func() bool { return strings.HasPrefix(j.text.Text, "[") })
	if j.editing || j.save.Visible() || !j.prev.Visible() {
		t.Error("it opened where it was left, mid-edit")
	}
}

func TestSavingADocumentOnProductionAsksFirst(t *testing.T) {
	fx := newFixture(t)
	c, err := fx.conns.Create(store.SavedConnection{Name: "prod", Driver: "postgres", Host: "db1", Environment: "production"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	fx.s.OpenObject(c.ID, itemsNode)
	tb := fx.onlyTab(t)
	pump(t, fx.q, func() bool {
		if tb.model == nil {
			return false
		}
		_, ok := tb.model.Row(tb.ctx, 3)
		return ok
	})
	j := jsonOn(t, fx, tb, tb.grid)
	tb.grid.Select(grid.CellID{Row: 1, Col: 0}, grid.CellID{Row: 1, Col: 0})
	j.buttons()
	test.Tap(j.edit)
	pump(t, fx.q, func() bool { return j.editing })

	before := len(writtenPlans())
	j.text.SetText(strings.Replace(j.text.Text, `"item 1"`, `"renamed"`, 1))
	test.Tap(j.save)
	if text := labelText(fx.s.win.Canvas().Overlays().Top()); !strings.Contains(text, "marked Production") {
		t.Fatalf("saving a document on production asks first: %q", text)
	}
	typeOnTop(t, fx, "prod")
	tapOnTop(t, fx, "Commit")
	pump(t, fx.q, func() bool { return len(writtenPlans()) > before })
	plan := writtenPlans()[before]
	if len(plan.Statements) != 1 || !plan.Statements[0].Confirmed {
		t.Fatalf("what was written with consent: %+v", plan.Statements)
	}
	// The consent was given to this document's change, not to nothing: the
	// plan is made again from the changes it came from.
	if got := plan.Statements[0].SQL; !strings.Contains(got, "renamed") {
		t.Errorf("what was written is %q, want the change consented to", got)
	}
}

func TestSavingADocumentWritesWhatChanged(t *testing.T) {
	fx, tb := loadedItems(t)
	j := jsonOn(t, fx, tb, tb.grid)
	tb.grid.Select(grid.CellID{Row: 1, Col: 0}, grid.CellID{Row: 1, Col: 0})
	j.buttons()
	test.Tap(j.edit)
	pump(t, fx.q, func() bool { return j.editing })

	// Saving what was not changed writes nothing, and says so.
	test.Tap(j.save)
	if !strings.Contains(j.note.Text, "Nothing was changed") {
		t.Errorf("it says %q", j.note.Text)
	}
	if !j.editing {
		t.Error("it stopped editing though nothing was written")
	}
	// Changed and saved, it is written: one change, to the document's own
	// field, addressed by the row's key.
	before := len(writtenPlans())
	j.text.SetText(strings.Replace(j.text.Text, `"item 1"`, `"renamed"`, 1))
	test.Tap(j.save)
	pump(t, fx.q, func() bool { return !j.editing })
	pump(t, fx.q, func() bool { return len(writtenPlans()) > before })
	plans := writtenPlans()
	last := plans[len(plans)-1]
	if len(last.Statements) != 1 {
		t.Fatalf("%d statements written, want the one change", len(last.Statements))
	}
	if got := last.Statements[0].SQL; !strings.Contains(got, "renamed") || !strings.Contains(got, "name:") {
		t.Errorf("what was written is %q, want the field that changed", got)
	}
}
