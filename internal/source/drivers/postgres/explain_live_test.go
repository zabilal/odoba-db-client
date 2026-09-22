//go:build conformance

package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// How PostgreSQL would run a statement, and how it did (FR-5.13).
//
// A plan is the server's own answer about its own behaviour, so nothing here
// can be settled against a fake. What is claimed is that the tree this reads
// is the tree the server described: the same steps, the same nesting, the
// same numbers.

func planOf(t *testing.T, src *pgSource, sql string, analyze bool) *source.Plan {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	p, err := src.Explain(ctx, source.Statement{SQL: sql}, analyze)
	if err != nil {
		t.Fatalf("Explain(%q, analyze=%v): %v", sql, analyze, err)
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

func operations(n *source.PlanNode) []string {
	var out []string
	walk(n, func(n *source.PlanNode) { out = append(out, n.Operation) })
	return out
}

func hasOp(ops []string, want string) bool {
	for _, o := range ops {
		if strings.Contains(o, want) {
			return true
		}
	}
	return false
}

// A plan names the steps, the tables and the estimates the server gave.
func TestLiveReadsAPlannedStatement(t *testing.T) {
	src := openSource(t, false)
	p := planOf(t, src, `SELECT status, count(*) FROM ikigai_it.orders GROUP BY status`, false)

	ops := operations(p.Root)
	if !hasOp(ops, "orders") {
		t.Errorf("the plan is %v; the table it reads is not named", ops)
	}
	if !hasOp(ops, "Aggregate") {
		t.Errorf("the plan is %v; a GROUP BY is aggregated somewhere", ops)
	}
	if p.Root.EstimatedCost <= 0 {
		t.Errorf("the root costs %v; a planned statement has a cost", p.Root.EstimatedCost)
	}
	if p.Root.EstimatedRows < 0 {
		t.Errorf("the root expects %d rows", p.Root.EstimatedRows)
	}
	// Nothing was run, so nothing was measured.
	walk(p.Root, func(n *source.PlanNode) {
		if n.ActualRows != -1 || n.ActualTime != 0 {
			t.Errorf("%s reports %d actual rows in %v, and the statement was never run",
				n.Operation, n.ActualRows, n.ActualTime)
		}
	})
	// The server's own words are kept, and are what was parsed.
	if !json.Valid([]byte(p.Text)) {
		t.Errorf("the raw plan is not the document it was asked for: %.80q", p.Text)
	}
}

// Analysing runs the statement and measures it, which is the difference
// between what was expected and what happened.
func TestLiveMeasuresAnAnalysedStatement(t *testing.T) {
	src := openSource(t, false)
	p := planOf(t, src, `SELECT count(*) FROM ikigai_it.orders WHERE status = 'paid'`, true)

	measured := false
	walk(p.Root, func(n *source.PlanNode) {
		if n.ActualRows >= 0 {
			measured = true
		}
	})
	if !measured {
		t.Error("nothing in an analysed plan was measured")
	}
	if p.Root.ActualTime <= 0 {
		t.Errorf("the whole statement took %v", p.Root.ActualTime)
	}
	if p.Root.ActualRows != 1 {
		t.Errorf("a count returned %d rows", p.Root.ActualRows)
	}
}

// A filter that throws rows away is the difference between an index that
// helped and one that did not, so the plan says how many it threw.
func TestLiveSaysWhatAFilterThrewAway(t *testing.T) {
	src := openSource(t, false)
	p := planOf(t, src, `SELECT * FROM ikigai_it.orders WHERE note IS NULL`, true)
	said := ""
	walk(p.Root, func(n *source.PlanNode) { said += n.Detail + " | " })
	if !strings.Contains(said, "Filter") {
		t.Errorf("the details are %q; the condition is not among them", said)
	}
	if !strings.Contains(said, "Rows removed by filter") {
		t.Errorf("the details are %q; what the filter threw away is not said", said)
	}
}

// A nested loop reports its inner side per loop. A plan that repeated that
// number would say a step returned a thousandth of what it did.
func TestLiveCountsEveryLoopOfANestedStep(t *testing.T) {
	src := openSource(t, false)
	p := planOf(t, src, `SELECT o.id FROM ikigai_it.orders o
	                     JOIN ikigai_it.nopk n ON n.a = o.id WHERE o.id < 50`, true)
	var deepest int64
	walk(p.Root, func(n *source.PlanNode) {
		if n.ActualRows > deepest {
			deepest = n.ActualRows
		}
	})
	if deepest <= 0 {
		t.Fatal("nothing was measured")
	}
	if p.Root.ActualRows < 0 {
		t.Errorf("the root returned %d rows", p.Root.ActualRows)
	}
}

// Asking the planner what it would do is a read whatever it is asked about.
// Running the statement to measure it is not, and that refusal is this
// program's own rather than something the server happened to catch.
func TestLiveReadOnlyPlansAWriteButWillNotRunIt(t *testing.T) {
	src := openSource(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sql := `UPDATE ikigai_it.writes SET n = n + 1`
	if _, err := src.Explain(ctx, source.Statement{SQL: sql}, false); err != nil {
		t.Errorf("planning a write on a read-only connection: %v", err)
	}
	_, err := src.Explain(ctx, source.Statement{SQL: sql}, true)
	if !errors.Is(err, source.ErrReadOnly) {
		t.Errorf("analysing a write on a read-only connection: %v, want it refused here", err)
	}
}

// A connection that reads plans says so, or the window would never offer to
// ask for one.
func TestLiveSaysItReadsPlans(t *testing.T) {
	caps := openSource(t, false).Capabilities()
	if !caps.Query.Explain || !caps.Query.ExplainAnalyze {
		t.Errorf("Explain is %v and ExplainAnalyze %v", caps.Query.Explain, caps.Query.ExplainAnalyze)
	}
}

// The tree is the tree the server described: the same shape, step for step.
func TestLiveTreeMatchesWhatTheServerSaid(t *testing.T) {
	src := openSource(t, false)
	p := planOf(t, src, `SELECT o.id, n.b FROM ikigai_it.orders o
	                     JOIN ikigai_it.nopk n ON n.a = o.id ORDER BY o.id`, false)

	var raw []struct {
		Plan map[string]any `json:"Plan"`
	}
	if err := json.Unmarshal([]byte(p.Text), &raw); err != nil {
		t.Fatalf("the raw plan: %v", err)
	}
	if len(raw) != 1 {
		t.Fatalf("%d plans in the document", len(raw))
	}
	if got, want := countNodes(p.Root), countRaw(raw[0].Plan); got != want {
		t.Errorf("the tree has %d steps and the server described %d", got, want)
	}
}

func countNodes(n *source.PlanNode) int {
	out := 1
	for _, c := range n.Children {
		out += countNodes(c)
	}
	return out
}

func countRaw(m map[string]any) int {
	out := 1
	kids, _ := m["Plans"].([]any)
	for _, k := range kids {
		if c, ok := k.(map[string]any); ok {
			out += countRaw(c)
		}
	}
	return out
}
