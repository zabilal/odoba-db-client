package grid

import (
	"context"
	"strings"
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/ui/theme"
)

func TestAValueIsDrawnWithItsLabel(t *testing.T) {
	test.NewTempApp(t)
	ctx := context.Background()
	m := NewModel(NewSyntheticFetcher(10))
	for !m.Resident(0) {
		m.Row(ctx, 0)
	}
	g := NewTableGrid(ctx, m, theme.Light)
	asked := 0
	g.Labels = func(col int, v any) (string, bool) {
		asked++
		if col != 0 {
			return "", false
		}
		return "the one " + strings.Repeat("x", 70), true
	}
	cell := g.createCell().(*cellWidget)
	g.UpdateCell(widget.TableCellID{Row: 3, Col: 0}, cell)
	value, label, ok := strings.Cut(cell.text, " · ")
	if !ok || value == "" || !strings.HasPrefix(label, "the one x") || !strings.HasSuffix(label, "x…") || len([]rune(label)) != 61 {
		t.Errorf("the value, then its label cut short: %q", cell.text)
	}
	if !strings.HasPrefix(cell.hint, "the one ") || len(cell.hint) != len("the one ")+70 {
		t.Errorf("the whole label is the hint: %q", cell.hint)
	}
	g.UpdateCell(widget.TableCellID{Row: 3, Col: 1}, cell)
	if strings.Contains(cell.text, " · ") {
		t.Errorf("a column without labels draws its value alone: %q", cell.text)
	}
	before := asked
	g.UpdateCell(widget.TableCellID{Row: 9999, Col: 0}, cell) // not loaded
	if asked != before {
		t.Error("a row still loading asks for no label")
	}
}
