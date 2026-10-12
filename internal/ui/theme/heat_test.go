package theme

import (
	"fmt"
	"math"
	"testing"
)

// The hottest cell of a heat map still has to be read (NFR-A2): on every
// accent, light and dark, a number on it is at AA contrast.
func TestAHeatMapKeepsItsTextReadable(t *testing.T) {
	worst := math.Inf(1)
	for _, a := range Accents() {
		for _, dark := range []bool{false, true} {
			p := accentPalette(a, dark)
			name := fmt.Sprintf("%s, dark %v", a, dark)
			if p.Heat(0) != p.ContentBackground {
				t.Errorf("%s: the smallest value is not left as the grid is", name)
			}
			if p.Heat(1) != mix(p.ContentBackground, p.ControlAccent, heatStrength) || p.Heat(1) == p.ContentBackground {
				t.Errorf("%s: the largest value is not tinted with the accent", name)
			}
			// Out of the range is held to its ends, and what is not a
			// number is not anywhere along it.
			if p.Heat(-3) != p.Heat(0) || p.Heat(7) != p.Heat(1) || p.Heat(math.NaN()) != p.Heat(0) || p.Heat(math.Inf(1)) != p.Heat(1) {
				t.Errorf("%s: a value outside its column's range is not held to the range", name)
			}
			// Further along is further from the grid's own colour, all the way.
			last := 0.0
			for i := 1; i <= 10; i++ {
				away := distance(p.Heat(float64(i)/10), p.ContentBackground)
				if away <= last {
					t.Errorf("%s: %d tenths is no further from the grid's colour than %d", name, i, i-1)
				}
				last = away
			}
			// Only a number is ever on a tint: a cell that holds nothing is
			// nowhere along the range, and is left as the grid is.
			on := contrast(p.Label, p.Heat(1))
			if on < 4.5 {
				t.Errorf("%s: a number on the hottest cell is at %.2f:1, under AA", name, on)
			}
			worst = math.Min(worst, on)
		}
	}
	t.Logf("the least contrast on a hottest cell, over every accent, is %.2f:1", worst)
}

func distance(a, b interface{ RGBA() (r, g, b, a uint32) }) float64 {
	ar, ag, ab, _ := a.RGBA()
	br, bg, bb, _ := b.RGBA()
	d := func(x, y uint32) float64 { return (float64(x) - float64(y)) * (float64(x) - float64(y)) }
	return math.Sqrt(d(ar, br) + d(ag, bg) + d(ab, bb))
}
