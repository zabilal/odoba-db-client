package grid

import (
	"context"
	"errors"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/ui/theme"
	"github.com/ikigai-db/ikigai-db/internal/ui/uithread"
)

// editGrid is a grid that writes its edits into pending changes, with the
// values OnEdit was given.
func editGrid(t *testing.T) (*TableGrid, *app.Pending, *[]any) {
	t.Helper()
	g := selectingGrid(t)
	p := pendingGrid(t, g)
	got := &[]any{}
	g.OnEdit = func(row model.Row, col int, v any) error {
		*got = append(*got, v)
		return p.Set(row, col, v)
	}
	return g, p, got
}

func focused(g *TableGrid) fyne.Focusable {
	return fyne.CurrentApp().Driver().CanvasForObject(g.table).Focused()
}

func key(name fyne.KeyName) *fyne.KeyEvent { return &fyne.KeyEvent{Name: name} }

func TestReturnEditsTheCellAndWritesWhatWasTyped(t *testing.T) {
	g, p, got := editGrid(t)
	row := rowOf(t, g, 1)
	click(g, 1, 2, 0)
	g.table.TypedKey(key(fyne.KeyReturn))
	e := g.editing
	if e == nil || e.entry == nil {
		t.Fatal("Return should open an editor on the active cell")
	}
	if e.entry.Text != row[2] || focused(g) != e.entry {
		t.Errorf("the editor starts from the value, %q, and takes the keyboard: %v", e.entry.Text, focused(g))
	}
	if drawn(g, 1, 2).editor == nil || drawn(g, 1, 1).editor != nil {
		t.Error("the cell edited, and only it, shows the editor")
	}
	e.entry.SetText("Someone Else")
	e.entry.TypedKey(key(fyne.KeyReturn))
	if len(*got) != 1 || (*got)[0] != "Someone Else" || g.editing != nil {
		t.Fatalf("Return writes and closes: %v, open %v", *got, g.editing != nil)
	}
	if v, ok := p.Value(row, 2); !ok || v != "Someone Else" {
		t.Errorf("the change is held: %v %v", v, ok)
	}
	if a, _ := g.Selection().Active(); a != (CellID{2, 2}) {
		t.Errorf("Return moves down, as in a spreadsheet: %v", a)
	}
	if c := drawn(g, 1, 2); c.editor != nil || c.text != "Someone Else" {
		t.Errorf("the cell shows its new value: %q", c.text)
	}
	if focused(g) != g.table {
		t.Error("the grid takes the keyboard back")
	}
	walk(g.View(), func(o fyne.CanvasObject) {
		if _, ok := o.(*editEntry); ok {
			t.Error("no cell on screen still shows the editor")
		}
	})
	click(g, 1, 2, 0)
	g.EditCell("")
	if g.editing.entry.Text != "Someone Else" {
		t.Errorf("a changed cell opens with its new value: %q", g.editing.entry.Text)
	}
}

func TestTypingEditsTheCellStartingWithWhatWasTyped(t *testing.T) {
	g, _, got := editGrid(t)
	click(g, 1, 2, 0)
	g.table.TypedRune('Z')
	if g.editing == nil || g.editing.entry.Text != "Z" {
		t.Fatal("typing should open the editor with what was typed")
	}
	g.editing.entry.TypedRune('q')
	if g.editing.entry.Text != "Zq" {
		t.Errorf("what is typed next follows it: %q", g.editing.entry.Text)
	}
	g.editing.entry.TypedKey(key(fyne.KeyEscape))
	if g.editing != nil || len(*got) != 0 || focused(g) != g.table {
		t.Errorf("Escape leaves the cell as it was: %v", *got)
	}
	walk(g.View(), func(o fyne.CanvasObject) {
		if _, ok := o.(*editEntry); ok {
			t.Error("the cell shows its value again, not the editor")
		}
	})
	g.table.TypedRune(' ')
	if g.editing != nil {
		t.Error("a space is the cell viewer's, not an edit")
	}
}

func TestTabWritesAndMovesAcross(t *testing.T) {
	g, _, got := editGrid(t)
	click(g, 1, 2, 0)
	g.EditCell("Right")
	g.editing.entry.TypedKey(key(fyne.KeyTab))
	if a, _ := g.Selection().Active(); a != (CellID{1, 3}) || len(*got) != 1 {
		t.Fatalf("Tab writes and moves right: %v, %v", a, *got)
	}
	g.EditCell("left@example.com")
	g.editing.entry.KeyDown(key("LeftShift"))
	g.editing.entry.TypedKey(key(fyne.KeyTab))
	if a, _ := g.Selection().Active(); a != (CellID{1, 2}) || len(*got) != 2 {
		t.Errorf("⇧Tab writes and moves left: %v, %v", a, *got)
	}
	if !g.editingNothing() {
		t.Error("the editor closes")
	}
}

func (g *TableGrid) editingNothing() bool { return g.editing == nil }

func TestAValueLeftAsItWasIsNoEdit(t *testing.T) {
	g, p, got := editGrid(t)
	click(g, 1, 3, 0)
	g.EditCell("")
	g.editing.entry.TypedKey(key(fyne.KeyReturn))
	click(g, 6, 3, 0) // id 7's email is NULL
	g.EditCell("")
	if g.editing.entry.Text != "" || g.editing.entry.PlaceHolder != NullText {
		t.Errorf("NULL opens empty, saying NULL: %q %q", g.editing.entry.Text, g.editing.entry.PlaceHolder)
	}
	g.editing.entry.TypedKey(key(fyne.KeyReturn))
	if len(*got) != 0 || p.Len() != 0 {
		t.Errorf("opening and closing a cell changes nothing, NULL included: %v", *got)
	}
}

func TestAValueThatCannotBeReadKeepsItsEditorOpen(t *testing.T) {
	g, _, got := editGrid(t)
	click(g, 1, 5, 0) // quantity, a whole number
	g.EditCell("lots")
	e := g.editing
	e.entry.TypedKey(key(fyne.KeyReturn))
	if len(*got) != 0 || g.editing != e {
		t.Fatalf("text that is not a whole number is not written, and the editor stays: %v", *got)
	}
	if !g.tip.Visible() || g.tipText.Text != "quantity: not a whole number" {
		t.Errorf("it says why under the cell: %v %q", g.tip.Visible(), g.tipText.Text)
	}
	g.focus(g.table)
	if g.editing != e || e.done {
		t.Error("leaving it does not lose what was typed")
	}
	click(g, 2, 2, 0)
	if g.EditCell("") || g.editing != e || focused(g) != e.entry {
		t.Error("no other cell is edited while one cannot be written; it takes the keyboard again")
	}
	e.entry.TypedKey(key(fyne.KeyEscape))
	if g.editing != nil || g.tip.Visible() {
		t.Error("Escape gives up on it")
	}
}

// Edit Cell can come from the menu after the cell selected has been
// scrolled away; the editor must be drawn to take the keyboard.
func TestAnEditorScrollsItsCellIntoView(t *testing.T) {
	g, _, _ := editGrid(t)
	click(g, 1, 7, 0) // total, past the window's width
	g.Table.ScrollToLeading()
	g.EditCell("")
	if g.editing == nil || focused(g) != g.editing.entry {
		t.Errorf("the editor should be in view with the keyboard: %v", focused(g))
	}
}

func TestLeavingTheEditorWritesIt(t *testing.T) {
	g, _, got := editGrid(t)
	click(g, 1, 2, 0)
	g.EditCell("Moved Away")
	// The focus goes out of the grid, which then draws nothing of its own
	// accord.
	fyne.CurrentApp().Driver().CanvasForObject(g.table).Unfocus()
	if len(*got) != 1 || (*got)[0] != "Moved Away" || g.editing != nil {
		t.Errorf("leaving the cell writes it, as a spreadsheet does: %v", *got)
	}
	walk(g.View(), func(o fyne.CanvasObject) {
		if _, ok := o.(*editEntry); ok {
			t.Error("the cell shows its new value, not the editor")
		}
	})
}

func TestARefusalIsSaidUnderTheCell(t *testing.T) {
	g, _, _ := editGrid(t)
	g.OnEdit = func(model.Row, int, any) error { return errors.New("refused") }
	click(g, 1, 2, 0)
	g.EditCell("x")
	g.editing.entry.TypedKey(key(fyne.KeyReturn))
	if g.editing == nil || g.tipText.Text != "customer: refused" {
		t.Errorf("open %v, saying %q", g.editing != nil, g.tipText.Text)
	}
}

func TestAYesOrNoIsPickedFromAMenu(t *testing.T) {
	g, p, got := editGrid(t)
	row := rowOf(t, g, 1) // id 2 is not a priority
	click(g, 1, 8, 0)
	g.table.TypedKey(key(fyne.KeyReturn))
	e := g.editing
	if e == nil || e.menu == nil || e.entry != nil {
		t.Fatal("a bool is picked, not typed")
	}
	if !e.menu.Visible() || len(e.items) != 2 || e.items[0].Label != "true" || !e.items[1].Checked {
		t.Fatalf("a menu of true and false, false ticked: shown %v, %d items", e.menu.Visible(), len(e.items))
	}
	if focused(g) != e.menu {
		t.Error("the menu takes the keyboard")
	}
	e.menu.TypedKey(key(fyne.KeyDown))
	e.menu.TypedKey(key(fyne.KeyReturn))
	if len(*got) != 1 || (*got)[0] != true || g.editing != nil {
		t.Fatalf("picking writes it and closes: %v", *got)
	}
	if v, _ := p.Value(row, 8); v != true {
		t.Errorf("held: %v", v)
	}
	shown := false
	walk(g.View(), func(o fyne.CanvasObject) {
		if c, ok := o.(*cellWidget); ok && c.text == "true" && c.style.Bold {
			shown = true
		}
	})
	if !shown {
		t.Error("the cell on screen shows the value picked, as a change")
	}
	g.EditCell("")
	g.editing.menu.TypedKey(key(fyne.KeyEscape))
	if g.editing != nil || len(*got) != 1 {
		t.Error("dismissing the menu leaves the cell as it was")
	}
}

func TestADeletedRowOrAGridThatDoesNotEditOpensNoEditor(t *testing.T) {
	g, p, _ := editGrid(t)
	p.Delete(rowOf(t, g, 2))
	click(g, 2, 1, 0)
	if g.CanEditCell() || g.EditCell("") {
		t.Error("a row to be deleted is not edited")
	}
	plain := selectingGrid(t)
	click(plain, 1, 1, 0)
	plain.table.TypedKey(key(fyne.KeyReturn))
	if plain.CanEditCell() || plain.editing != nil {
		t.Error("a grid with no OnEdit edits nothing")
	}
	f := rowsFetcher{cols: []model.ColumnDef{{Name: "id", Type: model.DataType{Class: model.TypeInteger}},
		{Name: "data", Type: model.DataType{Class: model.TypeBytes}}}, rows: []model.Row{{int64(1), []byte{1}}}}
	bin := NewTableGridWith(context.Background(), NewModel(f), theme.Light, (&uithread.Queue{}).Run, 0)
	test.NewTempWindow(t, bin.View())
	pendingGrid(t, bin)
	bin.OnEdit = func(model.Row, int, any) error { return nil }
	click(bin, 0, 1, 0)
	if bin.CanEditCell() {
		t.Error("bytes are not typed")
	}
}

func TestTheEditorMovesWithItsCell(t *testing.T) {
	g, _, _ := editGrid(t)
	click(g, 1, 2, 0)
	g.EditCell("")
	c := drawn(g, 1, 2)
	g.UpdateCell(widget.TableCellID{Row: 30, Col: 2}, c)
	if c.editor != nil {
		t.Error("a cell the table moves elsewhere gives up the editor")
	}
	if drawn(g, 1, 2).editor == nil {
		t.Error("the cell drawn at its place takes it")
	}
}
