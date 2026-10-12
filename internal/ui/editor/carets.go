package editor

import (
	"sort"
	"strings"
)

// More than one caret (FR-5.15).
//
// A document has a first caret, which is the one every other part of the
// editor knows — completion, bracket matching, the statement Run runs — and
// may have others. Typing, deleting and the arrow keys happen at all of them;
// placing the caret, which is what a click does, leaves one.
//
// What happens at several carets is the single-caret operation done at each,
// from the last in the document to the first, so that an edit never moves
// text a caret still to be visited is in; the carets already visited are
// carried along by each edit above them. That is the whole mechanism, and it
// is why every operation behaves at five carets exactly as it does at one.
//
// One keystroke at several carets is one step to undo.
//
// A block selection is carets on consecutive lines with the same columns
// selected: there is no second kind of selection.

// Range is one caret and the selection it ends: empty where the two are the
// same place.
type Range struct {
	Caret, Anchor Pos
	goal          int // the column vertical moves aim for; see Document.goal
}

// From and To are the range's ends in document order.
func (r Range) From() Pos {
	if r.Anchor.Less(r.Caret) {
		return r.Anchor
	}
	return r.Caret
}

func (r Range) To() Pos {
	if r.Anchor.Less(r.Caret) {
		return r.Caret
	}
	return r.Anchor
}

// Empty reports whether nothing is selected.
func (r Range) Empty() bool { return r.Caret == r.Anchor }

// Multiple reports whether there is more than one caret.
func (d *Document) Multiple() bool { return len(d.extra) > 0 }

// Carets are every caret, in document order. The first caret — Caret and
// Selection — is one of them, and not necessarily the first here.
func (d *Document) Carets() []Range {
	all, _ := d.all()
	return all
}

// all is every caret in document order, and which of them is the first.
func (d *Document) all() ([]Range, int) {
	all := make([]Range, 0, len(d.extra)+1)
	all = append(all, Range{Caret: d.caret, Anchor: d.anchor, goal: d.goal})
	all = append(all, d.extra...)
	first := all[0]
	sort.SliceStable(all, func(i, j int) bool { return all[i].From().Less(all[j].From()) })
	for i, r := range all {
		if r == first {
			return all, i
		}
	}
	return all, 0
}

// restore puts carets back as a snapshot has them: its first is the first.
func (d *Document) restore(snap []Range) {
	if len(snap) == 0 {
		return
	}
	d.caret, d.anchor = snap[0].Caret, snap[0].Anchor
	d.extra = append([]Range(nil), snap[1:]...)
}

// snapshot is every caret, the first first.
func (d *Document) snapshot() []Range {
	out := make([]Range, 0, len(d.extra)+1)
	out = append(out, Range{Caret: d.caret, Anchor: d.anchor})
	return append(out, d.extra...)
}

// Collapse leaves the first caret alone: Escape.
func (d *Document) Collapse() { d.extra = nil }

// AddCaret puts another caret at p: ⌥-click. On a caret that is already
// there it takes that one away instead, unless it is the only one.
func (d *Document) AddCaret(p Pos) {
	p = d.clamp(p)
	for i, r := range d.extra {
		if r.Empty() && r.Caret == p {
			d.extra = append(d.extra[:i:i], d.extra[i+1:]...)
			return
		}
	}
	if d.caret == p && d.anchor == p {
		if len(d.extra) > 0 { // the first goes, and another becomes it
			d.caret, d.anchor = d.extra[0].Caret, d.extra[0].Anchor
			d.extra = d.extra[1:]
		}
		return
	}
	// The one just placed is the first: it is where the eye is.
	d.extra = append(d.extra, Range{Caret: d.caret, Anchor: d.anchor, goal: -1})
	d.caret, d.anchor, d.goal = p, p, -1
	d.sealed = true
	d.tidy()
}

// AddCaretLine puts another caret on the line above the highest, when n is
// negative, or below the lowest, at the same visual column: ⌥⌘↑ and ⌥⌘↓. At
// the document's edge there is no such line: the caret asked for is where
// the edge's already is, and two carets at one place are one.
func (d *Document) AddCaretLine(n int) {
	all, _ := d.all()
	edge := all[0]
	line := edge.Caret.Line - 1
	if n > 0 {
		edge = all[len(all)-1]
		line = edge.Caret.Line + 1
	}
	col := edge.goal
	if col < 0 {
		col = d.Column(edge.Caret)
	}
	p := d.PosAtColumn(line, col)
	d.extra = append(d.extra, Range{Caret: d.caret, Anchor: d.anchor, goal: d.goal})
	d.caret, d.anchor, d.goal = p, p, col
	d.sealed = true
	d.tidy()
}

// SelectBlock selects a block: on every line from a's to b's, what lies
// between their two visual columns. ⌥-drag. A line that ends inside the block
// has what it has of it selected, and one too short to reach it has a caret at
// its end and nothing selected, so that typing still lands on every line.
func (d *Document) SelectBlock(a, b Pos) {
	a, b = d.clamp(a), d.clamp(b)
	ca, cb := d.Column(a), d.Column(b)
	step := 1
	if b.Line < a.Line {
		step = -1
	}
	var rs []Range
	for line := a.Line; ; line += step {
		rs = append(rs, Range{Anchor: d.PosAtColumn(line, ca), Caret: d.PosAtColumn(line, cb), goal: -1})
		if line == b.Line {
			break
		}
	}
	// The line the drag has reached holds the first caret.
	last := rs[len(rs)-1]
	d.caret, d.anchor, d.goal = last.Caret, last.Anchor, -1
	d.extra = rs[:len(rs)-1]
	d.sealed = true
	d.tidy()
}

// SelectedTexts is what every caret has selected, a line each, in document
// order: what Copy copies. With one caret it is SelectedText.
func (d *Document) SelectedTexts() string {
	if len(d.extra) == 0 {
		return d.SelectedText()
	}
	var parts []string
	for _, r := range d.Carets() {
		if !r.Empty() {
			parts = append(parts, d.textRange(r.From(), r.To()))
		}
	}
	return strings.Join(parts, "\n")
}

// Paste inserts text at every caret. Text of as many lines as there are
// carets is dealt out, a line to each, in document order — which is what
// puts back what was copied from them — and any other text goes in whole at
// each.
func (d *Document) Paste(text string) {
	text = normalise(text)
	lines := strings.Split(text, "\n")
	if len(lines) != len(d.extra)+1 {
		d.Insert(text)
		return
	}
	d.eachEdit(func(i int) { d.insert(lines[i]) })
}

// eachMove moves every caret as fn moves one.
func (d *Document) eachMove(fn func()) {
	if len(d.extra) == 0 {
		fn()
		return
	}
	all, first := d.all()
	for i, r := range all {
		d.caret, d.anchor, d.goal = r.Caret, r.Anchor, r.goal
		fn()
		all[i] = Range{Caret: d.caret, Anchor: d.anchor, goal: d.goal}
	}
	d.put(all, first)
	d.sealed = true
}

// eachEdit makes an edit at every caret as fn makes it at one, as one step
// to undo. fn is told which caret it is at, counting from the top.
func (d *Document) eachEdit(fn func(i int)) {
	if len(d.extra) == 0 {
		fn(0)
		return
	}
	before := d.snapshot()
	all, first := d.all()
	mark := len(d.undo)
	d.groups++
	// From the last to the first, so no edit moves text a caret not yet
	// visited is in; those already visited are carried by each edit above.
	for i := len(all) - 1; i >= 0; i-- {
		d.caret, d.anchor, d.goal = all[i].Caret, all[i].Anchor, all[i].goal
		d.sealed = true // each caret's change is its own; the group joins them
		n := len(d.undo)
		fn(i)
		all[i] = Range{Caret: d.caret, Anchor: d.anchor, goal: -1}
		for _, c := range d.undo[n:] {
			for j := i + 1; j < len(all); j++ {
				all[j].Caret, all[j].Anchor = carried(all[j].Caret, c), carried(all[j].Anchor, c)
			}
		}
	}
	d.put(all, first)
	after := d.snapshot()
	for i := mark; i < len(d.undo); i++ {
		d.undo[i].group, d.undo[i].before, d.undo[i].after = d.groups, before, after
	}
	d.sealed = true
}

// carried is where a position after a change is once the change is made: on
// the change's last line it moves along it, below it it moves by the lines
// the change added or took.
func carried(p Pos, c change) Pos {
	removedEnd, insertedEnd := endOf(c.at, c.removed), endOf(c.at, c.inserted)
	switch {
	case p.Less(removedEnd):
		return p // before the change: an edit above never reaches here
	case p.Line == removedEnd.Line:
		return Pos{insertedEnd.Line, insertedEnd.Col + p.Col - removedEnd.Col}
	}
	return Pos{p.Line + insertedEnd.Line - removedEnd.Line, p.Col}
}

// put makes carets the document's, with the one at first the first, and
// tidies them.
func (d *Document) put(all []Range, first int) {
	d.caret, d.anchor, d.goal = all[first].Caret, all[first].Anchor, all[first].goal
	d.extra = append(append([]Range(nil), all[:first]...), all[first+1:]...)
	d.tidy()
}

// tidy joins carets that have met: two at one place are one, and two
// selections that overlap are one selection, with its caret at the end the
// first caret's was at where the first caret is one of them. The first caret
// is never the one that goes.
func (d *Document) tidy() {
	if len(d.extra) == 0 {
		return
	}
	all, first := d.all()
	out := all[:0:0]
	kept := 0
	for i, r := range all {
		n := len(out)
		if n == 0 || !meets(out[n-1], r) {
			if i == first {
				kept = n
			}
			out = append(out, r)
			continue
		}
		lead := out[n-1] // whose caret the joined range keeps
		if i == first {
			lead, kept = r, n-1
		}
		from, to := out[n-1].From(), out[n-1].To()
		if to.Less(r.To()) {
			to = r.To()
		}
		joined := Range{Caret: to, Anchor: from, goal: lead.goal}
		if lead.Caret == lead.From() && !lead.Empty() {
			joined.Caret, joined.Anchor = from, to
		}
		out[n-1] = joined
	}
	d.caret, d.anchor, d.goal = out[kept].Caret, out[kept].Anchor, out[kept].goal
	d.extra = append(append([]Range(nil), out[:kept]...), out[kept+1:]...)
	if len(d.extra) == 0 {
		d.extra = nil
	}
}

// meets reports whether b, which begins at or after a, has met it: at the
// same place, or with their selections overlapping. Two selections that only
// touch are still two; a caret touching a selection has gone into it.
func meets(a, b Range) bool {
	if a.Empty() || b.Empty() {
		return !a.To().Less(b.From())
	}
	return b.From().Less(a.To())
}
