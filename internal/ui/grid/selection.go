package grid

import (
	"math"
	"slices"
)

// CellID names a cell by row and column.
type CellID struct{ Row, Col int }

// Rect is a block of cells, its bounds included. A Bottom of End runs to the
// last row, wherever that turns out to be: a grid that has not counted its
// rows can still select a whole column.
type Rect struct{ Top, Left, Bottom, Right int }

// End is a Rect's Bottom when it runs to the last row.
const End = math.MaxInt32

// Contains reports whether a cell is in the block.
func (r Rect) Contains(row, col int) bool {
	return row >= r.Top && row <= r.Bottom && col >= r.Left && col <= r.Right
}

func span(a, b CellID) Rect {
	return Rect{min(a.Row, b.Row), min(a.Col, b.Col), max(a.Row, b.Row), max(a.Col, b.Col)}
}

// Selection is the grid's selected cells (FR-3.7): blocks of them, and the
// cell that ⇧-click and ⇧-arrow stretch the last block from.
type Selection struct {
	rects  []Rect
	anchor CellID
	active CellID
}

// Click selects one cell, and only it.
func (s *Selection) Click(c CellID) {
	s.rects = []Rect{span(c, c)}
	s.anchor, s.active = c, c
}

// Extend stretches the last block from its anchor to c. With nothing
// selected, it is a click.
func (s *Selection) Extend(c CellID) {
	if len(s.rects) == 0 {
		s.Click(c)
		return
	}
	s.rects[len(s.rects)-1] = span(s.anchor, c)
	s.active = c
}

// Toggle adds a cell as a block of its own, as ⌘-click does; a cell that is
// already a block of its own comes back out.
func (s *Selection) Toggle(c CellID) {
	one := span(c, c)
	if i := slices.Index(s.rects, one); i >= 0 {
		s.rects = slices.Delete(s.rects, i, i+1)
		return
	}
	s.rects = append(s.rects, one)
	s.anchor, s.active = c, c
}

// All selects every cell of a grid with cols columns.
func (s *Selection) All(cols int) {
	if cols > 0 {
		s.rects = []Rect{{0, 0, End, cols - 1}}
		s.anchor, s.active = CellID{}, CellID{}
	}
}

// Row selects every cell in a row of a grid with cols columns.
func (s *Selection) Row(row, cols int) {
	if cols > 0 {
		s.rects = []Rect{{row, 0, row, cols - 1}}
		s.anchor, s.active = CellID{row, 0}, CellID{row, s.active.Col}
	}
}

// Column selects every cell in a column, to the last row.
func (s *Selection) Column(col int) {
	s.rects = []Rect{{0, col, End, col}}
	s.anchor, s.active = CellID{0, col}, CellID{s.active.Row, col}
}

// Clear selects nothing.
func (s *Selection) Clear() { s.rects = nil }

// Empty reports whether nothing is selected.
func (s Selection) Empty() bool { return len(s.rects) == 0 }

// Active is the cell last clicked or moved to, while anything is selected.
func (s Selection) Active() (CellID, bool) { return s.active, len(s.rects) > 0 }

// Contains reports whether a cell is selected.
func (s Selection) Contains(row, col int) bool {
	for _, r := range s.rects {
		if r.Contains(row, col) {
			return true
		}
	}
	return false
}

// HasRow reports whether any cell of a row is selected.
func (s Selection) HasRow(row int) bool {
	for _, r := range s.rects {
		if row >= r.Top && row <= r.Bottom {
			return true
		}
	}
	return false
}

// Rows is the first and last row any block reaches; last may be End.
func (s Selection) Rows() (first, last int) {
	if len(s.rects) == 0 {
		return 0, -1
	}
	first, last = math.MaxInt, -1
	for _, r := range s.rects {
		first, last = min(first, r.Top), max(last, r.Bottom)
	}
	return first, last
}

// Columns lists, in order, every column in which any cell is selected.
func (s Selection) Columns() []int {
	var out []int
	for _, r := range s.rects {
		for c := r.Left; c <= r.Right; c++ {
			if !slices.Contains(out, c) {
				out = append(out, c)
			}
		}
	}
	slices.Sort(out)
	return out
}
