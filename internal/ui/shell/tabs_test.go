package shell

import (
	"reflect"
	"testing"
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
