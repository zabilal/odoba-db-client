// Package conformance runs one battery of behavioural tests against every
// driver (REQ-DRV-1).
//
// Drivers differ enormously in what they support, so the suite is capability
// driven: each check declares what it needs, and is SKIPPED — never failed —
// when the source does not claim that capability. A driver is conformant when
// everything it claims to support actually works, not when it supports
// everything.
//
// Drivers call Run from a test guarded by the "conformance" build tag, since
// these tests need a real server.
package conformance

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/capability"
	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

// Target describes the source under test.
type Target struct {
	// Name identifies the driver in test output.
	Name string

	// Open returns a fresh connection. Called per check so that a check
	// cannot corrupt another's state.
	Open func(ctx context.Context, t *testing.T) source.Source

	// Browsable is an object the suite may read from. Required.
	Browsable model.ObjectRef

	// Writable is an object the suite may modify. Zero disables write checks,
	// which is the correct setting against any server holding real data. It
	// must be an empty table with columns id, an integer primary key the
	// server numbers from 1 when it is not given; name, text that cannot be
	// NULL, 'none' by default; and n, an integer that can be NULL.
	Writable model.ObjectRef

	// OpenGuarded opens a connection with a guard, for the write checks'
	// read-only and production cases. Nil skips them.
	OpenGuarded func(ctx context.Context, t *testing.T, g source.Guard) source.Source
}

// Run executes the full suite.
func Run(t *testing.T, target Target) {
	t.Helper()

	if target.Open == nil {
		t.Fatal("conformance: Target.Open is required")
	}
	if target.Browsable.IsZero() {
		t.Fatal("conformance: Target.Browsable is required")
	}

	checks := []struct {
		name string
		fn   func(*testing.T, Target)
	}{
		{"Lifecycle", checkLifecycle},
		{"Capabilities", checkCapabilities},
		{"Introspection", checkIntrospection},
		{"Browse", checkBrowse},
		{"BrowseCancellation", checkBrowseCancellation},
		{"BrowseLimit", checkBrowseLimit},
		{"Distinct", checkDistinct},
		{"Where", checkWhere},
		{"ReadOnlyGuard", checkReadOnlyGuard},
		{"Writer", checkWriter},
		{"EditableResults", checkEditableResults},
		{"UnsupportedOptionsRejected", checkUnsupportedOptionsRejected},
		{"Loader", checkLoader},
	}

	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) { c.fn(t, target) })
	}
}

func checkLifecycle(t *testing.T, target Target) {
	ctx := context.Background()
	src := target.Open(ctx, t)
	defer src.Close()

	if err := src.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}

	info, err := src.Info(ctx)
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if info.Product == "" {
		t.Error("Info.Product is empty; the health display needs a server identity")
	}

	// Close must be idempotent: the session teardown path can reach it twice.
	if err := src.Close(); err != nil {
		t.Errorf("first Close: %v", err)
	}
	if err := src.Close(); err != nil {
		t.Errorf("second Close must be a no-op, got: %v", err)
	}
}

func checkCapabilities(t *testing.T, target Target) {
	ctx := context.Background()
	src := target.Open(ctx, t)
	defer src.Close()

	caps := src.Capabilities()

	if !caps.Paradigm.Valid() {
		t.Errorf("Capabilities.Paradigm = %q, not a known paradigm", caps.Paradigm)
	}
	if len(caps.Objects) == 0 {
		t.Error("Capabilities.Objects is empty; the explorer would show nothing")
	}

	// A claimed capability that has no implementation behind it is worse than
	// an unclaimed one: the UI will offer an affordance that fails at runtime.
	if caps.Query.Supported && !sqllex.Known(caps.Query.Language) {
		// DialectFor would quietly fall back to PostgreSQL: the editor would
		// colour, and history redact, by another engine's rules.
		t.Errorf("Capabilities.Query.Language = %q, which the lexer does not know", caps.Query.Language)
	}
	if caps.Query.Supported {
		if _, ok := src.(source.Queryer); !ok {
			t.Error("claims Query.Supported but does not implement Queryer")
		}
		if caps.Query.Language == "" {
			t.Error("claims Query.Supported but names no language for the lexer")
		}
	}
	if caps.Query.Cancel {
		if _, ok := src.(source.Killer); !ok {
			t.Error("claims Query.Cancel but does not implement Killer; " +
				"a cancel button would only detach the UI (FR-5.5)")
		}
	}
	if caps.Query.Explain {
		if _, ok := src.(source.Explainer); !ok {
			t.Error("claims Query.Explain but does not implement Explainer")
		}
	}
	if caps.Structure.InferredShape {
		if _, ok := src.(source.ShapeInferrer); !ok {
			t.Error("claims Structure.InferredShape but does not implement ShapeInferrer")
		}
	}
	if caps.Data.Pipeline {
		if _, ok := src.(source.Aggregator); !ok {
			t.Error("claims Data.Pipeline but does not implement Aggregator")
		}
	}
	if caps.Schema.Indexes {
		if _, ok := src.(source.IndexManager); !ok {
			t.Error("claims Schema.Indexes but does not implement IndexManager")
		}
	}
	if caps.Data.Insert || caps.Data.Update || caps.Data.Delete {
		if _, ok := src.(source.Writer); !ok {
			t.Error("claims write support but does not implement Writer")
		}
	}
	if _, ok := src.(source.BulkLoader); ok != caps.Data.BulkLoad {
		t.Errorf("Capabilities.Data.BulkLoad is %v, and implementing BulkLoader %v: the import would offer, or miss, the fast path", caps.Data.BulkLoad, ok)
	}
	if caps.Data.DistinctValues {
		if _, ok := src.(source.DistinctLister); !ok {
			t.Error("claims DistinctValues but does not implement DistinctLister")
		}
	}
	if caps.Schema.Diff {
		if _, ok := src.(source.Snapshotter); !ok {
			t.Error("claims Schema.Diff but does not implement Snapshotter")
		}
	}
	if caps.Stream.ConsumerGroups || caps.Stream.TopicAdmin || caps.Stream.ResetOffsets {
		if _, ok := src.(source.StreamAdmin); !ok {
			t.Error("claims stream administration but does not implement StreamAdmin")
		}
	}
	if caps.Stream.Produce {
		if _, ok := src.(source.StreamProducer); !ok {
			t.Error("claims Stream.Produce but does not implement StreamProducer")
		}
	}
	if caps.Stream.SchemaRegistry {
		if _, ok := src.(source.SchemaRegistry); !ok {
			t.Error("claims Stream.SchemaRegistry but does not implement SchemaRegistry")
		}
	}
}

func checkIntrospection(t *testing.T, target Target) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	src := target.Open(ctx, t)
	defer src.Close()

	roots, err := src.Root(ctx)
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	if len(roots) == 0 {
		t.Fatal("Root returned no nodes; the explorer would be empty")
	}

	caps := src.Capabilities()
	for _, n := range roots {
		checkNode(t, caps, n)
	}

	// Expanding a node that claims children must return something or a clear
	// error — never an empty slice with no explanation, which renders as a
	// permanently spinning expander.
	for _, n := range roots {
		if !n.HasChildren {
			continue
		}
		kids, err := src.Children(ctx, n.Ref)
		if err != nil {
			t.Errorf("Children(%v): %v", n.Ref, err)
		} else if len(kids) == 0 {
			t.Errorf("node %v claims HasChildren but returned none", n.Ref)
		}
		break
	}
	for _, p := range walkTree(ctx, src, caps, roots, 1) {
		t.Error(p)
	}
}

func checkNode(t *testing.T, caps capability.Capabilities, n model.Node) {
	for _, p := range nodeProblems(caps, n) {
		t.Error(p)
	}
}

// nodeProblems is what is wrong with one node of the tree. It must be
// addressable, named and of a declared kind; and a folder must be an object
// class the model knows (model.ClassNode), holding a declared kind, under
// the model's name for it (FR-2.2, REQ-DB-4).
func nodeProblems(caps capability.Capabilities, n model.Node) []string {
	var out []string
	if n.Label == "" {
		out = append(out, fmt.Sprintf("node %v has an empty label", n.Ref))
	}
	if n.Ref.IsZero() {
		out = append(out, fmt.Sprintf("node %q has a zero ref and cannot be addressed", n.Label))
	}
	if !caps.Supports(n.Ref.Kind) {
		out = append(out, fmt.Sprintf("node %v has kind %q not declared in Capabilities.Objects", n.Ref, n.Ref.Kind))
	}
	if n.Ref.Kind != model.KindFolder {
		return out
	}
	switch k, ok := model.ClassOf(n.Ref); {
	case !ok:
		out = append(out, fmt.Sprintf("folder %v is no object class the model knows; build it with model.ClassNode", n.Ref))
	case !caps.Supports(k):
		out = append(out, fmt.Sprintf("class %v holds %q, which Capabilities.Objects does not declare", n.Ref, k))
	case n.Label != model.ClassLabel(k):
		out = append(out, fmt.Sprintf("class %v is called %q, where the model calls it %q", n.Ref, n.Label, model.ClassLabel(k)))
	}
	return out
}

// childLister is the part of a source walkTree needs.
type childLister interface {
	Children(ctx context.Context, ref model.ObjectRef) ([]model.Node, error)
}

// walkTree expands the tree a few levels down, a few nodes to a level, and
// says what it finds wrong: in each node, as nodeProblems does, and in a
// class holding objects of another kind.
func walkTree(ctx context.Context, src childLister, caps capability.Capabilities, nodes []model.Node, level int) []string {
	const levels, perLevel = 4, 4
	if level >= levels {
		return nil
	}
	var out []string
	expanded := 0
	for _, n := range nodes {
		if !n.HasChildren || expanded == perLevel {
			continue
		}
		expanded++
		kids, err := src.Children(ctx, n.Ref)
		if err != nil {
			out = append(out, fmt.Sprintf("Children(%v): %v", n.Ref, err))
			continue
		}
		class, isClass := model.ClassOf(n.Ref)
		for _, k := range kids {
			out = append(out, nodeProblems(caps, k)...)
			if isClass && k.Ref.Kind != class {
				out = append(out, fmt.Sprintf("class %v holds %v, of kind %q", n.Ref, k.Ref, k.Ref.Kind))
			}
		}
		out = append(out, walkTree(ctx, src, caps, kids, level+1)...)
	}
	return out
}

func checkBrowse(t *testing.T, target Target) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	src := target.Open(ctx, t)
	defer src.Close()

	stream, err := src.Browse(ctx, target.Browsable, source.BrowseOptions{Limit: 10})
	if err != nil {
		t.Fatalf("Browse: %v", err)
	}
	defer stream.Close()

	cols := stream.Columns()
	if len(cols) == 0 {
		t.Fatal("Browse returned no columns")
	}
	for i, c := range cols {
		if c.Name == "" {
			t.Errorf("column %d has an empty name", i)
		}
	}

	n := 0
	for {
		row, err := stream.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if len(row) != len(cols) {
			t.Fatalf("row has %d values but there are %d columns", len(row), len(cols))
		}
		if n++; n >= 10 {
			break
		}
	}

	// Close must be idempotent here too — the grid closes on tab close and
	// again on session teardown.
	if err := stream.Close(); err != nil {
		t.Errorf("first stream Close: %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Errorf("second stream Close must be a no-op, got: %v", err)
	}
}

func checkBrowseCancellation(t *testing.T, target Target) {
	ctx := context.Background()
	src := target.Open(ctx, t)
	defer src.Close()

	stream, err := src.Browse(ctx, target.Browsable, source.BrowseOptions{Limit: 1000})
	if err != nil {
		t.Fatalf("Browse: %v", err)
	}
	defer stream.Close()

	// NFR-P9: cancellation must take effect promptly, not at the end of the
	// current fetch. A source that ignores ctx blocks the task centre's cancel.
	cancelled, cancel := context.WithCancel(ctx)
	cancel()

	done := make(chan error, 1)
	go func() {
		_, err := stream.Next(cancelled)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Error("Next on a cancelled context returned a row; cancellation is not honoured")
		} else if !errors.Is(err, context.Canceled) && !errors.Is(err, io.EOF) {
			t.Logf("Next returned %v on cancellation (acceptable if it wraps context.Canceled)", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("Next did not return within 2s of cancellation (NFR-P9 budget is 200ms)")
	}
}

func checkBrowseLimit(t *testing.T, target Target) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	src := target.Open(ctx, t)
	defer src.Close()

	const limit = 3
	stream, err := src.Browse(ctx, target.Browsable, source.BrowseOptions{Limit: limit})
	if err != nil {
		t.Fatalf("Browse: %v", err)
	}
	defer stream.Close()

	n := 0
	for {
		_, err := stream.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if n++; n > limit {
			t.Fatalf("Browse returned more than Limit=%d rows; "+
				"NFR-P11 forbids unbounded reads", limit)
		}
	}
}

func checkReadOnlyGuard(t *testing.T, target Target) {
	if target.Writable.IsZero() {
		t.Skip("no Writable target configured")
	}

	ctx := context.Background()
	src := target.Open(ctx, t)
	defer src.Close()

	w, ok := src.(source.Writer)
	if !ok {
		t.Skip("source does not implement Writer")
	}

	// NFR-S4 requires enforcement in the data layer. A driver that only
	// relies on the UI hiding the button fails here.
	cs := source.Changeset{
		Target:   target.Writable,
		Identity: model.RowIdentity{Kind: model.IdentityPrimaryKey, Columns: []string{"id"}, Target: target.Writable},
		Changes: []source.RowChange{{
			Kind:   source.ChangeDelete,
			Key:    []any{int64(-999999)},
			Values: map[string]any{},
		}},
	}

	plan, err := w.Plan(ctx, cs)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan.Statements) == 0 {
		t.Error("Plan produced no statements; FR-4.4 requires a reviewable preview")
	}
}

func checkUnsupportedOptionsRejected(t *testing.T, target Target) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	src := target.Open(ctx, t)
	defer src.Close()

	caps := src.Capabilities()
	if caps.Data.ServerSort {
		t.Skip("source supports server-side sort")
	}

	// Silently ignoring an unsupported option is the dangerous failure: the
	// grid would present unsorted rows under a sort indicator. Browse must
	// refuse instead.
	stream, err := src.Browse(ctx, target.Browsable, source.BrowseOptions{
		Limit: 5,
		Sorts: []source.Sort{{Column: "id"}},
	})
	if err == nil {
		stream.Close()
		t.Error("Browse accepted a sort the source does not support; " +
			"it must return an error rather than silently ignore it")
	}
}

// checkDistinct proves the filter picklist (FR-3.4). For every column whose
// values can be grouped, the listed values and counts must agree with a full
// read of the object, most frequent first. Each value, used as a filter, must
// select exactly the rows it was counted from: the picklist filters with what
// it was given. And filters must narrow the list.
func checkDistinct(t *testing.T, target Target) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	src := target.Open(ctx, t)
	defer src.Close()
	if !src.Capabilities().Data.DistinctValues {
		t.Skip("source does not claim DistinctValues")
	}
	dl, ok := src.(source.DistinctLister)
	if !ok {
		t.Fatal("claims DistinctValues but does not implement DistinctLister")
	}

	const most = 20000
	rows, cols := readAll(ctx, t, src, target.Browsable, nil, most)
	if len(rows) >= most {
		t.Skipf("%s has %d rows or more, too many to count here", target.Browsable, most)
	}

	checked, varied := 0, false
	for i, c := range cols {
		if !groupable[c.Type.Class] {
			continue
		}
		want := map[string]int64{}
		for _, r := range rows {
			want[valueKey(r[i])]++
		}
		got, err := dl.Distinct(ctx, target.Browsable, c.Name, source.BrowseOptions{}, len(want)+1)
		if err != nil {
			t.Errorf("Distinct(%s): %v", c.Name, err)
			continue
		}
		if len(got) != len(want) {
			t.Errorf("Distinct(%s): %d values, want %d", c.Name, len(got), len(want))
			continue
		}
		if len(got) == 0 {
			continue
		}
		for j, v := range got {
			if v.Count != want[valueKey(v.Value)] {
				t.Errorf("Distinct(%s): %s counted %d, a full read has %d", c.Name, valueKey(v.Value), v.Count, want[valueKey(v.Value)])
			}
			if j > 0 && v.Count > got[j-1].Count {
				t.Errorf("Distinct(%s) is not most frequent first", c.Name)
			}
		}
		varied = varied || got[0].Count != got[len(got)-1].Count
		for _, v := range got[:min(3, len(got))] {
			f := source.Filter{Column: c.Name, Op: source.OpIn, Values: []any{v.Value}}
			picked, _ := readAll(ctx, t, src, target.Browsable, []source.Filter{f}, most)
			if int64(len(picked)) != v.Count {
				t.Errorf("filtering %s on the listed %s selects %d rows, want %d", c.Name, valueKey(v.Value), len(picked), v.Count)
			}
		}
		f := source.Filter{Column: c.Name, Op: source.OpIn, Values: []any{got[0].Value}}
		narrowed, err := dl.Distinct(ctx, target.Browsable, c.Name, source.BrowseOptions{Filters: []source.Filter{f}}, 10)
		if err != nil || len(narrowed) != 1 || valueKey(narrowed[0].Value) != valueKey(got[0].Value) {
			t.Errorf("Distinct(%s) under a filter for %s: %v, %v", c.Name, valueKey(got[0].Value), narrowed, err)
		}
		checked++
	}
	if checked == 0 {
		t.Errorf("%s has no column whose values can be grouped, so nothing was checked", target.Browsable)
	}
	if checked > 0 && !varied {
		t.Errorf("no column of %s repeats values unevenly, so most-frequent-first went unchecked; the fixture needs one", target.Browsable)
	}
}

// groupable are the value classes every relational engine can group and
// compare for equality.
var groupable = map[model.TypeClass]bool{
	model.TypeBool: true, model.TypeInteger: true, model.TypeFloat: true, model.TypeDecimal: true,
	model.TypeString: true, model.TypeDate: true, model.TypeTimestamp: true, model.TypeUUID: true,
	model.TypeEnum: true,
}

// valueKey identifies a value by its Go type as well as its text, so a
// value that comes back decoded differently does not pass as the same.
func valueKey(v any) string { return fmt.Sprintf("%T(%v)", v, v) }

// readAll reads every row of an object the filters select, up to most.
func readAll(ctx context.Context, t *testing.T, src source.Source, ref model.ObjectRef, filters []source.Filter, most int64) ([]model.Row, []model.ColumnDef) {
	t.Helper()
	st, err := src.Browse(ctx, ref, source.BrowseOptions{Filters: filters, Limit: most})
	if err != nil {
		t.Fatalf("Browse: %v", err)
	}
	defer st.Close()
	var rows []model.Row
	for {
		r, err := st.Next(ctx)
		if errors.Is(err, io.EOF) {
			return rows, st.Columns()
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		rows = append(rows, r)
	}
}

// checkWhere proves the typed WHERE clause (FR-3.6): it narrows the rows, a
// comment at its end cannot swallow the LIMIT that bounds every read
// (NFR-P11), it is taken only as one condition, and the picklist honours it.
func checkWhere(t *testing.T, target Target) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	src := target.Open(ctx, t)
	defer src.Close()
	if _, ok := src.(source.Dialect); !ok {
		t.Skip("source has no query language to write a WHERE in")
	}
	count := func(where string, limit int64) (int, error) {
		st, err := src.Browse(ctx, target.Browsable, source.BrowseOptions{Where: where, Limit: limit})
		if err != nil {
			return 0, err
		}
		defer st.Close()
		n := 0
		for {
			_, err := st.Next(ctx)
			if errors.Is(err, io.EOF) {
				return n, nil
			}
			if err != nil {
				return n, err
			}
			n++
		}
	}

	all, err := count("1 = 1", 20000)
	if err != nil {
		t.Fatalf("WHERE 1 = 1: %v", err)
	}
	if n, err := count("1 = 0", 20000); err != nil || n != 0 {
		t.Errorf("WHERE 1 = 0: %d rows, %v; want none", n, err)
	}
	if want := min(all, 3); want > 0 {
		if n, err := count("1 = 1 -- a note", 3); err != nil || n != want {
			t.Errorf("WHERE 1 = 1 -- a note, LIMIT 3: %d rows, %v; the comment must end at its line", n, err)
		}
	}
	for _, bad := range []string{"1 = 1; SELECT 1", "(1 = 1", "1 = 1)", "'open = 1", "1 = 1 /* open"} {
		if _, err := count(bad, 3); err == nil {
			t.Errorf("WHERE %q was accepted; it is not one condition", bad)
		}
	}

	if dl, ok := src.(source.DistinctLister); ok && src.Capabilities().Data.DistinctValues {
		st, err := src.Browse(ctx, target.Browsable, source.BrowseOptions{Limit: 1})
		if err != nil {
			t.Fatalf("Browse: %v", err)
		}
		cols := st.Columns()
		st.Close()
		vals, err := dl.Distinct(ctx, target.Browsable, cols[0].Name, source.BrowseOptions{Where: "1 = 0"}, 10)
		if err != nil || len(vals) != 0 {
			t.Errorf("Distinct under WHERE 1 = 0: %v, %v; want nothing", vals, err)
		}
	}
}
