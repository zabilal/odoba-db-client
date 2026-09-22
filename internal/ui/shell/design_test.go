package shell

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// designing opens the column editor on the fake's items table and waits for
// it to have read it.
func designing(t *testing.T) (*fixture, *tab, *designPanel) {
	t.Helper()
	fx := newFixture(t)
	c := fx.create(t, "db1", nil)
	tb := fx.s.OpenDesign(c.ID, itemsNode)
	pump(t, fx.q, func() bool { return tb.design != nil })
	return fx, tb, tb.design
}

// boxesPerColumn is how many text boxes one column's row draws: its name,
// its type, its default and its comment.
const boxesPerColumn = 4

// buttonsNamed is every button under o with this label, which is how a row's
// own Remove is told from the next one's.
func buttonsNamed(o fyne.CanvasObject, text string) []*widget.Button {
	var out []*widget.Button
	var walk func(fyne.CanvasObject)
	walk = func(o fyne.CanvasObject) {
		switch v := o.(type) {
		case nil:
			return
		case *widget.Button:
			if v.Text == text {
				out = append(out, v)
			}
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

func TestADesignerOpensOnATablesColumns(t *testing.T) {
	_, tb, p := designing(t)
	if got := len(p.design.Columns()); got != 2 {
		t.Fatalf("it opened on %d columns", got)
	}
	if tb.item.Text != "Design: items" {
		t.Errorf("the tab is called %q", tb.item.Text)
	}
	if got := tb.footer.Text; got != "No changes yet." {
		t.Errorf("a designer opens saying %q", got)
	}

	// The columns are drawn as they were read, the engine's own word for the
	// type among them.
	boxes := entriesIn(tb.body)
	if len(boxes) < 8 {
		t.Fatalf("two columns drew %d boxes", len(boxes))
	}
	if boxes[0].Text != "id" || boxes[1].Text != "integer" {
		t.Errorf("the first column reads %q %q", boxes[0].Text, boxes[1].Text)
	}
	if boxes[4].Text != "name" || boxes[5].Text != "text" {
		t.Errorf("the second column reads %q %q", boxes[4].Text, boxes[5].Text)
	}
}

// The designer changes a table on paper and nothing on the server: until the
// preview exists there is nothing to run, and nothing must reach one.
func TestADesignerRunsNothing(t *testing.T) {
	fx, tb, _ := designing(t)
	before := len(writtenPlans())
	entriesIn(tb.body)[0].SetText("identifier")
	pump(t, fx.q, func() bool { return strings.Contains(tb.footer.Text, "renamed") })
	if got := len(writtenPlans()); got != before {
		t.Errorf("designing a column wrote %d plans", got-before)
	}
}

func TestRenamingAColumnSaysSoAndIsNotADropAndAnAdd(t *testing.T) {
	_, tb, p := designing(t)
	entriesIn(tb.body)[0].SetText("identifier")

	changes := p.design.Changes()
	if len(changes) != 1 || !changes[0].Renamed() {
		t.Fatalf("it reads %+v", changes)
	}
	if got := tb.footer.Text; !strings.Contains(got, "id renamed to identifier") {
		t.Errorf("the footer says %q", got)
	}
	// And renaming it again follows the same column rather than looking for
	// one under the name it no longer has.
	entriesIn(tb.body)[0].SetText("ident")
	if got := len(p.design.Changes()); got != 1 {
		t.Errorf("renaming twice made %d changes", got)
	}
	if got := p.design.Columns()[0].Name; got != "ident" {
		t.Errorf("after renaming twice the column is called %q", got)
	}
	if got := tb.footer.Text; !strings.Contains(got, "id renamed to ident") {
		t.Errorf("the footer says %q", got)
	}
}

func TestChangingATypeAndANullAndAComment(t *testing.T) {
	_, tb, p := designing(t)
	boxes := entriesIn(tb.body)
	boxes[5].SetText("varchar(80)") // the name column's type
	boxes[7].SetText("what they are called")

	changes := p.design.Changes()
	if len(changes) != 1 || changes[0].Kind != 2 { // ColumnAltered
		t.Fatalf("it reads %+v", changes)
	}
	to := changes[0].To
	if to.Type.Native != "varchar(80)" {
		t.Errorf("the type reads %q", to.Type.Native)
	}
	// A type typed over by hand cannot keep the old length, or a column
	// widened from varchar(40) to varchar(80) would still say 40.
	if to.Type.Length != -1 {
		t.Errorf("the length is %d, and a retyped column knows none until the server says", to.Type.Length)
	}
	if to.Comment != "what they are called" {
		t.Errorf("the comment reads %q", to.Comment)
	}
}

func TestAddingAndRemovingColumns(t *testing.T) {
	fx, tb, p := designing(t)
	test.Tap(findButton(tb.body, "Add Column"))
	pump(t, fx.q, func() bool { return len(p.design.Columns()) == 3 })
	if got := tb.footer.Text; !strings.Contains(got, "added") {
		t.Errorf("the footer says %q", got)
	}

	// A new column is drawn like any other, and can be named at once. Its
	// boxes are counted from the front: the keys and constraints are drawn
	// after the columns, so counting from the back lands among them.
	entriesIn(tb.body)[2*boxesPerColumn].SetText("nickname")
	if got := p.design.Columns()[2].Name; got != "nickname" {
		t.Errorf("the new column is called %q", got)
	}

	// Removing a column that was added leaves the table as it was read: it
	// is not a drop, because there was nothing there to drop.
	if removes := buttonsNamed(tb.body, "Remove"); len(removes) > 2 {
		test.Tap(removes[2]) // the new column's own Remove
	} else {
		t.Fatalf("three columns drew %d Remove buttons", len(removes))
	}
	if got := tb.footer.Text; got != "No changes yet." {
		t.Errorf("adding a column and removing it says %q", got)
	}

	// A column an index names cannot be dropped out from under it.
	test.Tap(buttonsNamed(tb.body, "Remove")[1]) // the name column's
	if got := tb.footer.Text; !strings.Contains(got, "part of the index") {
		t.Errorf("the footer says %q", got)
	}

	// With the index gone it can go, and that is a drop.
	if err := p.design.DropIndex("items_name"); err != nil {
		t.Fatal(err)
	}
	test.Tap(buttonsNamed(tb.body, "Remove")[1])
	if got := tb.footer.Text; !strings.Contains(got, "name dropped") {
		t.Errorf("the footer says %q", got)
	}
}

// Dropping a column the primary key is made of would take the key with it,
// which is a larger change than the one asked for.
func TestRemovingAKeyColumnIsRefusedAndSaysWhy(t *testing.T) {
	_, tb, p := designing(t)
	test.Tap(buttonsNamed(tb.body, "Remove")[0]) // the id column's
	if got := len(p.design.Columns()); got != 2 {
		t.Errorf("the key's column was dropped: %d columns left", got)
	}
	if got := tb.footer.Text; !strings.Contains(got, "primary key") {
		t.Errorf("the footer says %q", got)
	}
}

func TestRevertingPutsTheTableBack(t *testing.T) {
	fx, tb, p := designing(t)
	entriesIn(tb.body)[0].SetText("identifier")
	test.Tap(findButton(tb.body, "Add Column"))
	pump(t, fx.q, func() bool { return len(p.design.Changes()) == 2 })

	test.Tap(findButton(tb.body, "Revert All"))
	if p.design.Changed() {
		t.Errorf("after reverting: %+v", p.design.Changes())
	}
	if got := tb.footer.Text; got != "No changes yet." {
		t.Errorf("the footer says %q", got)
	}
	if got := entriesIn(tb.body)[0].Text; got != "id" {
		t.Errorf("the first column reads %q", got)
	}
}

// Revert All is only offered when there is something to revert, so that it
// is never a button that does nothing.
func TestRevertAllIsOfferedOnlyWhenThereIsSomethingToRevert(t *testing.T) {
	_, tb, _ := designing(t)
	undo := findButton(tb.body, "Revert All")
	if !undo.Disabled() {
		t.Error("Revert All is live on a design nobody has changed")
	}
	entriesIn(tb.body)[0].SetText("identifier")
	if undo.Disabled() {
		t.Error("Revert All is out of reach on a design that has changed")
	}
}

// Designing the same table twice brings the first tab forward rather than
// opening a second design of it, which would let two of them disagree.
func TestDesigningTheSameTableTwiceOpensOneTab(t *testing.T) {
	fx, tb, _ := designing(t)
	again := fx.s.OpenDesign(tb.connID, itemsNode)
	if again != tb {
		t.Error("designing the same table twice opened a second tab")
	}
	if got := len(fx.s.open); got != 1 {
		t.Errorf("%d tabs are open", got)
	}
}

// A comment is the one field somebody changes on its own, to say what a
// column is for. It must count as a change like any other.
func TestChangingOnlyAComment(t *testing.T) {
	_, tb, p := designing(t)
	entriesIn(tb.body)[7].SetText("what they are called") // the name column's comment

	changes := p.design.Changes()
	if len(changes) != 1 {
		t.Fatalf("changing a comment alone reads as %+v", changes)
	}
	if changes[0].To.Comment != "what they are called" {
		t.Errorf("the comment reads %q", changes[0].To.Comment)
	}
	if got := tb.footer.Text; !strings.Contains(got, "name changed") {
		t.Errorf("the footer says %q", got)
	}
}
