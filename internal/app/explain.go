package app

import (
	"context"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Reading a query plan (FR-5.13).
//
// A plan arrives as a tree of steps, each carrying what the server said about
// it. What the window needs is a list of lines to draw and, for each, how
// much of the work it is responsible for — which is the whole reason a plan
// is read at all: to find the step to change.

// CanExplain reports whether a connection will say how it would run a
// statement.
func CanExplain(src source.Source) bool {
	if _, ok := src.(source.Explainer); !ok {
		return false
	}
	return src.Capabilities().Query.Explain
}

// CanAnalyse reports whether it will run a statement and measure it.
//
// Separate from CanExplain because SQLite has no such form and MySQL reports
// one only as text: a window that offered it everywhere would offer a button
// that could only fail.
func CanAnalyse(src source.Source) bool {
	return CanExplain(src) && src.Capabilities().Query.ExplainAnalyze
}

// Explain asks for a plan.
func Explain(ctx context.Context, src source.Source, stmt source.Statement, analyze bool) (*source.Plan, error) {
	e, ok := src.(source.Explainer)
	if !ok {
		return nil, ErrNoPlans
	}
	return e.Explain(ctx, stmt, analyze)
}

// ErrNoPlans is a connection that will not say how it runs anything.
var ErrNoPlans = errNoPlans{}

type errNoPlans struct{}

func (errNoPlans) Error() string { return "this connection cannot show a query plan" }

// PlanRow is one step of a plan, as a line to draw.
type PlanRow struct {
	Node  *source.PlanNode
	Depth int

	// Share is how much of the plan's own work this step accounts for, from
	// nought to one. It is what the heat is drawn from.
	Share float64

	// Self is the work this step did on its own, in whatever the plan is
	// measured in: time where it was measured, cost where it was not.
	Self float64
}

// Measured says what a plan's figures are.
type Measured uint8

const (
	// ByCost is a plan the server only estimated.
	ByCost Measured = iota
	// ByTime is a plan the server ran and measured.
	ByTime
	// ByNothing is a plan with no figures at all, as SQLite gives.
	ByNothing
)

// PlanReading is a plan laid out for the window.
type PlanReading struct {
	Rows []PlanRow

	// By says what the shares were worked out from, so that the window can
	// say what its heat means rather than leaving it to be guessed.
	By Measured

	// Total is the whole plan's cost, and Duration the whole plan's time
	// where it was measured.
	Total    float64
	Duration time.Duration

	// Hottest is the index in Rows of the step with the largest share, or
	// -1 where nothing was measured. It is what the window points at first.
	Hottest int
}

// ReadPlan flattens a plan into the lines to draw, with each step's share of
// the work.
//
// A step's own work is what it reports less what its children report,
// because a parent's figure includes everything under it. Never less than
// nothing: a parallel plan can report children that together took longer
// than their parent, which is real and not a step that did negative work.
func ReadPlan(p *source.Plan) PlanReading {
	out := PlanReading{By: ByNothing, Hottest: -1}
	if p == nil || p.Root == nil {
		return out
	}
	out.By = measuredBy(p.Root)
	flatten(p.Root, 0, &out)

	total := 0.0
	for _, r := range out.Rows {
		total += r.Self
	}
	for i := range out.Rows {
		if total > 0 {
			out.Rows[i].Share = out.Rows[i].Self / total
		}
		if out.Hottest < 0 || out.Rows[i].Self > out.Rows[out.Hottest].Self {
			out.Hottest = i
		}
	}
	if out.By == ByNothing || total == 0 {
		// Nothing was measured, so nothing is hotter than anything else.
		out.Hottest = -1
	}
	if p.Root.EstimatedCost >= 0 {
		out.Total = p.Root.EstimatedCost
	}
	out.Duration = p.Root.ActualTime
	return out
}

// measuredBy is what a plan's figures are: what it took, what it would cost,
// or nothing at all.
func measuredBy(n *source.PlanNode) Measured {
	if anyNode(n, func(n *source.PlanNode) bool { return n.ActualTime > 0 }) {
		return ByTime
	}
	if anyNode(n, func(n *source.PlanNode) bool { return n.EstimatedCost > 0 }) {
		return ByCost
	}
	return ByNothing
}

func anyNode(n *source.PlanNode, f func(*source.PlanNode) bool) bool {
	if f(n) {
		return true
	}
	for _, c := range n.Children {
		if anyNode(c, f) {
			return true
		}
	}
	return false
}

// flatten walks the tree in the order it is read: a step, then what it is
// made of.
func flatten(n *source.PlanNode, depth int, out *PlanReading) {
	out.Rows = append(out.Rows, PlanRow{Node: n, Depth: depth, Self: selfWork(n, out.By)})
	for _, c := range n.Children {
		flatten(c, depth+1, out)
	}
}

// selfWork is a step's own share of the work.
func selfWork(n *source.PlanNode, by Measured) float64 {
	own, kids := 0.0, 0.0
	switch by {
	case ByTime:
		own = float64(n.ActualTime)
		for _, c := range n.Children {
			kids += float64(c.ActualTime)
		}
	case ByCost:
		if n.EstimatedCost > 0 {
			own = n.EstimatedCost
		}
		for _, c := range n.Children {
			if c.EstimatedCost > 0 {
				kids += c.EstimatedCost
			}
		}
	default:
		return 0
	}
	return max(own-kids, 0)
}

// PlanSteps is how many steps a plan has, which is the first thing said
// about one.
func PlanSteps(p *source.Plan) int {
	if p == nil || p.Root == nil {
		return 0
	}
	n := 0
	walkPlan(p.Root, func(*source.PlanNode) { n++ })
	return n
}

// walkPlan visits every step.
func walkPlan(n *source.PlanNode, f func(*source.PlanNode)) {
	f(n)
	for _, c := range n.Children {
		walkPlan(c, f)
	}
}

// PlanMisestimated reports the steps whose row estimate was furthest from
// what happened, which is where a plan goes wrong.
//
// Only for a measured plan: an estimate can only be wrong against something
// that happened. The factor is how many times out it was, in whichever
// direction, and a step is only worth naming past MisestimateFactor.
const MisestimateFactor = 10.0

// Misestimated is a step whose estimate was far from what happened.
type Misestimated struct {
	Node   *source.PlanNode
	Factor float64
}

// PlanMisestimates is every step that was out by more than MisestimateFactor,
// worst first.
func PlanMisestimates(p *source.Plan) []Misestimated {
	if p == nil || p.Root == nil {
		return nil
	}
	var out []Misestimated
	walkPlan(p.Root, func(n *source.PlanNode) {
		if n.ActualRows < 0 || n.EstimatedRows < 0 {
			return
		}
		// A row either way, so that a step expecting one and returning none
		// is not infinitely wrong.
		est, act := float64(n.EstimatedRows)+1, float64(n.ActualRows)+1
		factor := max(est/act, act/est)
		if factor >= MisestimateFactor {
			out = append(out, Misestimated{Node: n, Factor: factor})
		}
	})
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Factor > out[j-1].Factor; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
