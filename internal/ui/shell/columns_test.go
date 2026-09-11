package shell

import (
	"reflect"
	"testing"

	"fyne.io/fyne/v2"

	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
)

func TestTheHeaderMenuOffersFilteringAndTheColumnCommands(t *testing.T) {
	fx, tb := openItems(t)
	var labels []string
	for _, it := range fx.s.headerMenu(tb, tb.grid, 1, fyne.NewPos(10, 10)).Items {
		labels = append(labels, it.Label)
	}
	want := []string{"Filter by Values…", "", "Hide Column", "Move Left", "Move Right", "", "Freeze Through This Column", "Unfreeze Columns"}
	if !reflect.DeepEqual(labels, want) {
		t.Errorf("menu %q\nwant %q", labels, want)
	}
}

func TestAHiddenColumnIsNotCopiedOrViewed(t *testing.T) {
	fx, tb := openItems(t)
	tb.grid.Select(grid.CellID{Row: 1, Col: 0}, grid.CellID{Row: 1, Col: 0})
	hide, _ := fx.s.reg.Get(cmdHideColumn)
	hide.Run()
	if got := tb.grid.Shown(); !reflect.DeepEqual(got, []int{1}) {
		t.Fatalf("shown %v after hiding id", got)
	}
	tb.grid.Select(grid.CellID{Row: 2, Col: 0}, grid.CellID{Row: 2, Col: 0})
	if got := copied(t, fx, tb); got != "item 2" {
		t.Errorf("copied %q; the first column shown is name now", got)
	}
	fx.s.toggleViewer()
	if v := tb.viewers[tb.grid]; v == nil || v.title.Text != "name" {
		t.Error("the viewer should show the column that is shown")
	}
	show, _ := fx.s.reg.Get(cmdShowColumns)
	if !show.Enabled() {
		t.Fatal("with a column hidden, Show All Columns applies")
	}
	show.Run()
	if got := tb.grid.Shown(); !reflect.DeepEqual(got, []int{0, 1}) {
		t.Errorf("shown %v after showing all", got)
	}
}

func TestAMovedColumnCopiesInTheOrderShown(t *testing.T) {
	fx, tb := openItems(t)
	tb.grid.Select(grid.CellID{Row: 3, Col: 1}, grid.CellID{Row: 3, Col: 1})
	left, _ := fx.s.reg.Get(cmdMoveLeft)
	left.Run()
	if got := tb.grid.Shown(); !reflect.DeepEqual(got, []int{1, 0}) || tb.grid.SelectedColumn() != 1 {
		t.Fatalf("shown %v, active column %d", got, tb.grid.SelectedColumn())
	}
	tb.grid.Select(grid.CellID{Row: 3, Col: 0}, grid.CellID{Row: 3, Col: 1})
	if got := copied(t, fx, tb); got != "item 3\t3" {
		t.Errorf("copied %q; name is shown first now", got)
	}
	tb.grid.SetFilterText(1, "item 3") // filters speak of the model's columns
	tb.grid.ApplyFilters()
	pump(t, fx.q, func() bool { f := tb.browse.Options().Filters; return len(f) == 1 && f[0].Column == "name" })
}
