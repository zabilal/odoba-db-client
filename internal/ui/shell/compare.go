package shell

import (
	"context"
	"fmt"
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
		var got diff.Node
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
func (s *Shell) showComparison(t *tab, root diff.Node, dir string) {
	p := &comparePanel{s: s, t: t, root: root, filter: showDifferences}
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
			return container.NewHBox(widget.NewIcon(theme.DocumentIcon()), widget.NewLabel("template"))
		},
		func(id widget.TreeNodeID, _ bool, o fyne.CanvasObject) { p.draw(id, o) },
	)
	p.tree.OnSelected = func(id widget.TreeNodeID) { p.showDetail(id) }

	t.body.Objects = []fyne.CanvasObject{
		container.NewBorder(
			container.NewBorder(nil, nil, widget.NewLabel("Show"), nil, filter),
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
		id := parent + "/" + string(n.Kind) + ":" + n.Name
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

// draw puts a node's name and what happened to it in a row.
func (p *comparePanel) draw(id widget.TreeNodeID, o fyne.CanvasObject) {
	row, ok := o.(*fyne.Container)
	if !ok || len(row.Objects) != 2 {
		return
	}
	n := p.nodes[id]
	icon, _ := row.Objects[0].(*widget.Icon)
	label, _ := row.Objects[1].(*widget.Label)
	if icon == nil || label == nil {
		return
	}
	icon.SetResource(iconFor(n.Status))
	label.SetText(rowText(n))
	label.Importance = importanceOf(n.Status)
	label.Refresh()
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
