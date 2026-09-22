package shell

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/diff"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/ui/filedlg"
)

// The side-by-side comparison (FR-7.2).
//
// The engine answers a tree in which everything appears, identical objects
// included (ADR-0119), because a tree of differences alone cannot be
// filtered into one that shows what did not change. This is what does the
// filtering, and it is why the whole tree is kept rather than trimmed on the
// way in: the filter changes what is drawn, never what was compared.
//
// The two sides are named the way the comparison reads them: the connection
// is what is there, and the other side is what is wanted, so Added is what
// this connection is missing.

// compareTimeout bounds reading both sides. A schema is many catalogue
// queries even in one pass, and two of them are two.
const compareTimeout = 3 * time.Minute

// show is which nodes a comparison draws.
type showing string

const (
	showEverything  showing = "Everything"
	showDifferences showing = "Differences"
	showAdded       showing = "Missing here"
	showRemoved     showing = "Only here"
	showChanged     showing = "Changed"
)

// statusOf is the status each filter keeps. Everything and Differences are
// not in it: they are about all of them, or all but one.
var statusOf = map[showing]diff.Status{
	showAdded:   diff.Added,
	showRemoved: diff.Removed,
	showChanged: diff.Changed,
}

// comparePanel is a comparison's state: the whole tree, and what is drawn.
type comparePanel struct {
	s    *Shell
	t    *tab
	root diff.Node

	// nodes and kids are the tree by the id the widget knows it as. A path
	// of names, because a comparison has no ids of its own and two objects
	// of different kinds can share a name.
	nodes map[string]diff.Node
	kids  map[string][]string
	// holds says which statuses are somewhere in a node's subtree, so that
	// filtering to one keeps the way down to it.
	holds map[string]map[diff.Status]bool

	filter  showing
	tree    *widget.Tree
	detail  *fyne.Container
	summary *widget.Label

	// live and wanted are the two models the comparison was made from. The
	// tree describes what differs and deliberately does not carry the
	// objects (ADR-0119), so writing a script needs them both again.
	live, wanted *model.Database
	// chosen is the differences ticked, by the names the comparison gives
	// them, which is what app.SyncScript is written from.
	chosen app.Selection
	// dir is the saved model this was compared against, kept so the
	// comparison can be made again after something has run.
	dir   string
	write *widget.Button
	save  *widget.Button
	apply *widget.Button
}

func compareKey(connID string, ref model.ObjectRef, against string) string {
	return "compare:" + connID + ":" + ref.String() + ":" + against
}

// canCompareSelected reports whether there is a database or schema selected
// whose structure can be read.
func (s *Shell) canCompareSelected() bool {
	conn, n, ok := s.Explorer.SelectedNode()
	if !ok || !holdsAClass[n.Ref.Kind] {
		return false
	}
	live, open := s.d.WS.Get(conn)
	return open && app.CanCompare(live.Source)
}

func (s *Shell) compareSelected() {
	conn, n, ok := s.Explorer.SelectedNode()
	if !ok || !holdsAClass[n.Ref.Kind] {
		return
	}
	s.askWhatToCompareAgainst(conn, n.Ref)
}

// askWhatToCompareAgainst asks for the other side: a saved model on disk.
//
// A model in version control is the case this is for — it is the one
// somebody reviewed and agreed — and it is also the only side that is
// certainly there, because another connection may not be open.
func (s *Shell) askWhatToCompareAgainst(connID string, ref model.ObjectRef) {
	s.d.Files.Open(s.win, filedlg.Options{
		Message:    "Compare " + ref.Name() + " against a saved model",
		Extensions: []string{"json"},
		Kind:       "saved model",
		Accept:     "Compare",
	}, func(path string, err error) {
		switch {
		case err != nil:
			s.showError(fmt.Errorf("could not choose a model: %w", err))
		case path == "":
			// Cancelled, which is an answer and not a failure.
		default:
			// The model is a tree of files and the chooser picks one of
			// them, so what was chosen names the directory it is in.
			s.OpenComparison(connID, ref, filepath.Dir(path))
		}
	})
}

// OpenComparison compares a database against a saved model and shows what
// differs, or brings the tab already on it forward.
func (s *Shell) OpenComparison(connID string, ref model.ObjectRef, dir string) *tab {
	key := compareKey(connID, ref, dir)
	if t := s.tabFor(key); t != nil {
		s.selectTab(t)
		return t
	}
	ctx, cancel := context.WithCancel(s.ctx)
	t := &tab{key: key, connID: connID, ref: ref, label: ref.Name(), structure: true, ctx: ctx, cancel: cancel,
		body: container.NewStack(quiet("Reading both sides…")), footer: widget.NewLabel("")}
	t.footer.Importance = widget.LowImportance
	t.item = container.NewTabItem("Compare: "+ref.Name(), container.NewBorder(nil, t.footer, nil, nil, t.body))
	s.open = append(s.open, t)
	s.addTab(t)
	s.sync()

	go func() {
		ctx, cancel := context.WithTimeout(ctx, compareTimeout)
		defer cancel()
		live, err := s.d.WS.Connect(ctx, connID)
		var got app.Comparison
		if err == nil {
			got, err = app.CompareWithSaved(ctx, live.Source, databaseOf(ref), dir)
		}
		s.d.Run(func() {
			if t.ctx.Err() != nil {
				return
			}
			if err != nil {
				s.tabFailed(t, fmt.Errorf("could not compare: %w", err))
				return
			}
			s.showComparison(t, got, dir)
		})
	}()
	return t
}

// databaseOf is the database a ref is in, which is the first thing in its
// path whatever depth the object sits at.
func databaseOf(ref model.ObjectRef) string {
	if len(ref.Path) == 0 {
		return ""
	}
	return ref.Path[0]
}

// showComparison draws the tree, the filter and the detail beside it.
func (s *Shell) showComparison(t *tab, got app.Comparison, dir string) {
	p := &comparePanel{s: s, t: t, root: got.Tree, filter: showDifferences,
		live: got.Live, wanted: got.Wanted, chosen: app.Selection{}, dir: dir}
	t.compare = p
	p.index()

	p.summary = widget.NewLabel(p.summarise(dir))
	p.summary.Wrapping = fyne.TextWrapWord
	p.detail = container.NewVBox(quiet("Choose something to see what differs."))

	filter := widget.NewSelect([]string{
		string(showDifferences), string(showEverything),
		string(showAdded), string(showRemoved), string(showChanged),
	}, func(choice string) {
		p.filter = showing(choice)
		p.tree.Refresh()
		p.tree.OpenAllBranches()
	})
	filter.Selected = string(p.filter)

	p.tree = widget.NewTree(
		func(id widget.TreeNodeID) []widget.TreeNodeID { return p.visibleKids(id) },
		func(id widget.TreeNodeID) bool { return len(p.kids[id]) > 0 },
		func(bool) fyne.CanvasObject {
			return container.NewHBox(widget.NewCheck("", nil),
				widget.NewIcon(theme.DocumentIcon()), widget.NewLabel("template"))
		},
		func(id widget.TreeNodeID, _ bool, o fyne.CanvasObject) { p.draw(id, o) },
	)
	p.tree.OnSelected = func(id widget.TreeNodeID) { p.showDetail(id) }

	// Three verbs, because they are three acts: read it, keep it, run it.
	p.write = widget.NewButton("Write the Script…", p.writeScript)
	p.save = widget.NewButton("Save…", p.saveScript)
	p.apply = widget.NewButton("Apply…", p.applyScript)
	p.apply.Importance = widget.HighImportance
	for _, b := range p.buttons() {
		b.Disable()
	}

	t.body.Objects = []fyne.CanvasObject{
		container.NewBorder(
			container.NewBorder(nil, nil, widget.NewLabel("Show"),
				container.NewHBox(p.write, p.save, p.apply), filter),
			nil, nil, nil,
			container.NewHSplit(
				container.NewBorder(p.summary, nil, nil, nil, p.tree),
				container.NewVScroll(p.detail),
			),
		),
	}
	t.body.Refresh()
	p.tree.OpenAllBranches()
	t.footer.SetText("Nothing has run. A comparison reads both sides and changes neither.")
}

// index walks the comparison once, giving every node an id and recording
// what its subtree holds.
func (p *comparePanel) index() {
	p.nodes = map[string]diff.Node{}
	p.kids = map[string][]string{}
	p.holds = map[string]map[diff.Status]bool{}

	var walk func(n diff.Node, parent string) (string, map[diff.Status]bool)
	walk = func(n diff.Node, parent string) (string, map[diff.Status]bool) {
		// The same names the engine gives them, so that what is ticked here
		// means the same thing to whatever writes a script from it.
		id := diff.ID(parent, n)
		p.nodes[id] = n
		held := map[diff.Status]bool{n.Status: true}
		for _, c := range n.Children {
			kid, below := walk(c, id)
			p.kids[id] = append(p.kids[id], kid)
			for st := range below {
				held[st] = true
			}
		}
		p.holds[id] = held
		return id, held
	}
	rootID, _ := walk(p.root, "")
	// The widget's own root is "", so the comparison's root hangs under it.
	p.kids[""] = []string{rootID}
	p.holds[""] = p.holds[rootID]
}

// visibleKids are the children the filter keeps, with the way down to
// anything it keeps below them.
func (p *comparePanel) visibleKids(id widget.TreeNodeID) []widget.TreeNodeID {
	var out []widget.TreeNodeID
	for _, kid := range p.kids[id] {
		if p.visible(kid) {
			out = append(out, kid)
		}
	}
	return out
}

func (p *comparePanel) visible(id string) bool {
	held := p.holds[id]
	switch p.filter {
	case showEverything:
		return true
	case showDifferences:
		return held[diff.Added] || held[diff.Removed] || held[diff.Changed]
	}
	want, ok := statusOf[p.filter]
	return ok && held[want]
}

// draw puts a node's tick box, its name and what happened to it in a row.
func (p *comparePanel) draw(id widget.TreeNodeID, o fyne.CanvasObject) {
	row, ok := o.(*fyne.Container)
	if !ok || len(row.Objects) != 3 {
		return
	}
	n := p.nodes[id]
	tick, _ := row.Objects[0].(*widget.Check)
	icon, _ := row.Objects[1].(*widget.Icon)
	label, _ := row.Objects[2].(*widget.Label)
	if tick == nil || icon == nil || label == nil {
		return
	}
	// Only a difference can be chosen: there is nothing to write for
	// something that is the same on both sides, and a tick box that does
	// nothing is a tick box somebody will tick.
	tick.OnChanged = nil
	tick.SetChecked(p.chosen[id])
	if n.Status == diff.Same {
		tick.Disable()
	} else {
		tick.Enable()
	}
	tick.OnChanged = func(on bool) { p.choose(id, on) }

	icon.SetResource(iconFor(n.Status))
	label.SetText(rowText(n))
	label.Importance = importanceOf(n.Status)
	label.Refresh()
}

// choose ticks or unticks one difference, and everything under it: choosing
// a table means the table, and choosing it is the only way to say so about
// the columns the tree does not list under it.
func (p *comparePanel) choose(id string, on bool) {
	var mark func(string)
	mark = func(at string) {
		if n := p.nodes[at]; n.Status != diff.Same {
			if on {
				p.chosen[at] = true
			} else {
				delete(p.chosen, at)
			}
		}
		for _, kid := range p.kids[at] {
			mark(kid)
		}
	}
	mark(id)
	// Choosing something inside an object is choosing a change to the
	// object, so nothing above is ticked: what is above is where the
	// statement will be aimed, not a second decision.
	p.saySelection()
	p.tree.Refresh()
}

// saySelection says how much is chosen, and turns the button on when there
// is anything to write.
func (p *comparePanel) saySelection() {
	if len(p.chosen) == 0 {
		for _, b := range p.buttons() {
			b.Disable()
		}
		p.t.footer.SetText("Nothing has run. A comparison reads both sides and changes neither.")
		return
	}
	for _, b := range p.buttons() {
		b.Enable()
	}
	p.t.footer.SetText(nounCount(len(p.chosen), "difference") +
		" chosen. Nothing has run: the script is written for you to read.")
}

func (p *comparePanel) buttons() []*widget.Button { return []*widget.Button{p.write, p.save, p.apply} }

// script renders the chosen differences, or says why it cannot.
func (p *comparePanel) script() ([]source.Statement, bool) {
	live, open := p.s.d.WS.Get(p.t.connID)
	if !open {
		p.t.footer.SetText("This connection is not open.")
		return nil, false
	}
	stmts, err := app.SyncScript(live.Source, p.live, p.wanted, p.root, p.chosen)
	if err != nil {
		p.s.showError(err)
		return nil, false
	}
	if len(stmts) == 0 {
		p.t.footer.SetText("What was chosen needs no statements.")
		return nil, false
	}
	return stmts, true
}

// saveScript keeps the script as a file, for a repository or a colleague
// or a change window later in the week (FR-7.4).
func (p *comparePanel) saveScript() {
	stmts, ok := p.script()
	if !ok {
		return
	}
	text := syncHeader(len(p.chosen)) + scriptOf(stmts)
	p.s.d.Files.Save(p.s.win, filedlg.Options{
		Message:    "Save the sync script",
		Name:       scriptFileName(p.t.label),
		Extensions: []string{"sql"},
		Kind:       "SQL",
		Accept:     "Save",
	}, func(path string, err error) {
		switch {
		case err != nil:
			p.s.showError(fmt.Errorf("could not save the script: %w", err))
		case path == "":
			// Cancelled.
		default:
			if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
				p.s.showError(fmt.Errorf("could not save the script: %w", err))
				return
			}
			p.t.footer.SetText("Saved to " + filepath.Base(path) + ". Nothing has run.")
		}
	})
}

// scriptFileName is what a saved script is called by default.
func scriptFileName(label string) string {
	name := strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == ':' {
			return '-'
		}
		return r
	}, label)
	return name + "-sync.sql"
}

// applyScript runs the chosen differences, read first like every other
// structural change (ADR-0115), and reads both sides again afterwards.
func (p *comparePanel) applyScript() {
	stmts, ok := p.script()
	if !ok {
		return
	}
	p.s.previewDDL(p.t.ddl(), stmts, func() { p.s.reopenComparison(p.t) })
}

// reopenComparison compares both sides again, after something has run.
//
// Whatever ran has moved the database, so what is on screen is a comparison
// of something that is no longer there. It is made again rather than
// adjusted, because adjusting it would mean this program deciding what the
// server did, and the server is right there to ask.
func (s *Shell) reopenComparison(t *tab) {
	p := t.compare
	if p == nil {
		return
	}
	dir := p.dir
	t.body.Objects = []fyne.CanvasObject{quiet("Reading both sides again…")}
	t.body.Refresh()
	go func() {
		ctx, cancel := context.WithTimeout(t.ctx, compareTimeout)
		defer cancel()
		live, err := s.d.WS.Connect(ctx, t.connID)
		var got app.Comparison
		if err == nil {
			got, err = app.CompareWithSaved(ctx, live.Source, databaseOf(t.ref), dir)
		}
		s.d.Run(func() {
			if t.ctx.Err() != nil {
				return
			}
			if err != nil {
				s.tabFailed(t, fmt.Errorf("could not compare again: %w", err))
				return
			}
			s.showComparison(t, got, dir)
		})
	}()
}

// writeScript renders the chosen differences and opens them as a script.
func (p *comparePanel) writeScript() {
	stmts, ok := p.script()
	if !ok {
		return
	}
	p.s.openScript(p.t.connID, syncHeader(len(p.chosen))+scriptOf(stmts))
}

// syncHeader says what the script is and what it is not, because a list of
// DROP statements wants saying out loud.
func syncHeader(n int) string {
	return "-- " + nounCount(n, "difference") + " chosen, written as statements.\n" +
		"-- Nothing here has run. Read it, edit it, and run it where you mean to.\n"
}

// rowText names a node and says what happened to it, in the words the two
// sides are named in rather than in the engine's.
func rowText(n diff.Node) string {
	name := n.Name
	if name == "" {
		name = string(n.Kind)
	}
	switch n.Status {
	case diff.Added:
		return name + " — missing here"
	case diff.Removed:
		return name + " — only here"
	case diff.Changed:
		return name + " — changed"
	}
	return name
}

func iconFor(st diff.Status) fyne.Resource {
	switch st {
	case diff.Added:
		return theme.ContentAddIcon()
	case diff.Removed:
		return theme.ContentRemoveIcon()
	case diff.Changed:
		return theme.DocumentCreateIcon()
	}
	return theme.ConfirmIcon()
}

func importanceOf(st diff.Status) widget.Importance {
	if st == diff.Same {
		return widget.LowImportance
	}
	return widget.MediumImportance
}

// showDetail draws one object's differences, with both values side by side.
func (p *comparePanel) showDetail(id widget.TreeNodeID) {
	n, ok := p.nodes[id]
	if !ok {
		return
	}
	title := strings.TrimSpace(string(n.Kind) + " " + n.Name)
	objs := []fyne.CanvasObject{bold(title), quietLabel(statusLine(n))}
	if len(n.Detail) > 0 {
		rows := [][]string{{"Property", "Here", "In the saved model"}}
		for _, d := range n.Detail {
			rows = append(rows, []string{d.Name, blankAsNothing(d.From), blankAsNothing(d.To)})
		}
		objs = append(objs, section("What differs", rows))
	}
	p.detail.Objects = objs
	p.detail.Refresh()
}

// statusLine says what happened to an object in a sentence, because the
// words added and removed are ambiguous with two sides in front of somebody.
func statusLine(n diff.Node) string {
	switch n.Status {
	case diff.Added:
		return "In the saved model and not in this database."
	case diff.Removed:
		return "In this database and not in the saved model."
	case diff.Changed:
		if len(n.Detail) == 0 {
			return "Something inside it differs."
		}
		return "It differs."
	}
	return "The same on both sides."
}

// blankAsNothing says that a value is absent rather than leaving a cell that
// reads as a space nobody typed.
func blankAsNothing(v string) string {
	if v == "" {
		return "—"
	}
	return v
}

// summarise is the line above the tree: how much there is, before anybody
// reads the rest.
func (p *comparePanel) summarise(dir string) string {
	c := p.root.Count()
	if c[diff.Added]+c[diff.Removed]+c[diff.Changed] == 0 {
		return fmt.Sprintf("Nothing differs. %s matches %s.", p.t.label, filepath.Base(dir))
	}
	return fmt.Sprintf("%s missing here, %s only here, %s changed, %s the same. Against %s.",
		nounCount(c[diff.Added], "object"), nounCount(c[diff.Removed], "object"),
		nounCount(c[diff.Changed], "object"), nounCount(c[diff.Same], "object"),
		filepath.Base(dir))
}
