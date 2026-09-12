package view

import (
	"sort"
	"strconv"
	"strings"
)

// A snippet is a statement written from two or three letters, with places in
// it for a person to fill in (FR-5.2, ADR-0061).
//
// The engine writes those places into the text it hands over, as ${1:name}
// in the order they are stepped through and $0 where the caret ends; this
// reads them out again, and the editor steps through what is left behind.

// stop is one place to fill in, as byte offsets into the document.
type stop struct{ start, end int }

// snippetRun is a snippet being filled in: the places left, and which one the
// caret is in. Tab moves to the next; it ends at the last, and at anything
// that moves the caret away.
type snippetRun struct {
	stops []stop
	at    int
}

// parseSnippet reads a body's places to fill in, returning the text to write
// and where they are in it.
//
// ${1:name} writes name and marks it; $1 and $0 mark a point without text;
// the same number twice marks the first and writes its text at both, which is
// how a CTE's name reaches its FROM clause. A '$' that begins none of these
// is text like any other, so a parameter written $1 in a snippet body would
// need care — none is, and none has to be.
func parseSnippet(body string) (string, []snippetStop) {
	var b strings.Builder
	var marks []snippetStop
	for i := 0; i < len(body); {
		if body[i] != '$' {
			b.WriteByte(body[i])
			i++
			continue
		}
		n, text, next, ok := placeholder(body, i)
		if !ok {
			b.WriteByte(body[i])
			i++
			continue
		}
		marks = append(marks, snippetStop{n: n, start: b.Len(), end: b.Len() + len(text)})
		b.WriteString(text)
		i = next
	}
	return b.String(), marks
}

// snippetStop is a place to fill in while the body is being read: its number
// decides the order, and its offsets are into the text being built.
type snippetStop struct {
	n          int
	start, end int
}

// placeholder reads one ${n:text}, $n or $0 at i.
func placeholder(body string, i int) (n int, text string, next int, ok bool) {
	j := i + 1
	braced := j < len(body) && body[j] == '{'
	if braced {
		j++
	}
	digits := j
	for j < len(body) && body[j] >= '0' && body[j] <= '9' {
		j++
	}
	if j == digits {
		return 0, "", 0, false
	}
	n, _ = strconv.Atoi(body[digits:j])
	if !braced {
		return n, "", j, true
	}
	if j < len(body) && body[j] == ':' {
		start := j + 1
		for j = start; j < len(body) && body[j] != '}'; j++ {
		}
		if j >= len(body) {
			return 0, "", 0, false
		}
		return n, body[start:j], j + 1, true
	}
	if j < len(body) && body[j] == '}' {
		return n, "", j + 1, true
	}
	return 0, "", 0, false
}

// order puts the stops in the order they are stepped through: by number, with
// $0 last because it is where the caret is left, and a number used twice kept
// only once — the first is where it is filled in.
func order(marks []snippetStop, base int) []stop {
	sort.SliceStable(marks, func(i, j int) bool {
		a, b := marks[i].n, marks[j].n
		if a == 0 || b == 0 {
			return b == 0 && a != 0
		}
		return a < b
	})
	var out []stop
	seen := map[int]bool{}
	for _, m := range marks {
		if seen[m.n] {
			continue
		}
		seen[m.n] = true
		out = append(out, stop{start: base + m.start, end: base + m.end})
	}
	return out
}

// begin starts filling in a snippet just written at base. One with no places
// to fill in leaves nothing behind: the first step off the end ends it.
func (e *Editor) begin(marks []snippetStop, base int) {
	e.snippet = &snippetRun{stops: order(marks, base), at: -1}
	e.nextStop()
}

// nextStop moves to the place after the one being filled in, selecting what
// is there so that typing replaces it. The run ends after the last.
func (e *Editor) nextStop() {
	r := e.snippet
	if r == nil {
		return
	}
	r.at++
	if r.at >= len(r.stops) {
		e.snippet = nil
		return
	}
	s := r.stops[r.at]
	d := e.doc
	d.SetCaret(d.PosAt(s.start), false)
	if s.end > s.start {
		d.SetCaret(d.PosAt(s.end), true)
	}
	if r.at == len(r.stops)-1 {
		// The last place is where the snippet leaves the caret. There is
		// nowhere further to go, so Tab goes back to indenting.
		e.snippet = nil
	}
	e.surface.changed()
}

// endSnippet stops filling one in: the caret has gone somewhere else, or the
// editor has lost the focus.
func (e *Editor) endSnippet() { e.snippet = nil }

// FillingSnippet reports whether a snippet is being filled in, so that Tab
// moves to the next place rather than indenting.
func (e *Editor) FillingSnippet() bool { return e.snippet != nil }

// shift moves the places still to be filled in by what an edit added or
// removed before them. Typing into one place must not leave the next in the
// middle of a word.
func (e *Editor) shift(at, delta int) {
	r := e.snippet
	if r == nil || delta == 0 {
		return
	}
	for i := range r.stops {
		if i <= r.at {
			continue
		}
		if r.stops[i].start >= at {
			r.stops[i].start += delta
			r.stops[i].end += delta
		}
	}
}
