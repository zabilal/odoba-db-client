// Package tabbar is the workspace's tab bar (T1.4, FR-15.2). It does what
// Fyne's DocTabs does, on the same *container.TabItem and keeping the
// selection the same way, and what DocTabs cannot: a tab is dragged to a new
// place. Every control has a name a screen reader can read out, a tab's
// close control included, and a tab asks for its context menu.
package tabbar

import (
	"image/color"
	"slices"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

const (
	// maxLabel bounds a tab's title, which an ellipsis cuts short past it.
	maxLabel = 200
	// barHeight is the line under the selected tab: it is marked by more
	// than a colour. The line, and the mark where a dragged tab would land,
	// are in the accent's text colour (Fyne's hyperlink colour), which keeps
	// 3:1 against the tabs where the plain accent falls just short in dark.
	barHeight   = 2
	markerWidth = 2
)

// Tabs is a row of tabs over the selected tab's content.
type Tabs struct {
	widget.BaseWidget

	Items []*container.TabItem

	// OnSelected is called when a tab becomes the selected one.
	OnSelected func(*container.TabItem)
	// OnTapped is called when a tab is tapped, after it is selected, even
	// if it was selected already: a tap says which tab bar is in use.
	OnTapped func(*container.TabItem)
	// CloseIntercept is called when a tab's close control is pressed. When
	// it is nil, the tab is removed.
	CloseIntercept func(*container.TabItem)
	// OnMove is called when a tab is dragged to a new place: to is where it
	// goes once taken out of its old one. When it is nil, the tab moves.
	OnMove func(it *container.TabItem, to int)
	// OnMenu is called for a secondary tap on a tab, with where on the
	// canvas it was.
	OnMenu func(it *container.TabItem, at fyne.Position)
	// MarkFor gives a tab's mark, or nil for none. It is asked each time
	// the tab is drawn, so a mark follows what it stands for.
	MarkFor func(*container.TabItem) *Mark

	current int
	chips   map[*container.TabItem]*chip
	drag    *dragState
	r       *renderer
}

// Mark is a word a tab carries before its title, on a fill of its own: a
// connection's environment, as PROD on red (FR-1.7, UX principle 8). The
// word is what makes it more than a colour, and a screen reader hears it
// with the title.
type Mark struct {
	Label      string
	Fill, Text color.Color
}

// dragState is a drag under way: the tab, how far it has gone, and where it
// would land.
type dragState struct {
	c  *chip
	dx float32
	to int
}

// New makes a tab bar holding items, the first of them selected.
func New(items ...*container.TabItem) *Tabs {
	t := &Tabs{Items: items, current: -1, chips: map[*container.TabItem]*chip{}}
	if len(items) > 0 {
		t.current = 0
	}
	t.ExtendBaseWidget(t)
	return t
}

// Append adds a tab at the end. The first tab added is selected.
func (t *Tabs) Append(it *container.TabItem) { t.SetItems(append(t.Items, it)) }

// SetItems replaces the tabs. The selection keeps its place, as DocTabs
// keeps it, or goes to the last tab if its place has gone.
func (t *Tabs) SetItems(items []*container.TabItem) {
	t.Items = items
	switch n := len(items); {
	case n == 0:
		t.current = -1
	case t.current < 0:
		t.SelectIndex(0)
	case t.current >= n:
		t.SelectIndex(n - 1)
	}
	t.Refresh()
}

// Remove takes a tab out. If it was the selected one, the tab taking its
// place is selected, or the one before it if it was last.
func (t *Tabs) Remove(it *container.TabItem) {
	i := slices.Index(t.Items, it)
	if i < 0 {
		return
	}
	was := t.Selected()
	t.Items = slices.Delete(slices.Clone(t.Items), i, i+1)
	switch {
	case len(t.Items) == 0:
		t.current = -1
	case i < t.current:
		t.current--
	case t.current >= len(t.Items):
		t.current = len(t.Items) - 1
	}
	t.Refresh()
	if now := t.Selected(); now != was && now != nil && t.OnSelected != nil {
		t.OnSelected(now)
	}
}

// Select selects a tab.
func (t *Tabs) Select(it *container.TabItem) {
	if i := slices.Index(t.Items, it); i >= 0 {
		t.SelectIndex(i)
	}
}

// SelectIndex selects the tab at i. Selecting the selected tab, or one that
// is not there, does nothing.
func (t *Tabs) SelectIndex(i int) {
	if i == t.current || i < 0 || i >= len(t.Items) {
		return
	}
	t.current = i
	t.Refresh()
	if t.OnSelected != nil {
		t.OnSelected(t.Items[i])
	}
}

// Selected is the selected tab, or nil.
func (t *Tabs) Selected() *container.TabItem {
	if t.current < 0 || t.current >= len(t.Items) {
		return nil
	}
	return t.Items[t.current]
}

// SelectedIndex is the selected tab's place, or -1.
func (t *Tabs) SelectedIndex() int { return t.current }

func (t *Tabs) close(it *container.TabItem) {
	if t.CloseIntercept != nil {
		t.CloseIntercept(it)
		return
	}
	t.Remove(it)
}

// dragged follows a tab dragged by dx more, marking where it would land.
// Only the distance is used: where the pointer is reported to be differs
// between Fyne's drivers.
func (t *Tabs) dragged(c *chip, dx float32) {
	if t.drag == nil || t.drag.c != c {
		t.drag = &dragState{c: c}
	}
	t.drag.dx += dx
	t.drag.to = t.dropIndex(c, t.drag.dx)
	if t.r != nil {
		t.r.showMarker()
	}
}

// dragEnd moves a dragged tab to where it was dropped.
func (t *Tabs) dragEnd(c *chip) {
	d := t.drag
	t.drag = nil
	if t.r != nil {
		t.r.showMarker()
	}
	from := slices.Index(t.Items, c.item)
	if d == nil || d.c != c || from < 0 || d.to == from {
		return
	}
	if t.OnMove != nil {
		t.OnMove(c.item, d.to)
		return
	}
	sel := t.Selected()
	t.Items = slices.Insert(slices.Delete(slices.Clone(t.Items), from, from+1), d.to, c.item)
	t.current = slices.Index(t.Items, sel)
	t.Refresh()
}

// dropIndex is where a tab moved by dx would land: after every other tab
// whose middle its own middle has passed.
func (t *Tabs) dropIndex(c *chip, dx float32) int {
	mid := c.Position().X + c.Size().Width/2 + dx
	to := 0
	for _, it := range t.Items {
		if o := t.chips[it]; it != c.item && o != nil && o.Position().X+o.Size().Width/2 < mid {
			to++
		}
	}
	return to
}

func (t *Tabs) chipFor(it *container.TabItem) *chip {
	c := t.chips[it]
	if c == nil {
		c = newChip(t, it)
		t.chips[it] = c
	}
	return c
}

// allMenu lists every tab, the selected one ticked, for when they do not
// all fit.
func (t *Tabs) allMenu() *fyne.Menu {
	items := make([]*fyne.MenuItem, len(t.Items))
	for i, it := range t.Items {
		items[i] = fyne.NewMenuItem(it.Text, func() { t.Select(it) })
		items[i].Checked = i == t.current
	}
	return fyne.NewMenu("", items...)
}

func (t *Tabs) CreateRenderer() fyne.WidgetRenderer {
	t.ExtendBaseWidget(t)
	r := &renderer{t: t, row: container.NewHBox(), marker: canvas.NewRectangle(color.Transparent),
		line: widget.NewSeparator(), content: container.NewStack(), revealed: -1}
	r.marker.Hide()
	r.scroll = container.NewHScroll(container.NewStack(r.row, container.NewWithoutLayout(r.marker)))
	r.all = widget.NewButton("All Tabs", r.showAll)
	r.all.Importance = widget.LowImportance
	t.r = r
	r.Refresh()
	return r
}

type renderer struct {
	t       *Tabs
	row     *fyne.Container
	marker  *canvas.Rectangle
	scroll  *container.Scroll
	all     *widget.Button
	line    *widget.Separator
	content *fyne.Container
	// showing is the tab whose content is shown, and revealed the place of
	// the selection last scrolled into view.
	showing  *container.TabItem
	revealed int
}

func (r *renderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.scroll, r.all, r.line, r.content}
}

func (r *renderer) Destroy() {}

func (r *renderer) MinSize() fyne.Size {
	c := r.content.MinSize()
	return fyne.NewSize(max(c.Width, r.all.MinSize().Width),
		r.row.MinSize().Height+r.line.MinSize().Height+c.Height)
}

func (r *renderer) Layout(size fyne.Size) {
	h := r.row.MinSize().Height
	w := size.Width
	if r.row.MinSize().Width > size.Width && len(r.t.Items) > 1 {
		aw := r.all.MinSize().Width
		r.all.Resize(fyne.NewSize(aw, h))
		r.all.Move(fyne.NewPos(size.Width-aw, 0))
		r.all.Show()
		w -= aw
	} else {
		r.all.Hide()
	}
	r.scroll.Resize(fyne.NewSize(w, h))
	lh := r.line.MinSize().Height
	r.line.Resize(fyne.NewSize(size.Width, lh))
	r.line.Move(fyne.NewPos(0, h))
	r.content.Resize(fyne.NewSize(size.Width, size.Height-h-lh))
	r.content.Move(fyne.NewPos(0, h+lh))
	r.reveal()
}

func (r *renderer) Refresh() {
	t := r.t
	objs := make([]fyne.CanvasObject, len(t.Items))
	for i, it := range t.Items {
		c := t.chipFor(it)
		c.selected = i == t.current
		c.Refresh()
		objs[i] = c
	}
	for it := range t.chips {
		if !slices.Contains(t.Items, it) {
			delete(t.chips, it)
		}
	}
	r.row.Objects = objs
	r.row.Refresh()
	// Only a change of tab refreshes the content: refreshing a grid each
	// time a tab is added or renamed would be work for nothing.
	if sel := t.Selected(); sel != r.showing {
		r.showing = sel
		r.content.Objects = nil
		if sel != nil {
			r.content.Objects = []fyne.CanvasObject{sel.Content}
		}
		r.content.Refresh()
	}
	r.showMarker()
	r.Layout(t.Size())
	canvas.Refresh(t)
}

// reveal scrolls a newly selected tab into view.
func (r *renderer) reveal() {
	if r.t.current == r.revealed {
		return
	}
	c := r.t.chips[r.t.Selected()]
	if c == nil || r.scroll.Size().Width <= 0 {
		return
	}
	r.revealed = r.t.current
	x, w, view := c.Position().X, c.Size().Width, r.scroll.Size().Width
	off := r.scroll.Offset.X
	switch {
	case x < off:
		off = x
	case x+w > off+view:
		off = x + w - view
	default:
		return
	}
	r.scroll.Offset.X = off
	r.scroll.Refresh()
}

// showMarker draws, while a tab is dragged, a line where it would land.
func (r *renderer) showMarker() {
	d := r.t.drag
	if d == nil {
		r.marker.Hide()
		return
	}
	r.marker.FillColor = r.t.Theme().Color(theme.ColorNameHyperlink, variant())
	r.marker.Resize(fyne.NewSize(markerWidth, r.row.Size().Height))
	r.marker.Move(fyne.NewPos(r.gap(d)-markerWidth/2, 0))
	r.marker.Show()
	r.marker.Refresh()
}

// gap is where along the row a dragged tab would land: between two tabs,
// or at either end.
func (r *renderer) gap(d *dragState) float32 {
	var others []*chip
	for _, it := range r.t.Items {
		if c := r.t.chips[it]; c != nil && c != d.c {
			others = append(others, c)
		}
	}
	half := r.t.Theme().Size(theme.SizeNamePadding) / 2
	switch {
	case len(others) == 0:
		return d.c.Position().X
	case d.to == 0:
		return others[0].Position().X
	case d.to >= len(others):
		last := others[len(others)-1]
		return last.Position().X + last.Size().Width + half
	}
	return others[d.to].Position().X - half
}

func (r *renderer) showAll() {
	d := fyne.CurrentApp().Driver()
	c := d.CanvasForObject(r.all)
	if c == nil {
		return
	}
	at := d.AbsolutePositionForObject(r.all).Add(fyne.NewPos(0, r.all.Size().Height))
	widget.ShowPopUpMenuAtPosition(r.t.allMenu(), c, at)
}

// chip is one tab in the row.
type chip struct {
	widget.BaseWidget
	t        *Tabs
	item     *container.TabItem
	selected bool
	hovered  bool
	close    *closer
}

func newChip(t *Tabs, it *container.TabItem) *chip {
	c := &chip{t: t, item: it}
	c.close = &closer{c: c}
	c.close.ExtendBaseWidget(c.close)
	c.ExtendBaseWidget(c)
	return c
}

func (c *chip) Tapped(*fyne.PointEvent) {
	c.t.Select(c.item)
	if c.t.OnTapped != nil {
		c.t.OnTapped(c.item)
	}
}

func (c *chip) TappedSecondary(e *fyne.PointEvent) {
	if c.t.OnMenu != nil {
		c.t.OnMenu(c.item, e.AbsolutePosition)
	}
}

func (c *chip) Dragged(e *fyne.DragEvent) { c.t.dragged(c, e.Dragged.DX) }
func (c *chip) DragEnd()                  { c.t.dragEnd(c) }

func (c *chip) MouseIn(*desktop.MouseEvent)    { c.hovered = true; c.Refresh() }
func (c *chip) MouseMoved(*desktop.MouseEvent) {}
func (c *chip) MouseOut()                      { c.hovered = false; c.Refresh() }

func (c *chip) mark() *Mark {
	if c.t.MarkFor == nil {
		return nil
	}
	return c.t.MarkFor(c.item)
}

// AccessibilityLabel is the tab's title, and its mark's word.
func (c *chip) AccessibilityLabel() string {
	if m := c.mark(); m != nil {
		return c.item.Text + ", " + m.Label
	}
	return c.item.Text
}

func (c *chip) AccessibilityRole() fyne.AccessibleRole { return fyne.AccessibleRoleButton }

func (c *chip) CreateRenderer() fyne.WidgetRenderer {
	r := &chipRenderer{c: c, bg: canvas.NewRectangle(color.Transparent), bar: canvas.NewRectangle(color.Transparent),
		icon: canvas.NewImageFromResource(nil), label: widget.NewLabel(""),
		pill: canvas.NewRectangle(color.Transparent), word: canvas.NewText("", color.Transparent)}
	r.icon.FillMode = canvas.ImageFillContain
	r.label.Truncation = fyne.TextTruncateEllipsis
	r.word.TextStyle.Bold = true
	r.word.Alignment = fyne.TextAlignCenter
	r.objs = []fyne.CanvasObject{r.bg, r.bar, r.icon, r.pill, r.word, r.label, c.close}
	r.Refresh()
	return r
}

type chipRenderer struct {
	c     *chip
	bg    *canvas.Rectangle
	bar   *canvas.Rectangle
	icon  *canvas.Image
	label *widget.Label
	objs  []fyne.CanvasObject
	// pill and word draw the mark; m is the one drawn.
	pill *canvas.Rectangle
	word *canvas.Text
	m    *Mark
}

func (r *chipRenderer) Objects() []fyne.CanvasObject { return r.objs }
func (r *chipRenderer) Destroy()                     {}

// labelWidth is the title's width, bold as when selected, so selecting a
// tab does not change its size.
func (r *chipRenderer) labelWidth() float32 {
	th := r.c.Theme()
	s := fyne.MeasureText(r.c.item.Text, th.Size(theme.SizeNameText), fyne.TextStyle{Bold: true})
	return min(s.Width+2*th.Size(theme.SizeNameInnerPadding), maxLabel)
}

// pillSize is the mark's size: its word in caption bold, with room around.
func (r *chipRenderer) pillSize() fyne.Size {
	th := r.c.Theme()
	s := fyne.MeasureText(r.m.Label, th.Size(theme.SizeNameCaptionText), fyne.TextStyle{Bold: true})
	return fyne.NewSize(s.Width+th.Size(theme.SizeNameInnerPadding), s.Height+2)
}

func (r *chipRenderer) MinSize() fyne.Size {
	th := r.c.Theme()
	pad, icon := th.Size(theme.SizeNamePadding), th.Size(theme.SizeNameInlineIcon)
	w := pad + r.labelWidth() + icon + pad
	if r.c.item.Icon != nil {
		w += icon
	}
	if r.m != nil {
		w += r.pillSize().Width + pad
	}
	return fyne.NewSize(w, r.label.MinSize().Height+barHeight)
}

func (r *chipRenderer) Layout(size fyne.Size) {
	th := r.c.Theme()
	pad, icon := th.Size(theme.SizeNamePadding), th.Size(theme.SizeNameInlineIcon)
	r.bg.Resize(size)
	r.bar.Resize(fyne.NewSize(size.Width, barHeight))
	r.bar.Move(fyne.NewPos(0, size.Height-barHeight))
	h := size.Height - barHeight
	x := pad
	if r.c.item.Icon != nil {
		r.icon.Resize(fyne.NewSquareSize(icon))
		r.icon.Move(fyne.NewPos(x, (h-icon)/2))
		x += icon
	}
	if r.m != nil {
		ps := r.pillSize()
		at := fyne.NewPos(x, (h-ps.Height)/2)
		r.pill.Resize(ps)
		r.pill.Move(at)
		r.word.Resize(ps)
		r.word.Move(at)
		x += ps.Width + pad
	}
	r.label.Resize(fyne.NewSize(size.Width-x-icon-pad, h))
	r.label.Move(fyne.NewPos(x, 0))
	r.c.close.Resize(fyne.NewSquareSize(icon))
	r.c.close.Move(fyne.NewPos(size.Width-pad-icon, (h-icon)/2))
}

func (r *chipRenderer) Refresh() {
	c := r.c
	th, v := c.Theme(), variant()
	r.label.TextStyle.Bold = c.selected
	r.label.SetText(c.item.Text)
	r.icon.Resource = c.item.Icon
	setShown(r.icon, c.item.Icon != nil)
	r.icon.Refresh()
	r.m = c.mark()
	if r.m != nil {
		r.pill.FillColor, r.pill.CornerRadius = r.m.Fill, th.Size(theme.SizeNameSelectionRadius)
		r.word.Text, r.word.Color, r.word.TextSize = r.m.Label, r.m.Text, th.Size(theme.SizeNameCaptionText)
	}
	setShown(r.pill, r.m != nil)
	setShown(r.word, r.m != nil)
	r.pill.Refresh()
	r.word.Refresh()
	switch {
	case c.selected:
		r.bg.FillColor = th.Color(theme.ColorNameInputBackground, v)
	case c.hovered:
		r.bg.FillColor = th.Color(theme.ColorNameHover, v)
	default:
		r.bg.FillColor = color.Transparent
	}
	r.bg.CornerRadius = th.Size(theme.SizeNameSelectionRadius)
	r.bg.Refresh()
	r.bar.FillColor = th.Color(theme.ColorNameHyperlink, v)
	setShown(r.bar, c.selected)
	r.bar.Refresh()
	setShown(c.close, c.selected || c.hovered)
	r.Layout(c.Size())
}

// closer is a tab's close control: an ×, named for a screen reader after
// the tab it closes. It is not hoverable, so the pointer over it still
// counts as over the tab, and it stays shown.
type closer struct {
	widget.BaseWidget
	c *chip
}

func (x *closer) Tapped(*fyne.PointEvent) { x.c.t.close(x.c.item) }

// AccessibilityLabel says which tab the control closes.
func (x *closer) AccessibilityLabel() string { return "Close " + x.c.item.Text }

func (x *closer) AccessibilityRole() fyne.AccessibleRole { return fyne.AccessibleRoleButton }

func (x *closer) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(widget.NewIcon(theme.CancelIcon()))
}

func variant() fyne.ThemeVariant { return fyne.CurrentApp().Settings().ThemeVariant() }

func setShown(o fyne.CanvasObject, on bool) {
	if on {
		o.Show()
	} else {
		o.Hide()
	}
}
