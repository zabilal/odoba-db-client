package localdb

import (
	"context"
	"testing"
)

// Saved views (FR-3.16).

func aView(name string, path ...string) View {
	return View{ID: name, Name: name, Tab: SessionTab{
		Kind: SessionObject, ConnectionID: "c1", RefKind: "table", RefPath: path,
		Filters: map[string]string{"status": "open"},
		Sorts:   []SessionSort{{Column: "id", Descending: true}},
		Hidden:  []string{"notes"},
	}}
}

// A view comes back as it went in: the arrangement is the point of it.
func TestAViewComesBackAsItWentIn(t *testing.T) {
	db, _ := open(t)
	ctx := context.Background()
	want := aView("Open first", "main", "orders")
	if err := db.PutView(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, err := db.Views(ctx, "c1", "table", []string{"main", "orders"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("saved one view and read %d", len(got))
	}
	v := got[0]
	if v.Name != want.Name || v.Tab.Filters["status"] != "open" ||
		len(v.Tab.Sorts) != 1 || !v.Tab.Sorts[0].Descending ||
		len(v.Tab.Hidden) != 1 || v.Tab.Hidden[0] != "notes" {
		t.Errorf("the view reads %+v", v)
	}
}

// A view belongs to its own table. Two tables that share a name in
// different schemas do not share views, and neither do two connections.
func TestAViewBelongsToItsOwnTable(t *testing.T) {
	db, _ := open(t)
	ctx := context.Background()
	for _, v := range []View{
		aView("ours", "main", "orders"),
		aView("theirs", "other", "orders"),
	} {
		if err := db.PutView(ctx, v); err != nil {
			t.Fatal(err)
		}
	}
	elsewhere := aView("elsewhere", "main", "orders")
	elsewhere.Tab.ConnectionID = "c2"
	if err := db.PutView(ctx, elsewhere); err != nil {
		t.Fatal(err)
	}

	got, err := db.Views(ctx, "c1", "table", []string{"main", "orders"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "ours" {
		t.Fatalf("a table's views are %+v", names(got))
	}
	// And a table with none has none, rather than everybody's.
	if got, err := db.Views(ctx, "c1", "table", []string{"main", "people"}); err != nil || len(got) != 0 {
		t.Errorf("a table with no views has %+v (%v)", names(got), err)
	}
}

// They come back by name, so that a list of them reads the same every time.
func TestViewsComeBackByName(t *testing.T) {
	db, _ := open(t)
	ctx := context.Background()
	for _, name := range []string{"Zed", "Anne", "Mary"} {
		if err := db.PutView(ctx, aView(name, "main", "orders")); err != nil {
			t.Fatal(err)
		}
	}
	got, err := db.Views(ctx, "c1", "table", []string{"main", "orders"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"Anne", "Mary", "Zed"}; !equal(names(got), want) {
		t.Errorf("the views read %v, want %v", names(got), want)
	}
}

// Saving one under an ID that is there replaces it, which is what saving
// over a view somebody named again has to mean.
func TestSavingOverAViewReplacesIt(t *testing.T) {
	db, _ := open(t)
	ctx := context.Background()
	v := aView("Open first", "main", "orders")
	if err := db.PutView(ctx, v); err != nil {
		t.Fatal(err)
	}
	v.Tab.Filters = map[string]string{"status": "closed"}
	if err := db.PutView(ctx, v); err != nil {
		t.Fatal(err)
	}
	got, err := db.Views(ctx, "c1", "table", []string{"main", "orders"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Tab.Filters["status"] != "closed" {
		t.Errorf("after saving over it: %+v", got)
	}
}

// A view forgotten is gone, and the rest are not.
func TestAViewForgottenIsGone(t *testing.T) {
	db, _ := open(t)
	ctx := context.Background()
	keep, drop := aView("keep", "main", "orders"), aView("drop", "main", "orders")
	for _, v := range []View{keep, drop} {
		if err := db.PutView(ctx, v); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.DeleteView(ctx, drop); err != nil {
		t.Fatal(err)
	}
	got, err := db.Views(ctx, "c1", "table", []string{"main", "orders"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "keep" {
		t.Errorf("what is left is %+v", names(got))
	}
}

// A view with nothing to be of, or nothing to be called, is refused: it
// could never be found again.
func TestAViewNeedsANameAndAnObject(t *testing.T) {
	db, _ := open(t)
	ctx := context.Background()
	for _, c := range []struct {
		why string
		v   View
	}{
		{"no ID", View{Name: "x", Tab: SessionTab{ConnectionID: "c1", RefPath: []string{"t"}}}},
		{"no name", View{ID: "1", Tab: SessionTab{ConnectionID: "c1", RefPath: []string{"t"}}}},
		{"a name of spaces", View{ID: "1", Name: "  ", Tab: SessionTab{ConnectionID: "c1", RefPath: []string{"t"}}}},
		{"no connection", View{ID: "1", Name: "x", Tab: SessionTab{RefPath: []string{"t"}}}},
		{"no object", View{ID: "1", Name: "x", Tab: SessionTab{ConnectionID: "c1"}}},
	} {
		if err := db.PutView(ctx, c.v); err == nil {
			t.Errorf("a view with %s was saved", c.why)
		}
	}
}

func names(vs []View) []string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = v.Name
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
