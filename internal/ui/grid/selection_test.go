package grid

import (
	"reflect"
	"testing"
)

func TestClickExtendAndToggle(t *testing.T) {
	var s Selection
	if !s.Empty() {
		t.Fatal("a new selection is empty")
	}
	s.Extend(CellID{2, 1}) // nothing to extend: a click
	if !s.Contains(2, 1) || s.Contains(2, 2) {
		t.Error("extending nothing should select the one cell")
	}
	s.Click(CellID{1, 1})
	s.Extend(CellID{3, 2})
	for _, c := range []CellID{{1, 1}, {2, 2}, {3, 1}, {3, 2}} {
		if !s.Contains(c.Row, c.Col) {
			t.Errorf("%v should be in the block from 1,1 to 3,2", c)
		}
	}
	if s.Contains(0, 1) || s.Contains(1, 0) || s.Contains(4, 2) {
		t.Error("the block reaches past its corners")
	}
	s.Extend(CellID{0, 0}) // stretched the other way, from the same anchor
	if !s.Contains(0, 0) || s.Contains(3, 2) {
		t.Error("extending again should re-stretch from the anchor, not grow")
	}
	s.Toggle(CellID{5, 3})
	if !s.Contains(5, 3) || !s.Contains(0, 0) {
		t.Error("⌘-click should add a cell and keep the rest")
	}
	s.Extend(CellID{6, 3}) // from the toggled cell, the new anchor
	if !s.Contains(6, 3) || !s.Contains(1, 1) {
		t.Error("extending after ⌘-click should stretch the new block only")
	}
	s.Toggle(CellID{7, 7})
	s.Toggle(CellID{7, 7})
	if s.Contains(7, 7) {
		t.Error("⌘-clicking a cell of its own again should take it out")
	}
	if got := s.Columns(); !reflect.DeepEqual(got, []int{0, 1, 3}) {
		t.Errorf("columns %v", got)
	}
	if first, last := s.Rows(); first != 0 || last != 6 {
		t.Errorf("rows %d–%d", first, last)
	}
	if !s.HasRow(4) == s.Contains(4, 0) || s.HasRow(4) {
		t.Error("row 4 has nothing selected")
	}
}

func TestWholeRowsColumnsAndEverything(t *testing.T) {
	var s Selection
	s.Click(CellID{4, 2})
	s.Row(4, 3)
	if !s.Contains(4, 0) || !s.Contains(4, 2) || s.Contains(3, 0) {
		t.Error("a row is every cell of that row")
	}
	if a, _ := s.Active(); a != (CellID{4, 2}) {
		t.Errorf("selecting the row moved the active cell to %v", a)
	}
	s.Column(1)
	if !s.Contains(0, 1) || !s.Contains(1_000_000, 1) || s.Contains(0, 0) {
		t.Error("a column runs to the last row, however far that is")
	}
	if _, last := s.Rows(); last != End {
		t.Errorf("a whole column should end at End, not %d", last)
	}
	s.All(3)
	if !s.Contains(9_999, 2) || s.Contains(0, 3) {
		t.Error("everything is every row of every column, and no more")
	}
	s.Clear()
	if _, ok := s.Active(); ok || !s.Empty() {
		t.Error("cleared, nothing is selected or active")
	}
}
