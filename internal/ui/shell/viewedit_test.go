package shell

import (
	"context"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
	uitheme "github.com/ikigai-db/ikigai-db/internal/ui/theme"
)

// viewerOn opens the items tab's cell viewer on a cell.
func viewerOn(fx *fixture, tb *tab, row, col int) *cellViewer {
	tb.grid.Select(grid.CellID{Row: row, Col: col}, grid.CellID{Row: row, Col: col})
	fx.s.run(cmdCellViewer)
	return tb.viewers[tb.grid]
}

func TestTheViewerEditsAValueAtLength(t *testing.T) {
	fx, tb := loadedItems(t)
	v := viewerOn(fx, tb, 1, 1)
	if v.editBtn.Disabled() {
		t.Fatal("a keyed table's cell can be edited in the viewer")
	}
	v.editBtn.OnTapped()
	if !v.editing || v.editor.Text != "item 1" || !v.editBox.Visible() || v.textBox.Visible() {
		t.Fatalf("editing %v, %q", v.editing, v.editor.Text)
	}
	if f := fyne.CurrentApp().Driver().CanvasForObject(tb.grid.Table).Focused(); f != v.editor {
		t.Errorf("the editor takes the keyboard, not %T", f)
	}
	if len(v.calBox.Objects) != 0 {
		t.Error("text has no calendar")
	}
	tb.grid.Select(grid.CellID{Row: 5, Col: 0}, grid.CellID{Row: 5, Col: 0})
	if v.title.Text != "Editing name" {
		t.Errorf("while editing, the viewer stays on its cell: %q", v.title.Text)
	}
	v.editor.SetText("two\nlines")
	v.finishEdit()
	row, _ := tb.model.Row(tb.ctx, 1)
	if got, ok := tb.pending.Value(row, 1); !ok || got != "two\nlines" || v.editing {
		t.Fatalf("Done keeps it as a pending change: %v %v, editing %v", got, ok, v.editing)
	}
	if v.editBox.Visible() || !v.textBox.Visible() {
		t.Error("the value is shown again, not the editor")
	}
	if v.title.Text != "id" || !strings.HasSuffix(tb.footer.Text, " · 1 pending change") {
		t.Errorf("then it follows the selection again: %q; footer %q", v.title.Text, tb.footer.Text)
	}
	tb.grid.Select(grid.CellID{Row: 1, Col: 1}, grid.CellID{Row: 1, Col: 1})
	if v.text.Text != "two\nlines" {
		t.Errorf("the viewer shows a cell's pending value: %q", v.text.Text)
	}
}

func TestTheViewerSaysWhyAValueIsNotWritten(t *testing.T) {
	fx, tb := loadedItems(t)
	v := viewerOn(fx, tb, 1, 0)
	v.beginEdit()
	v.editor.SetText("x")
	v.finishEdit()
	if !v.editing || v.meta.Text != "Not written: id: not a whole number" || tb.pending.Len() != 0 {
		t.Fatalf("editing %v, saying %q", v.editing, v.meta.Text)
	}
	v.editor.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})
	if v.editing || v.title.Text != "id" || tb.pending.Len() != 0 {
		t.Errorf("Escape gives it up: editing %v, %q", v.editing, v.title.Text)
	}
	v.beginEdit()
	v.cancelEdit()
	if v.editing || tb.pending.Len() != 0 {
		t.Error("Cancel gives it up")
	}
}

func TestEditInCellViewerOpensTheViewerToEdit(t *testing.T) {
	fx, tb := loadedItems(t)
	tb.grid.Select(grid.CellID{Row: 2, Col: 1}, grid.CellID{Row: 2, Col: 1})
	fx.s.run(cmdEditValue)
	v := tb.viewers[tb.grid]
	if v == nil || !v.shown() || !v.editing || v.editor.Text != "item 2" {
		t.Fatal("Edit in Cell Viewer should open the viewer on the cell, editing")
	}
	v.editor.SetText("kept")
	fx.s.run(cmdEditValue)
	if !v.shown() || v.editor.Text != "kept" {
		t.Error("asked again while editing, it keeps what was typed")
	}
	v.cancelEdit()
	fx.s.run(cmdEditValue)
	if !v.shown() || !v.editing {
		t.Error("asked with the viewer open, it edits there rather than closing it")
	}
}

func TestAViewerOnAGridThatDoesNotEditOffersNoEdit(t *testing.T) {
	fx, tb := loadedItems(t)
	tb.grid.OnEdit = nil
	v := viewerOn(fx, tb, 1, 1)
	v.beginEdit()
	if !v.editBtn.Disabled() || v.editing {
		t.Error("a cell that cannot be edited has no Edit")
	}
}

func TestADaysIsPickedOnACalendar(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()
	m := grid.NewModel(grid.NewSyntheticFetcher(10))
	g := grid.NewTableGridWith(ctx, m, uitheme.Light, fx.q.Run, 0)
	var got any
	g.OnEdit = func(_ model.Row, _ int, v any) error { got = v; return nil }
	holder := container.NewStack(g.View())
	test.NewTempWindow(t, holder).Resize(fyne.NewSize(900, 500))
	pump(t, fx.q, func() bool { _, ok := m.Row(ctx, 0); return ok })
	g.Select(grid.CellID{Row: 0, Col: 11}, grid.CellID{Row: 0, Col: 11}) // placed_at, an instant
	v := fx.s.newViewer(ctx, g, holder)
	v.show()
	v.beginEdit()
	if len(v.calBox.Objects) != 1 {
		t.Fatal("an instant is picked on a calendar")
	}
	cal := v.calBox.Objects[0].(*widget.Calendar)
	start := v.editor.Text
	cal.OnChanged(time.Date(2021, 3, 4, 0, 0, 0, 0, time.Local))
	if v.editor.Text != "2021-03-04"+start[10:] {
		t.Fatalf("the day picked, with the time of day kept: %q from %q", v.editor.Text, start)
	}
	v.finishEdit()
	w, _ := grid.Parse(v.start[:0]+"2021-03-04"+start[10:], m.Columns()[11], time.Local)
	if at, ok := got.(time.Time); !ok || !at.Equal(w.(time.Time)) {
		t.Errorf("written: %v, want %v", got, w)
	}
}

func TestADayReplacesTheDateBeforeATime(t *testing.T) {
	d := time.Date(2021, 3, 4, 0, 0, 0, 0, time.UTC)
	for text, want := range map[string]string{
		"2020-01-01 10:11:12": "2021-03-04 10:11:12",
		"2020-01-01":          "2021-03-04",
		"":                    "2021-03-04",
		"soon":                "2021-03-04",
		"not a date at all":   "2021-03-04",
	} {
		if got := withDate(text, d); got != want {
			t.Errorf("%q: %q, want %q", text, got, want)
		}
	}
}

func TestJSONIsEditedOnLinesAndWrittenCompact(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()
	m := grid.NewModel(grid.NewSyntheticFetcher(10))
	g := grid.NewTableGridWith(ctx, m, uitheme.Light, fx.q.Run, 0)
	var got any
	g.OnEdit = func(_ model.Row, _ int, v any) error { got = v; return nil }
	holder := container.NewStack(g.View())
	test.NewTempWindow(t, holder).Resize(fyne.NewSize(900, 500))
	pump(t, fx.q, func() bool { _, ok := m.Row(ctx, 0); return ok })
	g.Select(grid.CellID{Row: 0, Col: 10}, grid.CellID{Row: 0, Col: 10}) // metadata
	v := fx.s.newViewer(ctx, g, holder)
	v.show()
	v.beginEdit()
	if !strings.Contains(v.editor.Text, "\n  \"channel\"") || !v.editor.TextStyle.Monospace {
		t.Fatalf("JSON is laid out on lines, in a fixed font: %q", v.editor.Text)
	}
	v.finishEdit()
	if got != nil {
		t.Error("JSON only laid out is no edit")
	}
	v.beginEdit()
	v.editor.SetText("{\n  \"a\": [1, 2]\n}")
	v.finishEdit()
	if j, ok := got.(model.JSON); !ok || string(j) != `{"a":[1,2]}` {
		t.Errorf("written compact: %s", got)
	}
}
