package sqlite

import (
	"context"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// How SQLite would run a statement (FR-5.13).
//
// A real SQLite file, because a plan is the planner's own answer about its
// own behaviour and no fake can stand in for it.

func planOf(t *testing.T, s *sqliteSource, sql string) *source.Plan {
	t.Helper()
	p, err := s.Explain(context.Background(), source.Statement{SQL: sql}, false)
	if err != nil {
		t.Fatalf("Explain(%q): %v", sql, err)
	}
	if p == nil || p.Root == nil {
		t.Fatalf("Explain(%q) returned no plan", sql)
	}
	return p
}

func walk(n *source.PlanNode, f func(*source.PlanNode)) {
	f(n)
	for _, c := range n.Children {
		walk(c, f)
	}
}

func steps(n *source.PlanNode) []string {
	var out []string
	walk(n, func(n *source.PlanNode) { out = append(out, n.Operation) })
	return out
}

func joined(n *source.PlanNode) string { return strings.Join(steps(n), " | ") }

// A plan names the steps and the tables they read.
func TestAPlanNamesWhatIsRead(t *testing.T) {
	s := open(t, fixture(t), source.Guard{})
	defer s.Close()
	p := planOf(t, s, `SELECT * FROM people WHERE name = 'person 3'`)
	if !strings.Contains(joined(p.Root), "people") {
		t.Errorf("the plan is %q; the table it reads is not named", joined(p.Root))
	}
	if !strings.Contains(p.Text, "people") {
		t.Errorf("the raw plan is %q", p.Text)
	}
}

// An index is named where one is used, because that is the whole question a
// plan is read to answer.
func TestAPlanNamesTheIndexItUsed(t *testing.T) {
	s := open(t, fixture(t), source.Guard{})
	defer s.Close()
	p := planOf(t, s, `SELECT * FROM orders ORDER BY total DESC`)
	said := ""
	walk(p.Root, func(n *source.PlanNode) { said += n.Detail + " | " })
	if !strings.Contains(said, "orders_total") {
		t.Errorf("the details are %q; the index is not among them", said)
	}
	// The short name stops before the index, which the detail carries.
	for _, op := range steps(p.Root) {
		if strings.Contains(op, "USING") {
			t.Errorf("the step is called %q, which is the whole line rather than its name", op)
		}
	}
}

// SQLite publishes no estimates at all, so every figure says so rather than
// standing at a number nobody reported.
func TestSQLiteReportsNoFigures(t *testing.T) {
	s := open(t, fixture(t), source.Guard{})
	defer s.Close()
	p := planOf(t, s, `SELECT * FROM people`)
	walk(p.Root, func(n *source.PlanNode) {
		if n.EstimatedCost != -1 || n.EstimatedRows != -1 || n.ActualRows != -1 || n.ActualTime != 0 {
			t.Errorf("%s reports cost %v, %d rows, %d actual in %v; SQLite reports none of these",
				n.Operation, n.EstimatedCost, n.EstimatedRows, n.ActualRows, n.ActualTime)
		}
	})
}

// A subquery's steps sit under the step that runs them, which is what makes
// a plan a tree rather than a list.
func TestStepsSitUnderTheStepsTheyBelongTo(t *testing.T) {
	s := open(t, fixture(t), source.Guard{})
	defer s.Close()
	p := planOf(t, s, `SELECT * FROM people WHERE id IN (SELECT person_id FROM orders)`)

	// The scan of orders is inside the subquery, which is inside the
	// statement: a grandchild, not a step in a list.
	at := map[string]int{}
	depths(p.Root, 0, at)
	if at["SCAN orders"] != 2 {
		t.Errorf("the scan of orders sits at depth %d; the plan reads %v", at["SCAN orders"], at)
	}
	if at["LIST SUBQUERY 1"] != 1 {
		t.Errorf("the subquery sits at depth %d; the plan reads %v", at["LIST SUBQUERY 1"], at)
	}
}

// depths records how deep each step sits.
func depths(n *source.PlanNode, at int, out map[string]int) {
	out[n.Operation] = at
	for _, c := range n.Children {
		depths(c, at+1, out)
	}
}

// A statement with several steps at the top — a join is one — has no single
// step to be the root, and a tree has one, so they hang under one named
// after the statement itself.
func TestSeveralTopStepsHangUnderOne(t *testing.T) {
	s := open(t, fixture(t), source.Guard{})
	defer s.Close()
	p := planOf(t, s, `SELECT * FROM people, orders`)
	if p.Root.Operation != "Statement" {
		t.Errorf("the root is %q; two steps at the top need one over them", p.Root.Operation)
	}
	if len(p.Root.Children) != 2 {
		t.Errorf("the plan is %q; both tables are scanned", joined(p.Root))
	}
	if !strings.Contains(joined(p.Root), "people") || !strings.Contains(joined(p.Root), "orders") {
		t.Errorf("the plan is %q; both tables should be in it", joined(p.Root))
	}
	// One step at the top is the root itself, with nothing over it.
	one := planOf(t, s, `SELECT * FROM people`)
	if one.Root.Operation == "Statement" {
		t.Errorf("one step at the top was given something over it: %q", joined(one.Root))
	}
}

// There is no form here that runs the statement and measures it, so asking
// for one says so rather than quietly returning a plan that was not
// measured.
func TestAnalysingIsRefusedRatherThanPretended(t *testing.T) {
	s := open(t, fixture(t), source.Guard{})
	defer s.Close()
	_, err := s.Explain(context.Background(), source.Statement{SQL: `SELECT 1`}, true)
	if err == nil {
		t.Fatal("SQLite analysed a statement")
	}
	if !strings.Contains(err.Error(), "measures") {
		t.Errorf("it said %q", err)
	}
	if !s.Capabilities().Query.Explain || s.Capabilities().Query.ExplainAnalyze {
		t.Error("the capability should offer a plan and not an analysed one")
	}
}

// Asking the planner what it would do changes nothing, so a read-only
// connection may ask about anything.
func TestAReadOnlyConnectionMayPlanAWrite(t *testing.T) {
	s := open(t, fixture(t), source.Guard{ReadOnly: true})
	defer s.Close()
	if _, err := s.Explain(context.Background(),
		source.Statement{SQL: `UPDATE writes SET n = 1`}, false); err != nil {
		t.Errorf("planning a write on a read-only connection: %v", err)
	}
}

// A statement SQLite plans in no steps at all still answers something,
// rather than a step with no name.
func TestAStatementWithNoStepsStillHasAPlan(t *testing.T) {
	root := planTree(nil)
	if root == nil || root.Operation == "" {
		t.Errorf("a plan of no steps is %+v", root)
	}
	if len(root.Children) != 0 {
		t.Errorf("a plan of no steps has %d under it", len(root.Children))
	}

	s := open(t, fixture(t), source.Guard{})
	defer s.Close()
	p := planOf(t, s, `SELECT 1`)
	if p.Root.Operation == "" {
		t.Error("the plan says nothing at all")
	}
}
