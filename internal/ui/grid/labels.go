package grid

import "unicode/utf8"

// maxLabelRunes caps the part of a label drawn after its value; the whole
// label is the cell's hint.
const maxLabelRunes = 60

// withLabel is a cell's value with the label of the row it refers to after
// it (FR-3.12, ADR-0042). The middle dot, not a colour, says which part is
// the label (UX principle 14); the whole label is the hint, unless the cell
// has one of its own.
func withLabel(c Cell, label string) Cell {
	cut := label
	if utf8.RuneCountInString(cut) > maxLabelRunes {
		cut = string([]rune(cut)[:maxLabelRunes]) + "…"
	}
	c.Text, c.Truncated = shown(c)+" · "+cut, false
	if c.Hint == "" {
		c.Hint = label
	}
	return c
}
