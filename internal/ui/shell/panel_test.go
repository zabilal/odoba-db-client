package shell

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

func TestPanelsOpenBesideTheDataNotOverIt(t *testing.T) {
	fx, _ := openItems(t)
	fx.s.run(cmdHistory)
	if fx.s.Panel() == nil || fx.s.win.Canvas().Overlays().Top() != nil {
		t.Fatal("Query History should open as a panel, with nothing over the window")
	}
	if fx.s.panel.split.Leading != fx.s.work {
		t.Error("the tabs should stay in view beside the panel")
	}
	if !fx.s.menuItems[cmdHistory].Checked {
		t.Error("the menu should tick the panel that is open")
	}
	fx.s.run(cmdOpenSaved)
	if !fx.s.panelIs(panelSaved) || fx.s.menuItems[cmdHistory].Checked || !fx.s.menuItems[cmdOpenSaved].Checked {
		t.Error("one panel at a time: Saved Queries should replace Query History, and the ticks follow")
	}
	fx.s.run(cmdOpenSaved)
	if fx.s.Panel() != nil || fx.s.right.Objects[0] != fx.s.work || fx.s.menuItems[cmdOpenSaved].Checked {
		t.Error("the open panel's command should close it and give the tabs their room back")
	}
}

func TestEscapeOrCloseShutsThePanel(t *testing.T) {
	fx := newFixture(t)
	p := fx.s.showSaved()
	p.filter.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})
	if fx.s.Panel() != nil {
		t.Error("Escape in the panel's field should close it")
	}
	fx.s.showHistory()
	test.Tap(findButton(fx.s.Panel(), "Close"))
	if fx.s.Panel() != nil {
		t.Error("the Close button should close the panel")
	}
}

func TestAPanelKeepsTheWidthGivenIt(t *testing.T) {
	fx := newFixture(t)
	fx.s.showHistory()
	fx.s.panel.split.SetOffset(0.5)
	fx.s.showSaved()
	if got := fx.s.panel.split.Offset; got != 0.5 {
		t.Errorf("the panel that replaced it is at %v, want the 0.5 given", got)
	}
}

func TestAnEntryCanBeChosenAgain(t *testing.T) {
	fx := newFixture(t)
	_, q := openQuery(t, fx, "")
	q.editor.Document().SetText("rows 7;")
	fx.s.run(cmdQueryRun)
	pump(t, fx.q, func() bool { return len(searchHistory(fx, "")) == 1 })
	p := fx.s.showHistory()
	pump(t, fx.q, func() bool { return len(p.entries) == 1 })
	p.list.Select(0)
	p.list.Select(0)
	if len(fx.s.open) != 3 {
		t.Errorf("%d tabs; the same entry, chosen again, should open again", len(fx.s.open))
	}
}

func TestASavedQueryCanBeChosenAgain(t *testing.T) {
	fx := newFixture(t)
	tb, q := openQuery(t, fx, "")
	test.Type(q.editor.Focusable(), "rows 1;")
	p := fx.s.promptSave(tb, false)
	p.name.SetText("again")
	p.dlg.Submit()
	pump(t, fx.q, func() bool { return q.saved.ID != "" && !q.dirty })
	fx.s.closeTab(tb.item)
	sp := fx.s.showSaved()
	pump(t, fx.q, func() bool { return len(sp.shown) == 1 })
	sp.list.Select(0)
	fx.s.closeTab(fx.onlyTab(t).item)
	sp.list.Select(0)
	if len(fx.s.open) != 1 {
		t.Errorf("%d tabs; the same saved query, chosen again, should open again", len(fx.s.open))
	}
}
