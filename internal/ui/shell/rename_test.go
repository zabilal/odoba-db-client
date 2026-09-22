package shell

import (
	"errors"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer/view"
)

// Renaming an object, and reading what it costs first (FR-6.6).

// renaming opens the dialog on a node, with a connection already open, and
// waits until what depends on it has been read.
func renaming(t *testing.T, n model.Node) (*fixture, fyne.CanvasObject) {
	t.Helper()
	forgetStatements()
	fx := newFixture(t)
	c := fx.create(t, "db1", nil)
	// Opening a design is how the connection comes up; the rename itself is
	// asked for from the tree, which has no tab of its own.
	tb := fx.s.OpenDesign(c.ID, itemsNode)
	pump(t, fx.q, func() bool { return tb.design != nil })
	fx.s.Explorer.Tree.Select(view.NodeID(c.ID, n.Ref))

	fx.s.askToRename(c.ID, n.Ref)
	pump(t, fx.q, func() bool {
		top := fx.s.win.Canvas().Overlays().Top()
		return top != nil && !strings.Contains(labelText(top), "Reading what depends")
	})
	return fx, fx.s.win.Canvas().Overlays().Top()
}

// What breaks is read before a name is typed, because what depends on the
// object does not depend on what it is about to be called.
func TestARenameSaysWhatItBreaksBeforeANameIsTyped(t *testing.T) {
	_, top := renaming(t, itemsNode)
	said := labelText(top)
	if !strings.Contains(said, "1 object will break") {
		t.Errorf("the dialog says %q", said)
	}
	if !strings.Contains(said, "total()") {
		t.Errorf("it does not name what breaks: %q", said)
	}
	// The one that is carried is said too, and separately, because a
	// warning that lumped them together would be a warning about nothing.
	if !strings.Contains(said, "1 other object also names it") {
		t.Errorf("it does not account for what is carried: %q", said)
	}
	if got := len(ranStatements()); got != 0 {
		t.Errorf("%d statements ran while the dialog was only being read", got)
	}
}

// Nothing depending on it is said as that, and not as silence.
func TestARenameWithNothingDependingOnIt(t *testing.T) {
	node := model.Node{Ref: model.NewRef(model.KindTable, "main", "nothing"), Label: "nothing", Browsable: true}
	_, top := renaming(t, node)
	if said := labelText(top); !strings.Contains(said, "Nothing else names") {
		t.Errorf("the dialog says %q", said)
	}
}

// A connection that cannot say what depends is not a connection saying
// nothing depends. The difference is the whole warning.
func TestAConnectionThatCannotSayWhatDependsSaysSo(t *testing.T) {
	said := textOf(costOf(nil, app.ErrNoDependencies, "items"))
	if !strings.Contains(said, "Nothing here has been checked") {
		t.Errorf("it says %q", said)
	}
	// And it does not read as a failure, because it is not one: this engine
	// has no way of being asked.
	if strings.Contains(said, "Could not read") {
		t.Errorf("having no way to ask is reported as a failure: %q", said)
	}
	if broke := textOf(costOf(nil, errors.New("the server hung up"), "items")); !strings.Contains(broke, "Could not read") ||
		!strings.Contains(broke, "hung up") {
		t.Errorf("a failure says %q", broke)
	}
}

// The name is validated before the button works: an empty one, or the name
// it already has, is not a rename.
func TestTheNewNameIsCheckedBeforeAnythingRuns(t *testing.T) {
	_, top := renaming(t, itemsNode)
	e := entriesIn(top)
	if len(e) == 0 {
		t.Fatal("the dialog has nothing to type in")
	}
	if e[0].Text != "items" {
		t.Errorf("it opens on %q", e[0].Text)
	}
	for _, bad := range []string{"", "   ", "items"} {
		if err := e[0].Validator(bad); err == nil {
			t.Errorf("%q was accepted as a new name", bad)
		}
	}
	if err := e[0].Validator("folk"); err != nil {
		t.Errorf("a real name was refused: %v", err)
	}
}

// What is read is what runs, here as everywhere: the rename is previewed
// and the statement shown is the statement sent (ADR-0115).
func TestARenameIsPreviewedAndThenRunsExactlyThat(t *testing.T) {
	fx, top := renaming(t, itemsNode)
	e := entriesIn(top)[0]
	e.SetText("folk")
	tapOnTop(t, fx, "Rename…")
	pump(t, fx.q, func() bool {
		o := fx.s.win.Canvas().Overlays().Top()
		return o != nil && strings.Contains(previewText(o), "RENAME TO")
	})
	shown := previewText(fx.s.win.Canvas().Overlays().Top())
	if !strings.Contains(shown, "RENAME TO folk") {
		t.Errorf("the preview shows %q", shown)
	}
	if got := len(ranStatements()); got != 0 {
		t.Errorf("%d statements ran before the preview was answered", got)
	}

	tapOnTop(t, fx, "Run")
	pump(t, fx.q, func() bool { return len(ranStatements()) > 0 })
	if got := ranStatements()[0]; !strings.Contains(got, "RENAME TO folk") {
		t.Errorf("it ran %q, which is not what was read", got)
	}
}

// The command is offered for what this connection can rename, and not for
// what it cannot — which is the driver's answer and not a list kept here.
func TestWhichObjectsAreOfferedARename(t *testing.T) {
	fx := newFixture(t)
	c := selectItems(t, fx)
	if !fx.s.canRenameSelected() {
		t.Error("a table selected in the tree is not offered a rename")
	}
	if fx.s.menuItems[cmdRename].Disabled {
		t.Error("the menu item is disabled for a table")
	}
	// Which kinds is the connection's answer, so it is asked of the
	// connection rather than of the selection, which only ever holds one.
	for _, tc := range []struct {
		ref  model.ObjectRef
		want bool
	}{
		{itemsNode.Ref, true},
		{viewNode.Ref, true},
		{model.NewRef(model.KindSequence, "main", "items_id_seq"), true},
		{model.NewRef(model.KindDatabase, "main"), false},
		{model.NewRef(model.KindColumn, "main", "items", "name"), false},
	} {
		if got := fx.s.couldRename(c.ID, tc.ref); got != tc.want {
			t.Errorf("a %s is offered a rename: %v, want %v", tc.ref.Kind, got, tc.want)
		}
	}
}

// A connection that is not open offers no rename: nothing can say what it
// could do, and guessing would put a command up that fails when tapped.
func TestARenameIsNotOfferedOnAConnectionThatIsNotOpen(t *testing.T) {
	fx := newFixture(t)
	c := selectItems(t, fx)
	fx.s.release(c.ID, false)
	if fx.s.canRenameSelected() {
		t.Error("a closed connection offers a rename")
	}
}

// A trigger is listed under its schema's triggers and not under its table,
// so that is the folder read again after it is renamed.
func TestTheFolderReadAgainIsTheOneTheObjectIsListedIn(t *testing.T) {
	for _, tc := range []struct {
		ref  model.ObjectRef
		want []string
	}{
		{model.NewRef(model.KindTable, "db", "public", "people"), []string{"db", "public", "table"}},
		{model.NewRef(model.KindTrigger, "db", "public", "people", "audit"), []string{"db", "public", "trigger"}},
	} {
		got := classFolderOf(tc.ref)
		if got.Kind != model.KindFolder || strings.Join(got.Path, "/") != strings.Join(tc.want, "/") {
			t.Errorf("%s is listed in %v", tc.ref.Kind, got.Path)
		}
	}
}

// textOf is what a set of dialog lines reads as.
func textOf(objs []fyne.CanvasObject) string {
	var parts []string
	for _, o := range objs {
		if l, ok := o.(*widget.Label); ok {
			parts = append(parts, l.Text)
			continue
		}
		parts = append(parts, labelText(o))
	}
	return strings.Join(parts, "\n")
}
