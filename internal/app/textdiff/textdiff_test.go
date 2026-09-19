package textdiff

import (
	"fmt"
	"strings"
	"testing"
)

// Comparing two versions of a schema (T2.74, FR-13.14). What these hold is
// that the comparison reports what changed and not merely that something did,
// and that every line it reports can be found in the text it came from.

// render is a comparison as somebody would read it, which is how a wrong
// answer is easiest to see when a test fails.
func render(lines []Line) string {
	var b strings.Builder
	for _, l := range lines {
		switch l.Op {
		case Same:
			b.WriteString("  ")
		case Added:
			b.WriteString("+ ")
		case Removed:
			b.WriteString("- ")
		}
		b.WriteString(l.Text)
		b.WriteString("\n")
	}
	return b.String()
}

func TestALineAddedIsTheOnlyThingReported(t *testing.T) {
	before := "one\ntwo\nfour"
	after := "one\ntwo\nthree\nfour"
	got := Lines(before, after)
	if want := "  one\n  two\n+ three\n  four\n"; render(got) != want {
		t.Errorf("a line added reads as:\n%s\nwanted:\n%s", render(got), want)
	}
	added, removed := Summary(got)
	if added != 1 || removed != 0 {
		t.Errorf("a line added counts as %d added and %d removed, not 1 and 0", added, removed)
	}
}

func TestALineRemovedIsTheOnlyThingReported(t *testing.T) {
	got := Lines("one\ntwo\nthree", "one\nthree")
	if want := "  one\n- two\n  three\n"; render(got) != want {
		t.Errorf("a line removed reads as:\n%s\nwanted:\n%s", render(got), want)
	}
}

func TestALineChangedReadsAsWhatWentAndWhatArrived(t *testing.T) {
	got := Lines("one\ntwo\nthree", "one\nTWO\nthree")
	// What was there comes first: a line and the line replacing it read in
	// the order somebody would read them.
	if want := "  one\n- two\n+ TWO\n  three\n"; render(got) != want {
		t.Errorf("a line changed reads as:\n%s\nwanted:\n%s", render(got), want)
	}
}

func TestEveryLineSaysWhereItIsInItsOwnText(t *testing.T) {
	// "two" goes and two lines arrive, so after the change the two texts are
	// numbered differently and each line has to carry its own number.
	got := Lines("one\ntwo\nlast", "one\nA\nB\nlast")
	want := []Line{
		{Op: Same, Text: "one", Old: 1, New: 1},
		{Op: Removed, Text: "two", Old: 2},
		{Op: Added, Text: "A", New: 2},
		{Op: Added, Text: "B", New: 3},
		{Op: Same, Text: "last", Old: 3, New: 4},
	}
	if len(got) != len(want) {
		t.Fatalf("the comparison has %d lines, not %d:\n%s", len(got), len(want), render(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d is %+v, not %+v", i, got[i], want[i])
		}
	}
}

func TestTwoTextsTheSameReportNoChange(t *testing.T) {
	text := "one\ntwo\nthree"
	got := Lines(text, text)
	added, removed := Summary(got)
	if added != 0 || removed != 0 {
		t.Errorf("a text against itself reports %d added and %d removed:\n%s", added, removed, render(got))
	}
	if len(got) != 3 {
		t.Errorf("a text against itself has %d lines, not its own 3:\n%s", len(got), render(got))
	}
}

func TestATextAgainstNothingIsAllOfIt(t *testing.T) {
	if got := Lines("", "one\ntwo"); render(got) != "+ one\n+ two\n" {
		t.Errorf("a first version reads as:\n%s", render(got))
	}
	if got := Lines("one\ntwo", ""); render(got) != "- one\n- two\n" {
		t.Errorf("a text emptied reads as:\n%s", render(got))
	}
	if got := Lines("", ""); len(got) != 0 {
		t.Errorf("nothing against nothing has %d lines", len(got))
	}
}

func TestLineEndingsAreNotAChange(t *testing.T) {
	got := Lines("one\r\ntwo", "one\ntwo")
	added, removed := Summary(got)
	if added != 0 || removed != 0 {
		t.Errorf("how the lines end reads as a change: %d added, %d removed:\n%s",
			added, removed, render(got))
	}
}

func TestMoreThanCanBeComparedCarefullySaysSoPlainly(t *testing.T) {
	// Two texts sharing all but one line at each end, so that nothing can be
	// set aside before the comparison and the cap alone decides what happens.
	// The difference between the two answers is the whole point: compared
	// carefully these are one line apart, and past the cap they are reported
	// as one text replaced by another.
	const n = 2100
	var a, b []string
	for i := range n {
		a = append(a, fmt.Sprintf("line %d", i))
		b = append(b, fmt.Sprintf("line %d", i+1))
	}

	got := Lines(strings.Join(a, "\n"), strings.Join(b, "\n"))
	if added, removed := Summary(got); added != n || removed != n {
		t.Errorf("past the cap, %d arrived and %d went, not %d and %d", added, removed, n, n)
	}
	// Coarse, but still an account of every line of both texts.
	if len(got) != 2*n {
		t.Errorf("past the cap the answer has %d lines, not the %d of both texts", len(got), 2*n)
	}
	if got[0].Op != Removed || got[0].Old != 1 {
		t.Errorf("past the cap the first line is %+v, not the first line of the first text", got[0])
	}

	// The same shape, small enough to compare carefully: one line went and
	// one arrived, and everything between was recognised.
	const small = n / 3
	got = Lines(strings.Join(a[:small], "\n"), strings.Join(b[:small], "\n"))
	if added, removed := Summary(got); added != 1 || removed != 1 {
		t.Errorf("under the cap, %d arrived and %d went, not 1 and 1", added, removed)
	}
}

func TestASchemaOnOneLineIsLaidOutSoThereIsSomethingToCompare(t *testing.T) {
	// What a registry hands over: the whole schema as one string.
	before := `{"type":"record","name":"Order","fields":[{"name":"id","type":"string"}]}`
	after := `{"type":"record","name":"Order","fields":[{"name":"id","type":"string"},{"name":"total","type":"double"}]}`

	// Compared as they arrive, the only thing that can be said is that the
	// one line changed.
	flatly := Lines(before, after)
	if added, removed := Summary(flatly); added != 1 || removed != 1 {
		t.Errorf("one line against one line reports %d added and %d removed, not 1 and 1", added, removed)
	}

	// Laying it out is what makes that possible: a schema kept as one string
	// has to become several lines before a line comparison can say anything.
	if strings.Count(Indent(before), "\n") == 0 {
		t.Fatalf("a schema registered as one line was not laid out: %s", Indent(before))
	}

	// Laid out first, the comparison finds the field that was added and
	// leaves the rest of the schema alone.
	got := Lines(Indent(before), Indent(after))
	added, removed := Summary(got)
	if added == 0 {
		t.Fatalf("laying the schema out found nothing added:\n%s", render(got))
	}
	if removed > 2 {
		// The line the new field hangs off changes; the fields already there
		// must not.
		t.Errorf("laying the schema out reports %d lines gone, which is most of a schema that only gained a field:\n%s",
			removed, render(got))
	}
	var arrived string
	for _, l := range got {
		if l.Op == Added {
			arrived += l.Text
		}
	}
	if !strings.Contains(arrived, "total") {
		t.Errorf("the field that was added is not among what arrived:\n%s", render(got))
	}
}

func TestATextThatIsNotJSONIsLeftAsItIs(t *testing.T) {
	// Protobuf arrives with its own lines and must not be touched.
	proto := "syntax = \"proto3\";\n\nmessage Order {\n  string id = 1;\n}\n"
	if got := Indent(proto); got != proto {
		t.Errorf("a Protobuf schema was rewritten:\n%s", got)
	}
	if got := Indent(""); got != "" {
		t.Errorf("an empty schema became %q", got)
	}
	if got := Indent("not a schema at all"); got != "not a schema at all" {
		t.Errorf("text that is not JSON became %q", got)
	}
}

func TestATextThatRepeatsItsOnlyLineIsNotMiscounted(t *testing.T) {
	// The ends of the two texts are matched before anything is compared, and
	// here the same single line is both the first and the last. It must be
	// claimed by one end only: counted twice, there is nothing left in the
	// middle for either to describe.
	got := Lines("x", "x\nx")
	added, removed := Summary(got)
	if added != 1 || removed != 0 {
		t.Errorf("a line repeated reads as %d arrived and %d went:\n%s", added, removed, render(got))
	}
	if len(got) != 2 {
		t.Errorf("a line repeated reads as %d lines, not 2:\n%s", len(got), render(got))
	}

	// And the other way about: a text that loses its repeat.
	got = Lines("x\nx", "x")
	if added, removed = Summary(got); added != 0 || removed != 1 {
		t.Errorf("a repeat removed reads as %d arrived and %d went:\n%s", added, removed, render(got))
	}
}
