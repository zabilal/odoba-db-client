package grid

import (
	"math"
	"reflect"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

func TestAColumnIsShadedByHowLargeEachValueIs(t *testing.T) {
	g := selectingGrid(t)
	waitFor(t, func() bool { _, ok := g.model.Row(g.ctx, 99); return ok }, "the rows")
	pal := g.palette
	plain := func(row int) any { return g.stripe(row) }
	if g.Heated(0) || g.HeatedColumns() != nil || drawn(g, 50, 0).bg != plain(50) {
		t.Fatal("a grid nobody has shaded is not shaded")
	}
	// The ids run from 1 to 100, and are told to.
	g.SetHeat(0, 1, 100)
	for row, frac := range map[int]float64{0: 0, 33: 33.0 / 99, 50: 50.0 / 99, 99: 1} {
		if got := drawn(g, row, 0).bg; got != pal.Heat(frac) {
			t.Errorf("row %d of the shaded column is %v, not %v", row, got, pal.Heat(frac))
		}
	}
	if drawn(g, 99, 0).bg == drawn(g, 0, 0).bg {
		t.Error("the largest is drawn as the smallest")
	}
	// Its neighbours are as they were.
	if drawn(g, 51, 1).bg != plain(51) || drawn(g, 51, 2).bg != plain(51) {
		t.Error("a column nobody shaded is shaded")
	}
	// An exact number is placed as the number it is: prices run from 0.00
	// to 1.00 over these rows.
	g.SetHeat(6, 0.01, 1.00)
	if got := drawn(g, 50, 6).bg; got != pal.Heat(0.50/0.99) {
		t.Errorf("a decimal of 0.51 between 0.01 and 1.00 is %v, not %v", got, pal.Heat(0.50/0.99))
	}
	if lo, hi, ok := g.HeatRange(6); !ok || lo != 0.01 || hi != 1 {
		t.Errorf("the range it was told: %v to %v, %v", lo, hi, ok)
	}
	if _, _, ok := g.HeatRange(1); ok {
		t.Error("a column nobody shaded has a range")
	}
	if !reflect.DeepEqual(g.HeatedColumns(), []int{0, 6}) || !g.Heated(6) || g.Heated(1) {
		t.Errorf("the columns shaded: %v", g.HeatedColumns())
	}
	// A value outside the range it was told is drawn as the nearer end:
	// the rows may have changed since they were measured.
	g.SetHeat(0, 11, 21)
	if drawn(g, 5, 0).bg != pal.Heat(0) || drawn(g, 99, 0).bg != pal.Heat(1) || drawn(g, 15, 0).bg != pal.Heat(0.5) {
		t.Error("a value past the range is not held to its end")
	}
	// Text has no size, and a column of one value has none larger than
	// another: neither is shaded, whatever was asked.
	g.SetHeat(2, 0, 10)
	g.SetHeat(5, 3, 3)
	g.SetHeat(8, 7, 3)
	for _, col := range []int{2, 5, 8} {
		if drawn(g, 51, col).bg != plain(51) {
			t.Errorf("column %d, which has nothing to place along, is shaded", col)
		}
	}
	// The selection is drawn over the shading, and a change over it too:
	// what is about to be written matters more than how big it is.
	g.SetHeat(0, 1, 100)
	g.Select(CellID{Row: 40, Col: 0}, CellID{Row: 40, Col: 0})
	if got := drawn(g, 40, 0).bg; got != pal.SelectedUnemphasized {
		t.Errorf("a selected cell of a shaded column is %v", got)
	}
	p := pendingGrid(t, g)
	if err := p.Set(rowOf(t, g, 3), 0, int64(98)); err != nil {
		t.Fatal(err)
	}
	if got := drawn(g, 3, 0); got.bg != pal.ModifiedBg || got.text != "98" {
		t.Errorf("a changed cell of a shaded column is %q on %v", got.text, got.bg)
	}
	// Unshaded, it is as it was; and unshading what is not shaded is nothing.
	g.ClearHeat(0)
	g.ClearHeat(0)
	g.ClearHeat(77)
	if g.Heated(0) || drawn(g, 50, 0).bg != plain(50) || !g.Heated(6) {
		t.Error("a column unshaded is still shaded, or took another with it")
	}
}

// A column moved is shaded where it now is: the shading is the column's, not
// its place's.
func TestAShadedColumnTakesItsShadingWithIt(t *testing.T) {
	g := selectingGrid(t)
	waitFor(t, func() bool { _, ok := g.model.Row(g.ctx, 99); return ok }, "the rows")
	g.SetHeat(0, 1, 100)
	g.MoveColumn(0, 1)
	if drawn(g, 50, 1).bg != g.palette.Heat(50.0/99) || drawn(g, 50, 0).bg != g.stripe(50) {
		t.Error("a shaded column that was moved left its shading behind")
	}
}

func TestWhatHasASizeToShade(t *testing.T) {
	for _, c := range []struct {
		v    any
		want float64
		ok   bool
	}{
		{int64(-3), -3, true}, {2.5, 2.5, true}, {model.Decimal("12.50"), 12.5, true}, {model.Decimal("1e3"), 1000, true},
		{nil, 0, false}, {"7", 0, false}, {true, 0, false}, {[]byte{1}, 0, false}, {model.Decimal("NaN"), 0, false},
		{model.Decimal("seven"), 0, false}, {math.NaN(), 0, false}, {math.Inf(1), 0, false}, {math.Inf(-1), 0, false},
		{model.Decimal("Infinity"), 0, false},
	} {
		if got, ok := heatNumber(c.v); ok != c.ok || got != c.want {
			t.Errorf("%#v is %v, %v; not %v, %v", c.v, got, ok, c.want, c.ok)
		}
	}
	for _, c := range []struct {
		lo, hi float64
		v      any
		want   float64
		ok     bool
	}{
		{0, 10, int64(5), 0.5, true}, {0, 10, int64(0), 0, true}, {0, 10, int64(10), 1, true}, {-10, 10, 0.0, 0.5, true},
		{0, 10, int64(20), 2, true}, {0, 10, nil, 0, false}, {5, 5, int64(5), 0, false}, {9, 1, int64(5), 0, false},
		{math.NaN(), 5, int64(1), 0, false}, {0, math.NaN(), int64(1), 0, false},
	} {
		if got, ok := (heatRange{c.lo, c.hi}).along(c.v); ok != c.ok || got != c.want {
			t.Errorf("%v between %v and %v is %v, %v; not %v, %v", c.v, c.lo, c.hi, got, ok, c.want, c.ok)
		}
	}
}
