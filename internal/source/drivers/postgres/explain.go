package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// How PostgreSQL would run a statement, and how it did (FR-5.13).
//
// Asked for once, in JSON. PostgreSQL will render a plan as text, as JSON, as
// XML or as YAML, and asking twice to have both would mean running an
// analysed statement twice — which for anything that writes is not a second
// reading of the same thing, and for anything slow is twice the wait. So the
// tree is read from the JSON and the JSON is what is kept as the server's own
// words.
//
// EXPLAIN without ANALYZE does not run the statement: it asks the planner
// what it would do. EXPLAIN ANALYZE does run it, so it is guarded exactly as
// running it would be — a read-only connection refuses to analyse an INSERT,
// and a production connection asks first (NFR-S4, FR-4.9).

var _ source.Explainer = (*pgSource)(nil)

func (s *pgSource) Explain(ctx context.Context, stmt source.Statement, analyze bool) (*source.Plan, error) {
	ss, err := s.openSession(ctx)
	if err != nil {
		return nil, err
	}
	defer ss.Close()
	return ss.Explain(ctx, stmt, analyze)
}

func (ss *pgSession) Explain(ctx context.Context, stmt source.Statement, analyze bool) (*source.Plan, error) {
	access := source.AccessRead
	if analyze {
		// Analysing runs the statement, so what it is decides what it needs
		// permission for. Asking the planner does not, whatever it is asked
		// about.
		access = ss.src.Classify(stmt.SQL)
	}
	// An unbounded SELECT is not a concern here: a plan is one row whatever
	// the statement would return, and an analysed one is bounded by the
	// statement itself rather than by what is fetched from it.
	if err := ss.src.cfg.Guard.Allow(access, stmt.Confirmed); err != nil {
		return nil, err
	}

	sql, args, err := bindNamed(stmt)
	if err != nil {
		return nil, err
	}
	raw, err := ss.explainJSON(ctx, explainPrefix(analyze)+sql, args)
	if err != nil {
		return nil, err
	}
	root, err := parsePlan(raw)
	if err != nil {
		return nil, err
	}
	return &source.Plan{Root: root, Text: raw}, nil
}

// explainPrefix is what to ask for.
//
// BUFFERS says how much came from the cache and how much from the disk,
// which is most of the answer to "why was that slow"; it is only meaningful
// alongside ANALYZE. SETTINGS names any planner setting that is not at its
// default, which is the other half of that answer and costs nothing.
func explainPrefix(analyze bool) string {
	if analyze {
		return "EXPLAIN (FORMAT JSON, ANALYZE, BUFFERS, TIMING, SETTINGS) "
	}
	return "EXPLAIN (FORMAT JSON, SETTINGS) "
}

// explainJSON runs the EXPLAIN and returns the one JSON document it answers.
func (ss *pgSession) explainJSON(ctx context.Context, sql string, args []any) (string, error) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if ss.closed {
		return "", errClosed
	}
	var out string
	// An analysed statement writes where the statement writes, so a
	// read-only connection runs this inside its READ ONLY transaction like
	// everything else: the lexer is not the only thing standing in the way.
	if ss.src.cfg.Guard.ReadOnly {
		tx, err := ss.conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
		if err != nil {
			return "", err
		}
		defer endTx(tx)
		err = tx.QueryRow(ctx, sql, args...).Scan(&out)
		return out, statementError(err)
	}
	err := ss.conn.QueryRow(ctx, sql, args...).Scan(&out)
	return out, statementError(err)
}

// pgPlan is the shape PostgreSQL's JSON plan arrives in.
//
// Only the fields that are read are named. PostgreSQL adds more with every
// release and the rest are left in the raw text, which is kept whole.
type pgPlan struct {
	Plan          *pgNode  `json:"Plan"`
	PlanningTime  *float64 `json:"Planning Time"`
	ExecutionTime *float64 `json:"Execution Time"`
}

type pgNode struct {
	NodeType      string    `json:"Node Type"`
	Strategy      string    `json:"Strategy"`
	JoinType      string    `json:"Join Type"`
	Relation      string    `json:"Relation Name"`
	Alias         string    `json:"Alias"`
	IndexName     string    `json:"Index Name"`
	ParentRel     string    `json:"Parent Relationship"`
	TotalCost     *float64  `json:"Total Cost"`
	PlanRows      *float64  `json:"Plan Rows"`
	ActualRows    *float64  `json:"Actual Rows"`
	ActualTotal   *float64  `json:"Actual Total Time"`
	ActualLoops   *float64  `json:"Actual Loops"`
	Filter        string    `json:"Filter"`
	IndexCond     string    `json:"Index Cond"`
	RecheckCond   string    `json:"Recheck Cond"`
	HashCond      string    `json:"Hash Cond"`
	MergeCond     string    `json:"Merge Cond"`
	JoinFilter    string    `json:"Join Filter"`
	SortKey       []string  `json:"Sort Key"`
	GroupKey      []string  `json:"Group Key"`
	RowsRemoved   *float64  `json:"Rows Removed by Filter"`
	HeapFetches   *float64  `json:"Heap Fetches"`
	SharedRead    *float64  `json:"Shared Read Blocks"`
	SharedHit     *float64  `json:"Shared Hit Blocks"`
	SubplanName   string    `json:"Subplan Name"`
	ParallelAware bool      `json:"Parallel Aware"`
	Plans         []*pgNode `json:"Plans"`
}

// parsePlan reads the document into the tree the window draws.
func parsePlan(raw string) (*source.PlanNode, error) {
	var plans []pgPlan
	if err := json.Unmarshal([]byte(raw), &plans); err != nil {
		return nil, fmt.Errorf("postgres: could not read the plan: %w", err)
	}
	if len(plans) == 0 || plans[0].Plan == nil {
		return nil, fmt.Errorf("postgres: the server returned no plan")
	}
	return planNode(plans[0].Plan), nil
}

// planNode turns one step into one node.
func planNode(n *pgNode) *source.PlanNode {
	out := &source.PlanNode{
		Operation:     operationOf(n),
		Detail:        strings.Join(detailsOf(n), "; "),
		EstimatedCost: -1,
		EstimatedRows: -1,
		ActualRows:    -1,
	}
	if n.TotalCost != nil {
		out.EstimatedCost = *n.TotalCost
	}
	if n.PlanRows != nil {
		out.EstimatedRows = int64(*n.PlanRows)
	}
	if n.ActualRows != nil {
		// Per loop, as PostgreSQL reports it. A node inside a nested loop
		// run a thousand times returned a thousand times what it says.
		loops := 1.0
		if n.ActualLoops != nil && *n.ActualLoops > 0 {
			loops = *n.ActualLoops
		}
		out.ActualRows = int64(*n.ActualRows * loops)
	}
	if n.ActualTotal != nil {
		loops := 1.0
		if n.ActualLoops != nil && *n.ActualLoops > 0 {
			loops = *n.ActualLoops
		}
		out.ActualTime = time.Duration(*n.ActualTotal * loops * float64(time.Millisecond))
	}
	for _, c := range n.Plans {
		out.Children = append(out.Children, planNode(c))
	}
	return out
}

// operationOf names a step the way the text plan does.
func operationOf(n *pgNode) string {
	name := n.NodeType
	switch {
	case n.Strategy != "" && n.Strategy != "Plain":
		name = n.Strategy + " " + name
	case n.JoinType != "" && n.JoinType != "Inner":
		name = n.JoinType + " " + name
	}
	if n.ParallelAware {
		name = "Parallel " + name
	}
	switch {
	case n.IndexName != "" && n.Relation != "":
		name += " using " + n.IndexName + " on " + relationOf(n)
	case n.IndexName != "":
		name += " using " + n.IndexName
	case n.Relation != "":
		name += " on " + relationOf(n)
	}
	if n.SubplanName != "" {
		name = n.SubplanName + ": " + name
	}
	return name
}

// relationOf names the table, with its alias where the query gave it one.
func relationOf(n *pgNode) string {
	if n.Alias != "" && n.Alias != n.Relation {
		return n.Relation + " " + n.Alias
	}
	return n.Relation
}

// detailsOf is what the step did, in the server's own words.
func detailsOf(n *pgNode) []string {
	var out []string
	add := func(label, v string) {
		if v != "" {
			out = append(out, label+": "+v)
		}
	}
	add("Index Cond", n.IndexCond)
	add("Recheck Cond", n.RecheckCond)
	add("Hash Cond", n.HashCond)
	add("Merge Cond", n.MergeCond)
	add("Join Filter", n.JoinFilter)
	add("Filter", n.Filter)
	add("Sort Key", strings.Join(n.SortKey, ", "))
	add("Group Key", strings.Join(n.GroupKey, ", "))
	if n.RowsRemoved != nil && *n.RowsRemoved > 0 {
		// How many rows a filter threw away is the difference between an
		// index that helped and one that did not.
		out = append(out, fmt.Sprintf("Rows removed by filter: %.0f", *n.RowsRemoved))
	}
	if n.HeapFetches != nil && *n.HeapFetches > 0 {
		out = append(out, fmt.Sprintf("Heap fetches: %.0f", *n.HeapFetches))
	}
	if n.SharedRead != nil && *n.SharedRead > 0 {
		out = append(out, fmt.Sprintf("Read from disk: %.0f blocks", *n.SharedRead))
	}
	return out
}
