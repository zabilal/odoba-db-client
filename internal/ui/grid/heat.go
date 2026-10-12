package grid

import (
	"math"
	"strconv"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// A heat map (FR-3.17): a column of numbers shaded by how large each is,
// between the smallest and the largest its column holds. The grid is told
// those two, since it holds only the pages somebody has scrolled through and
// the smallest of those is not the smallest there is.

// heatRange is the smallest and largest a column's values are placed between.
type heatRange struct{ lo, hi float64 }

// along is how far a value is from the smallest to the largest: nothing, for
// a cell that holds no number, and for a column whose values are all one
// value, where none is larger than another.
func (h heatRange) along(v any) (float64, bool) {
	f, ok := heatNumber(v)
	if !ok || !(h.hi > h.lo) {
		return 0, false
	}
	return (f - h.lo) / (h.hi - h.lo), true
}

// heatNumber is a cell's value as a number, where it is one. A decimal is
// read as a float here and nowhere it is kept: it places a colour.
func heatNumber(v any) (float64, bool) {
	var f float64
	switch n := v.(type) {
	case int64:
		f = float64(n)
	case float64:
		f = n
	case model.Decimal:
		p, err := strconv.ParseFloat(string(n), 64)
		if err != nil {
			return 0, false
		}
		f = p
	default:
		return 0, false
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

// SetHeat shades a column, by its place in the model, between the smallest
// and largest values it holds. Values outside them are shaded as the nearer.
func (g *TableGrid) SetHeat(col int, lo, hi float64) {
	if g.heat == nil {
		g.heat = map[int]heatRange{}
	}
	g.heat[col] = heatRange{lo: lo, hi: hi}
	g.refresh()
}

// ClearHeat leaves a column as the grid draws any other.
func (g *TableGrid) ClearHeat(col int) {
	if _, ok := g.heat[col]; ok {
		delete(g.heat, col)
		g.refresh()
	}
}

// Heated reports whether a column is shaded.
func (g *TableGrid) Heated(col int) bool {
	_, ok := g.heat[col]
	return ok
}

// HeatRange is the smallest and largest a shaded column's values are placed
// between.
func (g *TableGrid) HeatRange(col int) (lo, hi float64, ok bool) {
	h, ok := g.heat[col]
	return h.lo, h.hi, ok
}

// HeatedColumns are the columns shaded, in the model's order.
func (g *TableGrid) HeatedColumns() []int {
	var out []int
	for col := range g.Model().Columns() {
		if g.Heated(col) {
			out = append(out, col)
		}
	}
	return out
}
