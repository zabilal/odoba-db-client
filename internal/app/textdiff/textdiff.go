// Package textdiff compares two texts line by line.
//
// It exists for one view: two versions of a schema, shown against each other
// so that what changed between them is visible (FR-13.14). It compares text
// and not meaning — two schemas that say the same thing in a different order
// read here as a change, because that is what was registered.
package textdiff

import (
	"bytes"
	"encoding/json"
	"strings"
)

// Op is what happened to a line between the two texts.
type Op int

const (
	// Same is a line both texts have.
	Same Op = iota
	// Added is a line only the second text has.
	Added
	// Removed is a line only the first text has.
	Removed
)

// Line is one line of a comparison, with where it sits in each text. Old is
// its 1-based line number in the first text and New in the second; each is 0
// where that text does not have the line.
type Line struct {
	Op   Op
	Text string
	Old  int
	New  int
}

// budget caps the comparison table. The table costs one int per pair of lines
// still in play once the matching ends of the two texts are set aside, so a
// pair of 2000-line schemas fits and nothing larger is attempted. Schemas run
// to tens of lines; the cap is there so that a pathological one degrades into
// a coarse answer rather than into a pause nobody asked for.
const budget = 4_000_000

// Lines compares two texts and returns every line of both, in reading order,
// each marked as kept, added or removed.
//
// Lines that both texts share at the start and at the end are matched
// directly and kept out of the comparison. That is exact rather than a
// shortcut — where two texts open with the same line there is always a best
// answer that pairs them — and it is what keeps the usual case, a field added
// to a schema that is otherwise untouched, cheap.
func Lines(before, after string) []Line {
	a, b := split(before), split(after)

	// The head both texts share.
	head := 0
	for head < len(a) && head < len(b) && a[head] == b[head] {
		head++
	}
	// The tail both texts share, never reaching back into the head.
	tail := 0
	for tail < len(a)-head && tail < len(b)-head && a[len(a)-1-tail] == b[len(b)-1-tail] {
		tail++
	}

	midA, midB := a[head:len(a)-tail], b[head:len(b)-tail]

	out := make([]Line, 0, len(a)+len(b))
	for i, text := range a[:head] {
		out = append(out, Line{Op: Same, Text: text, Old: i + 1, New: i + 1})
	}
	out = append(out, middle(midA, midB, head)...)
	for i, text := range a[len(a)-tail:] {
		out = append(out, Line{Op: Same, Text: text,
			Old: len(a) - tail + i + 1, New: len(b) - tail + i + 1})
	}
	return out
}

// middle compares what is left once the matching ends are set aside. off is
// how many lines of each text came before it, so that the line numbers it
// reports are the ones in the whole text.
func middle(a, b []string, off int) []Line {
	switch {
	case len(a) == 0 && len(b) == 0:
		return nil
	case len(a) == 0 || len(b) == 0 || len(a)*len(b) > budget:
		// Nothing to pair, or too much to pair carefully. Either way the
		// honest answer is that this much went and this much arrived.
		return flat(a, b, off)
	}

	// t[i][j] is how many lines a[i:] and b[j:] have in common.
	t := make([][]int, len(a)+1)
	for i := range t {
		t[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			switch {
			case a[i] == b[j]:
				t[i][j] = t[i+1][j+1] + 1
			case t[i+1][j] >= t[i][j+1]:
				t[i][j] = t[i+1][j]
			default:
				t[i][j] = t[i][j+1]
			}
		}
	}

	var out []Line
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			out = append(out, Line{Op: Same, Text: a[i], Old: off + i + 1, New: off + j + 1})
			i++
			j++
		case t[i+1][j] >= t[i][j+1]:
			// What was there and is not comes first, so that a line and its
			// replacement read in that order.
			out = append(out, Line{Op: Removed, Text: a[i], Old: off + i + 1})
			i++
		default:
			out = append(out, Line{Op: Added, Text: b[j], New: off + j + 1})
			j++
		}
	}
	// Whichever text still has lines left has only lines the other does not.
	for ; i < len(a); i++ {
		out = append(out, Line{Op: Removed, Text: a[i], Old: off + i + 1})
	}
	for ; j < len(b); j++ {
		out = append(out, Line{Op: Added, Text: b[j], New: off + j + 1})
	}
	return out
}

// flat is the answer when there is nothing to pair: everything in the first
// text went, and everything in the second arrived.
func flat(a, b []string, off int) []Line {
	out := make([]Line, 0, len(a)+len(b))
	for i, text := range a {
		out = append(out, Line{Op: Removed, Text: text, Old: off + i + 1})
	}
	for j, text := range b {
		out = append(out, Line{Op: Added, Text: text, New: off + j + 1})
	}
	return out
}

// Summary is how many lines arrived and how many went.
func Summary(lines []Line) (added, removed int) {
	for _, l := range lines {
		switch l.Op {
		case Added:
			added++
		case Removed:
			removed++
		}
	}
	return added, removed
}

// Indent lays a JSON text out over several lines, and returns anything else
// unchanged.
//
// A registry keeps a schema as the one string it was registered with, and for
// Avro and JSON Schema that is usually a single line however large the schema
// is. Comparing two such texts line by line would only ever report that the
// one line changed, which is true and useless. Laying them out first is what
// gives the comparison something to work with. Protobuf arrives with its own
// lines and is left alone.
func Indent(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || !json.Valid([]byte(trimmed)) {
		return text
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(trimmed), "", "  "); err != nil {
		return text
	}
	return buf.String()
}

// split is a text as its lines, with either line ending, and no lines at all
// for an empty text.
func split(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
