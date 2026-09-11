package shell

import (
	"reflect"
	"testing"

	"fyne.io/fyne/v2"
)

func TestTabsMoveAndPin(t *testing.T) {
	fx := newFixture(t)
	c := fx.create(t, "db1", nil)
	a, b, d := fx.s.OpenQuery(c.ID), fx.s.OpenQuery(c.ID), fx.s.OpenQuery(c.ID)
	order := func() []*tab {
		var out []*tab
		for _, it := range fx.s.tabs.Items {
			out = append(out, fx.s.tabOf(it))
		}
		return out
	}
	step := func(what string, want ...*tab) {
		t.Helper()
		if got := order(); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: tabs in the wrong order", what)
		}
	}
	step("opened", a, b, d)
	fx.s.run(cmdMoveTabLeft)
	step("the last moved left", a, d, b)
	if fx.s.activeTab() != d {
		t.Fatal("a moved tab should stay the active one")
	}
	fx.s.run(cmdPinTab)
	step("pinned, to the left", d, a, b)
	if d.item.Icon == nil || !fx.s.checked(cmdPinTab) {
		t.Error("a pinned tab wears the pin, and the menu ticks it")
	}
	if right, _ := fx.s.reg.Get(cmdMoveTabRight); right.Enabled() {
		t.Error("a pinned tab does not move in among the unpinned")
	}
	fx.s.tabs.Select(b.item)
	fx.s.run(cmdPinTab)
	step("a second pin comes after the first", d, b, a)
	fx.s.run(cmdMoveTabLeft)
	step("pinned tabs move among themselves", b, d, a)
	e := fx.s.OpenQuery(c.ID)
	step("a new tab opens after the pinned ones", b, d, a, e)
	fx.s.tabs.Select(b.item)
	fx.s.run(cmdPinTab)
	step("unpinned, it comes first among the rest", d, b, a, e)
	if b.item.Icon != nil || fx.s.checked(cmdPinTab) {
		t.Error("an unpinned tab loses its pin, and its tick")
	}
}

func TestDraggingATabKeepsItAmongItsKind(t *testing.T) {
	fx := newFixture(t)
	c := fx.create(t, "db1", nil)
	a, b, d := fx.s.OpenQuery(c.ID), fx.s.OpenQuery(c.ID), fx.s.OpenQuery(c.ID)
	step := func(what string, want ...*tab) {
		t.Helper()
		var got []*tab
		for _, it := range fx.s.tabs.Items {
			got = append(got, fx.s.tabOf(it))
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: tabs in the wrong order", what)
		}
	}
	fx.s.tabs.Select(a.item)
	fx.s.run(cmdPinTab)
	fx.s.tabs.OnMove(d.item, 0) // as the tab bar reports a drop
	step("dropped among the pinned, it stays first of the rest", a, d, b)
	if fx.s.activeTab() != d {
		t.Error("a dragged tab is selected")
	}
	fx.s.tabs.OnMove(a.item, 2)
	step("a pinned tab dropped among the rest stays last of the pinned", a, d, b)
	fx.s.tabs.OnMove(b.item, 1)
	step("dropped between two, it goes between them", a, b, d)
}

func TestATabsMenuIsMadeOfCommands(t *testing.T) {
	fx := newFixture(t)
	c := fx.create(t, "db1", nil)
	a, b := fx.s.OpenQuery(c.ID), fx.s.OpenQuery(c.ID)
	fx.s.tabs.Select(a.item)
	m := fx.s.tabMenu(b.item)
	if fx.s.activeTab() != b {
		t.Fatal("the menu's tab is selected first, so its commands act on it")
	}
	items := map[string]*fyne.MenuItem{}
	var labels []string
	for _, it := range m.Items {
		if !it.IsSeparator {
			items[it.Label] = it
			labels = append(labels, it.Label)
		}
	}
	title := func(id string) string { cmd, _ := fx.s.reg.Get(id); return cmd.Title }
	want := []string{title(cmdPinTab), title(cmdMoveTabLeft), title(cmdMoveTabRight), title(cmdMoveToPane), title(cmdTabClose)}
	if !reflect.DeepEqual(labels, want) {
		t.Fatalf("menu %v, want %v", labels, want)
	}
	if !items[title(cmdMoveToPane)].Disabled {
		t.Error("with one pane there is no other to move to")
	}
	if !items[title(cmdMoveTabRight)].Disabled || items[title(cmdMoveTabLeft)].Disabled {
		t.Error("the last tab can move left, not right")
	}
	items[title(cmdPinTab)].Action()
	if !b.pinned {
		t.Error("Pin Tab from the menu pins the tab it was opened on")
	}
	fx.s.tabs.OnMenu(a.item, fyne.NewPos(10, 10)) // as a secondary tap on a tab asks
	if fx.s.activeTab() != a || fx.s.win.Canvas().Overlays().Top() == nil {
		t.Error("a secondary tap on a tab shows its menu")
	}
}
