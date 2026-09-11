package grid

import (
	"image/color"
	"slices"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	ftheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/ui/theme"
)

// SetFilterable puts a filter field under each column's title (FR-3.5), or
// takes them away. Call it before the grid is first shown: the table makes
// its header cells when it first draws.
func (g *TableGrid) SetFilterable(on bool) {
	g.filterable = on
	height := theme.RowHeight
	if on {
		height += newColumnHeader(g).field.MinSize().Height + theme.SpaceXS
	}
	g.Table.SetRowHeight(-1, height)
}

// Filterable reports whether the grid has a filter row.
func (g *TableGrid) Filterable() bool { return g.filterable }

// columnHeader is a column's header: the title, which sorts when tapped,
// above a field holding the column's filter on a grid with a filter row, and
// a handle on its right edge that sets the column's width.
type columnHeader struct {
	widget.BaseWidget
	bg     *canvas.Rectangle
	title  *headerCell
	filter *filterField      // nil on a grid with no filter row
	field  fyne.CanvasObject // the filter, at a small control's size
	handle *resizeHandle
}

func newColumnHeader(g *TableGrid) *columnHeader {
	h := &columnHeader{bg: canvas.NewRectangle(g.palette.SidebarBackground),
		title: newHeaderCell(g), handle: newResizeHandle(g)}
	if g.filterable {
		h.filter = newFilterField(g)
		h.field = container.NewThemeOverride(h.filter, compact{})
	}
	h.ExtendBaseWidget(h)
	return h
}

func (h *columnHeader) CreateRenderer() fyne.WidgetRenderer {
	objects := []fyne.CanvasObject{h.bg, h.title}
	if h.field != nil {
		objects = append(objects, h.field)
	}
	return &columnHeaderRenderer{h: h, objects: append(objects, h.handle)}
}

type columnHeaderRenderer struct {
	h       *columnHeader
	objects []fyne.CanvasObject
}

// Layout puts the title in the top row and the field under it, with a gap
// below the field and between neighbouring fields.
func (r *columnHeaderRenderer) Layout(size fyne.Size) {
	r.h.bg.Resize(size)
	title := size.Height
	if r.h.field != nil {
		title = theme.RowHeight
		inset := theme.SpaceXS / 2
		r.h.field.Move(fyne.NewPos(inset, theme.RowHeight))
		r.h.field.Resize(fyne.NewSize(max(0, size.Width-2*inset), max(0, size.Height-theme.RowHeight-theme.SpaceXS)))
	}
	r.h.title.Move(fyne.NewPos(0, 0))
	r.h.title.Resize(fyne.NewSize(size.Width, title))
	r.h.handle.Move(fyne.NewPos(max(0, size.Width-handleWidth), 0))
	r.h.handle.Resize(fyne.NewSize(min(handleWidth, size.Width), size.Height))
}

// MinSize is one row, though the header is taller. Fyne makes every row as
// high as the taller of the cell and header templates, so a header asking
// for its real height would make every data row that high too. The grid sets
// the header's height itself (SetFilterable).
func (r *columnHeaderRenderer) MinSize() fyne.Size { return fyne.NewSize(0, theme.RowHeight) }

func (r *columnHeaderRenderer) Refresh() {
	r.h.bg.Refresh()
	r.h.title.Refresh()
	if r.h.field != nil {
		r.h.field.Refresh()
	}
}

func (r *columnHeaderRenderer) Objects() []fyne.CanvasObject { return r.objects }

func (r *columnHeaderRenderer) Destroy() {}

// compact is the app's theme at a small control's size, for the filter
// fields, so a header with a filter row is two rows high rather than three.
// It asks the app for everything else each time, so the fields follow an
// appearance change.
type compact struct{}

func appTheme() fyne.Theme { return fyne.CurrentApp().Settings().Theme() }

func (compact) Color(n fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	return appTheme().Color(n, v)
}
func (compact) Font(s fyne.TextStyle) fyne.Resource     { return appTheme().Font(s) }
func (compact) Icon(n fyne.ThemeIconName) fyne.Resource { return appTheme().Icon(n) }
func (compact) Size(n fyne.ThemeSizeName) float32 {
	switch n {
	case ftheme.SizeNameText:
		return theme.TextCallout
	case ftheme.SizeNameInnerPadding:
		return theme.SpaceXS
	}
	return appTheme().Size(n)
}

// filterField is one column's filter. Return applies every column's filter;
// Escape clears this one and applies the rest.
//
// The table recycles header cells as it scrolls sideways, so a field belongs
// to a column only until its next update. The text lives in the grid, and
// the field shows whichever column it is bound to.
type filterField struct {
	widget.Entry
	g   *TableGrid
	col int
}

func newFilterField(g *TableGrid) *filterField {
	f := &filterField{g: g, col: -1}
	f.PlaceHolder = "Filter"
	f.ExtendBaseWidget(f)
	f.OnChanged = func(s string) {
		if f.col >= 0 && f.col < len(g.filters) {
			g.filters[f.col] = s
		}
	}
	f.OnSubmitted = func(string) { g.ApplyFilters() }
	return f
}

func (f *filterField) TypedKey(k *fyne.KeyEvent) {
	if k.Name == fyne.KeyEscape && f.Text != "" {
		f.SetText("")
		f.g.ApplyFilters()
		return
	}
	f.Entry.TypedKey(k)
}

// bindFilter points a recycled field at a column. A focused field that moves
// to another column gives up the focus: otherwise typing would carry on into
// a column the person is not looking at.
func (g *TableGrid) bindFilter(f *filterField, col int) {
	if f.col != col {
		if c := fyne.CurrentApp().Driver().CanvasForObject(f); c != nil && c.Focused() == f {
			c.Unfocus()
		}
	}
	f.col = -1 // SetText's OnChanged must not write into the old column
	text := ""
	if col >= 0 && col < len(g.filters) {
		text = g.filters[col]
	}
	if f.Text != text {
		f.SetText(text)
	}
	f.col = col
}

// FilterTexts is every column's filter text, as typed.
func (g *TableGrid) FilterTexts() []string { return slices.Clone(g.filters) }

// SetFilterText sets one column's filter text without applying it.
func (g *TableGrid) SetFilterText(col int, text string) {
	if col >= 0 && col < len(g.filters) {
		g.filters[col] = text
		g.refresh()
	}
}

// ApplyFilters hands every column's filter text to OnFilter.
func (g *TableGrid) ApplyFilters() {
	if g.OnFilter != nil {
		g.OnFilter(slices.Clone(g.filters))
	}
}

// SetFilterErrors marks the columns whose filter could not be applied, and
// clears the mark from every other column. The shell says why in the footer:
// the red title is never the only sign.
func (g *TableGrid) SetFilterErrors(cols ...int) {
	clear(g.filterErr)
	for _, c := range cols {
		if c >= 0 && c < len(g.filterErr) {
			g.filterErr[c] = true
		}
	}
	g.refresh()
}

// FilterError reports whether a column's filter is marked as failed.
func (g *TableGrid) FilterError(col int) bool {
	return col >= 0 && col < len(g.filterErr) && g.filterErr[col]
}
