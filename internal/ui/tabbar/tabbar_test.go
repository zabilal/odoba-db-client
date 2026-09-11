package tabbar

import (
	"fmt"
	"image/color"
	"slices"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func named(names ...string) []*container.TabItem {
	out := make([]*container.TabItem, len(names))
	for i, n := range names {
		out[i] = container.NewTabItem(n, widget.NewLabel(n+" content"))
	}
	return out
}

// shown puts tabs holding names in a window of the given width, so they
// are laid out.
func shown(t *testing.T, width float32, names ...string) (*Tabs, []*container.TabItem) {
	t.Helper()
	test.NewTempApp(t)
	its := named(names...)
	tabs := New(its...)
	w := test.NewTempWindow(t, tabs)
	w.SetPadded(false)
	w.Resize(fyne.NewSize(width, 300))
	return tabs, its
}

func texts(its []*container.TabItem) []string {
	out := make([]string, len(its))
	for i, it := range its {
		out[i] = it.Text
	}
	return out
}

func TestAMarkIsDrawnBeforeTheTitleInWordsAndColour(t *testing.T) {
	tabs, its := shown(t, 800, "orders", "people")
	red := color.NRGBA{R: 0xC4, G: 0x1E, B: 0x18, A: 0xFF}
	c := tabs.chipFor(its[0])
	before := c.MinSize().Width
	tabs.MarkFor = func(it *container.TabItem) *Mark {
		if it == its[0] {
			return &Mark{Label: "PROD", Fill: red, Text: color.White}
		}
		return nil
	}
	tabs.Refresh()
	r := test.WidgetRenderer(c).(*chipRenderer)
	if !r.pill.Visible() || !r.word.Visible() || r.word.Text != "PROD" || r.pill.FillColor != red || r.word.Color != color.White {
		t.Fatalf("mark shown %v, saying %q in %v on %v", r.pill.Visible(), r.word.Text, r.word.Color, r.pill.FillColor)
	}
	if r.label.Position().X < r.pill.Position().X+r.pill.Size().Width {
		t.Errorf("the title at %v overlaps the mark at %v, %v wide", r.label.Position(), r.pill.Position(), r.pill.Size().Width)
	}
	if c.MinSize().Width <= before {
		t.Error("the mark should make room for itself, not squeeze the title")
	}
	if got := c.AccessibilityLabel(); got != "orders, PROD" {
		t.Errorf("a screen reader hears %q", got)
	}
	o := tabs.chipFor(its[1])
	if or := test.WidgetRenderer(o).(*chipRenderer); or.pill.Visible() || or.word.Visible() || o.AccessibilityLabel() != "people" {
		t.Error("a tab with no mark shows none")
	}
	tabs.MarkFor = nil
	tabs.Refresh()
	if r.pill.Visible() || r.word.Visible() || c.MinSize().Width != before {
		t.Error("a mark that goes leaves the tab as it was")
	}
}

// drag drags c by dx, as the driver would in two steps, and lets it go.
func drag(c *chip, dx float32) {
	c.Dragged(&fyne.DragEvent{Dragged: fyne.NewDelta(dx/2, 0)})
	c.Dragged(&fyne.DragEvent{Dragged: fyne.NewDelta(dx/2, 0)})
	c.DragEnd()
}

// past is how far c must move for its middle to pass other's.
func past(c, other *chip) float32 {
	return other.Position().X + other.Size().Width/2 - (c.Position().X + c.Size().Width/2) + sign(other, c)
}

func sign(other, c *chip) float32 {
	if other.Position().X > c.Position().X {
		return 1
	}
	return -1
}

func TestSelectionKeepsDocTabsRules(t *testing.T) {
	test.NewTempApp(t)
	its := named("a", "b", "c")
	a, b, c := its[0], its[1], its[2]
	var got []string
	tabs := New()
	tabs.OnSelected = func(it *container.TabItem) { got = append(got, it.Text) }
	tabs.Append(a)
	tabs.Append(b)
	tabs.Append(c)
	if tabs.Selected() != a || !slices.Equal(got, []string{"a"}) {
		t.Fatalf("selected %v, told %v: the first tab added is selected, and stays so", tabs.Selected(), got)
	}
	tabs.Select(c)
	tabs.Select(c)
	if tabs.Selected() != c || !slices.Equal(got, []string{"a", "c"}) {
		t.Errorf("told %v: selecting the selected tab again says nothing", got)
	}
	tabs.Remove(a)
	if tabs.Selected() != c || tabs.SelectedIndex() != 1 || len(got) != 2 {
		t.Errorf("removing a tab before the selection keeps it: %v at %d", tabs.Selected(), tabs.SelectedIndex())
	}
	tabs.Remove(c)
	if tabs.Selected() != b || !slices.Equal(got, []string{"a", "c", "b"}) {
		t.Errorf("removing the last, selected tab selects the one before: %v, told %v", tabs.Selected(), got)
	}
	tabs.Append(a)
	tabs.Remove(b)
	if tabs.Selected() != a || !slices.Equal(got, []string{"a", "c", "b", "a"}) {
		t.Errorf("removing the selected tab selects the one taking its place: %v, told %v", tabs.Selected(), got)
	}
	tabs.SelectIndex(3)
	four := named("w", "x", "y", "z")
	tabs.SetItems(four)
	tabs.Select(four[2])
	tabs.Remove(four[0])
	if tabs.Selected() != four[2] || tabs.SelectedIndex() != 1 {
		t.Errorf("removing a tab before the selection, not last, keeps it: %v at %d", tabs.Selected().Text, tabs.SelectedIndex())
	}
	tabs.SetItems(nil)
	if tabs.Selected() != nil || tabs.SelectedIndex() != -1 {
		t.Errorf("no tabs, yet %v at %d is selected", tabs.Selected(), tabs.SelectedIndex())
	}
}

func TestTappingATabSelectsItAndShowsItsContent(t *testing.T) {
	tabs, its := shown(t, 600, "one", "two", "three")
	test.Tap(tabs.chips[its[1]])
	if tabs.Selected() != its[1] {
		t.Fatalf("selected %v", tabs.Selected().Text)
	}
	if objs := tabs.r.content.Objects; len(objs) != 1 || objs[0] != its[1].Content || objs[0].Size().Width == 0 {
		t.Errorf("content %v; only the selected tab's shows, laid out", objs)
	}
}

func TestClosingAsksFirstWhenIntercepted(t *testing.T) {
	tabs, its := shown(t, 600, "one", "two", "three")
	var asked *container.TabItem
	tabs.CloseIntercept = func(it *container.TabItem) { asked = it }
	test.Tap(tabs.chips[its[0]].close)
	if asked != its[0] || len(tabs.Items) != 3 {
		t.Errorf("asked about %v; %d tabs", asked, len(tabs.Items))
	}
	tabs.CloseIntercept = nil
	test.Tap(tabs.chips[its[0]].close)
	if !slices.Equal(texts(tabs.Items), []string{"two", "three"}) {
		t.Errorf("tabs %v", texts(tabs.Items))
	}
}

func TestTheCloseControlShowsOnTheSelectedOrHoveredTab(t *testing.T) {
	tabs, its := shown(t, 600, "one", "two")
	one, two := tabs.chips[its[0]], tabs.chips[its[1]]
	if !one.close.Visible() || two.close.Visible() {
		t.Error("the selected tab shows its close control; the others do not")
	}
	two.MouseIn(nil)
	if !two.close.Visible() {
		t.Error("a tab under the pointer shows its close control")
	}
	two.MouseOut()
	if two.close.Visible() {
		t.Error("and hides it when the pointer leaves")
	}
}

func TestDraggingATabMovesIt(t *testing.T) {
	tabs, its := shown(t, 800, "one", "two", "three")
	var moved *container.TabItem
	to := -1
	tabs.OnMove = func(it *container.TabItem, i int) { moved, to = it, i }
	one, two, three := tabs.chips[its[0]], tabs.chips[its[1]], tabs.chips[its[2]]

	drag(one, 5)
	if moved != nil {
		t.Errorf("a nudge moved %v to %d", moved.Text, to)
	}
	drag(one, past(one, three))
	if moved != its[0] || to != 2 {
		t.Errorf("dragged past the last tab: %v to %d", moved, to)
	}
	drag(three, past(three, one))
	if moved != its[2] || to != 0 {
		t.Errorf("dragged past the first tab: %v to %d", moved.Text, to)
	}
	drag(one, past(one, two))
	if moved != its[0] || to != 1 {
		t.Errorf("dragged past its neighbour: %v to %d", moved.Text, to)
	}

	tabs.OnMove = nil
	tabs.Select(its[1])
	drag(one, past(one, two))
	if !slices.Equal(texts(tabs.Items), []string{"two", "one", "three"}) || tabs.Selected() != its[1] {
		t.Errorf("tabs %v, selected %v: with no one to ask, the tab moves and the selection stays",
			texts(tabs.Items), tabs.Selected().Text)
	}
}

func TestATabIsFoundToDragWhereItIsDrawn(t *testing.T) {
	tabs, its := shown(t, 800, "one", "two", "three")
	to := -1
	tabs.OnMove = func(_ *container.TabItem, i int) { to = i }
	one := tabs.chips[its[0]]
	at := fyne.CurrentApp().Driver().AbsolutePositionForObject(one).Add(fyne.NewPos(8, 8))
	c := fyne.CurrentApp().Driver().CanvasForObject(tabs)
	test.Drag(c, at, past(one, tabs.chips[its[2]]), 0)
	if to != 2 {
		t.Errorf("a drag starting on the first tab moved it to %d; its label or close control must not take the drag", to)
	}
}

func TestTheDropIsMarkedWhileDragging(t *testing.T) {
	tabs, its := shown(t, 800, "one", "two", "three")
	one, three := tabs.chips[its[0]], tabs.chips[its[2]]
	one.Dragged(&fyne.DragEvent{Dragged: fyne.NewDelta(past(one, three), 0)})
	m := tabs.r.marker
	end := three.Position().X + three.Size().Width
	if !m.Visible() || m.Position().X < end-markerWidth || m.Position().X > end+markerWidth*4 {
		t.Errorf("marker shown %v at %v; the last tab ends at %v", m.Visible(), m.Position().X, end)
	}
	one.DragEnd()
	if m.Visible() {
		t.Error("the mark goes when the tab is let go")
	}
}

func TestASecondaryTapAsksForTheTabsMenu(t *testing.T) {
	tabs, its := shown(t, 600, "one", "two")
	var asked *container.TabItem
	tabs.OnMenu = func(it *container.TabItem, _ fyne.Position) { asked = it }
	test.TapSecondary(tabs.chips[its[1]])
	if asked != its[1] {
		t.Errorf("asked for %v", asked)
	}
}

func TestEveryControlHasAName(t *testing.T) {
	tabs, its := shown(t, 300, "items", "orders", "customers", "invoices", "payments", "suppliers")
	c := tabs.chips[its[0]]
	if c.AccessibilityLabel() != "items" || c.AccessibilityRole() != fyne.AccessibleRoleButton {
		t.Errorf("tab named %q, a %q", c.AccessibilityLabel(), c.AccessibilityRole())
	}
	if c.close.AccessibilityLabel() != "Close items" || c.close.AccessibilityRole() != fyne.AccessibleRoleButton {
		t.Errorf("close control named %q, a %q", c.close.AccessibilityLabel(), c.close.AccessibilityRole())
	}
	if !tabs.r.all.Visible() || tabs.r.all.Text == "" {
		t.Errorf("the All Tabs button, shown %v, says %q", tabs.r.all.Visible(), tabs.r.all.Text)
	}
}

func TestAllTabsListsTabsThatDoNotFit(t *testing.T) {
	names := make([]string, 12)
	for i := range names {
		names[i] = fmt.Sprintf("Table %d", i+1)
	}
	tabs, its := shown(t, 300, names...)
	if !tabs.r.all.Visible() {
		t.Fatal("twelve tabs do not fit in 300 points: All Tabs should show")
	}
	m := tabs.allMenu()
	if len(m.Items) != 12 || !m.Items[0].Checked || m.Items[1].Checked {
		t.Errorf("%d items, first ticked %v", len(m.Items), m.Items[0].Checked)
	}
	m.Items[8].Action()
	if tabs.Selected() != its[8] {
		t.Errorf("choosing Table 9 selected %v", tabs.Selected().Text)
	}
	test.Tap(tabs.r.all)
	if fyne.CurrentApp().Driver().CanvasForObject(tabs).Overlays().Top() == nil {
		t.Error("All Tabs shows its menu")
	}

	few, _ := shown(t, 300, "one", "two")
	if few.r.all.Visible() {
		t.Error("two tabs fit: no All Tabs")
	}
}

func TestTheSelectedTabIsScrolledIntoView(t *testing.T) {
	names := make([]string, 12)
	for i := range names {
		names[i] = fmt.Sprintf("Table %d", i+1)
	}
	tabs, its := shown(t, 300, names...)
	tabs.SelectIndex(11)
	c, s := tabs.chips[its[11]], tabs.r.scroll
	if off := s.Offset.X; off <= 0 || c.Position().X < off || c.Position().X+c.Size().Width > off+s.Size().Width+0.5 {
		t.Errorf("the last tab spans %v–%v; the view %v–%v", c.Position().X, c.Position().X+c.Size().Width, off, off+s.Size().Width)
	}
	tabs.SelectIndex(0)
	if s.Offset.X != 0 {
		t.Errorf("back to the first tab, the view starts at %v", s.Offset.X)
	}
}

func TestTheSelectedTabIsMarkedByMoreThanColour(t *testing.T) {
	tabs, its := shown(t, 600, "one", "two")
	one := test.WidgetRenderer(tabs.chips[its[0]]).(*chipRenderer)
	two := test.WidgetRenderer(tabs.chips[its[1]]).(*chipRenderer)
	if !one.label.TextStyle.Bold || !one.bar.Visible() || two.label.TextStyle.Bold || two.bar.Visible() {
		t.Error("the selected tab's title is bold and underlined; the others' are not")
	}
	w := tabs.chips[its[1]].Size().Width
	tabs.Select(its[1])
	if tabs.chips[its[1]].Size().Width != w {
		t.Error("a tab keeps its width when selected, though its title turns bold")
	}
}

func TestARenamedTabShowsItsNewName(t *testing.T) {
	tabs, its := shown(t, 600, "Query 1")
	its[0].Text = "monthly totals"
	tabs.Refresh()
	if l := test.WidgetRenderer(tabs.chips[its[0]]).(*chipRenderer).label; l.Text != "monthly totals" {
		t.Errorf("the tab reads %q", l.Text)
	}
}

func TestATapIsReportedEvenOnTheSelectedTab(t *testing.T) {
	tabs, its := shown(t, 600, "one", "two")
	var tapped []string
	tabs.OnTapped = func(it *container.TabItem) { tapped = append(tapped, it.Text) }
	test.Tap(tabs.chips[its[0]])
	test.Tap(tabs.chips[its[1]])
	if !slices.Equal(tapped, []string{"one", "two"}) || tabs.Selected() != its[1] {
		t.Errorf("told of %v, %v selected", tapped, tabs.Selected().Text)
	}
}
