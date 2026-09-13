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
	"github.com/ikigai-db/ikigai-db/internal/store/localdb"
	"github.com/ikigai-db/ikigai-db/internal/ui/commands"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer/view"
	"github.com/ikigai-db/ikigai-db/internal/ui/filedlg"
	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
	"github.com/ikigai-db/ikigai-db/internal/ui/palette"
	"github.com/ikigai-db/ikigai-db/internal/ui/tabbar"
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
	// Scratch keeps query text saved nowhere else through a crash or a quit.
	// Nil turns autosave off. Autosave is how soon an edit is kept; zero
	// means autosaveDelay.
	Scratch  app.ScratchStore
	Autosave time.Duration
	// Session keeps the window as it was left, for the next start. Nil
	// starts with an empty window every time.
	Session app.SessionStore

	// Run schedules work on the UI goroutine, and refreshes are coalesced over
	// Delay. Nil means Fyne's goroutine and one frame. Tests pass a
	// uithread.Queue and zero: Fyne's test driver runs fyne.Do on whichever
	// goroutine calls it, which would race the test.
	Run   uithread.Runner
	Delay time.Duration
	// GOOS picks platform shortcut labels. Empty means runtime.GOOS.
	GOOS string
	// Files shows file dialogs. Nil means the platform's own.
	Files filedlg.Chooser
	// Params remembers the values given to query parameters. Nil asks with
	// nothing filled in.
	Params app.ParamStore
}

// Shell is one main window.
type Shell struct {
	d   Deps
	app fyne.App
	win fyne.Window
	// away is whether the app is in the background, where long work that
	// ends is told by a notification (notify.go).
	away bool
	// lastParams is the Parameters panel last opened (params.go).
	lastParams *paramsPanel
	reg        *commands.Registry

	Explorer *view.Explorer
	sidebar  fyne.CanvasObject
	split    *container.Split
	tabs     *tabbar.Tabs
	// panes are the one or two panes of tabs (panes.go), and tabs the one
	// worked in. paneBox holds the one, or the split between the two.
	panes     []*tabbar.Tabs
	paneBox   *fyne.Container
	paneSplit *container.Split
	splitDir  string
	empty     fyne.CanvasObject
	status    *widget.Label
	errors    *errorBar
	// taskButton, in the status bar, says while tasks run, and opens the
	// Tasks panel (tasks.go). tasks is every task not cleared; taskView is
	// the panel, current while it is open.
	taskButton *widget.Button
	tasks      []*task
	taskView   *tasksPanel
	pal        *palette.Palette
	// work is the tabs, or the empty state, and right holds work alone or
	// beside the open side panel (panel.go).
	work, right *fyne.Container
	panel       *sidePanel
	// favBox is the sidebar's favourites section, and favRows its rows.
	favBox, favRows *fyne.Container

	menu      *fyne.MainMenu
	menuItems map[string]*fyne.MenuItem

	open    []*tab
	queries int // numbers query tabs

	writer         *writer // nil without a scratch or session store
	autosave       time.Duration
	autosaveWarned bool
	// restoring is true while restore reopens the last session, which is
	// not saved again until it is whole. sessionPending is true while a save
	// is scheduled, and lastSession is what was last saved.
	restoring      bool
	sessionPending bool
	lastSession    []byte

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
	// ed is the table's editing (edits.go): its pending changes are nil
	// where the rows cannot be told apart, which are never edited (FR-4.7).
	ed *edits
	// browseSeq numbers re-browses (a sort or a filter), so only the latest
	// is applied. want is what the latest asked for, so a sort made while a
	// filter is on its way keeps it. applied and filtered are the sort and
	// filter text the rows are in, for putting things back on failure.
	browseSeq int
	want      source.BrowseOptions
	applied   []grid.SortKey
	filtered  []string
	picked    map[int]pick    // filters chosen from a column\'s picklist
	top       *fyne.Container // above the grid: the WHERE bar or the pipeline, when shown
	where     *whereBar
	pipeline  *pipelineBar
	// holders are where each grid's view lives, viewers their cell viewers
	// and forms their form views, so all go when the tab does.
	holders map[*grid.TableGrid]*fyne.Container
	viewers map[*grid.TableGrid]*cellViewer
	forms   map[*grid.TableGrid]*formView
	jsons   map[*grid.TableGrid]*jsonView
	// said is what the last action said, such as where an export went. A
	// table\'s footer keeps it beside the row count, as it keeps problem.
	said string
	// problem is the last sort or filter failure. The footer keeps showing
	// it beside the row count until a re-browse succeeds.
	problem string
	query   *queryTab // set only on query tabs
	pinned  bool      // kept at the left, in the order pinned: see tabs.go
	// ref is an object tab's object. restore is how its rows were viewed
	// when the last session ended, put back once they first show.
	ref     model.ObjectRef
	restore *localdb.SessionTab
	// table is a table tab's description, for its foreign keys (fkeys.go):
	// nil until read, and for anything but a table. then runs once the grid
	// is attached: what a jump asked of a tab still opening.
	table *model.Table
	then  func()
	// referrers are the keys of other tables that refer to it (referring.go).
	referrers []model.Referrer
	// labels are its keys' values' labels (labels.go), from the moment its
	// grid is attached.
	labels *valueLabels
	// center holds a table's body, or it and its detail panel in a split
	// (detail.go); detail is that panel, once shown.
	center *fyne.Container
	detail *detailPanel
	// imp is an import's panel, where the tab is one (importui.go).
	imp *importPanel
	// structure marks a structure tab (structure.go), and label is its
	// object's name.
	structure bool
	label     string
	// band is across the top of the content, for a production tab
	// (envband.go); lost is below it while the connection is lost (lost.go).
	band *envBand
	lost *lostBand
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
	if d.Files == nil {
		d.Files = filedlg.Native{}
	}
	if d.Settings != nil {
		d.Theme.Appearance = appearanceNamed(d.Settings.Get().Appearance)
		d.Theme.Accent = uitheme.AccentNamed(d.Settings.Get().Accent)
	}
	a.Settings().SetTheme(d.Theme)

	s := &Shell{d: d, app: a, reg: commands.NewRegistry(), menuItems: map[string]*fyne.MenuItem{}}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.registerCommands()
	a.Lifecycle().SetOnExitedForeground(func() { s.away = true })
	a.Lifecycle().SetOnEnteredForeground(func() { s.away = false })

	s.win = a.NewWindow("Ikigai DB")
	s.win.Resize(fyne.NewSize(1280, 800))
	s.win.SetMaster()

	s.Explorer = view.New(&view.Loader{Conns: d.Conns, WS: d.WS}, d.Run, d.Delay)
	s.Explorer.OnOpen = s.OpenObject
	s.Explorer.OnMenu = s.showExplorerMenu
	s.Explorer.OnSelect = func(string) { s.sync() }
	s.Explorer.OnExpand = s.sessionChanged

	s.tabs = s.newPane()
	s.panes = []*tabbar.Tabs{s.tabs}
	s.paneBox = container.NewStack(s.tabs)
	s.tabs.Hide()
	s.empty = s.emptyState()

	s.status = widget.NewLabel("")
	s.status.Truncation = fyne.TextTruncateEllipsis
	s.status.Importance = widget.LowImportance
	s.taskButton = widget.NewButton("", func() { s.run(cmdTasks) })
	s.taskButton.Importance = widget.LowImportance
	s.taskButton.Hide()

	s.sidebar = s.buildSidebar()
	s.work = container.NewStack(s.empty, s.paneBox)
	s.right = container.NewStack(s.work)
	s.split = container.NewHSplit(s.sidebar, s.right)
	s.split.Offset = 0.24
	s.errors = s.newErrorBar()
	s.win.SetContent(container.NewBorder(s.errors.slot,
		container.NewVBox(widget.NewSeparator(), container.NewBorder(nil, nil, nil, s.taskButton, s.status)), nil, nil, s.split))

	s.applyBindings() // before the menu bar, whose items carry the shortcuts
	s.menu = s.buildMenu()
	s.win.SetMainMenu(s.menu)
	s.pal = palette.New(s.reg, s.win)

	d.WS.OnStatus(func(id string, _ app.Status) { d.Run(func() { s.connectionStatus(id) }) })
	a.Settings().AddListener(func(fyne.Settings) { d.Run(s.recolour) })
	s.win.SetOnClosed(s.shutdown)
	s.win.SetCloseIntercept(s.requestQuit) // Quit comes this way too
	s.autosave = d.Autosave
	if s.autosave <= 0 {
		s.autosave = autosaveDelay
	}
	if d.Scratch != nil || d.Session != nil {
		s.writer = newWriter(func(err error) { d.Run(func() { s.autosaveFailed(err) }) })
	}
	s.restore()
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
	for _, c := range append([]commands.Command{
		{ID: cmdShortcuts, Category: "Help", Title: "Keyboard Shortcuts", Keywords: []string{"keys", "bindings", "reference", "cheat sheet"},
			Shortcut: sc("/", commands.ModShortcut), Run: func() { s.togglePanel(panelShortcuts, func() { s.showShortcuts() }) }},
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
		{ID: cmdFavorite, Category: "Explorer", Title: "Favourite", Keywords: []string{"pin", "bookmark", "star", "favorite"},
			Shortcut: sc("D", commands.ModShortcut), Enabled: func() bool { _, ok := s.selectedFavorite(); return ok },
			Run: s.toggleFavorite},
		{ID: cmdScriptSelect, Category: "Explorer", Title: "Script as SELECT", Keywords: []string{"query", "template", "sql"},
			Enabled: s.selectionBrowsable, Run: func() { s.scriptSelected(app.ScriptSelect) }},
		{ID: cmdScriptInsert, Category: "Explorer", Title: "Script as INSERT", Keywords: []string{"add", "row", "template", "sql"},
			Enabled: s.selectionIsTable, Run: func() { s.scriptSelected(app.ScriptInsert) }},
		{ID: cmdScriptUpdate, Category: "Explorer", Title: "Script as UPDATE", Keywords: []string{"change", "row", "template", "sql"},
			Enabled: s.selectionIsTable, Run: func() { s.scriptSelected(app.ScriptUpdate) }},
		{ID: cmdStructure, Category: "Explorer", Title: "Open Structure", Keywords: []string{"columns", "indexes", "keys", "schema", "describe"},
			Shortcut: sc("O", commands.ModShortcut|commands.ModAlt), Enabled: s.selectionBrowsable, Run: s.openSelectedStructure},
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
		{ID: cmdCellViewer, Category: "View", Title: "Cell Viewer", Keywords: []string{"value", "inspect", "json", "expand"},
			Enabled: func() bool { return s.activeGrid() != nil }, Run: s.toggleViewer},
		{ID: cmdFormView, Category: "View", Title: "Form View", Keywords: []string{"record", "row", "vertical", "fields", "wide"},
			Enabled: func() bool { t, g := s.activeTab(), s.activeGrid(); return t != nil && g != nil && t.holders[g] != nil },
			Run:     s.toggleForm},
		{ID: cmdPipeline, Category: "Data", Title: "Aggregation Pipeline…",
			Keywords: []string{"mongo", "aggregate", "stages", "group", "match", "document"},
			Enabled:  s.canPipeline, Run: s.togglePipeline},
		{ID: cmdJSONView, Category: "View", Title: "JSON View", Keywords: []string{"document", "mongo", "nested", "raw", "json"},
			Enabled: func() bool { t, g := s.activeTab(), s.activeGrid(); return t != nil && g != nil && t.holders[g] != nil },
			Run:     s.toggleJSON},
		{ID: cmdGoToReferenced, Category: "View", Title: "Go to Referenced Row", Keywords: []string{"foreign key", "fk", "parent", "follow", "jump", "reference"},
			Enabled: s.canGoToReferenced, Run: s.goToReferenced},
		{ID: cmdOpenRowObject, Category: "View", Title: "Open What the Row Holds",
			Keywords: []string{"redis", "key", "value", "open", "contents", "drill"},
			Enabled:  s.canOpenRowObject, Run: s.openRowObject},
		{ID: cmdShowReferring, Category: "View", Title: "Show Referring Rows", Keywords: []string{"foreign key", "fk", "children", "child", "referenced by", "what points"},
			Enabled: s.canShowReferring, Run: s.showReferring},
		{ID: cmdDetail, Category: "View", Title: "Detail Rows", Keywords: []string{"master", "detail", "children", "child rows", "nested", "related"},
			Enabled: s.canShowDetail, Run: s.toggleDetail},
		{ID: cmdHideColumn, Category: "View", Title: "Hide Column", Enabled: s.hasColumn,
			Run: func() { s.onColumn((*grid.TableGrid).HideColumn) }},
		{ID: cmdShowColumns, Category: "View", Title: "Show All Columns",
			Enabled: func() bool { g := s.activeGrid(); return g != nil && g.Hidden() },
			Run:     func() { s.onGrid((*grid.TableGrid).ShowAllColumns) }},
		{ID: cmdMoveLeft, Category: "View", Title: "Move Column Left", Enabled: s.hasColumn,
			Run: func() { s.onColumn(func(g *grid.TableGrid, c int) { g.MoveColumn(c, -1) }) }},
		{ID: cmdMoveRight, Category: "View", Title: "Move Column Right", Enabled: s.hasColumn,
			Run: func() { s.onColumn(func(g *grid.TableGrid, c int) { g.MoveColumn(c, 1) }) }},
		{ID: cmdFreeze, Category: "View", Title: "Freeze Columns Through This One", Keywords: []string{"pin", "sticky"},
			Enabled: s.hasColumn, Run: func() { s.onColumn((*grid.TableGrid).FreezeThrough) }},
		{ID: cmdUnfreeze, Category: "View", Title: "Unfreeze Columns", Keywords: []string{"unpin"},
			Enabled: func() bool { g := s.activeGrid(); return g != nil && g.Frozen() > 0 },
			Run:     func() { s.onGrid((*grid.TableGrid).Unfreeze) }},
		{ID: cmdCopyCells, Category: "Edit", Title: "Copy Cells", Keywords: []string{"clipboard", "tsv", "selection"},
			Enabled: s.hasSelection, Run: s.copyActive},
		{ID: cmdCopyCSV, Category: "Edit", Title: "Copy as CSV", Keywords: []string{"clipboard", "comma"},
			Enabled: s.hasSelection, Run: s.copyCSV},
		{ID: cmdCopyJSON, Category: "Edit", Title: "Copy as JSON", Keywords: []string{"clipboard", "objects"},
			Enabled: s.hasSelection, Run: s.copyJSON},
		{ID: cmdCopyMarkdown, Category: "Edit", Title: "Copy as Markdown", Keywords: []string{"clipboard", "table", "md"},
			Enabled: s.hasSelection, Run: s.copyMarkdown},
		{ID: cmdCopyInsert, Category: "Edit", Title: "Copy as INSERT", Keywords: []string{"clipboard", "sql", "statements"},
			Enabled: s.canCopyInsert, Run: s.copyInsert},
		{ID: cmdPasteCells, Category: "Edit", Title: "Paste Cells", Keywords: []string{"clipboard", "tsv", "csv", "spreadsheet", "fill"},
			Enabled: s.canPaste, Run: s.pasteActive},
		{ID: cmdSelectRow, Category: "Edit", Title: "Select Row", Enabled: s.hasSelection,
			Run: func() { s.onGrid((*grid.TableGrid).SelectRow) }},
		{ID: cmdSelectColumn, Category: "Edit", Title: "Select Column", Enabled: s.hasSelection,
			Run: func() { s.onGrid((*grid.TableGrid).SelectColumn) }},
		{ID: cmdSelectAllCells, Category: "Edit", Title: "Select All Cells", Keywords: []string{"everything"},
			Enabled: func() bool { return s.activeGrid() != nil }, Run: func() { s.onGrid((*grid.TableGrid).SelectAll) }},
		{ID: cmdEditCell, Category: "Edit", Title: "Edit Cell", Keywords: []string{"change", "value", "type", "update"},
			Enabled: s.canEditCell, Run: func() { s.onGrid(func(g *grid.TableGrid) { g.EditCell("") }) }},
		{ID: cmdEditValue, Category: "Edit", Title: "Edit in Cell Viewer", Keywords: []string{"long", "json", "date", "calendar", "text", "value"},
			Enabled: s.canEditCell, Run: s.editInViewer},
		{ID: cmdSetNull, Category: "Edit", Title: "Set to NULL", Keywords: []string{"clear", "empty", "null", "value"},
			Enabled: s.canChangeRows, Run: s.setNull},
		{ID: cmdSetValue, Category: "Edit", Title: "Set Value…", Keywords: []string{"bulk", "fill", "column", "many", "same", "update"},
			Enabled: s.canChangeRows, Run: s.setValue},
		{ID: cmdInsertRow, Category: "Edit", Title: "Insert Row", Keywords: []string{"add", "new", "row", "record"},
			Enabled: s.canInsert, Run: s.insertRow},
		{ID: cmdDuplicateRows, Category: "Edit", Title: "Duplicate Rows", Keywords: []string{"copy", "clone", "row", "record"},
			Enabled: s.canChangeRows, Run: s.duplicateRows},
		{ID: cmdDeleteRows, Category: "Edit", Title: "Delete Rows", Keywords: []string{"remove", "row", "record"},
			Enabled: s.canChangeRows, Run: s.deleteRows},
		{ID: cmdReviewChanges, Category: "Edit", Title: "Review Changes…", Keywords: []string{"commit", "save", "write", "apply", "preview", "sql"},
			Enabled: s.canReview, Run: s.reviewActive},
		{ID: cmdRevertCells, Category: "Edit", Title: "Revert Cells", Keywords: []string{"undo", "restore", "cell", "value"},
			Enabled: s.canRevert, Run: s.revertCells},
		{ID: cmdRevertRows, Category: "Edit", Title: "Revert Rows", Keywords: []string{"undo", "restore", "row", "undelete"},
			Enabled: s.canRevert, Run: s.revertRows},
		{ID: cmdDiscardAll, Category: "Edit", Title: "Discard All Changes…", Keywords: []string{"revert", "undo", "throw away", "forget"},
			Enabled: s.canReview, Run: s.discardAll},
		{ID: cmdChooseKey, Category: "Edit", Title: "Choose a Key…", Keywords: []string{"key", "identity", "primary", "unique", "edit"},
			Enabled: s.canChooseKey, Run: s.chooseKey},
		{ID: cmdFilterObjects, Category: "View", Title: "Filter Objects", Keywords: []string{"find", "search", "go to", "table", "jump"},
			Shortcut: sc("F", commands.ModShortcut|commands.ModShift), Run: s.filterObjects},
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
		{ID: cmdMoveTabLeft, Category: "Tab", Title: "Move Tab Left", Keywords: []string{"reorder"},
			Enabled: func() bool { return s.canMoveTab(-1) }, Run: func() { s.moveTab(-1) }},
		{ID: cmdMoveTabRight, Category: "Tab", Title: "Move Tab Right", Keywords: []string{"reorder"},
			Enabled: func() bool { return s.canMoveTab(1) }, Run: func() { s.moveTab(1) }},
		{ID: cmdPinTab, Category: "Tab", Title: "Pin Tab", Keywords: []string{"keep", "stick"},
			Enabled: func() bool { return s.activeTab() != nil }, Run: s.togglePin},
		{ID: cmdSplitRight, Category: "Window", Title: "Split Right", Keywords: []string{"pane", "side by side", "compare", "two"},
			Enabled: s.canSplit, Run: func() { s.splitPane(splitRight) }},
		{ID: cmdSplitDown, Category: "Window", Title: "Split Down", Keywords: []string{"pane", "above", "below", "stack", "two"},
			Enabled: s.canSplit, Run: func() { s.splitPane(splitDown) }},
		{ID: cmdMoveToPane, Category: "Window", Title: "Move Tab to Other Pane", Keywords: []string{"pane", "split", "other side"},
			Enabled: func() bool { return len(s.panes) == 2 }, Run: s.moveToOtherPane},
		{ID: cmdJoinPanes, Category: "Window", Title: "Join Panes", Keywords: []string{"unsplit", "close split", "merge", "one pane"},
			Enabled: func() bool { return len(s.panes) == 2 }, Run: s.joinPanes},
		{ID: cmdTasks, Category: "Window", Title: "Tasks", Keywords: []string{"progress", "export", "background", "running", "cancel", "task centre", "task center"},
			Run: func() { s.togglePanel(panelTasks, func() { s.showTasks() }) }},
		{ID: cmdQueryNew, Category: "Query", Title: "New Query", Keywords: []string{"sql", "editor", "script"},
			Shortcut: sc("T", commands.ModShortcut), Enabled: hasConn, Run: func() {
				if id, ok := s.selectedConn(); ok {
					s.OpenQuery(id)
				}
			}},
		{ID: cmdQueryComplete, Category: "Query", Title: "Complete",
			Keywords: []string{"autocomplete", "suggest", "candidates", "intellisense", "column", "table"},
			Shortcut: sc("Space", commands.ModControl), Enabled: s.canComplete, Run: s.completeQuery},
		{ID: cmdQueryRun, Category: "Query", Title: "Run", Keywords: []string{"execute", "statement", "selection"},
			Shortcut: sc("Return", commands.ModShortcut), Enabled: s.canRun, Run: func() { s.runQuery(false) }},
		{ID: cmdQueryRunAll, Category: "Query", Title: "Run All", Keywords: []string{"execute", "script"},
			Shortcut: sc("Return", commands.ModShortcut|commands.ModShift), Enabled: s.canRun, Run: func() { s.runQuery(true) }},
		{ID: cmdQueryStop, Category: "Query", Title: "Stop", Keywords: []string{"cancel", "abort"},
			Shortcut: sc(".", commands.ModShortcut), Enabled: s.running, Run: s.stopQuery},
		{ID: cmdHistory, Category: "Query", Title: "Query History", Keywords: []string{"recent", "past", "find", "search"},
			Shortcut: sc("H", commands.ModShortcut|commands.ModShift), Enabled: func() bool { return s.d.History != nil },
			Run: func() { s.togglePanel(panelHistory, func() { s.showHistory() }) }},
		{ID: cmdQuerySave, Category: "Query", Title: "Save Query", Keywords: []string{"keep", "store"},
			Shortcut: sc("S", commands.ModShortcut), Enabled: s.canSave, Run: s.saveQuery},
		{ID: cmdQuerySaveAs, Category: "Query", Title: "Save Query As…", Keywords: []string{"copy", "duplicate"},
			Shortcut: sc("S", commands.ModShortcut|commands.ModShift), Enabled: s.canSave, Run: func() {
				if t, _ := s.activeQuery(); t != nil {
					s.promptSave(t, true)
				}
			}},
		{ID: cmdOpenSaved, Category: "Query", Title: "Saved Queries", Keywords: []string{"saved", "library", "load"},
			Shortcut: sc("O", commands.ModShortcut|commands.ModShift), Enabled: func() bool { return s.d.Saved != nil },
			Run: func() { s.togglePanel(panelSaved, func() { s.showSaved() }) }},
		{ID: cmdExport, Category: "Data", Title: "Export…", Keywords: []string{"csv", "json", "ndjson", "tsv", "save", "download"},
			Shortcut: sc("E", commands.ModShortcut|commands.ModShift), Enabled: func() bool { return s.exportSource() != nil },
			Run: s.showExport},
		{ID: cmdImport, Category: "Data", Title: "Import…", Keywords: []string{"csv", "tsv", "json", "ndjson", "excel", "xlsx", "load", "upload"},
			Enabled: s.canImport, Run: s.showImport},
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
	}, append(append(s.accentCommands(), s.folderCommands()...), s.lostCommands()...)...) {
		s.reg.MustRegister(c)
	}
}

func (s *Shell) buildSidebar() fyne.CanvasObject {
	title := widget.NewLabelWithStyle("Connections", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	add := widget.NewButtonWithIcon("New", fynetheme.ContentAddIcon(), func() { s.run(cmdConnNew) })
	add.Importance = widget.LowImportance
	return container.NewBorder(container.NewVBox(container.NewBorder(nil, nil, nil, add, title), s.newFavorites()), nil, nil, nil, s.Explorer.View())
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
	s.sessionChanged()
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
		s.selectTab(t)
		return
	}

	ctx, cancel := context.WithCancel(s.ctx)
	t := &tab{key: key, connID: connID, ref: n.Ref, ctx: ctx, cancel: cancel,
		body: container.NewStack(quiet("Opening…")), footer: widget.NewLabel("")}
	t.footer.Importance = widget.LowImportance
	t.top = container.NewVBox()
	t.ed = &edits{ctx: ctx, connID: connID, say: func(text string) { t.said = text },
		show: func() { s.showCount(t) }, reload: func() { s.reload(t) }}
	t.ed.review = widget.NewButton("Review Changes…", func() {
		if t.ed.reviewable() {
			s.review(t.ed)
		}
	})
	t.ed.review.Hide()
	foot := container.NewBorder(nil, nil, nil, t.ed.review, t.footer)
	t.center = container.NewStack(t.body)
	t.item = container.NewTabItem(n.Label, container.NewBorder(t.top, foot, nil, nil, t.center))
	s.open = append(s.open, t)
	s.addTab(t)
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
	// The labels are there before any page arrives.
	t.labels = newValueLabels()
	m.OnPageLoaded = func(page int64) {
		g.ScheduleRefresh()
		footer()
		go s.readLabels(t, page) // labels.go
	}
	m.OnError = func(err error) {
		s.d.Run(func() {
			t.footer.SetText("Could not load rows: " + err.Error())
			s.crashed(t.connID, err)
		})
	}
	t.model, t.grid, t.browse = m, g, bs
	t.ed.grid, t.ed.model, t.ed.writes = g, m, bs
	s.startEditing(t, bs.Identity()) // key.go
	t.want = bs.Options()
	g.Sortable = bs.CanSort()
	g.OnSort = func(keys []grid.SortKey) { s.resort(t, keys) }
	g.SetFilterable(bs.CanFilter()) // before the grid is shown
	g.OnFilter = func(texts []string) { s.refilter(t, texts) }
	g.OnHeaderMenu = func(col int, at fyne.Position) { s.showHeaderMenu(t, g, col, at) }
	// Filter by Values and the cell viewer follow the selected cell.
	g.OnSelectCell = func() { s.sync(); t.viewerFollow(g) }
	g.OnCopy = func() { s.copyCells(t.ctx, g) }
	g.OnPaste = func() { s.paste(editsFor(t, g)) }
	g.OnSpace = func() { s.toggleViewerFor(t, g) }
	g.OnLayout = func() { // a resize or move is kept within a second; a form shows the columns shown
		s.sessionChanged()
		t.viewerFollow(g)
	}
	t.hold(g, t.body)
	t.body.Objects = []fyne.CanvasObject{g.View()}
	t.body.Refresh()
	s.count(t)
	s.sync()
	if v := t.restore; v != nil {
		t.restore = nil
		s.restoreView(t, *v)
	}
	if f := t.then; f != nil {
		t.then = nil
		f()
	}
	s.describe(t) // fkeys.go
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
				s.crashed(t.connID, err)
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
	n, final := t.model.Extent()
	n -= int64(t.model.Added()) // new rows are counted among the changes
	switch {
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
	if e := t.ed; e != nil {
		if n := e.changes(); n > 0 {
			text += " · " + pendingText(n)
		}
		e.showReview()
	}
	if t.said != "" {
		text += " — " + t.said
	}
	if t.problem != "" {
		text += " — " + t.problem
	}
	t.footer.SetText(text)
}

// say puts what an action did in the tab's footer. A table's footer keeps it
// beside the row count, which pages loading behind it would otherwise wipe;
// a query tab has no count to lose it to.
func (s *Shell) say(t *tab, text string) {
	if t.model == nil {
		t.footer.SetText(text)
		return
	}
	t.said = text
	s.showCount(t)
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
	if t := s.activeTab(); t != nil && t.model != nil {
		s.reload(t)
	}
}

// reload reads a tab's rows again.
func (s *Shell) reload(t *tab) {
	t.model.Invalidate()
	t.grid.ScheduleRefresh()
	s.count(t)
}

func (s *Shell) activeTabLoaded() bool {
	t := s.activeTab()
	return t != nil && t.model != nil
}

// closeTab closes a tab the user closed: unsaved text in it is forgotten.
func (s *Shell) closeTab(it *container.TabItem) { s.removeTab(it, false) }

// removeTab closes a tab. keep holds on to its unsaved text, which reopens
// at the next start: the app is closing the tab, not the user.
func (s *Shell) removeTab(it *container.TabItem, keep bool) {
	for i, t := range s.open {
		if t.item == it {
			if keep {
				s.keep(t)
			} else {
				s.forgetScratch(t)
			}
			t.cancel()
			if q := t.query; q != nil {
				if q.unschema != nil {
					q.unschema()
				}
				if q.session != nil {
					go q.session.Close() // may wait on the network; never on the UI goroutine
				}
			}
			s.open = append(s.open[:i], s.open[i+1:]...)
			break
		}
	}
	if p := s.paneOf(it); p != nil {
		p.Remove(it)
		if len(p.Items) == 0 {
			s.closePane(p)
		}
	}
	if len(s.open) == 0 {
		s.showTabs(false)
	}
	s.sync()
}

func (s *Shell) closeTabsOf(connID string, keep bool) {
	for _, t := range append([]*tab(nil), s.open...) {
		if t.connID == connID {
			s.removeTab(t.item, keep)
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

// filterObjects puts the keyboard in the explorer's filter, showing the
// sidebar first if it is hidden.
func (s *Shell) filterObjects() {
	if !s.sidebar.Visible() {
		s.toggleSidebar()
	}
	s.win.Canvas().Focus(s.Explorer.Filter)
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
	// Refreshing says the structure has changed under us, which is as true
	// of what completion holds as of the tree (T2.29).
	for _, connID := range s.d.WS.OpenIDs() {
		if l, ok := s.d.WS.Get(connID); ok {
			l.Schema().Invalidate()
		}
	}
}

func (s *Shell) duplicateSelected() {
	id, ok := s.selectedConn()
	if !ok {
		return
	}
	if _, err := s.d.Conns.Duplicate(id); err != nil {
		s.showError(err)
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
	s.release(id, false) // its tabs close on purpose: the dialog said so
	if err := s.d.Conns.Delete(id); err != nil {
		s.showError(err)
		return
	}
	s.refreshFavorites() // its favourites went with it
	s.Explorer.Refresh(explorer.RootID)
	s.sync()
}

// disconnect closes a connection and every tab using it, and collapses its
// tree node so that nothing reconnects until the user expands it again.
// Unsaved query text in those tabs reopens at the next start.
func (s *Shell) disconnect(id string) { s.release(id, true) }

// release is disconnect, keeping unsaved query text or not.
func (s *Shell) release(id string, keep bool) {
	s.closeSessions(func(t *tab) bool { return t.connID == id }) // before the pool: see shutdown
	s.closeTabsOf(id, keep)
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
	defer s.refreshFavorites() // a renamed connection renames its favourites' rows
	if _, open := s.d.WS.Get(id); edited && open {
		s.disconnect(id)
	}
	s.Explorer.Refresh(explorer.RootID)
	s.markTabs() // its environment may have changed under a tab still open
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
	case cmdPinTab:
		t := s.activeTab()
		return t != nil && t.pinned
	case cmdHistory:
		return s.panelIs(panelHistory)
	case cmdOpenSaved:
		return s.panelIs(panelSaved)
	case cmdShortcuts:
		return s.panelIs(panelShortcuts)
	case cmdTasks:
		return s.panelIs(panelTasks)
	case cmdFavorite:
		f, ok := s.selectedFavorite()
		return ok && s.isFavorite(f)
	}
	if name, ok := strings.CutPrefix(id, accentPrefix); ok {
		return s.d.Theme.Accent.String() == name
	}
	return false
}

func (s *Shell) colours() uitheme.Palette {
	return s.d.Theme.PaletteFor(s.app.Settings().ThemeVariant())
}

// recolour repaints what caches colours: open grids draw from a palette
// captured when they were built.
func (s *Shell) recolour() {
	if s.errors != nil {
		s.errors.repaint(s.colours())
	}
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
	// The session and unsaved query text first, while the tabs still hold
	// them. Both come back at the next start (NFR-R3).
	s.saveSession()
	for _, t := range s.open {
		s.keep(t)
	}
	if s.writer != nil && !s.writer.close(shutdownWait) {
		s.d.Log.Warn("unsaved query text or the session still being written at exit")
	}
	s.cancel()
	// Tasks stop with the context, and clean up after themselves: wait, or
	// an export quit halfway would leave a file that looks complete.
	if !s.waitTasks(shutdownWait) {
		s.d.Log.Warn("a task still stopping at exit")
	}
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
