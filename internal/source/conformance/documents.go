package conformance

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// checkDocumentWriter writes to a store whose rows are documents (FR-12.1).
//
// It assumes nothing a document store has not got: no declared columns, no
// default the server fills in, no key it numbers. What it does assume is the
// contract — that a document is told from another by the identity its stream
// reports, that a change writes only the fields it names, and that a field
// can be taken away (model.Removed).
func checkDocumentWriter(t *testing.T, target Target, src source.Source, w source.Writer) {
	ctx := context.Background()
	ref := target.Writable

	// read returns every document as a map of its fields, by the value of a
	// named one.
	read := func(t *testing.T) map[string]map[string]any {
		t.Helper()
		rs, err := src.Browse(ctx, ref, source.BrowseOptions{Limit: 100})
		if err != nil {
			t.Fatalf("Browse: %v", err)
		}
		defer rs.Close()
		cols := rs.Columns()
		out := map[string]map[string]any{}
		for {
			row, err := rs.Next(ctx)
			if errors.Is(err, io.EOF) {
				return out
			}
			if err != nil {
				t.Fatalf("Next: %v", err)
			}
			doc := map[string]any{}
			for i, c := range cols {
				if i < len(row) {
					doc[c.Name] = row[i]
				}
			}
			out[fmt.Sprint(doc["name"])] = doc
		}
	}
	apply := func(t *testing.T, id model.RowIdentity, changes ...source.RowChange) *source.WriteOutcome {
		t.Helper()
		plan, err := w.Plan(ctx, source.Changeset{Target: ref, Identity: id, Changes: changes})
		if err != nil {
			t.Fatalf("Plan: %v", err)
		}
		if len(plan.Statements) != len(changes) || len(plan.Descriptions) != len(changes) {
			t.Fatalf("a plan of %d statements and %d descriptions for %d changes",
				len(plan.Statements), len(plan.Descriptions), len(changes))
		}
		if atomic := src.Capabilities().Data.TransactionalWrite; plan.Atomic != atomic {
			t.Fatalf("a plan that says atomic %v, where the source claims TransactionalWrite %v",
				plan.Atomic, atomic)
		}
		out, err := w.Apply(ctx, plan)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		return out
	}

	// New documents need no key: nothing addresses them yet.
	out := apply(t, model.RowIdentity{},
		source.RowChange{Kind: source.ChangeInsert, Values: map[string]any{"name": "one", "n": int64(1)}},
		source.RowChange{Kind: source.ChangeInsert, Values: map[string]any{"name": "two", "n": int64(2)}})
	if out.Err != nil || out.Applied != 2 {
		t.Fatalf("adding two documents: %+v", out)
	}
	docs := read(t)
	if len(docs) != 2 || docs["one"] == nil || docs["two"] == nil {
		t.Fatalf("after adding two: %v", docs)
	}

	id := writableIdentity(ctx, t, src, ref)
	if !id.Editable() {
		t.Fatalf("the documents cannot be told apart: %+v", id)
	}
	key := func(t *testing.T, name string) []any {
		t.Helper()
		doc := read(t)[name]
		if doc == nil {
			t.Fatalf("no document called %s", name)
		}
		out := make([]any, 0, len(id.Columns))
		for _, c := range id.Columns {
			out = append(out, doc[c])
		}
		return out
	}

	// A change writes the fields it names, and leaves the rest alone.
	if out := apply(t, id, source.RowChange{Kind: source.ChangeUpdate, Key: key(t, "one"),
		Values: map[string]any{"n": int64(11)}}); out.Err != nil {
		t.Fatalf("changing a document: %+v", out)
	}
	docs = read(t)
	if got := docs["one"]["n"]; fmt.Sprint(got) != "11" {
		t.Errorf("the field changed is %v", got)
	}
	if docs["two"]["n"] == nil || fmt.Sprint(docs["two"]["n"]) != "2" {
		t.Errorf("another document was written: %v", docs["two"])
	}

	// A field can be taken away, which is a document's own to have.
	if out := apply(t, id, source.RowChange{Kind: source.ChangeUpdate, Key: key(t, "two"),
		Values: map[string]any{"n": model.Removed{}}}); out.Err != nil {
		t.Fatalf("removing a field: %+v", out)
	}
	if got := read(t)["two"]["n"]; got != nil {
		t.Errorf("the field removed is %v", got)
	}

	// A change to a document that is not there fails, and says which change
	// it was.
	gone := key(t, "one")
	if out := apply(t, id, source.RowChange{Kind: source.ChangeDelete, Key: gone}); out.Err != nil {
		t.Fatalf("deleting a document: %+v", out)
	}
	out = apply(t, id, source.RowChange{Kind: source.ChangeUpdate, Key: gone,
		Values: map[string]any{"n": int64(3)}})
	if out.Err == nil || out.FailedAt != 0 {
		t.Errorf("a change to a document that is gone: %+v", out)
	}

	// The guard is asked before anything is written.
	if target.OpenGuarded == nil {
		return
	}
	add := source.RowChange{Kind: source.ChangeInsert, Values: map[string]any{"name": "guarded"}}
	ro := target.OpenGuarded(ctx, t, source.Guard{ReadOnly: true})
	defer ro.Close()
	row := ro.(source.Writer)
	plan, err := row.Plan(ctx, source.Changeset{Target: ref, Identity: id, Changes: []source.RowChange{add}})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if _, err := row.Apply(ctx, plan); !errors.Is(err, source.ErrReadOnly) {
		t.Errorf("a read-only connection writes nothing: %v", err)
	}
	prod := target.OpenGuarded(ctx, t, source.Guard{Environment: source.EnvProduction})
	defer prod.Close()
	pw := prod.(source.Writer)
	plan, err = pw.Plan(ctx, source.Changeset{Target: ref, Identity: id, Changes: []source.RowChange{add}})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if !plan.Guarded {
		t.Error("a production plan is guarded")
	}
	if _, err := pw.Apply(ctx, plan); !errors.Is(err, source.ErrConfirmationRequired) {
		t.Errorf("production without consent: %v", err)
	}
	consented, err := pw.Plan(ctx, source.Changeset{Target: ref, Identity: id,
		Changes: []source.RowChange{add}, Confirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	if out, err := pw.Apply(ctx, consented); err != nil || out.Err != nil {
		t.Errorf("production with consent: %v %+v", err, out)
	}
	if read(t)["guarded"] == nil {
		t.Error("the document consented to was not written")
	}
}

// writableIdentity is how the rows of the Writable object are told apart. A
// store with declared columns has the id column the suite asks for; a
// document store names its own key, and only a stream of its documents can
// say what it is (FR-12.1).
func writableIdentity(ctx context.Context, t *testing.T, src source.Source, ref model.ObjectRef) model.RowIdentity {
	t.Helper()
	if src.Capabilities().Paradigm != model.ParadigmDocument {
		return model.RowIdentity{Kind: model.IdentityPrimaryKey, Columns: []string{"id"}, Target: ref}
	}
	rs, err := src.Browse(ctx, ref, source.BrowseOptions{Limit: 1})
	if err != nil {
		t.Fatalf("Browse: %v", err)
	}
	defer rs.Close()
	return model.IdentityOf(rs)
}
