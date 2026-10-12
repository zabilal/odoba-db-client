package shell

import (
	"context"
	"strconv"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/l10n"
	"github.com/ikigai-db/ikigai-db/internal/model"
)

// A heat map on a column of numbers (FR-3.17).
//
// The column in front is shaded by how large each value is, between the
// smallest and the largest it holds. Those two are asked of the server, over
// the rows the grid's own filters select, the way a column is measured
// (FR-3.14): the grid holds only the pages somebody has scrolled through, and
// a picture drawn from those would change as they scrolled. Narrowing the
// grid measures again, so the shading is always of what is being looked at.
//
// The number stays printed in every cell. The colour is a way to find the
// large ones, not the only way to know them.

// heatable reports whether a column's values can be placed along a range.
func heatable(def model.ColumnDef) bool {
	switch def.Type.Class {
	case model.TypeInteger, model.TypeFloat, model.TypeDecimal:
		return true
	}
	return false
}

// canHeat reports whether the column in front can be shaded: a column of
// numbers, on a table whose source measures columns.
func (s *Shell) canHeat() bool {
	t := s.activeTab()
	if t == nil || t.browse == nil || t.grid == nil || t.model == nil || !t.browse.CanMeasure() {
		return false
	}
	col, cols := t.grid.SelectedColumn(), t.model.Columns()
	return col >= 0 && col < len(cols) && heatable(cols[col])
}

// toggleHeat shades the column in front, or leaves it unshaded if it is.
func (s *Shell) toggleHeat() {
	if !s.canHeat() {
		return
	}
	t := s.activeTab()
	col := t.grid.SelectedColumn()
	name := t.model.Columns()[col].Name
	if t.grid.Heated(col) {
		t.grid.ClearHeat(col)
		delete(t.heated, name)
		t.said = l10n.F("%s is no longer shaded", name)
		s.showCount(t)
		return
	}
	if t.heated == nil {
		t.heated = map[string]bool{}
	}
	t.heated[name] = true
	s.heat(t, name)
}

// reheat measures every shaded column again, after the rows the grid shows
// have changed: the smallest of them may not be what it was.
func (s *Shell) reheat(t *tab) {
	for name := range t.heated {
		s.heat(t, name)
	}
}

// heat measures a column and shades it between its smallest and largest.
func (s *Shell) heat(t *tab, name string) {
	col := columnNamed(t.model.Columns(), name)
	if col < 0 {
		return
	}
	b, def, seq := t.browse, t.model.Columns()[col], t.browseSeq
	t.said = l10n.F("Measuring %s to shade it…", name)
	s.showCount(t)
	go func() {
		ctx, cancel := context.WithTimeout(t.ctx, statsTimeout)
		defer cancel()
		st, err := b.Measure(ctx, def)
		s.d.Run(func() {
			// Narrowed again since, or unshaded while it was measured: this
			// answer is to a question nobody is asking now.
			if t.ctx.Err() != nil || seq != t.browseSeq || !t.heated[name] {
				return
			}
			at := columnNamed(t.model.Columns(), name)
			if at < 0 {
				return
			}
			if err != nil {
				delete(t.heated, name)
				t.grid.ClearHeat(at)
				t.said = l10n.F("Could not measure %s: %s", name, err.Error())
				s.showCount(t)
				return
			}
			lo, okLo := app.Number(st.Min)
			hi, okHi := app.Number(st.Max)
			if !okLo || !okHi || !(hi > lo) {
				// Nothing, or one value: no cell is larger than another, and a
				// column shaded one colour would say something it does not mean.
				t.grid.ClearHeat(at)
				t.said = l10n.F("%s holds no two different numbers here, so there is nothing to shade", name)
				s.showCount(t)
				return
			}
			t.grid.SetHeat(at, lo, hi)
			t.said = l10n.F("%s is shaded from %s to %s", name, heatFigure(lo), heatFigure(hi))
			s.showCount(t)
		})
	}()
}

// columnNamed is where a column of that name is, or -1.
func columnNamed(cols []model.ColumnDef, name string) int {
	for i, c := range cols {
		if c.Name == name {
			return i
		}
	}
	return -1
}

// heatFigure is a number as a person reads one: with no exponent while it has
// none to need, and no more digits than tell it apart.
func heatFigure(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }
