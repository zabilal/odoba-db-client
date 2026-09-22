package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// How MySQL and MariaDB would run a statement, and how MariaDB did (FR-5.13).
//
// Both answer EXPLAIN FORMAT=JSON with a document of nested objects rather
// than with a tree of uniform nodes: a query block holds an ordering
// operation, which holds a nested loop, which holds tables. The two servers
// name the same things differently — MySQL puts its costs inside a cost_info
// object and counts rows_examined_per_scan; MariaDB puts a cost and rows
// beside each other — so this reads what it recognises from either and keeps
// the document whole for what it does not.
//
// Only MariaDB will run a statement and measure it into the same document,
// with ANALYZE FORMAT=JSON. MySQL's EXPLAIN ANALYZE answers text in a shape
// of its own, which is a plan somebody can read but not one this can build a
// tree from, so the capability does not claim it there rather than offering
// a button that would answer something else.

var _ source.Explainer = (*mysqlSource)(nil)

func (s *mysqlSource) Explain(ctx context.Context, stmt source.Statement, analyze bool) (*source.Plan, error) {
	access := source.AccessRead
	if analyze {
		// Analysing runs the statement, so what it is decides what it needs
		// permission for. Asking the optimiser does not.
		access = s.Classify(stmt.SQL)
	}
	// Before anything about what this server can report: whether a statement
	// may run at all is not conditional on how its plan would be written.
	if err := s.cfg.Guard.Allow(access, stmt.Confirmed); err != nil {
		return nil, err
	}
	if analyze && !s.fl.mariadb {
		return nil, fmt.Errorf("mysql: this server measures a statement only as text, " +
			"which is a plan to read rather than one to draw")
	}

	args := append([]any(nil), stmt.Args...)
	for k, v := range stmt.Named {
		args = append(args, sql.Named(k, v))
	}
	raw, err := s.explainJSON(ctx, explainPrefix(analyze)+stmt.SQL, args)
	if err != nil {
		return nil, err
	}
	root, err := parsePlan(raw)
	if err != nil {
		return nil, err
	}
	return &source.Plan{Root: root, Text: raw}, nil
}

// explainPrefix is what to ask for. MariaDB's ANALYZE runs the statement and
// writes what happened into the same document beside what was expected.
func explainPrefix(analyze bool) string {
	if analyze {
		return "ANALYZE FORMAT=JSON "
	}
	return "EXPLAIN FORMAT=JSON "
}

func (s *mysqlSource) explainJSON(ctx context.Context, q string, args []any) (string, error) {
	var out string
	if err := s.db.QueryRowContext(ctx, q, args...).Scan(&out); err != nil {
		return "", statementError(err, ctx)
	}
	return out, nil
}

// parsePlan reads the document into the tree the window draws.
func parsePlan(raw string) (*source.PlanNode, error) {
	var doc map[string]any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return nil, fmt.Errorf("mysql: could not read the plan: %w", err)
	}
	nodes := stepsIn(doc)
	switch len(nodes) {
	case 0:
		return nil, fmt.Errorf("mysql: the server returned no plan")
	case 1:
		return nodes[0], nil
	}
	return &source.PlanNode{Operation: "Statement", EstimatedCost: -1, EstimatedRows: -1,
		ActualRows: -1, Children: nodes}, nil
}

// operations are the keys that name a step rather than describe one.
//
// A key the server adds in a later release is not a step until it is listed
// here, and its contents are still reached through whatever does name one,
// so a plan never invents a step out of a field.
var operations = map[string]string{
	"query_block":                  "Query block",
	"ordering_operation":           "Order",
	"grouping_operation":           "Group",
	"duplicates_removal":           "Remove duplicates",
	"materialized_from_subquery":   "Materialize",
	"materialised_from_subquery":   "Materialize",
	"union_result":                 "Union",
	"read_sorted_file":             "Read sorted file",
	"filesort":                     "Sort",
	"buffer":                       "Buffer",
	"block-nl-join":                "Block nested loop",
	"temporary_table":              "Temporary table",
	"window_functions_computation": "Window functions",
	"insert":                       "Insert",
	"update":                       "Update",
	"delete":                       "Delete",
}

// containers hold steps without being one: a nested loop is the order its
// tables are joined in, not a thing the server does.
var containers = map[string]bool{
	"nested_loop":          true,
	"subqueries":           true,
	"attached_subqueries":  true,
	"query_specifications": true,
	"having_subqueries":    true,
	"expression_cache":     true,
}

// stepsIn is every step an object holds, in the order its keys read.
func stepsIn(m map[string]any) []*source.PlanNode {
	var out []*source.PlanNode
	for _, k := range keysOf(m) {
		v := m[k]
		switch {
		case k == "table":
			if t, ok := v.(map[string]any); ok {
				out = append(out, tableNode(t))
			}
		case operations[k] != "":
			if o, ok := v.(map[string]any); ok {
				n := &source.PlanNode{Operation: operations[k], EstimatedCost: -1,
					EstimatedRows: -1, ActualRows: -1}
				readFigures(n, o)
				n.Detail = strings.Join(detailsOf(o), "; ")
				n.Children = stepsIn(o)
				out = append(out, n)
			}
		case containers[k]:
			out = append(out, stepsUnder(v)...)
		}
	}
	return out
}

// stepsUnder is every step inside a container, which is a list or an object.
func stepsUnder(v any) []*source.PlanNode {
	switch x := v.(type) {
	case []any:
		var out []*source.PlanNode
		for _, e := range x {
			if m, ok := e.(map[string]any); ok {
				out = append(out, stepsIn(m)...)
			}
		}
		return out
	case map[string]any:
		return stepsIn(x)
	}
	return nil
}

// keysOf is an object's keys in a settled order.
//
// Go hands a map's keys back in no order at all, and a plan whose steps came
// out differently each time it was asked for would be unreadable. JSON does
// not keep an order either, so the only order available is a stated one.
func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// tableNode is one table, named the way a plan is read: what was done to it,
// and what it was.
func tableNode(t map[string]any) *source.PlanNode {
	n := &source.PlanNode{EstimatedCost: -1, EstimatedRows: -1, ActualRows: -1}
	name := text(t["table_name"])
	access := text(t["access_type"])
	switch {
	case access != "" && name != "":
		n.Operation = accessName(access) + " on " + name
	case name != "":
		n.Operation = "Read " + name
	default:
		n.Operation = "Table"
	}
	if key := text(t["key"]); key != "" {
		n.Operation += " using " + key
	}
	readFigures(n, t)
	n.Detail = strings.Join(detailsOf(t), "; ")
	n.Children = stepsIn(t)
	return n
}

// accessName says what an access type does, in words.
//
// The server's own abbreviations are what somebody used to reading plans
// looks for, so they are kept and explained rather than replaced.
func accessName(access string) string {
	switch access {
	case "ALL":
		return "Full scan"
	case "index":
		return "Index scan"
	case "range":
		return "Range scan"
	case "ref", "eq_ref", "const", "system":
		return "Lookup (" + access + ")"
	}
	return access
}

// readFigures takes the numbers out of an object, from wherever the server
// put them.
func readFigures(n *source.PlanNode, m map[string]any) {
	if c, ok := number(m["cost"]); ok {
		n.EstimatedCost = c // MariaDB
	} else if ci, ok := m["cost_info"].(map[string]any); ok {
		// MySQL. prefix_cost is the running total through the join, which is
		// what a cost means everywhere else in a plan; where there is none,
		// whatever this step alone cost.
		for _, k := range []string{"prefix_cost", "query_cost", "read_cost", "sort_cost"} {
			if v, ok := number(ci[k]); ok {
				n.EstimatedCost = v
				break
			}
		}
	}
	for _, k := range []string{"rows_examined_per_scan", "rows"} {
		if v, ok := number(m[k]); ok {
			n.EstimatedRows = int64(v)
			break
		}
	}
	// r_rows is per loop, as MariaDB reports it.
	if v, ok := number(m["r_rows"]); ok {
		loops := 1.0
		if l, ok := number(m["r_loops"]); ok && l > 0 {
			loops = l
		}
		n.ActualRows = int64(v * loops)
	}
	if v, ok := number(m["r_total_time_ms"]); ok {
		n.ActualTime = time.Duration(v * float64(time.Millisecond))
	}
}

// detailsOf is what the step did, in the server's own words.
func detailsOf(m map[string]any) []string {
	var out []string
	add := func(label string, v any) {
		if s := text(v); s != "" {
			out = append(out, label+": "+s)
		}
	}
	add("Condition", m["attached_condition"])
	add("Index condition", m["index_condition"])
	add("Sort key", m["sort_key"])
	add("Ref", joinText(m["ref"]))
	add("Key parts", joinText(m["used_key_parts"]))
	add("Possible keys", joinText(m["possible_keys"]))
	add("Message", m["message"])
	if v, ok := number(m["filtered"]); ok && v < 100 {
		out = append(out, "Filtered to "+strconv.FormatFloat(v, 'f', -1, 64)+"%")
	}
	if m["using_filesort"] == true {
		out = append(out, "Sorted on disk")
	}
	if m["using_temporary_table"] == true {
		out = append(out, "Uses a temporary table")
	}
	return out
}

// joinText reads a list of strings as one.
func joinText(v any) string {
	list, ok := v.([]any)
	if !ok {
		return ""
	}
	parts := make([]string, 0, len(list))
	for _, e := range list {
		if s := text(e); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, ", ")
}

// number reads a figure the servers write either as a number or as text.
func number(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case string:
		f, err := strconv.ParseFloat(x, 64)
		return f, err == nil
	}
	return 0, false
}

// text reads a value that is meant to be a word.
func text(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
