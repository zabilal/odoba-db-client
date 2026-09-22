package shell

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// keyBoxes are the boxes the keys and constraints are drawn in, which come
// after the columns'.
func keyBoxes(tb *tab) []*widget.Entry {
	boxes := entriesIn(tb.body)
	cols := len(tb.design.design.Columns()) * boxesPerColumn
	if cols > len(boxes) {
		return nil
	}
	return boxes[cols:]
}

func TestTheDesignerDrawsTheKeyItRead(t *testing.T) {
	_, tb, _ := designing(t)
	boxes := keyBoxes(tb)
	if len(boxes) < 2 {
		t.Fatalf("the keys drew %d boxes", len(boxes))
	}
	// The primary key comes first: its columns, then its name.
	if boxes[0].Text != "id" {
		t.Errorf("the key is on %q", boxes[0].Text)
	}
	if boxes[1].Text != "items_pkey" {
		t.Errorf("the key is called %q", boxes[1].Text)
	}
	// And every kind is offered, so a table with none of one still has
	// somewhere to make one.
	for _, want := range []string{"Add Unique Constraint", "Add Foreign Key", "Add Check Constraint"} {
		if findButton(tb.body, want) == nil {
			t.Errorf("there is no %q", want)
		}
	}
}

func TestChangingTheKeyIsAChange(t *testing.T) {
	_, tb, p := designing(t)
	keyBoxes(tb)[1].SetText("items_key") // the key's name

	changes := p.design.ConstraintChanges()
	if len(changes) != 1 {
		t.Fatalf("it reads %+v", changes)
	}
	if got := tb.footer.Text; !strings.Contains(got, "primary key items_pkey changed") {
		t.Errorf("the footer says %q", got)
	}
}

// A key on a nullable column is refused, and the footer says why rather than
// the design quietly taking it.
func TestAKeyOnANullableColumnSaysWhy(t *testing.T) {
	_, tb, p := designing(t)
	// The fake's name column is nullable.
	keyBoxes(tb)[0].SetText("name")
	if got := tb.footer.Text; !strings.Contains(got, "nullable") {
		t.Errorf("the footer says %q", got)
	}
	if got := p.design.ConstraintChanges(); len(got) != 0 {
		t.Errorf("a refused key changed the design: %+v", got)
	}
}

// Clearing the key's columns is how it is removed by typing, which is what
// somebody does before reading the Remove button beside it.
func TestClearingTheKeysColumnsRemovesIt(t *testing.T) {
	_, tb, p := designing(t)
	keyBoxes(tb)[0].SetText("")

	changes := p.design.ConstraintChanges()
	if len(changes) != 1 || changes[0].Change != 1 { // ColumnDropped
		t.Fatalf("it reads %+v", changes)
	}
	if got := tb.footer.Text; !strings.Contains(got, "dropped") {
		t.Errorf("the footer says %q", got)
	}
}

func TestAddingEachKindOfConstraint(t *testing.T) {
	fx, tb, p := designing(t)
	for _, c := range []struct{ button, says string }{
		{"Add Unique Constraint", "unique constraint added"},
		{"Add Foreign Key", "foreign key added"},
		{"Add Check Constraint", "check constraint added"},
	} {
		test.Tap(findButton(tb.body, c.button))
		pump(t, fx.q, func() bool { return strings.Contains(tb.footer.Text, c.says) })
	}
	if got := len(p.design.ConstraintChanges()); got != 3 {
		t.Errorf("three constraints added read as %d changes", got)
	}
}

func TestRemovingAConstraintTheTableWasReadWith(t *testing.T) {
	fx, tb, p := designing(t)
	test.Tap(findButton(tb.body, "Add Unique Constraint"))
	pump(t, fx.q, func() bool { return len(p.design.ConstraintChanges()) == 1 })

	// The one that was just added goes away again, leaving nothing behind.
	removes := buttonsNamed(tb.body, "Remove")
	test.Tap(removes[len(removes)-1])
	if got := p.design.ConstraintChanges(); len(got) != 0 {
		t.Errorf("adding a constraint and removing it reads as %+v", got)
	}
	if got := tb.footer.Text; got != "No changes yet." {
		t.Errorf("the footer says %q", got)
	}
}

// A foreign key's referred table is typed as schema.table, and read back
// apart so that a statement can qualify it.
func TestAForeignKeysReferredTableIsReadApart(t *testing.T) {
	fx, tb, p := designing(t)
	test.Tap(findButton(tb.body, "Add Foreign Key"))
	pump(t, fx.q, func() bool { return len(p.design.ConstraintChanges()) == 1 })

	boxes := keyBoxes(tb)
	var refTable *widget.Entry
	for _, b := range boxes {
		if b.Text == "another_table" {
			refTable = b
		}
	}
	if refTable == nil {
		t.Fatal("the new key names no table to refer to")
	}
	refTable.SetText("public.owners")

	_, _, keys, _ := p.design.Constraints()
	if len(keys) != 1 {
		t.Fatalf("the design holds %d keys", len(keys))
	}
	if keys[0].RefSchema != "public" || keys[0].RefTable != "owners" {
		t.Errorf("it refers to %q.%q", keys[0].RefSchema, keys[0].RefTable)
	}
}

// What a key does when the row it refers to goes is chosen from the five the
// standard names, because there are five and nobody should have to spell
// them.
func TestWhatAForeignKeyDoesIsChosen(t *testing.T) {
	fx, tb, p := designing(t)
	test.Tap(findButton(tb.body, "Add Foreign Key"))
	pump(t, fx.q, func() bool { return len(p.design.ConstraintChanges()) == 1 })

	var picked bool
	for _, sel := range selectsIn(tb.body) {
		if len(sel.Options) == len(actions) && sel.Options[3] == "CASCADE" {
			sel.SetSelected("CASCADE")
			picked = true
			break
		}
	}
	if !picked {
		t.Fatal("nothing offers what a key does")
	}
	_, _, keys, _ := p.design.Constraints()
	if keys[0].OnDelete != "CASCADE" {
		t.Errorf("on delete it does %q", keys[0].OnDelete)
	}
}

// Columns are typed comma separated, and their order is kept: a key on
// (a, b) is not a key on (b, a).
func TestAKeysColumnsKeepTheirOrder(t *testing.T) {
	_, tb, p := designing(t)
	notNull := p.design.Columns()[1]
	notNull.Type.Nullable = false
	if err := p.design.ChangeColumn("name", notNull); err != nil {
		t.Fatal(err)
	}
	keyBoxes(tb)[0].SetText("name, id")

	pk, _, _, _ := p.design.Constraints()
	if pk == nil || len(pk.Columns) != 2 || pk.Columns[0] != "name" || pk.Columns[1] != "id" {
		t.Errorf("the key is on %v", pk)
	}
}

// selectsIn is every chooser under o, in the order they are drawn.
func selectsIn(o fyne.CanvasObject) []*widget.Select {
	var out []*widget.Select
	var walk func(fyne.CanvasObject)
	walk = func(o fyne.CanvasObject) {
		switch v := o.(type) {
		case nil:
			return
		case *widget.Select:
			out = append(out, v)
		case *fyne.Container:
			for _, c := range v.Objects {
				walk(c)
			}
		case fyne.Widget:
			for _, c := range test.WidgetRenderer(v).Objects() {
				walk(c)
			}
		}
	}
	walk(o)
	return out
}
