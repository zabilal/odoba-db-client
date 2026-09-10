package grid

import (
	"context"
	"image/color"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/ui/theme"
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

	// selection is a single anchor cell for now; range selection is FR-3.7
	// and lands in Phase 1 proper.
	selRow, selCol int

	// refreshQueued coalesces refreshes. See ScheduleRefresh.
	refreshQueued atomic.Bool
}

// RefreshInterval bounds how often the table is refreshed in response to data
// arriving. Roughly one frame at 60 fps.
const RefreshInterval = 16 * time.Millisecond

// ScheduleRefresh requests a table refresh, coalescing bursts into one.
//
// Refreshing directly from every page-load callback is wrong twice over.
//
// It is a correctness bug: during a fast scroll many pages land within
// milliseconds of each other, each calling fyne.Do from its own goroutine.
// Those re-enter Fyne's table renderer while it is already refreshing and
// corrupt its internal cell map — a hard runtime fault, not a glitch. Spike W1
// hit exactly this (see ADR-0002).
//
// It is also wasteful: a full refresh redraws every visible cell, so eight
// pages landing together would redraw the viewport eight times to show one
// screen of data.
//
// At most one refresh is in flight; further requests during the window are
// absorbed by the one already queued.
func (g *TableGrid) ScheduleRefresh() {
	if g.refreshQueued.Swap(true) {
		return
	}
	time.AfterFunc(RefreshInterval, func() {
		g.refreshQueued.Store(false)
		fyne.Do(func() {
			if g.Table != nil {
				g.Table.Refresh()
			}
		})
	})
}

// NewTableGrid builds a grid over a model.
func NewTableGrid(ctx context.Context, m *Model, pal theme.Palette) *TableGrid {
	g := &TableGrid{
		model:   m,
		palette: pal,
		loc:     time.Local,
		ctx:     ctx,
		selRow:  -1,
		selCol:  -1,
	}

	cols := m.Columns()
	g.widths = make([]float32, len(cols))
	for i, c := range cols {
		g.widths[i] = defaultWidth(c)
	}

	t := widget.NewTable(g.length, g.createCell, g.UpdateCell)
	t.ShowHeaderRow = true
	t.CreateHeader = g.createHeader
	t.UpdateHeader = g.updateHeader

	// Freeze the first column. FR-3.2 asks for user-configurable pinning;
	// this proves the mechanism exists and costs nothing extra to draw.
	t.StickyColumnCount = 1

	for i, w := range g.widths {
		t.SetColumnWidth(i, w)
	}

	t.OnSelected = func(id widget.TableCellID) {
		g.selRow, g.selCol = id.Row, id.Col
	}

	g.Table = t
	return g
}

func (g *TableGrid) length() (int, int) {
	total, known := g.model.Total()
	if !known {
		// Until the count resolves, report what is resident so the user sees
		// data immediately rather than an empty grid (NFR-P3).
		total = int64(g.model.Stats().ResidentPages) * PageSize
	}
	return int(total), len(g.model.Columns())
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

func (g *TableGrid) background(id widget.TableCellID) color.Color {
	if id.Row == g.selRow {
		return g.palette.SelectedUnemphasized
	}
	if g.RowStates != nil {
		switch g.RowStates(int64(id.Row)) {
		case CellKind(200): // placeholder; real changeset states land in Phase 1
		}
	}
	// macOS alternating table rows aid row tracking across wide tables.
	if id.Row%2 == 1 {
		return g.palette.AlternateRow
	}
	return g.palette.ContentBackground
}

func (g *TableGrid) createHeader() fyne.CanvasObject { return newCellWidget() }

func (g *TableGrid) updateHeader(id widget.TableCellID, o fyne.CanvasObject) {
	cell, ok := o.(*cellWidget)
	if !ok {
		return
	}
	cols := g.model.Columns()
	if id.Col < 0 || id.Col >= len(cols) {
		cell.set("", g.palette.SecondaryLabel, g.palette.SidebarBackground,
			fyne.TextAlignLeading, fyne.TextStyle{})
		return
	}
	cell.set(cols[id.Col].Name, g.palette.SecondaryLabel, g.palette.SidebarBackground,
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
