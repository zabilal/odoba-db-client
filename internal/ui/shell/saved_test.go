package shell

import (
	"context"
	"strings"
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/ikigai-db/ikigai-db/internal/store/localdb"
)

func savedList(fx *fixture) []localdb.SavedQuery {
	got, _ := fx.hist.SavedQueries(context.Background())
	return got
}

func TestSaveAsksForANameThenSavesInPlace(t *testing.T) {
	fx := newFixture(t)
	tb, q := openQuery(t, fx, "")
	test.Type(q.editor.Focusable(), "rows 1;")
	if !strings.HasSuffix(tb.item.Text, "•") {
		t.Errorf("tab %q should show that it has unsaved edits", tb.item.Text)
	}
	p := fx.s.promptSave(tb, false)
	p.name.SetText("Monthly revenue")
	p.folder.SetText("Finance")
	p.dlg.Submit()
	pump(t, fx.q, func() bool { return q.saved.ID != "" })
	if tb.item.Text != "Monthly revenue" {
		t.Errorf("tab %q", tb.item.Text)
	}
	list := savedList(fx)
	if len(list) != 1 || list[0].Name != "Monthly revenue" || list[0].Folder != "Finance" ||
		list[0].Body != "rows 1;" || list[0].ConnectionID != tb.connID {
		t.Fatalf("saved %+v", list)
	}

	test.Type(q.editor.Focusable(), " ")
	fx.s.run(cmdQuerySave)
	if fx.s.win.Canvas().Overlays().Top() != nil {
		t.Error("saving a saved query should not ask for its name again")
	}
	pump(t, fx.q, func() bool { l := savedList(fx); return len(l) == 1 && l[0].Body == "rows 1; " })
	pump(t, fx.q, func() bool { return tb.item.Text == "Monthly revenue" })
}

func TestSaveAsCopiesAndDuplicateNamesAreRefused(t *testing.T) {
	fx := newFixture(t)
	tb, q := openQuery(t, fx, "")
	q.editor.Document().SetText("rows 1;")
	p := fx.s.promptSave(tb, false)
	p.name.SetText("a")
	p.dlg.Submit()
	pump(t, fx.q, func() bool { return q.saved.ID != "" })

	p = fx.s.promptSave(tb, true)
	if p.name.Text != "a copy" {
		t.Errorf("Save As suggests %q", p.name.Text)
	}
	p.dlg.Submit()
	pump(t, fx.q, func() bool { return len(savedList(fx)) == 2 })

	tb2 := fx.s.OpenQuery(tb.connID)
	p = fx.s.promptSave(tb2, false)
	p.name.SetText("a")
	p.dlg.Submit()
	pump(t, fx.q, func() bool {
		top := fx.s.win.Canvas().Overlays().Top()
		return top != nil && strings.Contains(labelText(top), "already exists")
	})
	if tb2.query.saved.ID != "" || len(savedList(fx)) != 2 {
		t.Error("a duplicate name was saved")
	}
}

func TestOpenSavedQueryReopensItOrBringsItsTabForward(t *testing.T) {
	fx := newFixture(t)
	tb, q := openQuery(t, fx, "")
	q.editor.Document().SetText("rows 3;")
	p := fx.s.promptSave(tb, false)
	p.name.SetText("r")
	p.dlg.Submit()
	pump(t, fx.q, func() bool { return q.saved.ID != "" })
	fx.s.closeTab(tb.item)

	sp := fx.s.showSaved()
	pump(t, fx.q, func() bool { return len(sp.shown) == 1 })
	sp.list.Select(0)
	re := fx.onlyTab(t)
	if re.query.editor.Document().Text() != "rows 3;" || re.item.Text != "r" || re.query.dirty {
		t.Errorf("reopened %q as %q (dirty %v)", re.query.editor.Document().Text(), re.item.Text, re.query.dirty)
	}
	sp = fx.s.showSaved()
	pump(t, fx.q, func() bool { return len(sp.shown) == 1 })
	sp.list.Select(0)
	if len(fx.s.open) != 1 {
		t.Errorf("%d tabs; an open saved query should come forward, not open twice", len(fx.s.open))
	}
}

func TestDeletingASavedQueryUnbindsItsTab(t *testing.T) {
	fx := newFixture(t)
	tb, q := openQuery(t, fx, "")
	q.editor.Document().SetText("rows 1;")
	p := fx.s.promptSave(tb, false)
	p.name.SetText("gone soon")
	p.dlg.Submit()
	pump(t, fx.q, func() bool { return q.saved.ID != "" })

	sp := fx.s.showSaved()
	pump(t, fx.q, func() bool { return len(sp.shown) == 1 })
	sp.confirmDelete(sp.shown[0])
	test.Tap(findButton(fx.s.win.Canvas().Overlays().Top(), "Delete"))
	pump(t, fx.q, func() bool { return len(savedList(fx)) == 0 && q.saved.ID == "" })
	if !q.dirty {
		t.Error("the tab's text is now saved nowhere, and should say so")
	}
}

func TestClosingAnEditedQueryAsksFirst(t *testing.T) {
	fx := newFixture(t)
	tb, q := openQuery(t, fx, "")
	test.Type(q.editor.Focusable(), "rows 1;")
	fx.s.run(cmdTabClose)
	top := fx.s.win.Canvas().Overlays().Top()
	if len(fx.s.open) != 1 || top == nil {
		t.Fatal("an edited query closed without asking")
	}
	test.Tap(findButton(top, "Close"))
	if len(fx.s.open) != 0 {
		t.Fatal("confirming did not close it")
	}
	fx.s.OpenQuery(tb.connID)
	fx.s.run(cmdTabClose)
	if len(fx.s.open) != 0 {
		t.Error("an untouched query should close without asking")
	}
}
