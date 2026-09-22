package postgres

import (
	"strings"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Reading PostgreSQL's JSON plan.
//
// The live tests prove this reads what the server actually says (see
// explain_live_test.go). These prove the reading itself, against documents
// written out by hand, including the ones no server would send.

func parsed(t *testing.T, raw string) *source.PlanNode {
	t.Helper()
	n, err := parsePlan(raw)
	if err != nil {
		t.Fatalf("parsePlan: %v", err)
	}
	return n
}

// A strategy is part of what a step is called: a hashed aggregate and a
// sorted one are different things, and "Aggregate" alone says neither.
func TestAStepIsNamedWithItsStrategy(t *testing.T) {
	n := parsed(t, `[{"Plan": {"Node Type": "Aggregate", "Strategy": "Hashed", "Total Cost": 10}}]`)
	if n.Operation != "Hashed Aggregate" {
		t.Errorf("the step is called %q", n.Operation)
	}
	// Plain is not a strategy anybody needs told.
	n = parsed(t, `[{"Plan": {"Node Type": "Aggregate", "Strategy": "Plain", "Total Cost": 10}}]`)
	if n.Operation != "Aggregate" {
		t.Errorf("the step is called %q", n.Operation)
	}
}

// A join says which kind it is, unless it is the ordinary one.
func TestAJoinSaysWhichKindItIs(t *testing.T) {
	n := parsed(t, `[{"Plan": {"Node Type": "Hash Join", "Join Type": "Left"}}]`)
	if n.Operation != "Left Hash Join" {
		t.Errorf("the step is called %q", n.Operation)
	}
	n = parsed(t, `[{"Plan": {"Node Type": "Hash Join", "Join Type": "Inner"}}]`)
	if n.Operation != "Hash Join" {
		t.Errorf("the step is called %q", n.Operation)
	}
}

// What a step reads is named, with the index where there is one and the
// alias where the statement gave one.
func TestAStepNamesWhatItReads(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{`{"Node Type": "Seq Scan", "Relation Name": "orders"}`, "Seq Scan on orders"},
		{`{"Node Type": "Seq Scan", "Relation Name": "orders", "Alias": "o"}`, "Seq Scan on orders o"},
		{`{"Node Type": "Seq Scan", "Relation Name": "orders", "Alias": "orders"}`, "Seq Scan on orders"},
		{`{"Node Type": "Index Scan", "Relation Name": "orders", "Index Name": "orders_pkey"}`,
			"Index Scan using orders_pkey on orders"},
		{`{"Node Type": "Index Only Scan", "Index Name": "orders_pkey"}`, "Index Only Scan using orders_pkey"},
		{`{"Node Type": "Seq Scan", "Relation Name": "orders", "Parallel Aware": true}`,
			"Parallel Seq Scan on orders"},
		{`{"Node Type": "Seq Scan", "Relation Name": "orders", "Subplan Name": "CTE x"}`,
			"CTE x: Seq Scan on orders"},
	} {
		if got := parsed(t, `[{"Plan": `+tc.raw+`}]`).Operation; got != tc.want {
			t.Errorf("%s\n  became %q, want %q", tc.raw, got, tc.want)
		}
	}
}

// What a step tested, and what it threw away, in the server's own words.
func TestAStepSaysWhatItDid(t *testing.T) {
	n := parsed(t, `[{"Plan": {"Node Type": "Seq Scan", "Relation Name": "orders",
		"Filter": "(id > 1)", "Rows Removed by Filter": 40, "Sort Key": ["a", "b"],
		"Heap Fetches": 3, "Shared Read Blocks": 7}}]`)
	for _, want := range []string{"Filter: (id > 1)", "Rows removed by filter: 40",
		"Sort Key: a, b", "Heap fetches: 3", "Read from disk: 7 blocks"} {
		if !strings.Contains(n.Detail, want) {
			t.Errorf("the detail is %q; %q is not in it", n.Detail, want)
		}
	}
	// Nothing is said about what did not happen.
	n = parsed(t, `[{"Plan": {"Node Type": "Seq Scan", "Rows Removed by Filter": 0,
		"Heap Fetches": 0, "Shared Read Blocks": 0}}]`)
	if n.Detail != "" {
		t.Errorf("the detail is %q, want nothing", n.Detail)
	}
}

// A step that was never run reports no measurement, and one that was reports
// what every loop of it did rather than what one did.
func TestWhatIsMeasuredAndWhatIsNot(t *testing.T) {
	n := parsed(t, `[{"Plan": {"Node Type": "Seq Scan", "Total Cost": 12.5, "Plan Rows": 40}}]`)
	if n.EstimatedCost != 12.5 || n.EstimatedRows != 40 {
		t.Errorf("the step costs %v for %d rows", n.EstimatedCost, n.EstimatedRows)
	}
	if n.ActualRows != -1 || n.ActualTime != 0 {
		t.Errorf("a step that was never run reports %d rows in %v", n.ActualRows, n.ActualTime)
	}

	n = parsed(t, `[{"Plan": {"Node Type": "Index Scan", "Actual Rows": 3,
		"Actual Loops": 1000, "Actual Total Time": 0.004}}]`)
	if n.ActualRows != 3000 {
		t.Errorf("a step run a thousand times returned %d rows, want 3000", n.ActualRows)
	}
	if want := 4 * time.Millisecond; n.ActualTime != want {
		t.Errorf("it took %v, want %v", n.ActualTime, want)
	}
	// No loops reported is one loop, not none.
	n = parsed(t, `[{"Plan": {"Node Type": "Index Scan", "Actual Rows": 3, "Actual Total Time": 1}}]`)
	if n.ActualRows != 3 || n.ActualTime != time.Millisecond {
		t.Errorf("it returned %d rows in %v", n.ActualRows, n.ActualTime)
	}
	// A cost nobody reported is not a cost of nothing.
	n = parsed(t, `[{"Plan": {"Node Type": "Result"}}]`)
	if n.EstimatedCost != -1 || n.EstimatedRows != -1 {
		t.Errorf("an unreported cost came out as %v for %d rows", n.EstimatedCost, n.EstimatedRows)
	}
}

// A step's own steps sit under it, however deep.
func TestStepsNestAsTheServerNestedThem(t *testing.T) {
	n := parsed(t, `[{"Plan": {"Node Type": "Aggregate", "Plans": [
		{"Node Type": "Hash Join", "Plans": [
			{"Node Type": "Seq Scan", "Relation Name": "a"},
			{"Node Type": "Hash", "Plans": [{"Node Type": "Seq Scan", "Relation Name": "b"}]}]}]}}]`)
	if len(n.Children) != 1 || len(n.Children[0].Children) != 2 {
		t.Fatalf("the tree is %s", shape(n))
	}
	if got := shape(n); got != "Aggregate(Hash Join(Seq Scan on a,Hash(Seq Scan on b)))" {
		t.Errorf("the tree is %s", got)
	}
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

// A document that is not a plan says so rather than being read as an empty
// one, which would look like a statement the server ran in no steps.
func TestADocumentThatIsNotAPlan(t *testing.T) {
	for _, raw := range []string{"", "not json", "[]", `[{}]`, `[{"Plan": null}]`} {
		if _, err := parsePlan(raw); err == nil {
			t.Errorf("%q was read as a plan", raw)
		}
	}
}
