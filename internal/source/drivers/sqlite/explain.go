package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// How SQLite would run a statement (FR-5.13).
//
// SQLite answers EXPLAIN QUERY PLAN with rows rather than with a document:
// an id, the id of the step it sits under, and a line of English. The tree is
// those rows arranged by their parents, and the English is what the step is
// called — there is nothing else to call it.
//
// There are no numbers at all. SQLite's planner does not publish its
// estimates and has no form that runs the statement and measures it, so
// every figure is left as "not reported" rather than invented, and the
// capability says so, so the window never offers to analyse here.

var _ source.Explainer = (*sqliteSource)(nil)

func (s *sqliteSource) Explain(ctx context.Context, stmt source.Statement, analyze bool) (*source.Plan, error) {
	if analyze {
		return nil, fmt.Errorf("sqlite: a plan here is what the planner would do; " +
			"SQLite has no form that runs the statement and measures it")
	}
	// Asking the planner does not run the statement, whatever it is about.
	if err := s.cfg.Guard.Allow(source.AccessRead, stmt.Confirmed); err != nil {
		return nil, err
	}
	args := append([]any(nil), stmt.Args...)
	for k, v := range stmt.Named {
		args = append(args, sql.Named(k, v))
	}
	rows, err := s.db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+stmt.SQL, args...)
	if err != nil {
		return nil, statementError(err)
	}
	defer rows.Close()

	steps, err := readSteps(rows)
	if err != nil {
		return nil, err
	}
	return &source.Plan{Root: planTree(steps), Text: planText(steps)}, nil
}

// step is one row of EXPLAIN QUERY PLAN.
type step struct {
	id, parent int64
	detail     string
}

func readSteps(rows *sql.Rows) ([]step, error) {
	var out []step
	for rows.Next() {
		var s step
		var unused any
		if err := rows.Scan(&s.id, &s.parent, &unused, &s.detail); err != nil {
			return nil, fmt.Errorf("sqlite: could not read the plan: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, statementError(err)
	}
	return out, nil
}

// planTree arranges the steps under their parents.
//
// A statement can have several steps at the top — a compound SELECT has one
// per arm — and the tree has one root, so where there is more than one they
// hang under a step named after the statement itself.
func planTree(steps []step) *source.PlanNode {
	at := map[int64]*source.PlanNode{}
	var roots []*source.PlanNode
	// By id, so that a step is always built before anything that sits under
	// it: SQLite numbers them in that order, and sorting says so rather than
	// relying on it.
	sort.SliceStable(steps, func(i, j int) bool { return steps[i].id < steps[j].id })
	for _, s := range steps {
		n := &source.PlanNode{
			Operation:     operationOf(s.detail),
			Detail:        s.detail,
			EstimatedCost: -1,
			EstimatedRows: -1,
			ActualRows:    -1,
		}
		at[s.id] = n
		if p, ok := at[s.parent]; ok && s.parent != s.id {
			p.Children = append(p.Children, n)
			continue
		}
		roots = append(roots, n)
	}
	switch len(roots) {
	case 0:
		// A statement SQLite plans in no steps at all, such as one that
		// reads nothing. Saying so is the plan.
		return &source.PlanNode{Operation: "No steps", EstimatedCost: -1, EstimatedRows: -1, ActualRows: -1}
	case 1:
		return roots[0]
	}
	return &source.PlanNode{Operation: "Statement", EstimatedCost: -1, EstimatedRows: -1,
		ActualRows: -1, Children: roots}
}

// operationOf is the short name of a step, which is the first clause of what
// SQLite said about it: "SCAN orders", "SEARCH nopk USING INDEX …".
func operationOf(detail string) string {
	if i := strings.Index(detail, " USING "); i > 0 {
		return detail[:i]
	}
	if i := strings.Index(detail, " ("); i > 0 {
		return detail[:i]
	}
	return detail
}

// planText is what SQLite said, laid out as its shell lays it out, so that
// the server's own words are kept whole.
func planText(steps []step) string {
	var b strings.Builder
	for _, s := range steps {
		fmt.Fprintf(&b, "%d|%d|0|%s\n", s.id, s.parent, s.detail)
	}
	return b.String()
}
