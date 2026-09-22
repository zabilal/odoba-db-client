package app

import (
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Reading a query plan (FR-5.13).

// node builds a step. -1 is what "not reported" looks like in a plan.
func node(op string, cost float64, est, act int64, took time.Duration, kids ...*source.PlanNode) *source.PlanNode {
	return &source.PlanNode{Operation: op, EstimatedCost: cost, EstimatedRows: est,
		ActualRows: act, ActualTime: took, Children: kids}
}

func planOf(root *source.PlanNode) *source.Plan { return &source.Plan{Root: root} }

// A plan is read from the top down, as it is written.
func TestAPlanIsFlattenedInTheOrderItIsRead(t *testing.T) {
	p := planOf(node("Aggregate", 100, 1, -1, 0,
		node("Hash Join", 90, 50, -1, 0,
			node("Seq Scan on a", 30, 500, -1, 0),
			node("Seq Scan on b", 20, 200, -1, 0)),
		node("Sort", 5, 1, -1, 0)))
	r := ReadPlan(p)
	var got []string
	for _, row := range r.Rows {
		got = append(got, row.Node.Operation)
	}
	want := []string{"Aggregate", "Hash Join", "Seq Scan on a", "Seq Scan on b", "Sort"}
	if len(got) != len(want) {
		t.Fatalf("the plan reads %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("step %d is %q, want %q", i, got[i], want[i])
		}
	}
	depths := []int{0, 1, 2, 2, 1}
	for i, d := range depths {
		if r.Rows[i].Depth != d {
			t.Errorf("%s sits at depth %d, want %d", r.Rows[i].Node.Operation, r.Rows[i].Depth, d)
		}
	}
	if PlanSteps(p) != 5 {
		t.Errorf("%d steps, want 5", PlanSteps(p))
	}
}

// A step's own work is what it reports less what its children report,
// because a parent's figure includes everything under it. Otherwise the root
// is always the hottest step and a plan says nothing.
func TestAStepsOwnWorkIsWhatItsChildrenDidNotDo(t *testing.T) {
	p := planOf(node("Aggregate", 100, 1, -1, 0,
		node("Seq Scan on items", 90, 500, -1, 0)))
	r := ReadPlan(p)
	if r.By != ByCost {
		t.Fatalf("the plan is measured by %v, want by cost", r.By)
	}
	if r.Rows[0].Self != 10 || r.Rows[1].Self != 90 {
		t.Errorf("own work is %v and %v, want 10 and 90", r.Rows[0].Self, r.Rows[1].Self)
	}
	if r.Rows[0].Share != 0.1 || r.Rows[1].Share != 0.9 {
		t.Errorf("shares are %v and %v, want a tenth and nine tenths", r.Rows[0].Share, r.Rows[1].Share)
	}
	if r.Hottest != 1 {
		t.Errorf("the hottest step is %d, want the scan", r.Hottest)
	}
	if r.Total != 100 {
		t.Errorf("the whole plan costs %v, want 100", r.Total)
	}
}

// Where a statement was run and measured, time is what the shares are of:
// what a plan cost is a guess and what it took is not.
func TestAMeasuredPlanIsSharedOutByTime(t *testing.T) {
	p := planOf(node("Aggregate", 100, 1, 1, 100*time.Millisecond,
		node("Seq Scan on items", 90, 500, 9000, 90*time.Millisecond)))
	r := ReadPlan(p)
	if r.By != ByTime {
		t.Fatalf("the plan is measured by %v, want by time", r.By)
	}
	if r.Rows[1].Share != 0.9 {
		t.Errorf("the scan's share is %v, want nine tenths of the time", r.Rows[1].Share)
	}
	if r.Duration != 100*time.Millisecond {
		t.Errorf("the plan took %v", r.Duration)
	}
}

// A parallel plan can report children that together took longer than their
// parent. That is real, and it is not a step that did negative work.
func TestNoStepDoesLessThanNothing(t *testing.T) {
	p := planOf(node("Gather", 100, 1, 1, 50*time.Millisecond,
		node("Parallel Seq Scan", 90, 500, 9000, 40*time.Millisecond),
		node("Parallel Seq Scan", 90, 500, 9000, 40*time.Millisecond)))
	r := ReadPlan(p)
	for _, row := range r.Rows {
		if row.Self < 0 || row.Share < 0 {
			t.Errorf("%s did %v of the work", row.Node.Operation, row.Self)
		}
	}
}

// A plan with no figures at all — as SQLite gives — has no heat, and says so
// rather than sharing nothing out into equal parts.
func TestAPlanWithNoFiguresHasNoHeat(t *testing.T) {
	p := planOf(&source.PlanNode{Operation: "SCAN items", EstimatedCost: -1, EstimatedRows: -1, ActualRows: -1,
		Children: []*source.PlanNode{{Operation: "USING INDEX", EstimatedCost: -1, EstimatedRows: -1, ActualRows: -1}}})
	r := ReadPlan(p)
	if r.By != ByNothing {
		t.Errorf("the plan is measured by %v, want by nothing", r.By)
	}
	if r.Hottest != -1 {
		t.Errorf("step %d is hottest in a plan with no figures", r.Hottest)
	}
	for _, row := range r.Rows {
		if row.Share != 0 {
			t.Errorf("%s has a share of %v", row.Node.Operation, row.Share)
		}
	}
}

// A plan of nothing is not a failure.
func TestAPlanOfNothing(t *testing.T) {
	for _, p := range []*source.Plan{nil, {}} {
		r := ReadPlan(p)
		if len(r.Rows) != 0 || r.Hottest != -1 {
			t.Errorf("a plan of nothing read as %+v", r)
		}
		if PlanSteps(p) != 0 {
			t.Errorf("%d steps in a plan of nothing", PlanSteps(p))
		}
	}
}

// A plan goes wrong where the estimate was wrong, so the steps furthest out
// are named, worst first.
func TestTheWorstEstimatesAreNamedWorstFirst(t *testing.T) {
	p := planOf(node("Aggregate", 100, 1, 1, time.Millisecond,
		node("Seq Scan on a", 90, 10, 9000, time.Millisecond),
		node("Seq Scan on b", 90, 100, 5000, time.Millisecond),
		node("Seq Scan on c", 90, 500, 500, time.Millisecond)))
	wrong := PlanMisestimates(p)
	if len(wrong) != 2 {
		t.Fatalf("%d steps were named: %+v", len(wrong), wrong)
	}
	if wrong[0].Node.Operation != "Seq Scan on a" {
		t.Errorf("the worst is %q, want the one that was furthest out", wrong[0].Node.Operation)
	}
	if wrong[0].Factor < wrong[1].Factor {
		t.Errorf("factors %v then %v; the worst comes first", wrong[0].Factor, wrong[1].Factor)
	}
}

// An estimate is only wrong against something that happened, so a plan that
// was never run has none.
func TestAnUnmeasuredPlanHasNoWrongEstimates(t *testing.T) {
	p := planOf(node("Seq Scan on a", 90, 10, -1, 0))
	if got := PlanMisestimates(p); len(got) != 0 {
		t.Errorf("%d steps named in a plan that was never run", len(got))
	}
	if got := PlanMisestimates(nil); got != nil {
		t.Errorf("a plan of nothing named %+v", got)
	}
}

// A step that expected no rows is not infinitely wrong about the few it
// returned: a row either way, so that nothing divides by nothing.
func TestAnEstimateOfNothingIsNotInfinitelyWrong(t *testing.T) {
	p := planOf(node("Seq Scan on a", 90, 0, 0, time.Millisecond))
	if got := PlanMisestimates(p); len(got) != 0 {
		t.Errorf("a step that expected none and returned none was named: %+v", got)
	}
	// Expected none and returned five: out by six, which is not far enough
	// to name, and not infinitely far.
	p = planOf(node("Seq Scan on a", 90, 0, 5, time.Millisecond))
	if got := PlanMisestimates(p); len(got) != 0 {
		t.Errorf("a step that expected none and returned five was named: %+v", got)
	}
	// And far enough is still named.
	p = planOf(node("Seq Scan on a", 90, 0, 500, time.Millisecond))
	if got := PlanMisestimates(p); len(got) != 1 {
		t.Errorf("a step that expected none and returned five hundred was not named: %+v", got)
	}
}
