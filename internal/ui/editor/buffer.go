package editor

import "strings"

// Buffer is a line-based text buffer.
//
// Deliberately simple: a slice of lines, with edits rebuilding the affected
// line. A rope or piece table would win on pathological input — a single
// multi-megabyte line — but SQL is line-oriented and short-lined, and the
// benchmarks show line rebuilds are far below the keystroke budget. Choosing
// the simpler structure keeps the editor's hardest parts (highlighting,
// caret, selection) legible.
//
// A Buffer is not safe for concurrent use.
type Buffer struct {
	lines []string
}

// NewBuffer builds a buffer from text.
func NewBuffer(text string) *Buffer {
	return &Buffer{lines: splitLines(text)}
}

func splitLines(text string) []string {
	if text == "" {
		return []string{""}
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return strings.Split(text, "\n")
}

// LineCount returns the number of lines. Always at least one: an empty buffer
// is one empty line, so the caret always has somewhere to be.
func (b *Buffer) LineCount() int { return len(b.lines) }

// Line returns line i, or "" when out of range.
func (b *Buffer) Line(i int) string {
	if i < 0 || i >= len(b.lines) {
		return ""
	}
	return b.lines[i]
}

// Text returns the whole buffer.
func (b *Buffer) Text() string { return strings.Join(b.lines, "\n") }

// Edit describes what a mutation touched, so the highlighter knows where to
// resume rather than re-lexing everything.
type Edit struct {
	// Line is the first line affected.
	Line int
	// LinesRemoved and LinesInserted describe the change in line count, which
	// is what forces the state cache to shift rather than merely re-lex.
	LinesRemoved  int
	LinesInserted int
}

// Insert inserts text at (line, col), where col is a byte offset. The text may
// contain newlines. It returns the resulting caret position and the edit.
func (b *Buffer) Insert(line, col int, text string) (int, int, Edit) {
	line = clampInt(line, 0, len(b.lines)-1)
	cur := b.lines[line]
	col = clampInt(col, 0, len(cur))

	if !strings.Contains(text, "\n") {
		b.lines[line] = cur[:col] + text + cur[col:]
		return line, col + len(text), Edit{Line: line, LinesRemoved: 1, LinesInserted: 1}
	}

	parts := splitLines(text)
	head, tail := cur[:col], cur[col:]

	parts[0] = head + parts[0]
	endCol := len(parts[len(parts)-1])
	parts[len(parts)-1] += tail

	rest := append([]string{}, b.lines[line+1:]...)
	b.lines = append(b.lines[:line], append(parts, rest...)...)

	return line + len(parts) - 1, endCol, Edit{
		Line: line, LinesRemoved: 1, LinesInserted: len(parts),
	}
}

// DeleteRange removes text between two positions, returning the edit.
func (b *Buffer) DeleteRange(startLine, startCol, endLine, endCol int) Edit {
	startLine = clampInt(startLine, 0, len(b.lines)-1)
	endLine = clampInt(endLine, 0, len(b.lines)-1)
	if startLine > endLine || (startLine == endLine && startCol >= endCol) {
		return Edit{Line: startLine, LinesRemoved: 1, LinesInserted: 1}
	}

	startCol = clampInt(startCol, 0, len(b.lines[startLine]))
	endCol = clampInt(endCol, 0, len(b.lines[endLine]))

	if startLine == endLine {
		l := b.lines[startLine]
		b.lines[startLine] = l[:startCol] + l[endCol:]
		return Edit{Line: startLine, LinesRemoved: 1, LinesInserted: 1}
	}

	merged := b.lines[startLine][:startCol] + b.lines[endLine][endCol:]
	removed := endLine - startLine + 1
	b.lines = append(b.lines[:startLine], append([]string{merged}, b.lines[endLine+1:]...)...)
	return Edit{Line: startLine, LinesRemoved: removed, LinesInserted: 1}
}

// SetText replaces the entire buffer.
func (b *Buffer) SetText(text string) Edit {
	removed := len(b.lines)
	b.lines = splitLines(text)
	return Edit{Line: 0, LinesRemoved: removed, LinesInserted: len(b.lines)}
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
