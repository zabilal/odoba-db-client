package grid

import (
	"context"
	"reflect"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/ui/theme"
	"github.com/ikigai-db/ikigai-db/internal/ui/uithread"
)

func sortableGrid(t *testing.T) *TableGrid {
	t.Helper()
	test.NewTempApp(t)
	g := NewTableGridWith(context.Background(), NewModel(NewSyntheticFetcher(100)), theme.Light, (&uithread.Queue{}).Run, 0)
	g.Sortable = true
	return g
}

func TestHeaderClicksCycleTheSort(t *testing.T) {
	g := sortableGrid(t)
	var got []SortKey
	g.OnSort = func(k []SortKey) { got = k }
	for _, step := range []struct {
		col  int
		add  bool
		want []SortKey
	}{
		{1, false, []SortKey{{1, false}}},
		{1, false, []SortKey{{1, true}}},
		{1, false, nil},
		{1, false, []SortKey{{1, false}}},
		{0, true, []SortKey{{1, false}, {0, false}}},
		{0, true, []SortKey{{1, false}, {0, true}}},
		{0, true, []SortKey{{1, false}}},
		{2, false, []SortKey{{2, false}}},
	} {
		g.ToggleSort(step.col, step.add)
		if !reflect.DeepEqual(got, step.want) {
			t.Fatalf("toggle %d (add %v): %v, want %v", step.col, step.add, got, step.want)
		}
	}
}

func TestHeaderShowsTheSortAndShiftClickAdds(t *testing.T) {
	g := sortableGrid(t)
	g.OnSort = func([]SortKey) {}
	name := g.model.Columns()[1].Name
	h := g.createHeader().(*columnHeader).title
	g.updateHeader(widget.TableCellID{Row: -1, Col: 1}, h)
	h.Tapped(&fyne.PointEvent{})
	g.updateHeader(widget.TableCellID{Row: -1, Col: 1}, h)
	if h.text != name+" ↑" {
		t.Errorf("header %q after a click", h.text)
	}
	other := g.createHeader().(*columnHeader).title
	g.updateHeader(widget.TableCellID{Row: -1, Col: 0}, other)
	other.MouseDown(&desktop.MouseEvent{Button: desktop.MouseButtonPrimary, PointEvent: fyne.PointEvent{}, Modifier: fyne.KeyModifierShift})
	other.Tapped(&fyne.PointEvent{})
	g.updateHeader(widget.TableCellID{Row: -1, Col: 1}, h)
	if len(g.Sorts()) != 2 || h.text != name+" ↑1" {
		t.Errorf("sorts %v, header %q; ⇧-click should add a second key", g.Sorts(), h.text)
	}
}

func TestAnUnsortableGridIgnoresHeaderClicks(t *testing.T) {
	g := sortableGrid(t)
	g.Sortable = false
	called := false
	g.OnSort = func([]SortKey) { called = true }
	h := g.createHeader().(*columnHeader).title
	g.updateHeader(widget.TableCellID{Row: -1, Col: 0}, h)
	h.Tapped(&fyne.PointEvent{})
	if called || len(g.Sorts()) != 0 {
		t.Error("a query's result cannot be re-sorted without running it again")
	}
}

// labelled serves ten rows of one value, released when told: a fetch that is
// still running when the model moves on.
type labelled struct {
	label   string
	release chan struct{}
	done    chan struct{}
}

func newLabelled(label string, open bool) *labelled {
	l := &labelled{label: label, release: make(chan struct{}), done: make(chan struct{}, 8)}
	if open {
		close(l.release)
	}
	return l
}

func (l *labelled) Columns() []model.ColumnDef           { return []model.ColumnDef{{Name: "v"}} }
func (l *labelled) Count(context.Context) (int64, error) { return 10, nil }
func (l *labelled) Fetch(ctx context.Context, off, lim int64) ([]model.Row, error) {
	<-l.release
	defer func() { l.done <- struct{}{} }()
	rows := make([]model.Row, 0, 10)
	for i := off; i < 10 && i < off+lim; i++ {
		rows = append(rows, model.Row{l.label})
	}
	return rows, nil
}

func TestAFetchThatLandsAfterTheSourceChangesIsDropped(t *testing.T) {
	old := newLabelled("old", false)
	m := NewModel(old)
	m.Row(context.Background(), 0) // fetching page 0 from the old source, held
	m.SetFetcher(newLabelled("new", true))
	close(old.release) // the old fetch lands now, too late
	select {
	case <-old.done:
	case <-time.After(3 * time.Second):
		t.Fatal("the old fetch never finished")
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		row, ok := m.Row(context.Background(), 0)
		if ok {
			if row[0] != "new" {
				t.Fatalf("row 0 = %v; a page from the replaced source was kept", row[0])
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the new source's page never arrived")
		}
		time.Sleep(time.Millisecond)
	}
}
