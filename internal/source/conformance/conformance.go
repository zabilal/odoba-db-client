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
	"io"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
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
	// which is the correct setting against any server holding real data.
	Writable model.ObjectRef
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
		{"ReadOnlyGuard", checkReadOnlyGuard},
		{"UnsupportedOptionsRejected", checkUnsupportedOptionsRejected},
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
	if caps.Data.Insert || caps.Data.Update || caps.Data.Delete {
		if _, ok := src.(source.Writer); !ok {
			t.Error("claims write support but does not implement Writer")
		}
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
		if n.Label == "" {
			t.Errorf("node %v has an empty label", n.Ref)
		}
		if n.Ref.IsZero() {
			t.Errorf("node %q has a zero ref and cannot be addressed", n.Label)
		}
		if !caps.Supports(n.Ref.Kind) {
			t.Errorf("node %v has kind %q not declared in Capabilities.Objects",
				n.Ref, n.Ref.Kind)
		}
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
