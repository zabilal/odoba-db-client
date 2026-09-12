package conformance

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// checkWriter plans and applies changes to the Writable table (FR-4.4,
// FR-4.5, ADR-0031): each change written by its key, all of a plan or none
// of it, and the guard asked first.
func checkWriter(t *testing.T, target Target) {
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
	ref := target.Writable
	byID := model.RowIdentity{Kind: model.IdentityPrimaryKey, Columns: []string{"id"}, Target: ref}
	planned := func(t *testing.T, w source.Writer, confirmed bool, changes ...source.RowChange) *source.WritePlan {
		t.Helper()
		plan, err := w.Plan(ctx, source.Changeset{Target: ref, Identity: byID, Changes: changes, Confirmed: confirmed})
		if err != nil {
			t.Fatalf("Plan: %v", err)
		}
		if len(plan.Statements) != len(changes) || len(plan.Descriptions) != len(changes) {
			t.Fatalf("a plan of %d statements and %d descriptions for %d changes",
				len(plan.Statements), len(plan.Descriptions), len(changes))
		}
		// A plan says what the source claims: a source without transactions
		// must not promise one, and one with them must keep it (FR-4.5).
		if atomic := src.Capabilities().Data.TransactionalWrite; plan.Atomic != atomic {
			t.Fatalf("a plan that says atomic %v, where the source claims TransactionalWrite %v",
				plan.Atomic, atomic)
		}
		return plan
	}
	apply := func(t *testing.T, changes ...source.RowChange) *source.WriteOutcome {
		t.Helper()
		out, err := w.Apply(ctx, planned(t, w, false, changes...))
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		return out
	}
	rows := func(t *testing.T) string {
		t.Helper()
		rs, err := src.Browse(ctx, ref, source.BrowseOptions{Sorts: []source.Sort{{Column: "id"}}})
		if err != nil {
			t.Fatal(err)
		}
		defer rs.Close()
		var out []string
		for {
			r, err := rs.Next(ctx)
			if errors.Is(err, io.EOF) {
				return strings.Join(out, " ")
			}
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, fmt.Sprint(r))
		}
	}
	insert := func(id int64, name string, n any) source.RowChange {
		v := map[string]any{"id": id, "name": name}
		if n != nil {
			v["n"] = n
		}
		return source.RowChange{Kind: source.ChangeInsert, Values: v}
	}

	// A row given nothing takes every default: the server numbers it.
	out := apply(t, source.RowChange{Kind: source.ChangeInsert})
	if out.Err != nil || out.Applied != 1 {
		t.Fatalf("a row of defaults: %+v", out)
	}
	if got := rows(t); got != "[1 none <nil>]" {
		t.Fatalf("after a row of defaults: %s", got)
	}
	if out := apply(t, source.RowChange{Kind: source.ChangeDelete, Key: []any{int64(1)}}); out.Err != nil {
		t.Fatalf("deleting it: %+v", out)
	}

	out = apply(t, insert(1, "one", int64(1)), insert(2, "two", int64(2)), insert(3, "three", nil))
	if out.Err != nil || out.Applied != 3 || out.Affected != 3 || out.FailedAt != -1 {
		t.Fatalf("inserts: %+v", out)
	}
	if got := rows(t); got != "[1 one 1] [2 two 2] [3 three <nil>]" {
		t.Fatalf("after the inserts: %s", got)
	}

	out = apply(t,
		source.RowChange{Kind: source.ChangeUpdate, Key: []any{int64(1)}, Values: map[string]any{"name": "uno"}},
		source.RowChange{Kind: source.ChangeDelete, Key: []any{int64(2)}},
		insert(4, "four", nil))
	if out.Err != nil || out.Applied != 3 || out.Affected != 3 {
		t.Fatalf("an update, a delete and an insert: %+v", out)
	}
	if got := rows(t); got != "[1 uno 1] [3 three <nil>] [4 four <nil>]" {
		t.Fatalf("after them: %s", got)
	}
	// A value set to what it is still matches its row: an engine that counts
	// only the rows it changed would read the row as gone.
	if out := apply(t, source.RowChange{Kind: source.ChangeUpdate, Key: []any{int64(3)}, Values: map[string]any{"name": "three"}}); out.Err != nil {
		t.Errorf("a value set to what it is: %+v", out)
	}

	out = apply(t,
		source.RowChange{Kind: source.ChangeUpdate, Key: []any{int64(3)}, Values: map[string]any{"name": "tres"}},
		source.RowChange{Kind: source.ChangeUpdate, Key: []any{int64(99)}, Values: map[string]any{"name": "gone"}})
	if out.Err == nil || out.FailedAt != 1 || !out.RolledBack {
		t.Errorf("a change to a row not there fails, and undoes the rest: %+v", out)
	}
	out = apply(t, insert(5, "five", nil), insert(1, "again", nil))
	if out.Err == nil || out.FailedAt != 1 || !out.RolledBack {
		t.Errorf("a key used twice fails, and undoes the rest: %+v", out)
	}
	if got := rows(t); got != "[1 uno 1] [3 three <nil>] [4 four <nil>]" {
		t.Errorf("after two plans that failed, nothing of them is left: %s", got)
	}

	// A key that does not tell two rows apart changes both, and so nothing.
	apply(t, insert(7, "twin", nil), insert(8, "twin", nil))
	byName := model.RowIdentity{Kind: model.IdentityPrimaryKey, Columns: []string{"name"}, Target: ref}
	twin, err := w.Plan(ctx, source.Changeset{Target: ref, Identity: byName, Changes: []source.RowChange{
		{Kind: source.ChangeUpdate, Key: []any{"twin"}, Values: map[string]any{"n": int64(2)}}}})
	if err != nil {
		t.Fatal(err)
	}
	if out, err := w.Apply(ctx, twin); err != nil || out.Err == nil || out.FailedAt != 0 || !out.RolledBack {
		t.Errorf("a change matching two rows fails, and is undone: %v %+v", err, out)
	}
	if got := rows(t); got != "[1 uno 1] [3 three <nil>] [4 four <nil>] [7 twin <nil>] [8 twin <nil>]" {
		t.Errorf("both rows as they were: %s", got)
	}
	apply(t, source.RowChange{Kind: source.ChangeDelete, Key: []any{int64(7)}}, source.RowChange{Kind: source.ChangeDelete, Key: []any{int64(8)}})

	// New rows need no key, as none addresses them: a file is imported into a
	// table without one (ADR-0049).
	keyless, err := w.Plan(ctx, source.Changeset{Target: ref, Changes: []source.RowChange{insert(9, "keyless", nil)}})
	if err != nil {
		t.Fatalf("new rows planned without a key: %v", err)
	}
	if out, err := w.Apply(ctx, keyless); err != nil || out.Err != nil || out.Applied != 1 {
		t.Errorf("new rows written without a key: %v %+v", err, out)
	}
	if got := rows(t); got != "[1 uno 1] [3 three <nil>] [4 four <nil>] [9 keyless <nil>]" {
		t.Errorf("after a row added without a key: %s", got)
	}
	apply(t, source.RowChange{Kind: source.ChangeDelete, Key: []any{int64(9)}})

	if target.OpenGuarded == nil {
		return
	}
	ro := target.OpenGuarded(ctx, t, source.Guard{ReadOnly: true})
	defer ro.Close()
	rw := ro.(source.Writer)
	if _, err := rw.Apply(ctx, planned(t, rw, true, insert(6, "six", nil))); !errors.Is(err, source.ErrReadOnly) {
		t.Errorf("a read-only connection writes nothing: %v", err)
	}
	prod := target.OpenGuarded(ctx, t, source.Guard{Environment: source.EnvProduction})
	defer prod.Close()
	pw := prod.(source.Writer)
	del := source.RowChange{Kind: source.ChangeDelete, Key: []any{int64(4)}}
	plan := planned(t, pw, false, del)
	if !plan.Guarded {
		t.Error("a production plan is guarded")
	}
	if _, err := pw.Apply(ctx, plan); !errors.Is(err, source.ErrConfirmationRequired) {
		t.Errorf("production without consent: %v", err)
	}
	if out, err := pw.Apply(ctx, planned(t, pw, true, del)); err != nil || out.Err != nil {
		t.Errorf("production with consent: %v %+v", err, out)
	}
	if got := rows(t); got != "[1 uno 1] [3 three <nil>]" {
		t.Errorf("after the consented delete: %s", got)
	}
}
