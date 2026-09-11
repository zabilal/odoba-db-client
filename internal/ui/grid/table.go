package grid

import (
	"context"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"image/color"
	"slices"
	"strconv"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/ui/theme"
	"github.com/ikigai-db/ikigai-db/internal/ui/uithread"
)

// TableGrid is the widget.Table-based grid candidate for spike W1.
//
// widget.Table already supplies virtualisation, sticky (frozen) rows and
// columns, per-column widths and a header row — FR-3.1 and FR-3.2 largely for
// free. The open question this spike answers is whether its per-cell object
// model can sustain NFR-P4 over a result set of any size.
type TableGrid struct {
	Table *widget.Table

	model   *Model
	palette theme.Palette
	loc     *time.Location

	ctx context.Context

	// widths are the resolved pixel widths per column.
	widths []float32

	// RowStates optionally reports a row's changeset state, tinting the row
	// (FR-4.3). Nil in the spike.
	RowStates func(row int64) CellKind

	// sel is the selected cells (FR-3.7); table is the Table that hears the
	// clicks and keys that change it.
	sel   Selection
	table *gridTable

	// refresh is the coalesced refresh trigger. See ScheduleRefresh.
	refresh func()

	bg   *canvas.Rectangle
	view *fyne.Container

	// Sortable lets the header sort the grid, and OnSort hears the new sort.
	// A browse re-sorts on the server; a query's result cannot, since that
	// means running the query again, so it stays unsortable.
	Sortable bool
	OnSort   func(keys []SortKey)
	sorts    []SortKey

	// OnFilter hears every column's filter text when one is submitted, on a
	// grid with a filter row (SetFilterable).
	OnFilter func(texts []string)
	// OnPickValues opens a column's list of values (FR-3.4) at a point on
	// screen; a right-click on the column's title asks for it there. Nil
	// where the source cannot list.
	OnPickValues func(col int, at fyne.Position)
	// OnSelectCell hears that the selected cell changed.
	OnSelectCell func()
	// OnCopy is asked to copy the selection, by ⌘C on the focused grid.
	OnCopy func()
	// OnSpace is asked to show or hide the cell viewer, by Space on the
	// focused grid, as Quick Look does.
	OnSpace    func()
	filterable bool
	filters    []string
	filterErr  []bool
}

// SortKey is one column of a sort, by index into the grid's columns.
type SortKey struct {
	Column     int
	Descending bool
}

// ToggleSort changes the sort as a header click does. Alone, a column cycles
// ascending, descending and unsorted, replacing any other sort; with add
// (⇧-click) it joins the sort as its next key, or cycles within it.
func (g *TableGrid) ToggleSort(col int, add bool) {
	if !g.Sortable {
		return
	}
	i := slices.IndexFunc(g.sorts, func(k SortKey) bool { return k.Column == col })
	var next []SortKey
	switch {
	case add && i < 0:
		next = append(slices.Clone(g.sorts), SortKey{Column: col})
	case add:
		next = slices.Clone(g.sorts)
		if next[i].Descending {
			next = slices.Delete(next, i, i+1)
		} else {
			next[i].Descending = true
		}
	case i >= 0 && len(g.sorts) == 1:
		if !g.sorts[0].Descending {
			next = []SortKey{{Column: col, Descending: true}}
		}
	default:
		next = []SortKey{{Column: col}}
	}
	g.SetSorts(next)
	if g.OnSort != nil {
		g.OnSort(slices.Clone(next))
	}
}

// Sorts is the sort the header shows.
func (g *TableGrid) Sorts() []SortKey { return slices.Clone(g.sorts) }

// SetSorts shows a sort in the header without asking for it: to put back the
// previous sort when a new one fails.
func (g *TableGrid) SetSorts(keys []SortKey) {
	g.sorts = slices.Clone(keys)
	if g.Table != nil {
		g.Table.Refresh()
	}
}

// sortMark is the header's arrow for a column, numbered when several
// columns sort.
func (g *TableGrid) sortMark(col int) string {
	for i, k := range g.sorts {
		if k.Column != col {
			continue
		}
		mark := " ↑"
		if k.Descending {
			mark = " ↓"
		}
		if len(g.sorts) > 1 {
			mark += strconv.Itoa(i + 1)
		}
		return mark
	}
	return ""
}

// View is what to put on screen: the table over the content background, so
// the space around and between cells belongs to the grid rather than showing
// the window through it.
func (g *TableGrid) View() fyne.CanvasObject { return g.view }

// ScheduleRefresh requests a table refresh, coalescing bursts into one.
//
// Refreshing directly from every page-load callback is wrong twice over.
//
// It is a correctness bug. During a fast scroll many pages land within
// milliseconds, each calling back from its own goroutine. Those callbacks
// re-enter Fyne's table renderer while it is already refreshing and corrupt
// its internal cell map. That is a hard runtime fault, not a glitch; spike W1
// hit exactly this (ADR-0002).
//
// It is also wasteful. A full refresh redraws every visible cell, so eight
// pages landing together would redraw the viewport eight times to show one
// screen of data.
//
// At most one refresh is in flight, run through the grid's Runner (the UI
// goroutine in production, a test's own goroutine under uithread.Queue).
func (g *TableGrid) ScheduleRefresh() { g.refresh() }

// SetPalette recolours the grid, for an appearance change. UI goroutine only.
func (g *TableGrid) SetPalette(p theme.Palette) {
	g.palette = p
	g.bg.FillColor = p.ContentBackground
	g.bg.Refresh()
	if g.Table != nil {
		g.Table.Refresh()
	}
}

// NewTableGrid builds a grid over a model, refreshing on Fyne's UI goroutine.
func NewTableGrid(ctx context.Context, m *Model, pal theme.Palette) *TableGrid {
	return NewTableGridWith(ctx, m, pal, uithread.Fyne, uithread.FrameDelay)
}

// NewTableGridWith builds a grid whose refreshes go through run, coalesced over
// delay. Tests pass a uithread.Queue and zero, so the refresh happens on the
// test's goroutine: Fyne's headless driver would otherwise run it on a timer's.
func NewTableGridWith(ctx context.Context, m *Model, pal theme.Palette, run uithread.Runner, delay time.Duration) *TableGrid {
	g := &TableGrid{
		model:   m,
		palette: pal,
		loc:     time.Local,
		ctx:     ctx,
	}

	cols := m.Columns()
	g.widths = make([]float32, len(cols))
	g.filters = make([]string, len(cols))
	g.filterErr = make([]bool, len(cols))
	for i, c := range cols {
		g.widths[i] = defaultWidth(c)
	}

	g.table = newGridTable(g)
	t := &g.table.Table
	t.ShowHeaderRow = true
	t.CreateHeader = g.createHeader
	t.UpdateHeader = g.updateHeader

	// Freeze the first column. FR-3.2 asks for user-configurable pinning;
	// this proves the mechanism exists and costs nothing extra to draw.
	t.StickyColumnCount = 1

	for i, w := range g.widths {
		t.SetColumnWidth(i, w)
	}

	g.Table = t
	g.bg = canvas.NewRectangle(pal.ContentBackground)
	// The grid's own table goes on screen, not the Table inside it: Fyne hands
	// mouse events to the object in the tree, and only the grid's hears the
	// modifiers of a click.
	g.view = container.NewStack(g.bg, g.table)
	g.refresh = uithread.Coalesce(run, delay, func() {
		if g.Table != nil {
			g.Table.Refresh()
		}
	})
	return g
}

func (g *TableGrid) length() (int, int) {
	n, final := g.model.Extent()
	if !final {
		// The total is unknown, as it is wherever counting is a full scan.
		// Show what has loaded plus one page of placeholders: drawing those is
		// what fetches the next page, so the grid grows as the user scrolls
		// until a short page marks the end. With nothing loaded yet, this is
		// the page of placeholders that schedules the first fetch (NFR-P3).
		// Sizing by resident pages alone would be zero rows, which draws no
		// cells and so never fetches anything.
		n += PageSize
	}
	return int(n), len(g.model.Columns())
}

func (g *TableGrid) createCell() fyne.CanvasObject { return newCellWidget() }

// UpdateCell renders one cell. This is the hot path: it runs for every visible
// cell whenever the table refreshes, and it is what the W1 benchmark measures.
func (g *TableGrid) UpdateCell(id widget.TableCellID, o fyne.CanvasObject) {
	cell, ok := o.(*cellWidget)
	if !ok {
		return
	}

	cols := g.model.Columns()
	if id.Col < 0 || id.Col >= len(cols) {
		return
	}
	col := cols[id.Col]

	row, loaded := g.model.Row(g.ctx, int64(id.Row))

	var c Cell
	switch {
	case !loaded:
		c = PendingCell()
	case id.Col < len(row):
		c = Format(row[id.Col], col, g.loc)
	default:
		c = Cell{Text: "", Kind: CellNormal}
	}

	fg := g.foreground(c.Kind)
	bg := g.background(id)

	align := fyne.TextAlignLeading
	if c.Kind.RightAligned() {
		align = fyne.TextAlignTrailing
	}

	style := fyne.TextStyle{}
	if c.Kind == CellNull || c.Kind == CellPending {
		// Italic is the second channel that keeps NULL from reading as the
		// literal string "NULL" (UX principle 7).
		style.Italic = true
	}
	if c.Kind == CellNumber {
		// Tabular figures make a numeric column scannable.
		style.Monospace = true
	}

	cell.set(c.Text, fg, bg, align, style)
}

func (g *TableGrid) foreground(k CellKind) color.Color {
	switch k {
	case CellNull:
		return g.palette.Null
	case CellPending:
		return g.palette.QuaternaryLabel
	case CellStructured, CellBinary:
		return g.palette.SecondaryLabel
	default:
		return g.palette.Label
	}
}

// background is a cell's fill. Rows are not striped: a cell can only tint its
// own width, and with the grid's background showing past the last column the
// stripes read as grey blocks rather than rows. Full-width stripes need a
// background drawn in step with scrolling (T1.50).
func (g *TableGrid) background(id widget.TableCellID) color.Color {
	if g.sel.Contains(id.Row, id.Col) {
		return g.palette.SelectedUnemphasized
	}
	if g.RowStates != nil {
		switch g.RowStates(int64(id.Row)) {
		case CellKind(200): // placeholder; real changeset states land in Phase 1
		}
	}
	return g.palette.ContentBackground
}

func (g *TableGrid) createHeader() fyne.CanvasObject {
	if g.filterable {
		return newColumnHeader(g)
	}
	return newHeaderCell(g)
}

func (g *TableGrid) updateHeader(id widget.TableCellID, o fyne.CanvasObject) {
	switch h := o.(type) {
	case *columnHeader:
		if h.bg.FillColor != g.palette.SidebarBackground {
			h.bg.FillColor = g.palette.SidebarBackground
			h.bg.Refresh()
		}
		g.updateTitle(id.Col, h.title)
		g.bindFilter(h.filter, id.Col)
	case *headerCell:
		g.updateTitle(id.Col, h)
	}
}

func (g *TableGrid) updateTitle(col int, h *headerCell) {
	h.col = col
	cols := g.model.Columns()
	if col < 0 || col >= len(cols) {
		h.set("", g.palette.SecondaryLabel, g.palette.SidebarBackground,
			fyne.TextAlignLeading, fyne.TextStyle{})
		return
	}
	fg := g.palette.SecondaryLabel
	if g.FilterError(col) {
		fg = g.palette.Danger
	}
	h.set(cols[col].Name+g.sortMark(col), fg, g.palette.SidebarBackground,
		fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
}

// Prefetch warms the model around the visible range. Driven by scroll position
// in the real widget; called explicitly by the spike harness.
func (g *TableGrid) Prefetch(first, last int64) {
	g.model.Prefetch(g.ctx, first, last)
}

// VisibleRows estimates how many rows fit in a viewport of the given height.
func VisibleRows(height float32) int {
	n := int(height / theme.RowHeight)
	if n < 1 {
		return 1
	}
	return n
}

// defaultWidth picks a starting column width from the declared type. Getting
// this roughly right matters: a grid that opens with every column the same
// width forces the user to resize before they can read anything.
func defaultWidth(c model.ColumnDef) float32 {
	switch c.Type.Class {
	case model.TypeBool:
		return 72
	case model.TypeInteger:
		return 96
	case model.TypeFloat, model.TypeDecimal:
		return 112
	case model.TypeDate:
		return 104
	case model.TypeTime:
		return 96
	case model.TypeTimestamp:
		return 168
	case model.TypeUUID:
		return 260
	case model.TypeJSON, model.TypeXML, model.TypeStruct, model.TypeArray:
		return 240
	case model.TypeBytes:
		return 180
	default:
		return 160
	}
}

// SelectedColumn is the selected cell's column, or -1.
func (g *TableGrid) SelectedColumn() int {
	if c, ok := g.sel.Active(); ok {
		return c.Col
	}
	return -1
}
