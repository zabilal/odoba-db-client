// Package shell is the main window (T1.1–T1.12): the object explorer beside
// the document tabs, one command registry behind both the menu bar and the
// command palette, and a status line for the connection in focus.
//
// The shell composes; it does not decide. What a command does lives in
// internal/app, and how a row looks lives in the grid and explorer packages,
// which keeps this package thin enough to test headlessly.
package shell

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	fynetheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/store"
	"github.com/ikigai-db/ikigai-db/internal/ui/commands"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer/view"
	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
	"github.com/ikigai-db/ikigai-db/internal/ui/palette"
	uitheme "github.com/ikigai-db/ikigai-db/internal/ui/theme"
	"github.com/ikigai-db/ikigai-db/internal/ui/uithread"
)

// Deps is what a shell is built from.
type Deps struct {
	Conns *app.Connections
	WS    *app.Workspace
	// Settings persists the appearance choice. Nil keeps it for the session.
	Settings *store.SettingsFile
	Theme    *uitheme.Theme
	Log      *slog.Logger
	// History records what is run and finds it again. Nil turns history off.
	History app.HistoryStore
	// Saved keeps named queries. Nil turns saving off.
	Saved app.SavedQueryStore

	// Run schedules work on the UI goroutine, and refreshes are coalesced over
	// Delay. Nil means Fyne's goroutine and one frame. Tests pass a
	// uithread.Queue and zero: Fyne's test driver runs fyne.Do on whichever
	// goroutine calls it, which would race the test.
	Run   uithread.Runner
	Delay time.Duration
	// GOOS picks platform shortcut labels. Empty means runtime.GOOS.
	GOOS string
}

// Shell is one main window.
type Shell struct {
	d   Deps
	app fyne.App
	win fyne.Window
	reg *commands.Registry

	Explorer *view.Explorer
	sidebar  fyne.CanvasObject
	split    *container.Split
	tabs     *container.DocTabs
	empty    fyne.CanvasObject
	status   *widget.Label
	pal      *palette.Palette

	menu      *fyne.MainMenu
	menuItems map[string]*fyne.MenuItem

	open    []*tab
	queries int // numbers query tabs

	ctx    context.Context
	cancel context.CancelFunc
}

// tab is one open document. Phase 1 has one kind: an object's rows.
type tab struct {
	key    string
	connID string
	item   *container.TabItem
	body   *fyne.Container
	footer *widget.Label
	ctx    context.Context
	cancel context.CancelFunc
	model  *grid.Model // nil until the object opens
	grid   *grid.TableGrid
	browse *app.BrowseSource
	// browseSeq numbers re-browses (a sort or a filter), so only the latest
	// is applied. want is what the latest asked for, so a sort made while a
	// filter is on its way keeps it. applied and filtered are the sort and
	// filter text the rows are in, for putting things back on failure.
	browseSeq int
	want      source.BrowseOptions
	applied   []grid.SortKey
	filtered  []string
	picked    map[int]pick    // filters chosen from a column\'s picklist
	top       *fyne.Container // above the grid: the WHERE bar, when shown
	where     *whereBar
	// problem is the last sort or filter failure. The footer keeps showing
	// it beside the row count until a re-browse succeeds.
	problem string
	query   *queryTab // set only on query tabs
}

// New builds the main window. Show it with Window().ShowAndRun().
func New(a fyne.App, d Deps) *Shell {
	if d.Run == nil {
		d.Run, d.Delay = uithread.Fyne, uithread.FrameDelay
	}
	if d.GOOS == "" {
		d.GOOS = runtime.GOOS
	}
	if d.Log == nil {
		d.Log = slog.New(slog.DiscardHandler)
	}
	if d.Theme == nil {
		d.Theme = uitheme.New()
	}
	if d.Settings != nil {
		d.Theme.Appearance = appearanceNamed(d.Settings.Get().Appearance)
	}
	a.Settings().SetTheme(d.Theme)

	s := &Shell{d: d, app: a, reg: commands.NewRegistry(), menuItems: map[string]*fyne.MenuItem{}}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.registerCommands()

	s.win = a.NewWindow("Ikigai DB")
	s.win.Resize(fyne.NewSize(1280, 800))
	s.win.SetMaster()

	s.Explorer = view.New(&view.Loader{Conns: d.Conns, WS: d.WS}, d.Run, d.Delay)
	s.Explorer.OnOpen = s.OpenObject
	s.Explorer.OnSelect = func(string) { s.sync() }

	s.tabs = container.NewDocTabs()
	s.tabs.CloseIntercept = s.requestClose
	s.tabs.OnSelected = func(*container.TabItem) { s.sync() }
	s.tabs.Hide()
	s.empty = s.emptyState()

	s.status = widget.NewLabel("")
	s.status.Truncation = fyne.TextTruncateEllipsis
	s.status.Importance = widget.LowImportance

	s.sidebar = s.buildSidebar()
	s.split = container.NewHSplit(s.sidebar, container.NewStack(s.empty, s.tabs))
	s.split.Offset = 0.24
	s.win.SetContent(container.NewBorder(nil,
		container.NewVBox(widget.NewSeparator(), s.status), nil, nil, s.split))

	s.menu = s.buildMenu()
	s.win.SetMainMenu(s.menu)
	s.pal = palette.New(s.reg, s.win)

	d.WS.OnStatus(func(string, app.Status) { d.Run(s.sync) })
	a.Settings().AddListener(func(fyne.Settings) { d.Run(s.recolour) })
	s.win.SetOnClosed(s.shutdown)
	s.sync()
	return s
}

// Window is the shell's window.
func (s *Shell) Window() fyne.Window { return s.win }

// Commands is the registry behind the menus and the palette.
func (s *Shell) Commands() *commands.Registry { return s.reg }

// ShowNotice tells the user what happened to their settings file at startup,
// if anything worth telling did.
func (s *Shell) ShowNotice(n store.OpenNotice) {
	switch {
	case n.Recovered != "":
		dialog.ShowInformation("Settings Were Reset",
			"Your settings file could not be read, so a new one was started. "+
				"The old file was kept at:\n\n"+n.Recovered, s.win)
	case n.Frozen != "":
		dialog.ShowInformation("Settings Will Not Be Saved", n.Frozen, s.win)
	}
}

func sc(key string, mods commands.Mod) commands.Shortcut {
	return commands.Shortcut{Key: key, Mods: mods}
}

func (s *Shell) registerCommands() {
	hasConn := func() bool { _, ok := s.selectedConn(); return ok }
	for _, c := range []commands.Command{
		{ID: cmdPalette, Category: "View", Title: "Command Palette…", Keywords: []string{"actions", "commands"},
			Shortcut: sc("K", commands.ModShortcut), Run: func() { s.pal.Show() }},
		{ID: cmdConnNew, Category: "Connection", Title: "New Connection…", Keywords: []string{"add", "server", "database"},
			Shortcut: sc("N", commands.ModShortcut), Run: func() { s.showConnectionForm("") }},
		{ID: cmdConnEdit, Category: "Connection", Title: "Edit Connection…", Keywords: []string{"password", "settings"},
			Enabled: hasConn, Run: func() {
				if id, ok := s.selectedConn(); ok {
					s.showConnectionForm(id)
				}
			}},
		{ID: cmdConnDup, Category: "Connection", Title: "Duplicate Connection", Keywords: []string{"copy", "clone"},
			Enabled: hasConn, Run: s.duplicateSelected},
		{ID: cmdConnDelete, Category: "Connection", Title: "Delete Connection…", Keywords: []string{"remove"},
			Enabled: hasConn, Run: s.confirmDeleteSelected},
		{ID: cmdDisconnect, Category: "Connection", Title: "Disconnect", Keywords: []string{"close"},
			Enabled: s.selectedConnOpen, Run: func() {
				if id, ok := s.selectedConn(); ok {
					s.disconnect(id)
				}
			}},
		{ID: cmdOpen, Category: "Explorer", Title: "Open Data", Keywords: []string{"browse", "rows", "table"},
			Shortcut: sc("O", commands.ModShortcut), Enabled: s.selectionBrowsable,
			Run: func() { s.Explorer.OpenSelected() }},
		{ID: cmdRefresh, Category: "Explorer", Title: "Refresh", Keywords: []string{"reload", "tree"},
			Shortcut: sc("R", commands.ModShortcut), Run: s.refreshSelected},
		{ID: cmdReload, Category: "Tab", Title: "Reload Rows", Keywords: []string{"refresh", "data"},
			Shortcut: sc("R", commands.ModShortcut|commands.ModShift), Enabled: s.activeTabLoaded, Run: s.reloadTab},
		{ID: cmdFilterValues, Category: "Data", Title: "Filter by Values…", Keywords: []string{"picklist", "distinct", "filter"},
			Enabled: s.canPickValues, Run: func() {
				if t := s.activeTab(); t != nil && t.grid != nil {
					s.showPicklist(t, t.grid.SelectedColumn(), s.underHeader(t))
				}
			}},
		{ID: cmdWhere, Category: "Data", Title: "WHERE Clause", Keywords: []string{"sql", "condition", "filter", "statement"},
			Enabled: s.canWhere, Run: s.toggleWhere},
		{ID: cmdSidebar, Category: "View", Title: "Toggle Sidebar", Keywords: []string{"explorer", "hide", "show"},
			Shortcut: sc("0", commands.ModShortcut), Run: s.toggleSidebar},
		{ID: cmdTabClose, Category: "Tab", Title: "Close Tab", Shortcut: sc("W", commands.ModShortcut),
			Enabled: func() bool { return len(s.open) > 0 }, Run: func() {
				if it := s.tabs.Selected(); it != nil {
					s.requestClose(it)
				}
			}},
		{ID: cmdTabNext, Category: "Tab", Title: "Show Next Tab", Shortcut: sc("]", commands.ModShortcut|commands.ModShift),
			Enabled: func() bool { return len(s.open) > 1 }, Run: func() { s.cycleTab(1) }},
		{ID: cmdTabPrev, Category: "Tab", Title: "Show Previous Tab", Shortcut: sc("[", commands.ModShortcut|commands.ModShift),
			Enabled: func() bool { return len(s.open) > 1 }, Run: func() { s.cycleTab(-1) }},
		{ID: cmdQueryNew, Category: "Query", Title: "New Query", Keywords: []string{"sql", "editor", "script"},
			Shortcut: sc("T", commands.ModShortcut), Enabled: hasConn, Run: func() {
				if id, ok := s.selectedConn(); ok {
					s.OpenQuery(id)
				}
			}},
		{ID: cmdQueryRun, Category: "Query", Title: "Run", Keywords: []string{"execute", "statement", "selection"},
			Shortcut: sc("Return", commands.ModShortcut), Enabled: s.canRun, Run: func() { s.runQuery(false) }},
		{ID: cmdQueryRunAll, Category: "Query", Title: "Run All", Keywords: []string{"execute", "script"},
			Shortcut: sc("Return", commands.ModShortcut|commands.ModShift), Enabled: s.canRun, Run: func() { s.runQuery(true) }},
		{ID: cmdQueryStop, Category: "Query", Title: "Stop", Keywords: []string{"cancel", "abort"},
			Shortcut: sc(".", commands.ModShortcut), Enabled: s.running, Run: s.stopQuery},
		{ID: cmdHistory, Category: "Query", Title: "Query History…", Keywords: []string{"recent", "past", "find", "search"},
			Shortcut: sc("H", commands.ModShortcut|commands.ModShift), Enabled: func() bool { return s.d.History != nil },
			Run: func() { s.showHistory() }},
		{ID: cmdQuerySave, Category: "Query", Title: "Save Query", Keywords: []string{"keep", "store"},
			Shortcut: sc("S", commands.ModShortcut), Enabled: s.canSave, Run: s.saveQuery},
		{ID: cmdQuerySaveAs, Category: "Query", Title: "Save Query As…", Keywords: []string{"copy", "duplicate"},
			Shortcut: sc("S", commands.ModShortcut|commands.ModShift), Enabled: s.canSave, Run: func() {
				if t, _ := s.activeQuery(); t != nil {
					s.promptSave(t, true)
				}
			}},
		{ID: cmdOpenSaved, Category: "Query", Title: "Open Saved Query…", Keywords: []string{"saved", "library", "load"},
			Shortcut: sc("O", commands.ModShortcut|commands.ModShift), Enabled: func() bool { return s.d.Saved != nil },
			Run: func() { s.showSaved() }},
		{ID: cmdExport, Category: "Data", Title: "Export…", Keywords: []string{"csv", "json", "ndjson", "tsv", "save", "download"},
			Shortcut: sc("E", commands.ModShortcut|commands.ModShift), Enabled: func() bool { return s.exportSource() != nil },
			Run: s.showExport},
		{ID: cmdFind, Category: "Edit", Title: "Find…", Keywords: []string{"search", "look for"},
			Shortcut: sc("F", commands.ModShortcut), Enabled: s.hasQuery, Run: func() { s.withFind(func(f *findBar) { f.show(false) }) }},
		{ID: cmdFindReplace, Category: "Edit", Title: "Find and Replace…", Keywords: []string{"substitute", "change"},
			Shortcut: sc("F", commands.ModShortcut|commands.ModAlt), Enabled: s.hasQuery, Run: func() { s.withFind(func(f *findBar) { f.show(true) }) }},
		{ID: cmdFindNext, Category: "Edit", Title: "Find Next", Shortcut: sc("G", commands.ModShortcut),
			Enabled: s.hasQuery, Run: func() { s.withFind(func(f *findBar) { f.next(false) }) }},
		{ID: cmdFindPrev, Category: "Edit", Title: "Find Previous", Shortcut: sc("G", commands.ModShortcut|commands.ModShift),
			Enabled: s.hasQuery, Run: func() { s.withFind(func(f *findBar) { f.next(true) }) }},
		{ID: cmdAppearSystem, Category: "Appearance", Title: "Follow System", Keywords: []string{"theme", "auto"},
			Run: func() { s.setAppearance(uitheme.AppearanceSystem) }},
		{ID: cmdAppearLight, Category: "Appearance", Title: "Light", Keywords: []string{"theme"},
			Run: func() { s.setAppearance(uitheme.AppearanceLight) }},
		{ID: cmdAppearDark, Category: "Appearance", Title: "Dark", Keywords: []string{"theme", "night"},
			Run: func() { s.setAppearance(uitheme.AppearanceDark) }},
	} {
		s.reg.MustRegister(c)
	}
}

func (s *Shell) buildSidebar() fyne.CanvasObject {
	title := widget.NewLabelWithStyle("Connections", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	add := widget.NewButtonWithIcon("", fynetheme.ContentAddIcon(), func() { s.run(cmdConnNew) })
	add.Importance = widget.LowImportance
	return container.NewBorder(container.NewBorder(nil, nil, nil, add, title), nil, nil, nil, s.Explorer.Tree)
}

func (s *Shell) emptyState() fyne.CanvasObject {
	label := func(id string) string {
		c, _ := s.reg.Get(id)
		return c.Shortcut.Label(s.d.GOOS)
	}
	title := widget.NewLabelWithStyle("No Open Tabs", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	hint := widget.NewLabel(fmt.Sprintf(
		"Double-click a table in the sidebar to see its rows,\nor press %s to write a query.\nPress %s to find any command.",
		label(cmdQueryNew), label(cmdPalette)))
	hint.Alignment = fyne.TextAlignCenter
	hint.Importance = widget.LowImportance
	return container.NewCenter(container.NewVBox(title, hint))
}

// sync brings everything derived from the selection and connection state up
// to date: the status line, and each menu item's enabled and checked state.
// The native macOS menu reads Disabled as it opens, so the field must already
// be current then; there is no hook to compute it on demand.
func (s *Shell) sync() {
	s.status.SetText(s.statusText())
	changed := false
	for id, it := range s.menuItems {
		c, _ := s.reg.Get(id)
		disabled := c.Enabled != nil && !c.Enabled()
		checked := s.checked(id)
		if it.Disabled != disabled || it.Checked != checked {
			it.Disabled, it.Checked = disabled, checked
			changed = true
		}
	}
	if changed && s.menu != nil {
		s.menu.Refresh()
	}
}

func (s *Shell) statusText() string {
	id, ok := s.selectedConn()
	if !ok {
		switch n := len(s.d.WS.OpenIDs()); n {
		case 0:
			return "Not connected"
		case 1:
			return "1 connection open"
		default:
			return fmt.Sprintf("%d connections open", n)
		}
	}
	c, _ := s.d.Conns.Get(id)
	live, open := s.d.WS.Get(id)
	if !open {
		return c.Name + " — not connected"
	}
	switch st := live.Status(); {
	case st.Err != nil:
		return c.Name + " — " + st.Message()
	case st.State == app.StateConnected:
		return c.Name + " — Connected"
	default:
		name := st.State.String()
		return c.Name + " — " + strings.ToUpper(name[:1]) + name[1:]
	}
}

// selectedConn is the connection in focus: the one the explorer selection
// belongs to, or else the active tab's.
func (s *Shell) selectedConn() (string, bool) {
	id, ok := view.ConnectionOf(s.Explorer.Selected())
	if !ok {
		t := s.activeTab()
		if t == nil {
			return "", false
		}
		id = t.connID
	}
	_, exists := s.d.Conns.Get(id)
	return id, exists
}

func (s *Shell) selectedConnOpen() bool {
	id, ok := s.selectedConn()
	if !ok {
		return false
	}
	_, open := s.d.WS.Get(id)
	return open
}

func (s *Shell) selectionBrowsable() bool {
	_, n, ok := s.Explorer.SelectedNode()
	return ok && n.Browsable
}

// OpenObject shows an object's rows in a tab, or brings its tab forward if it
// is already open. It never blocks: the connection and the first page arrive
// in the background, and the tab says so meanwhile (NFR-P3).
func (s *Shell) OpenObject(connID string, n model.Node) {
	key := view.NodeID(connID, n.Ref)
	if t := s.tabFor(key); t != nil {
		s.tabs.Select(t.item)
		return
	}

	ctx, cancel := context.WithCancel(s.ctx)
	t := &tab{key: key, connID: connID, ctx: ctx, cancel: cancel,
		body: container.NewStack(quiet("Opening…")), footer: widget.NewLabel("")}
	t.footer.Importance = widget.LowImportance
	t.top = container.NewVBox()
	t.item = container.NewTabItem(n.Label, container.NewBorder(t.top, t.footer, nil, nil, t.body))
	s.open = append(s.open, t)
	s.showTabs(true)
	s.tabs.Append(t.item)
	s.tabs.Select(t.item)
	s.sync()

	go func() {
		bs, err := s.browse(ctx, connID, n.Ref)
		s.d.Run(func() {
			if ctx.Err() != nil {
				return // the tab closed while it was opening
			}
			if err != nil {
				s.tabFailed(t, err)
				return
			}
			s.attachGrid(t, bs)
		})
	}()
}

func (s *Shell) browse(ctx context.Context, connID string, ref model.ObjectRef) (*app.BrowseSource, error) {
	live, err := s.d.WS.Connect(ctx, connID)
	if err != nil {
		return nil, err
	}
	return app.NewBrowseSource(ctx, live.Source, ref, source.BrowseOptions{})
}

func (s *Shell) attachGrid(t *tab, bs *app.BrowseSource) {
	m := grid.NewModel(bs)
	g := grid.NewTableGridWith(t.ctx, m, s.colours(), s.d.Run, s.d.Delay)
	footer := uithread.Coalesce(s.d.Run, s.d.Delay, func() {
		if t.ctx.Err() == nil {
			s.showCount(t)
		}
	})
	// Coalesced, never a refresh per page: see grid.TableGrid.ScheduleRefresh.
	m.OnPageLoaded = func(int64) {
		g.ScheduleRefresh()
		footer()
	}
	m.OnError = func(err error) {
		s.d.Run(func() { t.footer.SetText("Could not load rows: " + err.Error()) })
	}
	t.model, t.grid, t.browse = m, g, bs
	t.want = bs.Options()
	g.Sortable = bs.CanSort()
	g.OnSort = func(keys []grid.SortKey) { s.resort(t, keys) }
	g.SetFilterable(bs.CanFilter()) // before the grid is shown
	g.OnFilter = func(texts []string) { s.refilter(t, texts) }
	if bs.CanListValues() {
		g.OnPickValues = func(col int, at fyne.Position) { s.showPicklist(t, col, at) }
	}
	g.OnSelectCell = s.sync // Filter by Values follows the selected cell
	t.body.Objects = []fyne.CanvasObject{g.View()}
	t.body.Refresh()
	s.count(t)
	s.sync()
}

// count fetches the row count in the background. Rows show before it
// resolves, and for sources that cannot count cheaply it resolves to nothing:
// the footer then reports what has loaded until the end of the data is found.
func (s *Shell) count(t *tab) {
	t.footer.SetText("Counting rows…")
	m, g := t.model, t.grid
	go func() {
		err := m.LoadCount(t.ctx)
		s.d.Run(func() {
			if t.ctx.Err() != nil {
				return
			}
			if err != nil {
				t.footer.SetText("Row count unavailable: " + err.Error())
				return
			}
			s.showCount(t)
			g.ScheduleRefresh()
		})
	}()
}

// showCount words what is known about a tab's row count.
func (s *Shell) showCount(t *tab) {
	var text string
	switch n, final := t.model.Extent(); {
	case final:
		text = rowCount(n, true)
	case n > 0:
		text = group(n) + "+ rows"
	default:
		text = "Loading rows…"
	}
	if o := t.browse; o != nil && (len(o.Options().Filters) > 0 || o.Options().Where != "") {
		text += " · filtered"
	}
	if t.problem != "" {
		text += " — " + t.problem
	}
	t.footer.SetText(text)
}

func (s *Shell) tabFailed(t *tab, err error) {
	msg := widget.NewLabel(err.Error())
	msg.Wrapping = fyne.TextWrapWord
	objs := []fyne.CanvasObject{msg}
	if missing := (*app.MissingSecretsError)(nil); errors.As(err, &missing) {
		msg.SetText("This connection needs a password, and none is saved.")
		objs = append(objs, container.NewHBox(
			widget.NewButton("Edit Connection…", func() { s.showConnectionForm(t.connID) })))
	}
	t.body.Objects = []fyne.CanvasObject{container.NewPadded(container.NewVBox(objs...))}
	t.body.Refresh()
	t.footer.SetText("")
}

func (s *Shell) reloadTab() {
	t := s.activeTab()
	if t == nil || t.model == nil {
		return
	}
	t.model.Invalidate()
	t.grid.ScheduleRefresh()
	s.count(t)
}

func (s *Shell) activeTabLoaded() bool {
	t := s.activeTab()
	return t != nil && t.model != nil
}

func (s *Shell) closeTab(it *container.TabItem) {
	for i, t := range s.open {
		if t.item == it {
			t.cancel()
			if q := t.query; q != nil && q.session != nil {
				go q.session.Close() // may wait on the network; never on the UI goroutine
			}
			s.open = append(s.open[:i], s.open[i+1:]...)
			break
		}
	}
	s.tabs.Remove(it)
	if len(s.open) == 0 {
		s.showTabs(false)
	}
	s.sync()
}

func (s *Shell) closeTabsOf(connID string) {
	for _, t := range append([]*tab(nil), s.open...) {
		if t.connID == connID {
			s.closeTab(t.item)
		}
	}
}

func (s *Shell) tabFor(key string) *tab {
	for _, t := range s.open {
		if t.key == key {
			return t
		}
	}
	return nil
}

func (s *Shell) activeTab() *tab {
	sel := s.tabs.Selected()
	if sel == nil {
		return nil
	}
	for _, t := range s.open {
		if t.item == sel {
			return t
		}
	}
	return nil
}

func (s *Shell) showTabs(show bool) {
	if show {
		s.empty.Hide()
		s.tabs.Show()
	} else {
		s.tabs.Hide()
		s.empty.Show()
	}
}

func (s *Shell) cycleTab(delta int) {
	n := len(s.tabs.Items)
	if n < 2 {
		return
	}
	s.tabs.SelectIndex((s.tabs.SelectedIndex() + delta + n) % n)
}

func (s *Shell) toggleSidebar() {
	if s.sidebar.Visible() {
		s.sidebar.Hide()
	} else {
		s.sidebar.Show()
	}
	s.split.Refresh()
}

func (s *Shell) refreshSelected() {
	id := s.Explorer.Selected()
	if id == "" || explorer.IsPlaceholder(id) {
		id = explorer.RootID
	}
	s.Explorer.Refresh(id)
}

func (s *Shell) duplicateSelected() {
	id, ok := s.selectedConn()
	if !ok {
		return
	}
	if _, err := s.d.Conns.Duplicate(id); err != nil {
		dialog.ShowError(err, s.win)
		return
	}
	s.Explorer.Refresh(explorer.RootID)
}

func (s *Shell) confirmDeleteSelected() {
	id, ok := s.selectedConn()
	if !ok {
		return
	}
	c, _ := s.d.Conns.Get(id)
	d := dialog.NewConfirm(fmt.Sprintf("Delete “%s”?", c.Name),
		"Its saved password is removed from the keychain and its tabs close. This cannot be undone.",
		func(yes bool) {
			if yes {
				s.deleteConnection(id)
			}
		}, s.win)
	d.SetConfirmText("Delete")
	d.SetDismissText("Cancel")
	d.SetConfirmImportance(widget.DangerImportance)
	d.Show()
}

func (s *Shell) deleteConnection(id string) {
	s.disconnect(id)
	if err := s.d.Conns.Delete(id); err != nil {
		dialog.ShowError(err, s.win)
		return
	}
	s.Explorer.Refresh(explorer.RootID)
	s.sync()
}

// disconnect closes a connection and every tab using it, and collapses its
// tree node so that nothing reconnects until the user expands it again.
func (s *Shell) disconnect(id string) {
	s.closeSessions(func(t *tab) bool { return t.connID == id }) // before the pool: see shutdown
	s.closeTabsOf(id)
	if _, open := s.d.WS.Get(id); open {
		if err := s.d.WS.Disconnect(id); err != nil {
			s.d.Log.Warn("disconnecting", "connection", id, "err", err)
		}
	}
	node := view.ConnectionID(id)
	s.Explorer.Tree.CloseBranch(node)
	s.Explorer.Refresh(node)
	s.sync()
}

// connectionSaved updates the window after the connection form saves. An
// edited connection that is open is closed: its session still uses the old
// settings, and quietly keeping it would show data from the wrong place.
func (s *Shell) connectionSaved(id string, edited bool) {
	if _, open := s.d.WS.Get(id); edited && open {
		s.disconnect(id)
	}
	s.Explorer.Refresh(explorer.RootID)
	s.sync()
}

func (s *Shell) setAppearance(a uitheme.Appearance) {
	s.d.Theme.Appearance = a
	s.app.Settings().SetTheme(s.d.Theme)
	s.recolour()
	if s.d.Settings != nil {
		if err := s.d.Settings.Update(func(st *store.Settings) error {
			st.Appearance = appearanceName(a)
			return nil
		}); err != nil {
			s.d.Log.Warn("saving appearance", "err", err)
		}
	}
	s.sync()
}

func (s *Shell) checked(id string) bool {
	switch id {
	case cmdAppearSystem:
		return s.d.Theme.Appearance == uitheme.AppearanceSystem
	case cmdAppearLight:
		return s.d.Theme.Appearance == uitheme.AppearanceLight
	case cmdAppearDark:
		return s.d.Theme.Appearance == uitheme.AppearanceDark
	}
	return false
}

func (s *Shell) colours() uitheme.Palette {
	return s.d.Theme.PaletteFor(s.app.Settings().ThemeVariant())
}

// recolour repaints what caches colours: open grids draw from a palette
// captured when they were built.
func (s *Shell) recolour() {
	p := s.colours()
	for _, t := range s.open {
		if t.grid != nil {
			t.grid.SetPalette(p)
		}
		if q := t.query; q != nil {
			q.editor.SetPalette(p)
			for _, g := range q.grids {
				g.SetPalette(p)
			}
		}
	}
}

// shutdownWait bounds how long quitting waits for connections to close. The
// window has already gone, and a server that has vanished can hold a close
// for its whole timeout: better to exit than to hang.
const shutdownWait = 3 * time.Second

func (s *Shell) shutdown() {
	s.cancel()
	// Query sessions first. Each holds a pooled connection, and a pool will
	// not close while any are out: closing connections first hung quitting
	// whenever a query tab was open (found by the J3 journey test).
	s.closeSessions(func(*tab) bool { return true })
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := s.d.WS.CloseAll(); err != nil {
			s.d.Log.Warn("closing connections", "err", err)
		}
	}()
	select {
	case <-done:
	case <-time.After(shutdownWait):
		s.d.Log.Warn("connections still closing at exit")
	}
}

// closeSessions closes the query sessions of the tabs that match,
// synchronously, so that the pools they draw from can close after.
func (s *Shell) closeSessions(match func(*tab) bool) {
	for _, t := range s.open {
		if q := t.query; q != nil && q.session != nil && match(t) {
			q.session.Close()
		}
	}
}

func appearanceNamed(name string) uitheme.Appearance {
	switch name {
	case "light":
		return uitheme.AppearanceLight
	case "dark":
		return uitheme.AppearanceDark
	}
	return uitheme.AppearanceSystem
}

func appearanceName(a uitheme.Appearance) string {
	switch a {
	case uitheme.AppearanceLight:
		return "light"
	case uitheme.AppearanceDark:
		return "dark"
	}
	return "system"
}

func quiet(text string) fyne.CanvasObject {
	l := widget.NewLabel(text)
	l.Importance = widget.LowImportance
	return container.NewCenter(l)
}

// rowCount words a row count for a tab's footer.
func rowCount(n int64, known bool) string {
	switch {
	case !known || n < 0:
		return "Row count unknown"
	case n == 1:
		return "1 row"
	}
	return group(n) + " rows"
}

// group writes n with thousands separators.
func group(n int64) string {
	s := strconv.FormatInt(n, 10)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
