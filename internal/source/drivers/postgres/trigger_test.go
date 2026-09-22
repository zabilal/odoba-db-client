package postgres

import (
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// pg_trigger.tgtype is a bitmask, and the structure tab has drawn a Triggers
// section with a "When" column since it was written. Reading the bits wrong
// fills that column with something plausible and false, which is worse than
// leaving it empty.

func TestATriggerSaysWhenItFires(t *testing.T) {
	for _, c := range []struct {
		what   string
		bits   int32
		timing string
		row    bool
		events []string
	}{
		{"after insert, for each statement", trigInsert, "AFTER", false, []string{"INSERT"}},
		{"before insert, for each row", trigBefore | trigRow | trigInsert, "BEFORE", true, []string{"INSERT"}},
		{"instead of update on a view", trigInstead | trigRow | trigUpdate, "INSTEAD OF", true, []string{"UPDATE"}},
		{"after several things", trigRow | trigInsert | trigUpdate | trigDelete, "AFTER", true,
			[]string{"INSERT", "DELETE", "UPDATE"}},
		{"after truncate", trigTruncate, "AFTER", false, []string{"TRUNCATE"}},
		// INSTEAD OF sets the BEFORE bit too, so reading them in the wrong
		// order calls an INSTEAD OF trigger a BEFORE one.
		{"instead of, which also says before", trigInstead | trigBefore | trigRow | trigInsert,
			"INSTEAD OF", true, []string{"INSERT"}},
	} {
		got := triggerOf("audit", "CREATE TRIGGER audit", c.bits, "new.id > 0")
		if got.Timing != c.timing {
			t.Errorf("%s: it fires %s, want %s", c.what, got.Timing, c.timing)
		}
		if got.ForEachRow != c.row {
			t.Errorf("%s: for each row is %v, want %v", c.what, got.ForEachRow, c.row)
		}
		if strings.Join(got.Events, ",") != strings.Join(c.events, ",") {
			t.Errorf("%s: it fires on %v, want %v", c.what, got.Events, c.events)
		}
		if got.Condition != "new.id > 0" || got.Name != "audit" {
			t.Errorf("%s: it read %+v", c.what, got)
		}
	}
}

// An index's key columns come back as names, and an expression index as the
// expression pg_get_indexdef printed.
func TestReadingWhatAnIndexIsMadeOf(t *testing.T) {
	got := indexColumnsOf([]string{`"Order"`, "lower(name)", "id"}, []bool{false, true})
	want := []model.IndexColumn{
		{Name: "Order"},
		{Expression: "lower(name)", Descending: true},
		{Name: "id"},
	}
	if len(got) != len(want) {
		t.Fatalf("it read %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("column %d is %+v, want %+v", i, got[i], want[i])
		}
	}
}

// An included column comes back as its name. The DDL generator quotes what
// it is given, so a name left with its quotes renders a column nobody has.
func TestAnIncludedColumnIsANameAndNotAQuotedOne(t *testing.T) {
	if got := trimNames([]string{`"Order"`, "id"}); strings.Join(got, ",") != "Order,id" {
		t.Errorf("it read %q", got)
	}
	if got := trimNames(nil); got != nil {
		t.Errorf("nothing read as %q", got)
	}
}
