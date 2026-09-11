package grid

import (
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/ui/theme"
)

func TestTimesAreShownAsTheirTypeSays(t *testing.T) {
	when := time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC)
	plus2 := time.FixedZone("", 7200)
	col := func(class model.TypeClass, zone bool) model.ColumnDef {
		return model.ColumnDef{Type: model.DataType{Class: class, TimeZone: zone}}
	}
	if c := Format(when, col(model.TypeTimestamp, true), plus2); c.Text != "2024-05-06 09:08:09" || c.Hint != "2024-05-06 07:08:09 UTC" {
		t.Errorf("an instant is shown local, with UTC to hover: %q, %q", c.Text, c.Hint)
	}
	if c := Format(when, col(model.TypeTimestamp, false), plus2); c.Text != "2024-05-06 07:08:09" || c.Hint != "" {
		t.Errorf("a time with no zone is shown as stored: %q, %q", c.Text, c.Hint)
	}
	if c := Format(when, col(model.TypeDate, false), plus2); c.Text != "2024-05-06" {
		t.Errorf("a date is a date: %q", c.Text)
	}
}

func TestACutValueEndsInAnEllipsis(t *testing.T) {
	c := Format(strings.Repeat("x", MaxCellRunes+10), model.ColumnDef{}, time.UTC)
	if got := shown(c); !c.Truncated || !strings.HasSuffix(got, "…") || len([]rune(got)) != MaxCellRunes+1 {
		t.Errorf("cut %v, shown %d runes", c.Truncated, len([]rune(got)))
	}
	if got := shown(Format("short", model.ColumnDef{}, time.UTC)); got != "short" {
		t.Errorf("a whole value has no ellipsis: %q", got)
	}
}

func TestRowsAreStripedToTheGridsEdge(t *testing.T) {
	g := selectingGrid(t)
	if g.background(widget.TableCellID{Row: 0, Col: 0}) != g.palette.ContentBackground ||
		g.background(widget.TableCellID{Row: 1, Col: 0}) != g.palette.AlternateRow {
		t.Error("every other row should be tinted")
	}
	_, cols := g.length()
	if cols != len(g.Shown())+1 {
		t.Fatalf("%d columns for %d shown; a filler should follow the last", cols, len(g.Shown()))
	}
	filler := g.createCell().(*cellWidget)
	g.UpdateCell(widget.TableCellID{Row: 1, Col: cols - 1}, filler)
	if filler.bg != g.palette.AlternateRow || filler.text != "" {
		t.Errorf("the filler carries the stripe and nothing else: %v %q", filler.bg, filler.text)
	}
	used := float32(0)
	for _, c := range g.Shown() {
		used += g.ColumnWidth(c) + theme.SeparatorWidth
	}
	if g.viewWidth == 0 || g.fillerWidth != max(1, g.viewWidth-used) {
		t.Errorf("filler %v in a view %v wide with %v used", g.fillerWidth, g.viewWidth, used)
	}
	g.ResizeColumn(0, g.ColumnWidth(0)+50)
	if g.fillerWidth != max(1, g.viewWidth-used-50) {
		t.Errorf("a wider column should take its width from the filler: %v", g.fillerWidth)
	}
	click(g, 1, cols-1, 0)
	if !g.Selection().Empty() {
		t.Error("the filler is never selected")
	}
}

func TestRestingOnAnInstantShowsItInUTC(t *testing.T) {
	tipDelay = 0
	t.Cleanup(func() { tipDelay = 600 * time.Millisecond })
	g := selectingGrid(t)
	at := &desktop.MouseEvent{PointEvent: fyne.PointEvent{AbsolutePosition: fyne.NewPos(50, 50)}}
	cell := g.createCell().(*cellWidget)
	cell.hint = "2024-05-06 07:08:09 UTC"
	cell.MouseIn(at)
	if !g.tip.Visible() || g.tipText.Text != cell.hint {
		t.Fatalf("tip shown %v, saying %q", g.tip.Visible(), g.tipText.Text)
	}
	cell.MouseOut()
	if g.tip.Visible() {
		t.Error("the tip should go when the pointer leaves")
	}
	plain := g.createCell().(*cellWidget)
	plain.MouseIn(at)
	if g.tip.Visible() {
		t.Error("a cell with nothing to say shows no tip")
	}
}

// A resize must refit the filler itself. Under the test driver a refresh
// re-lays out the whole window, which would refit it anyway and hide a
// missing call; a real window does not. So this grid is in no window.
func TestAResizeRefitsTheFillerWithNoLayoutToHelp(t *testing.T) {
	g := sortableGrid(t)
	used := float32(0)
	for _, c := range g.Shown() {
		used += g.ColumnWidth(c) + theme.SeparatorWidth
	}
	g.viewWidth = used + 300 // room to spare, so the filler is not at its floor
	g.fitFiller()
	if g.fillerWidth != 300 {
		t.Fatalf("filler %v with 300 to spare", g.fillerWidth)
	}
	g.ResizeColumn(0, g.ColumnWidth(0)+50)
	if g.fillerWidth != 250 {
		t.Errorf("filler %v after a column grew by 50; want 250", g.fillerWidth)
	}
}
