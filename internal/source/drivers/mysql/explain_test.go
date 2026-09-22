package mysql

import (
	"strings"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Reading MySQL's and MariaDB's JSON plans.
//
// The live tests prove this reads what the servers actually say (see
// explain_live_test.go). These prove the reading itself, against the two
// servers' own words written out by hand — the same statement, planned by
// each, which is the whole difficulty.

func parsed(t *testing.T, raw string) *source.PlanNode {
	t.Helper()
	n, err := parsePlan(raw)
	if err != nil {
		t.Fatalf("parsePlan: %v", err)
	}
	return n
}

func shape(n *source.PlanNode) string {
	if len(n.Children) == 0 {
		return n.Operation
	}
	var kids []string
	for _, c := range n.Children {
		kids = append(kids, shape(c))
	}
	return n.Operation + "(" + strings.Join(kids, ",") + ")"
}

// mysqlJoin is MySQL 8's plan for a join with an ordering, cut to the fields
// that are read.
const mysqlJoin = `{"query_block": {"select_id": 1, "cost_info": {"query_cost": "0.81"},
  "ordering_operation": {"using_filesort": true, "cost_info": {"sort_cost": "0.11"},
    "nested_loop": [
      {"table": {"table_name": "o", "access_type": "ALL", "possible_keys": ["fk_person"],
        "rows_examined_per_scan": 7, "filtered": "100.00",
        "cost_info": {"read_cost": "0.25", "prefix_cost": "0.35"},
        "attached_condition": "(` + "`o`.`person_id`" + ` is not null)"}},
      {"table": {"table_name": "p", "access_type": "eq_ref", "key": "PRIMARY",
        "used_key_parts": ["id"], "ref": ["db.o.person_id"], "rows_examined_per_scan": 1,
        "filtered": "11.11", "cost_info": {"prefix_cost": "0.70"}}}]}}}`

// mariaJoin is MariaDB 11's plan for the same statement, in its own words.
const mariaJoin = `{"query_block": {"select_id": 1, "cost": 0.0133,
  "nested_loop": [
    {"read_sorted_file": {"filesort": {"sort_key": "o.total",
      "table": {"table_name": "o", "access_type": "ALL", "loops": 1, "rows": 7,
        "cost": 0.011, "filtered": 100, "attached_condition": "o.person_id is not null"}}}},
    {"table": {"table_name": "p", "access_type": "eq_ref", "key": "PRIMARY",
      "used_key_parts": ["id"], "ref": ["db.o.person_id"], "loops": 1, "rows": 1,
      "cost": 0.0017, "filtered": 100}}]}}`

// Both servers' words come out as the same steps over the same tables.
func TestBothServersReadAsTheSameSteps(t *testing.T) {
	for name, raw := range map[string]string{"mysql": mysqlJoin, "mariadb": mariaJoin} {
		n := parsed(t, raw)
		said := shape(n)
		for _, want := range []string{"Full scan on o", "Lookup (eq_ref) on p using PRIMARY"} {
			if !strings.Contains(said, want) {
				t.Errorf("%s: the plan is %s; %q is not in it", name, said, want)
			}
		}
		if n.Operation != "Query block" {
			t.Errorf("%s: the root is %q", name, n.Operation)
		}
	}
}

// The costs come out wherever each server put them, and the rows likewise.
func TestTheFiguresAreFoundWhereverTheServerPutThem(t *testing.T) {
	my := parsed(t, mysqlJoin)
	maria := parsed(t, mariaJoin)
	for name, n := range map[string]*source.PlanNode{"mysql": my, "mariadb": maria} {
		var tables []*source.PlanNode
		walkNodes(n, func(n *source.PlanNode) {
			if strings.Contains(n.Operation, " on o") {
				tables = append(tables, n)
			}
		})
		if len(tables) != 1 {
			t.Fatalf("%s: %d steps read o", name, len(tables))
		}
		if tables[0].EstimatedRows != 7 {
			t.Errorf("%s: it expects %d rows, want 7", name, tables[0].EstimatedRows)
		}
		if tables[0].EstimatedCost <= 0 {
			t.Errorf("%s: it costs %v", name, tables[0].EstimatedCost)
		}
	}
}

func walkNodes(n *source.PlanNode, f func(*source.PlanNode)) {
	f(n)
	for _, c := range n.Children {
		walkNodes(c, f)
	}
}

// A sort is a step, wherever a server hangs it.
func TestASortIsAStepInEitherServersWords(t *testing.T) {
	if !strings.Contains(shape(parsed(t, mysqlJoin)), "Order") {
		t.Errorf("MySQL's ordering is not a step: %s", shape(parsed(t, mysqlJoin)))
	}
	if !strings.Contains(shape(parsed(t, mariaJoin)), "Sort") {
		t.Errorf("MariaDB's sort is not a step: %s", shape(parsed(t, mariaJoin)))
	}
}

// What a step tested, and what it narrowed to.
func TestAStepSaysWhatItDid(t *testing.T) {
	n := parsed(t, mysqlJoin)
	said := ""
	walkNodes(n, func(n *source.PlanNode) { said += n.Detail + " | " })
	for _, want := range []string{"Condition:", "Ref: db.o.person_id", "Key parts: id",
		"Possible keys: fk_person", "Filtered to 11.11%"} {
		if !strings.Contains(said, want) {
			t.Errorf("the details are %q; %q is not in them", said, want)
		}
	}
	if !strings.Contains(said, "Sorted on disk") {
		t.Errorf("the details are %q; the filesort is not said", said)
	}
	// A step that narrowed to everything narrowed to nothing worth saying.
	if strings.Contains(said, "Filtered to 100%") {
		t.Errorf("the details are %q; narrowing to everything is not narrowing", said)
	}
}

// MariaDB measures a statement into the same document, per loop.
func TestMariaDBsMeasurementsAreCountedForEveryLoop(t *testing.T) {
	n := parsed(t, `{"query_block": {"select_id": 1, "r_total_time_ms": 12.5,
	  "table": {"table_name": "people", "access_type": "ALL", "rows": 101,
	    "r_rows": 3, "r_loops": 1000, "r_total_time_ms": 5.5}}}`)
	if n.ActualTime != 12500*time.Microsecond {
		t.Errorf("the statement took %v", n.ActualTime)
	}
	if len(n.Children) != 1 {
		t.Fatalf("the plan is %s", shape(n))
	}
	if got := n.Children[0].ActualRows; got != 3000 {
		t.Errorf("a step run a thousand times returned %d rows, want 3000", got)
	}
}

// A statement over no tables still has a plan, and the server's message is
// what it says.
func TestAStatementOverNoTables(t *testing.T) {
	n := parsed(t, `{"query_block": {"select_id": 1, "message": "No tables used"}}`)
	if n.Operation != "Query block" {
		t.Errorf("the step is %q", n.Operation)
	}
	if !strings.Contains(n.Detail, "No tables used") {
		t.Errorf("the detail is %q", n.Detail)
	}
}

// A key the servers add later is not a step until it is named as one, so a
// plan never invents one out of a field.
func TestAnUnknownKeyIsNotAStep(t *testing.T) {
	n := parsed(t, `{"query_block": {"select_id": 1,
	  "some_future_thing": {"table": {"table_name": "x", "access_type": "ALL"}}}}`)
	if got := shape(n); got != "Query block" {
		t.Errorf("the plan is %s; an unnamed key became a step", got)
	}
}

// A document that is not a plan says so rather than being read as an empty
// one.
func TestADocumentThatIsNotAPlan(t *testing.T) {
	for _, raw := range []string{"", "not json", "{}", `{"nothing": 1}`} {
		if _, err := parsePlan(raw); err == nil {
			t.Errorf("%q was read as a plan", raw)
		}
	}
}

// Several steps at the top have nothing to be the root, so they hang under
// one named after the statement.
func TestSeveralTopStepsHangUnderOne(t *testing.T) {
	n := parsed(t, `{"query_block": {"select_id": 1}, "insert": {"table": {"table_name": "x"}}}`)
	if n.Operation != "Statement" || len(n.Children) != 2 {
		t.Errorf("the plan is %s", shape(n))
	}
}

// The steps come out in the same order every time: a map's keys do not, and
// a plan that rearranged itself between two readings would be unreadable.
//
// A document with several steps in one object is what settles it — one step
// comes out in one order however it was reached.
const several = `{"query_block": {"table": {"table_name": "a", "access_type": "ALL"}},
  "insert": {"table": {"table_name": "b", "access_type": "ALL"}},
  "update": {"table": {"table_name": "c", "access_type": "ALL"}},
  "delete": {"table": {"table_name": "d", "access_type": "ALL"}}}`

func TestAPlanReadsTheSameEveryTime(t *testing.T) {
	for _, raw := range []string{mysqlJoin, mariaJoin, several} {
		first := shape(parsed(t, raw))
		for i := 0; i < 50; i++ {
			if got := shape(parsed(t, raw)); got != first {
				t.Fatalf("reading %d gave %s, the first gave %s", i, got, first)
			}
		}
	}
}

// An access type is said in words, with the server's own abbreviation kept
// for anybody who reads plans already.
func TestAnAccessTypeIsSaidInWords(t *testing.T) {
	for access, want := range map[string]string{
		"ALL": "Full scan", "index": "Index scan", "range": "Range scan",
		"eq_ref": "Lookup (eq_ref)", "const": "Lookup (const)", "unknown_thing": "unknown_thing",
	} {
		if got := accessName(access); got != want {
			t.Errorf("%q is said as %q, want %q", access, got, want)
		}
	}
	// A table with no access type is still a table.
	n := parsed(t, `{"query_block": {"table": {"table_name": "x"}}}`)
	if !strings.Contains(shape(n), "Read x") {
		t.Errorf("the plan is %s", shape(n))
	}
	n = parsed(t, `{"query_block": {"table": {"access_type": "ALL"}}}`)
	if !strings.Contains(shape(n), "Table") {
		t.Errorf("the plan is %s", shape(n))
	}
}
