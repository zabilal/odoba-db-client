// Package view is the explorer's Fyne widget, and the loader that fills it
// from saved connections and the sources behind them (FR-2.1, FR-2.2).
package view

import (
	"context"
	"fmt"
	"image/color"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	fynetheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer"
	uitheme "github.com/ikigai-db/ikigai-db/internal/ui/theme"
	"github.com/ikigai-db/ikigai-db/internal/ui/uithread"
)

// connItem and objItem are the payloads carried in explorer.Item.Data.
type connItem struct {
	ConnID      string
	Environment string
}

type objItem struct {
	ConnID string
	Node   model.Node
}

// Loader fills the tree. Saved connections sit at the root. Expanding one
// connects through the shared workspace and lists the source's Root.
// Everything below comes from the source's Children.
type Loader struct {
	Conns *app.Connections
	WS    *app.Workspace
}

var _ explorer.Loader = (*Loader)(nil)

// Badge reads an object's count or size from its source (FR-2.5). The
// driver is called here directly, so a panic comes back as an error.
func (l *Loader) Badge(ctx context.Context, connID string, ref model.ObjectRef) (_ model.Badge, _ bool, err error) {
	defer panics.Recover(&err, "reading a badge")
	live, err := l.WS.Connect(ctx, connID)
	if err != nil {
		return model.Badge{}, false, err
	}
	return live.Source.Badge(ctx, ref)
}

// badger reads badges. The view's Loader is one; a loader that is not has
// no badges to fetch.
type badger interface {
	Badge(ctx context.Context, connID string, ref model.ObjectRef) (model.Badge, bool, error)
}

// sep joins the parts of a node ID. A path joined with "." would be
// ambiguous: schema "a.b" with table "c" and schema "a" with table "b.c" would
// share an ID, and expanding one would show the other's columns. The unit
// separator cannot occur in an object name.
const sep = "\x1f"

// Load returns a node's children.
func (l *Loader) Load(ctx context.Context, parent explorer.Item) (_ []explorer.Item, err error) {
	defer panics.Recover(&err, "listing objects") // the driver is called here directly
	switch d := parent.Data.(type) {
	case nil:
		var out []explorer.Item
		for _, c := range l.Conns.List() {
			out = append(out, explorer.Item{
				ID: "c" + sep + c.ID, Label: c.Name, HasChildren: true,
				Data: connItem{ConnID: c.ID, Environment: c.Environment},
			})
		}
		return out, nil
	case connItem:
		live, err := l.WS.Connect(ctx, d.ConnID)
		if err != nil {
			return nil, err
		}
		nodes, err := live.Source.Root(ctx)
		if err != nil {
			live.Check() // a failure here may mean the connection dropped
			return nil, err
		}
		return items(d.ConnID, nodes), nil
	case objItem:
		live, err := l.WS.Connect(ctx, d.ConnID)
		if err != nil {
			return nil, err
		}
		nodes, err := live.Source.Children(ctx, d.Node.Ref)
		if err != nil {
			live.Check()
			return nil, err
		}
		return items(d.ConnID, nodes), nil
	}
	return nil, nil
}

func items(connID string, nodes []model.Node) []explorer.Item {
	out := make([]explorer.Item, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, explorer.Item{
			ID:          NodeID(connID, n.Ref),
			Label:       n.Label,
			HasChildren: n.HasChildren,
			Data:        objItem{ConnID: connID, Node: n},
		})
	}
	return out
}

// NodeID is the tree ID of an object under a connection.
func NodeID(connID string, ref model.ObjectRef) string {
	return "o" + sep + connID + sep + string(ref.Kind) + sep + strings.Join(ref.Path, sep)
}

// ConnectionID is the tree ID of a saved connection.
func ConnectionID(connID string) string { return "c" + sep + connID }

// ConnectionOf returns the saved connection a tree node belongs to. Loading
// and error rows belong to their parent's connection.
func ConnectionOf(id string) (string, bool) {
	if i := strings.IndexByte(id, 0); i >= 0 {
		id = id[:i] // a placeholder: its parent's ID, then the suffix
	}
	parts := strings.SplitN(id, sep, 3)
	if len(parts) >= 2 && (parts[0] == "c" || parts[0] == "o") {
		return parts[1], true
	}
	return "", false
}

// Explorer is the tree widget.
type Explorer struct {
	Tree  *widget.Tree
	Model *explorer.Model

	// OnOpen is called when a browsable node — a table, view or collection —
	// is activated by double-click or OpenSelected.
	OnOpen func(connID string, node model.Node)
	// OnSelect is called on the UI goroutine when the selection changes.
	OnSelect func(id string)
	// OnMenu is called when a node is right-clicked, after it has been
	// selected, with where the click was: the shell shows its context menu
	// there (FR-2.4).
	OnMenu func(id string, at fyne.Position)

	// OnExpand is called on the UI goroutine when a branch opens or closes.
	OnExpand func()

	selected string
	open     map[string]bool // the branches open
	refresh  func()

	// Badges are fetched as their rows are drawn, badgeWorkers at a time,
	// and remembered, "none" included (FR-2.5). badges and waiting are the
	// UI goroutine's.
	badger  badger
	run     uithread.Runner
	badges  map[string]fetched
	waiting map[string]*fetch
	slots   chan struct{}

	// Filter narrows the sidebar to the objects whose path matches
	// (FR-2.3, ADR-0018), listed in the tree's place.
	Filter  *filterEntry
	results *widget.List
	note    *widget.Label
	hits    []explorer.Hit
	found   fyne.CanvasObject // the results and their note
	body    *fyne.Container   // the tree, or found in its place
	view    fyne.CanvasObject
	search  func()
}

// searchLimit is how many matches the filter lists.
const searchLimit = 200

// New builds an explorer over a loader. run gets work onto the UI goroutine
// (uithread.Fyne in production) and delay coalesces refreshes
// (uithread.FrameDelay). Tests pass a uithread.Queue and zero.
func New(l explorer.Loader, run uithread.Runner, delay time.Duration) *Explorer {
	e := &Explorer{Model: explorer.NewModel(l, 30*time.Second), run: run,
		badges: map[string]fetched{}, waiting: map[string]*fetch{}, slots: make(chan struct{}, badgeWorkers)}
	e.badger, _ = l.(badger)
	e.Tree = widget.NewTree(
		func(id widget.TreeNodeID) []widget.TreeNodeID { return e.Model.Children(id) },
		func(id widget.TreeNodeID) bool { return e.Model.IsBranch(id) },
		func(bool) fyne.CanvasObject { return newNodeRow() },
		func(id widget.TreeNodeID, _ bool, o fyne.CanvasObject) { e.update(id, o.(*nodeRow)) },
	)
	e.open = map[string]bool{}
	e.Tree.OnBranchOpened = func(id widget.TreeNodeID) { e.expanded(id, true) }
	e.Tree.OnBranchClosed = func(id widget.TreeNodeID) { e.expanded(id, false) }
	e.Tree.OnSelected = func(id widget.TreeNodeID) {
		e.selected = id
		if e.OnSelect != nil {
			e.OnSelect(id)
		}
	}

	// Children arrive on background goroutines, often several at once as
	// branches fill. Refreshing the widget from each callback is what
	// corrupted the data grid's state in spike W1 (ADR-0002). Changes are
	// coalesced into at most one queued refresh, run on the UI goroutine.
	// A filter's matches follow the tree as more of it loads, in the same
	// one refresh.
	e.refresh = uithread.Coalesce(run, delay, func() { e.Tree.Refresh(); e.runSearch() })
	e.Filter = &filterEntry{}
	e.Filter.ExtendBaseWidget(e.Filter)
	e.Filter.SetPlaceHolder("Filter loaded objects")
	e.note = widget.NewLabel("")
	e.note.Importance = widget.LowImportance
	e.note.Truncation = fyne.TextTruncateEllipsis
	e.results = widget.NewList(
		func() int { return len(e.hits) },
		func() fyne.CanvasObject {
			name := widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
			name.Truncation = fyne.TextTruncateEllipsis
			where := widget.NewLabel("")
			where.Importance = widget.LowImportance
			where.Truncation = fyne.TextTruncateEllipsis
			return container.NewVBox(name, where)
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			if i >= len(e.hits) {
				return
			}
			h, box := e.hits[i], o.(*fyne.Container)
			box.Objects[0].(*widget.Label).SetText(h.Path[len(h.Path)-1])
			box.Objects[1].(*widget.Label).SetText(strings.Join(h.Path[:len(h.Path)-1], explorer.PathSep))
		})
	e.results.OnSelected = func(i widget.ListItemID) {
		if i < len(e.hits) {
			e.choose(e.hits[i].ID)
		}
		e.results.UnselectAll()
	}
	e.found = container.NewBorder(nil, e.note, nil, nil, e.results)
	e.body = container.NewStack(e.Tree)
	e.view = container.NewBorder(e.Filter, nil, nil, nil, e.body)
	e.search = uithread.Coalesce(run, delay, e.runSearch)
	e.Filter.OnChanged = func(string) { e.search() }
	e.Filter.OnSubmitted = func(string) {
		if len(e.hits) > 0 {
			e.choose(e.hits[0].ID)
		}
	}
	e.Model.OnChange = func(string) { e.refresh() }
	return e
}

// View is the explorer as the sidebar shows it: the filter above the tree.
func (e *Explorer) View() fyne.CanvasObject { return e.view }

// filterEntry is the explorer's filter field: Escape clears it.
type filterEntry struct {
	widget.Entry
}

func (f *filterEntry) TypedKey(k *fyne.KeyEvent) {
	if k.Name == fyne.KeyEscape && f.Text != "" {
		f.SetText("")
		return
	}
	f.Entry.TypedKey(k)
}

// runSearch lists the filter's matches in the tree's place, or brings the
// tree back once the filter is empty. It runs on the UI goroutine.
func (e *Explorer) runSearch() {
	text := strings.TrimSpace(e.Filter.Text)
	if text == "" {
		e.hits = nil
		e.show(e.Tree)
		return
	}
	res := e.Model.Search(text, searchLimit)
	e.hits = res.Hits
	e.results.Refresh()
	e.note.SetText(searchNote(len(res.Hits), res.Unopened))
	e.show(e.found)
}

func (e *Explorer) show(o fyne.CanvasObject) {
	if len(e.body.Objects) == 1 && e.body.Objects[0] == o {
		return
	}
	e.body.Objects = []fyne.CanvasObject{o}
	e.body.Refresh()
}

// searchNote says how many objects matched, and which connections the
// search could not look inside.
func searchNote(n, unopened int) string {
	var s string
	switch n {
	case 0:
		s = "No loaded objects match"
	case 1:
		s = "1 match"
	default:
		s = fmt.Sprintf("%d matches", n)
	}
	switch unopened {
	case 0:
		return s + "."
	case 1:
		return s + ". Not searched: 1 connection not opened."
	}
	return s + fmt.Sprintf(". Not searched: %d connections not opened.", unopened)
}

// choose opens a matching table as a double-click would. Any other match
// is shown in the tree: the filter clears and the match is selected, and
// selecting it opens the branches above it, since the tree scrolls to what
// it selects. TestChoosingAFolderShowsItInTheTree holds Fyne to that.
func (e *Explorer) choose(id string) {
	it, _, _ := e.Model.Item(id)
	if d, ok := it.Data.(objItem); ok && d.Node.Browsable {
		e.activate(id)
		return
	}
	e.Filter.SetText("")
	e.hits = nil
	e.show(e.Tree)
	e.Tree.Select(id)
}

// Refresh reloads a node's children, or the whole tree with explorer.RootID.
// Badges below it are fetched afresh, and fetches under way are cancelled.
func (e *Explorer) Refresh(id string) {
	e.dropBadges(id, true)
	e.Model.Refresh(id)
}

// expanded records a branch opening or closing.
func (e *Explorer) expanded(id string, open bool) {
	if open {
		e.open[id] = true
	} else {
		delete(e.open, id)
		e.dropBadges(id, false) // its rows are out of view: stop reading their badges
	}
	if e.OnExpand != nil {
		e.OnExpand()
	}
}

// Expanded is the branches open, in order.
func (e *Explorer) Expanded() []string { return slices.Sorted(maps.Keys(e.open)) }

// Selected returns the selected node's ID.
func (e *Explorer) Selected() string { return e.selected }

// SelectedNode is the selected object and the connection it belongs to. ok is
// false when nothing is selected, or when a connection, placeholder or error
// row is.
func (e *Explorer) SelectedNode() (connID string, n model.Node, ok bool) {
	if e.selected == "" {
		return "", model.Node{}, false
	}
	it, _, _ := e.Model.Item(e.selected)
	d, ok := it.Data.(objItem)
	return d.ConnID, d.Node, ok
}

// OpenSelected activates the selected node, for the keyboard (Enter).
func (e *Explorer) OpenSelected() { e.activate(e.selected) }

func (e *Explorer) activate(id string) {
	it, _, _ := e.Model.Item(id)
	if d, ok := it.Data.(objItem); ok && d.Node.Browsable && e.OnOpen != nil {
		e.OnOpen(d.ConnID, d.Node)
	}
}

func (e *Explorer) update(id string, r *nodeRow) {
	r.onDouble = func() { e.activate(id) }
	r.onSecondary = func(at fyne.Position) { e.menu(id, at) }
	it, st, _ := e.Model.Item(id)
	if explorer.IsPlaceholder(id) {
		r.placeholder(it.Label, st == explorer.Failed)
		return
	}
	switch d := it.Data.(type) {
	case connItem:
		r.show(uitheme.IconNameDatabase, it.Label, environmentBadge(d.Environment))
	case objItem:
		r.show(iconFor(d.Node.Ref.Kind), it.Label, e.badgeOf(id, d))
	default:
		r.show("", it.Label, badgeText{})
	}
}

var kindIcons = map[model.ObjectKind]fyne.ThemeIconName{
	model.KindDatabase:         uitheme.IconNameDatabase,
	model.KindSchema:           uitheme.IconNameSchema,
	model.KindTable:            uitheme.IconNameTable,
	model.KindView:             uitheme.IconNameView,
	model.KindMaterializedView: uitheme.IconNameView,
	model.KindColumn:           uitheme.IconNameColumn,
	model.KindIndex:            uitheme.IconNameIndex,
	model.KindForeignKey:       uitheme.IconNameForeignKey,
	model.KindRoutine:          uitheme.IconNameRoutine,
	model.KindTrigger:          uitheme.IconNameTrigger,
	model.KindSequence:         uitheme.IconNameSequence,
	model.KindUserType:         uitheme.IconNameType,
	model.KindCollection:       uitheme.IconNameCollection,
	model.KindKey:              uitheme.IconNameKey,
	model.KindTopic:            uitheme.IconNameTopic,
	model.KindPartition:        uitheme.IconNamePartition,
	model.KindConsumerGroup:    uitheme.IconNameConsumerGroup,
	model.KindCluster:          uitheme.IconNameCluster,
	model.KindFolder:           fynetheme.IconNameFolder,
}

func iconFor(k model.ObjectKind) fyne.ThemeIconName {
	if n, ok := kindIcons[k]; ok {
		return n
	}
	return fynetheme.IconNameFile
}

type badgeText struct {
	text     string
	emphatic bool
}

// badge renders a node's count. An estimate is marked with ~, so a
// reltuples guess is never read as an exact figure (FR-2.5).
func badge(n model.Node) badgeText {
	if n.Badge == nil || n.Badge.Text == "" {
		if n.Attrs["type"] != "" {
			return badgeText{text: n.Attrs["type"]} // a column shows its type
		}
		return badgeText{}
	}
	if n.Badge.Exact {
		return badgeText{text: n.Badge.Text}
	}
	return badgeText{text: "~" + n.Badge.Text}
}

// badgeWorkers is how many badges are read at once. Each is a query on
// the server, and a schema of a thousand tables must not send a thousand.
const badgeWorkers = 4

// badgeTimeout bounds reading one badge.
const badgeTimeout = 10 * time.Second

// fetched is a badge as read: ok is false when the source has none for the
// node, or could not say, and either way it is not asked again.
type fetched struct {
	badge model.Badge
	ok    bool
}

// fetch is one badge being read. cancel ends it.
type fetch struct{ cancel context.CancelFunc }

// badgeOf is the badge to draw for an object. A node that came with its own
// is drawn as it is; otherwise its badge is fetched, the first time its row
// is drawn, and drawn once it arrives. Folders and columns have none.
func (e *Explorer) badgeOf(id string, d objItem) badgeText {
	own := badge(d.Node)
	if d.Node.Badge != nil || own.text != "" || e.badger == nil ||
		d.Node.Ref.Kind == model.KindFolder || d.Node.Ref.Kind == model.KindColumn {
		return own
	}
	if f, ok := e.badges[id]; ok {
		if f.ok {
			return badge(model.Node{Badge: &f.badge})
		}
		return own
	}
	e.fetchBadge(id, d)
	return own
}

// fetchBadge reads a badge off the UI goroutine, waiting for one of the
// badgeWorkers slots, and draws it when it lands. It never delays drawing
// the tree: a row is drawn at once, and again when its badge arrives.
func (e *Explorer) fetchBadge(id string, d objItem) {
	if _, ok := e.waiting[id]; ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), badgeTimeout)
	f := &fetch{cancel: cancel}
	e.waiting[id] = f
	go func() {
		defer cancel()
		select {
		case e.slots <- struct{}{}:
			defer func() { <-e.slots }()
		case <-ctx.Done():
			e.run(func() { e.landed(id, f, fetched{}, ctx.Err()) })
			return
		}
		b, ok, err := e.badger.Badge(ctx, d.ConnID, d.Node.Ref)
		if err == nil {
			err = ctx.Err()
		}
		e.run(func() { e.landed(id, f, fetched{badge: b, ok: ok && err == nil}, err) })
	}()
}

// landed records a badge read, unless its fetch was cancelled: dropBadges
// takes a cancelled fetch out of waiting, so it lands here unrecorded and
// its row, if drawn again, asks again. A timeout or an error is remembered
// as no badge, so a slow or failing server is not asked on every redraw.
func (e *Explorer) landed(id string, f *fetch, got fetched, err error) {
	if e.waiting[id] != f {
		return // cancelled, and maybe asked again since
	}
	delete(e.waiting, id)
	_ = err // any failure is remembered as no badge: got.ok is false
	e.badges[id] = got
	e.refresh()
}

// dropBadges cancels the badge reads below a branch, and with forget also
// drops what was read there, for a refresh to read it afresh. The whole
// tree takes everything, including reads for nodes the model has dropped.
func (e *Explorer) dropBadges(branch string, forget bool) {
	below := func(id string) bool {
		return branch == explorer.RootID || id == branch || e.Model.Within(id, branch)
	}
	for id, f := range e.waiting {
		if below(id) {
			f.cancel()
			delete(e.waiting, id)
		}
	}
	if forget {
		for id := range e.badges {
			if below(id) {
				delete(e.badges, id)
			}
		}
	}
}

// environmentBadge labels a connection's environment in words as well as
// colour (UX principle 14): dev and production are a red/green pair, the axis
// of the most common colour-vision deficiency.
func environmentBadge(env string) badgeText {
	if env == "" {
		return badgeText{}
	}
	th := uitheme.New()
	t := th.Environment(env, false)
	return badgeText{text: t.Label, emphatic: t.Emphatic}
}

// nodeRow is one tree row: icon, label, and a right-aligned badge.
type nodeRow struct {
	widget.BaseWidget
	icon     *widget.Icon
	label    *canvas.Text
	badge    *canvas.Text
	onDouble func()
	// onSecondary is a right-click, with where it was on the canvas.
	onSecondary func(at fyne.Position)
}

func newNodeRow() *nodeRow {
	r := &nodeRow{icon: widget.NewIcon(nil), label: canvas.NewText("", nil), badge: canvas.NewText("", nil)}
	r.badge.Alignment = fyne.TextAlignTrailing
	r.ExtendBaseWidget(r)
	return r
}

// DoubleTapped opens the node. Only double taps are taken: single taps fall
// through to the tree, which handles selection.
func (r *nodeRow) DoubleTapped(*fyne.PointEvent) {
	if r.onDouble != nil {
		r.onDouble()
	}
}

// TappedSecondary asks for the node's context menu.
func (r *nodeRow) TappedSecondary(e *fyne.PointEvent) {
	if r.onSecondary != nil {
		r.onSecondary(e.AbsolutePosition)
	}
}

// menu selects a right-clicked node, so the commands in its menu act on it,
// and asks for the menu. A loading or error row has none.
func (e *Explorer) menu(id string, at fyne.Position) {
	if explorer.IsPlaceholder(id) {
		return
	}
	e.Tree.Select(id)
	if e.OnMenu != nil {
		e.OnMenu(id, at)
	}
}

func colors() (fg, secondary, danger color.Color) {
	a := fyne.CurrentApp()
	th, v := a.Settings().Theme(), a.Settings().ThemeVariant()
	return th.Color(fynetheme.ColorNameForeground, v),
		th.Color(fynetheme.ColorNamePlaceHolder, v),
		th.Color(fynetheme.ColorNameError, v)
}

func (r *nodeRow) show(icon fyne.ThemeIconName, label string, b badgeText) {
	fg, secondary, danger := colors()
	if icon != "" {
		th := fyne.CurrentApp().Settings().Theme()
		res := th.Icon(icon)
		if res == nil {
			// A theme that lacks one of the custom icons still draws
			// something, instead of a row with a hole where the icon goes.
			res = th.Icon(fynetheme.IconNameFile)
		}
		r.icon.SetResource(res)
	} else {
		r.icon.SetResource(nil)
	}
	r.label.Text, r.label.Color, r.label.TextStyle = label, fg, fyne.TextStyle{}
	r.badge.Text, r.badge.Color = b.text, secondary
	r.badge.TextStyle = fyne.TextStyle{}
	if b.emphatic {
		r.badge.Color, r.badge.TextStyle = danger, fyne.TextStyle{Bold: true}
	}
	r.Refresh()
}

func (r *nodeRow) placeholder(text string, failed bool) {
	_, secondary, danger := colors()
	r.icon.SetResource(nil)
	r.label.Text, r.label.Color = text, secondary
	r.label.TextStyle = fyne.TextStyle{Italic: true}
	if failed {
		r.label.Color = danger
		r.label.TextStyle = fyne.TextStyle{}
	}
	r.badge.Text = ""
	r.Refresh()
}

func (r *nodeRow) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewBorder(nil, nil,
		container.NewHBox(r.icon, r.label), r.badge))
}

// debugID is a readable form of a node ID, for test failure messages.
func debugID(id string) string { return strconv.Quote(strings.ReplaceAll(id, sep, "/")) }
