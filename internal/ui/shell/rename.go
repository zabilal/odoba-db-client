package shell

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer/view"
)

// dependsTimeout bounds reading what depends on an object. It is a catalogue
// query and a search of routine bodies, so it is quick on a small schema and
// not instant on a large one.
const dependsTimeout = 10 * time.Second

// Renaming an object, and saying first what it costs (FR-6.6).
//
// What makes a rename worth warning about is not that things refer to the
// object — most things do, and most of them are carried along untouched.
// It is that a few of them hold the name as text nobody will re-resolve, and
// those break silently: the next call fails, in a body somebody wrote months
// ago, at whatever hour it next runs.
//
// So the warning is read before the name is typed, not after. What depends
// on the object does not depend on what it is about to be called, so there
// is no reason to make somebody commit to a name first and only then find
// out what it costs. It is read as soon as the dialog opens, and the ones
// that break are shown first and on their own.

// canRenameSelected reports whether there is an object selected that this
// connection could rename.
func (s *Shell) canRenameSelected() bool {
	conn, n, ok := s.structureTarget()
	return ok && s.couldRename(conn, n.Ref)
}

// couldRename asks the connection, which is the only thing that knows which
// kinds it can rename.
func (s *Shell) couldRename(connID string, ref model.ObjectRef) bool {
	live, open := s.d.WS.Get(connID)
	return open && app.CanRename(live.Source, ref)
}

func (s *Shell) renameSelected() {
	conn, n, ok := s.structureTarget()
	if !ok {
		return
	}
	if !s.couldRename(conn, n.Ref) {
		return
	}
	s.askToRename(conn, n.Ref)
}

// askToRename puts up the dialog: a name to type, and what it costs.
func (s *Shell) askToRename(connID string, ref model.ObjectRef) {
	was := ref.Name()
	entry := widget.NewEntry()
	entry.SetText(was)
	entry.Validator = func(text string) error {
		switch text = strings.TrimSpace(text); {
		case text == "":
			return errors.New("a rename needs a name")
		case text == was:
			return errors.New("that is the name it already has")
		}
		return nil
	}

	// The warning starts as a question, not as silence: an empty space here
	// would read as "nothing depends on this", which nothing has established
	// yet.
	cost := widget.NewLabel("Reading what depends on it…")
	cost.Wrapping = fyne.TextWrapWord
	costs := container.NewVBox(cost)

	d := dialog.NewForm(renameTitle(ref.Kind), "Rename…", "Cancel",
		[]*widget.FormItem{
			{Text: "New name", Widget: entry},
			{Widget: costs},
		}, func(ok bool) {
			if !ok {
				return
			}
			s.previewRename(connID, ref, strings.TrimSpace(entry.Text))
		}, s.win)
	d.Resize(fyne.NewSize(560, d.MinSize().Height))
	d.Show()

	go func() {
		ctx, cancel := context.WithTimeout(s.ctx, dependsTimeout)
		defer cancel()
		live, err := s.d.WS.Connect(ctx, connID)
		var deps []model.Dependent
		if err == nil {
			deps, err = app.DependentsOf(ctx, live.Source, ref)
		}
		s.d.Run(func() {
			costs.Objects = costOf(deps, err, was)
			costs.Refresh()
			d.Resize(fyne.NewSize(560, d.MinSize().Height))
		})
	}()
}

// costOf is what the dialog says about a rename, once it is known.
func costOf(deps []model.Dependent, err error, was string) []fyne.CanvasObject {
	switch {
	case errors.Is(err, app.ErrNoDependencies):
		// Not a failure, and it must not read as one: this engine has no way
		// to be asked, which is a different thing from having been asked and
		// having answered nothing.
		return []fyne.CanvasObject{
			warn("Nothing here has been checked: this connection cannot say what names “" + was + "”."),
		}
	case err != nil:
		return []fyne.CanvasObject{warn("Could not read what depends on it: " + err.Error())}
	}

	breaks := app.Breaking(deps)
	if len(breaks) == 0 {
		if len(deps) == 0 {
			return []fyne.CanvasObject{quiet("Nothing else names “" + was + "”.")}
		}
		return []fyne.CanvasObject{
			quiet(fmt.Sprintf("%s names “%s”, and %s carried along by the rename.",
				nounCount(len(deps), "other object"), was, isAre(len(deps)))),
			quiet(listOf(deps)),
		}
	}

	out := []fyne.CanvasObject{
		warn(fmt.Sprintf("%s will break: %s named in text the server never resolved, so the rename will not reach %s.",
			nounCount(len(breaks), "object"), isAre(len(breaks))+" it", them(len(breaks)))),
		quiet(listOf(breaks)),
	}
	if rest := len(deps) - len(breaks); rest > 0 {
		out = append(out, quiet(fmt.Sprintf("%s also names it and %s carried along.",
			nounCount(rest, "other object"), isAre(rest))))
	}
	return out
}

// previewRename renders the statements and shows them, like every other
// structural change: what is read is what runs (ADR-0115).
func (s *Shell) previewRename(connID string, ref model.ObjectRef, to string) {
	live, ok := s.d.WS.Get(connID)
	if !ok {
		s.status.SetText("This connection is not open.")
		return
	}
	stmts, err := app.PlanRename(live.Source, ref, to)
	if err != nil {
		s.showError(err)
		return
	}
	if len(stmts) == 0 {
		s.status.SetText("That is the name it already has.")
		return
	}
	target := ddlTarget{connID: connID, ctx: s.ctx, say: s.status.SetText}
	s.previewDDL(target, stmts, func() {
		// The tree is what showed the old name, so it is what has to be
		// read again. Nothing here assumes the rename took: the refresh
		// asks the server what is there now.
		s.Explorer.Refresh(view.NodeID(connID, classFolderOf(ref)))
	})
}

// listOf names the objects, one per line, for the dialog to show.
func listOf(deps []model.Dependent) string {
	names := make([]string, len(deps))
	for i, d := range deps {
		names[i] = "· " + d.Label + " — " + d.Note
	}
	return strings.Join(names, "\n")
}

// warn is a line the reader is meant to stop at.
func warn(text string) fyne.CanvasObject {
	l := widget.NewLabel(text)
	l.Wrapping = fyne.TextWrapWord
	l.Importance = widget.WarningImportance
	return l
}

func isAre(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

func them(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}

// classFolderOf is the tree folder an object is listed in.
//
// It is the schema's folder for the object's kind, and not the object's
// parent in the path: a trigger's path carries the table it is on, because
// its name is only unique there, but the tree lists every schema's triggers
// together.
func classFolderOf(ref model.ObjectRef) model.ObjectRef {
	if len(ref.Path) < 2 {
		return ref
	}
	return model.ClassRef(model.NewRef(model.KindSchema, ref.Path[0], ref.Path[1]), ref.Kind)
}

// kindWord is what to call one object of a kind, for a title. The class
// labels beside it in the tree are plural — Tables, Indexes — and there is
// no rule that turns those back into the singular: table is not tabl.
var kindWord = map[model.ObjectKind]string{
	model.KindTable:            "Table",
	model.KindView:             "View",
	model.KindMaterializedView: "Materialized View",
	model.KindSequence:         "Sequence",
	model.KindIndex:            "Index",
	model.KindRoutine:          "Routine",
	model.KindTrigger:          "Trigger",
}

// renameTitle names what is being renamed, falling back to the plain word
// for a kind this has no name for.
func renameTitle(k model.ObjectKind) string {
	if w, ok := kindWord[k]; ok {
		return "Rename " + w
	}
	return "Rename"
}
