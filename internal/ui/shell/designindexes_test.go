package shell

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// indexBoxes are the boxes an index is drawn in, which come after the
// columns' and the constraints'.
func indexBoxes(tb *tab) []*widget.Entry {
	boxes := entriesIn(tb.body)
	// Columns, then the primary key's two, then one row of two for each
	// unique, then the indexes. The fake's table has no uniques, foreign
	// keys or checks, so what is left after the key is the indexes'.
	at := len(tb.design.design.Columns())*boxesPerColumn + 2
	if at > len(boxes) {
		return nil
	}
	return boxes[at:]
}

func TestTheDesignerDrawsTheIndexItRead(t *testing.T) {
	_, tb, _ := designing(t)
	boxes := indexBoxes(tb)
	if len(boxes) < 5 {
		t.Fatalf("the index drew %d boxes", len(boxes))
	}
	if boxes[0].Text != "name" {
		t.Errorf("the index is on %q", boxes[0].Text)
	}
	if boxes[1].Text != "items_name" {
		t.Errorf("the index is called %q", boxes[1].Text)
	}
	if findButton(tb.body, "Add Index") == nil {
		t.Error("there is no Add Index")
	}
}

func TestChangingAnIndexIsAChange(t *testing.T) {
	_, tb, p := designing(t)
	// An index row draws its columns and name, then its method, predicate
	// and included columns.
	indexBoxes(tb)[2].SetText("hash")

	changes := p.design.IndexChanges()
	if len(changes) != 1 || changes[0].Change != 2 { // ColumnAltered
		t.Fatalf("it reads %+v", changes)
	}
	if changes[0].Now.Method != "hash" {
		t.Errorf("its method reads %q", changes[0].Now.Method)
	}
	if got := tb.footer.Text; !strings.Contains(got, "index items_name changed") {
		t.Errorf("the footer says %q", got)
	}
}

// An index's order is part of what it answers, so DESC is typed with the
// column and read back with it.
func TestAnIndexsDirectionIsTypedWithItsColumn(t *testing.T) {
	_, tb, p := designing(t)
	indexBoxes(tb)[0].SetText("name DESC, id")

	got := p.design.Indexes()[0]
	if len(got.Columns) != 2 {
		t.Fatalf("it is on %+v", got.Columns)
	}
	if got.Columns[0].Name != "name" || !got.Columns[0].Descending {
		t.Errorf("its first column is %+v", got.Columns[0])
	}
	if got.Columns[1].Name != "id" || got.Columns[1].Descending {
		t.Errorf("its second column is %+v", got.Columns[1])
	}
	// And it is drawn back the way it was typed. The row has to be drawn
	// again for that to mean anything: read straight after typing, the box
	// holds what was typed whatever the design made of it.
	test.Tap(findButton(tb.body, "Add Index"))
	if want := "name DESC, id"; indexBoxes(tb)[0].Text != want {
		t.Errorf("it is drawn as %q", indexBoxes(tb)[0].Text)
	}
}

func TestAddingAndRemovingAnIndex(t *testing.T) {
	fx, tb, p := designing(t)
	test.Tap(findButton(tb.body, "Add Index"))
	pump(t, fx.q, func() bool { return len(p.design.Indexes()) == 2 })
	if got := tb.footer.Text; !strings.Contains(got, "index added") {
		t.Errorf("the footer says %q", got)
	}

	// The new one goes away again, leaving nothing behind.
	removes := buttonsNamed(tb.body, "Remove")
	test.Tap(removes[len(removes)-1])
	if got := p.design.IndexChanges(); len(got) != 0 {
		t.Errorf("adding an index and removing it reads as %+v", got)
	}
	if got := tb.footer.Text; got != "No changes yet." {
		t.Errorf("the footer says %q", got)
	}
}

// An index that cannot be made says so, and leaves the one that was there.
func TestAnIndexOnAColumnNobodyHasSaysSo(t *testing.T) {
	_, tb, p := designing(t)
	indexBoxes(tb)[0].SetText("nobody")

	if got := tb.footer.Text; !strings.Contains(got, "no column called") {
		t.Errorf("the footer says %q", got)
	}
	if got := p.design.Indexes(); len(got) != 1 || got[0].Columns[0].Name != "name" {
		t.Errorf("the index is now %+v", got)
	}
}

// An expression is the engine's own language and is kept as it was typed.
func TestAnIndexOverAnExpressionIsKeptAsTyped(t *testing.T) {
	_, tb, p := designing(t)
	indexBoxes(tb)[0].SetText("(lower(name))")

	got := p.design.Indexes()[0]
	if got.Columns[0].Expression != "(lower(name))" || got.Columns[0].Name != "" {
		t.Errorf("it is on %+v", got.Columns[0])
	}
}
