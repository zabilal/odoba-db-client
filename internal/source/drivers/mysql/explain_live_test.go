//go:build conformance

package mysql

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// How MySQL and MariaDB would run a statement (FR-5.13).
//
// Both servers, because they write the same plan in different words, and
// this reads both. A plan is the optimiser's own answer about its own
// behaviour, so nothing here can be settled against a fake.

func planOf(t *testing.T, src *mysqlSource, sql string, analyze bool) *source.Plan {
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

func walkPlan(n *source.PlanNode, f func(*source.PlanNode)) {
	f(n)
	for _, c := range n.Children {
		walkPlan(c, f)
	}
}

func planSteps(n *source.PlanNode) []string {
	var out []string
	walkPlan(n, func(n *source.PlanNode) { out = append(out, n.Operation) })
	return out
}

// A plan names the steps, the tables and the optimiser's estimates.
func TestLiveReadsAPlan(t *testing.T) {
	each(t, func(t *testing.T, srv server) {
		src := open(t, srv, source.Guard{})
		// Unaliased, because a plan names a table what the statement
		// called it, and an alias is what a statement called it.
		p := planOf(t, src, `SELECT people.id, orders.total FROM people
	                     JOIN orders ON orders.person_id = people.id WHERE people.name LIKE 'p%'`, false)

		said := strings.Join(planSteps(p.Root), " | ")
		for _, want := range []string{"people", "orders"} {
			if !strings.Contains(said, want) {
				t.Errorf("the plan is %q; %s is not named", said, want)
			}
		}
		costed, rowed := false, false
		walkPlan(p.Root, func(n *source.PlanNode) {
			if n.EstimatedCost >= 0 {
				costed = true
			}
			if n.EstimatedRows >= 0 {
				rowed = true
			}
			if n.ActualRows != -1 || n.ActualTime != 0 {
				t.Errorf("%s reports %d actual rows in %v, and the statement was never run",
					n.Operation, n.ActualRows, n.ActualTime)
			}
		})
		if !costed || !rowed {
			t.Errorf("the plan has no costs (%v) or no row estimates (%v)", costed, rowed)
		}
		if !json.Valid([]byte(p.Text)) {
			t.Errorf("the raw plan is not the document it was asked for: %.80q", p.Text)
		}
	})
}

// The condition a step applied is what a plan is read to find.
func TestLiveSaysWhatEachStepTested(t *testing.T) {
	each(t, func(t *testing.T, srv server) {
		src := open(t, srv, source.Guard{})
		p := planOf(t, src, `SELECT * FROM people WHERE name LIKE 'p%'`, false)
		said := ""
		walkPlan(p.Root, func(n *source.PlanNode) { said += n.Detail + " | " })
		if !strings.Contains(said, "Condition") {
			t.Errorf("the details are %q; the condition is not among them", said)
		}
	})
}

// MariaDB runs the statement and measures it into the same document. MySQL
// says so rather than answering something else.
func TestLiveMeasuresWhereTheServerCan(t *testing.T) {
	each(t, func(t *testing.T, srv server) {
		src := open(t, srv, source.Guard{})
		caps := src.Capabilities()
		if caps.Query.ExplainAnalyze != src.fl.mariadb {
			t.Errorf("ExplainAnalyze is %v on %s", caps.Query.ExplainAnalyze, src.fl.version)
		}
		if !src.fl.mariadb {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_, err := src.Explain(ctx, source.Statement{SQL: `SELECT 1`}, true)
			if err == nil {
				t.Error("MySQL answered an analysed plan")
			}
			return
		}
		p := planOf(t, src, `SELECT count(*) FROM people WHERE name LIKE 'p%'`, true)
		measured := false
		walkPlan(p.Root, func(n *source.PlanNode) {
			if n.ActualRows >= 0 {
				measured = true
			}
		})
		if !measured {
			t.Errorf("nothing in an analysed plan was measured: %q", strings.Join(planSteps(p.Root), " | "))
		}
	})
}

// Running a statement to measure it is a write where the statement is one,
// and a read-only connection refuses it here rather than at the server.
//
// Asking the optimiser what it would do is a read, and this does not stand
// in its way — but these servers do: they refuse EXPLAIN of a write inside a
// read-only transaction, which is their rule and not this program's. What is
// claimed here is only what this program decides.
func TestLiveReadOnlyWillNotRunAWriteToMeasureIt(t *testing.T) {
	each(t, func(t *testing.T, srv server) {
		src := open(t, srv, source.Guard{ReadOnly: true})
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		_, err := src.Explain(ctx, source.Statement{SQL: `UPDATE writes SET n = n + 1`}, true)
		if !errors.Is(err, source.ErrReadOnly) {
			t.Errorf("analysing a write on a read-only connection: %v, want it refused here", err)
		}
		// A plan of a statement that only reads is had as usual.
		if _, err := src.Explain(ctx, source.Statement{SQL: `SELECT * FROM people`}, false); err != nil {
			t.Errorf("planning a read on a read-only connection: %v", err)
		}
	})
}

// A statement over no tables still has a plan, and the server's message is
// what it says.
func TestLivePlansAStatementOverNoTables(t *testing.T) {
	each(t, func(t *testing.T, srv server) {
		src := open(t, srv, source.Guard{})
		p := planOf(t, src, `SELECT 1`, false)
		if p.Root.Operation == "" {
			t.Error("the plan says nothing at all")
		}
	})
}

// The steps come out in the same order every time. A map's keys do not, and
// a plan that rearranged itself between two readings would be unreadable.
func TestLiveAPlanReadsTheSameEveryTime(t *testing.T) {
	each(t, func(t *testing.T, srv server) {
		src := open(t, srv, source.Guard{})
		sql := `SELECT p.id, o.total FROM people p JOIN orders o ON o.person_id = p.id ORDER BY o.total`
		first := strings.Join(planSteps(planOf(t, src, sql, false).Root), " | ")
		for i := 0; i < 5; i++ {
			if got := strings.Join(planSteps(planOf(t, src, sql, false).Root), " | "); got != first {
				t.Fatalf("reading %d gave %q, the first gave %q", i, got, first)
			}
		}
	})
}
