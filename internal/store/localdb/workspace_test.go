package localdb

import (
	"context"
	"strings"
	"testing"
)

// Workspaces (FR-15.9).

// A workspace comes back as it went in: what it is over and what it was
// left at.
func TestAWorkspaceComesBackAsItWentIn(t *testing.T) {
	db, _ := open(t)
	ctx := context.Background()
	want := Workspace{ID: "w1", Name: "Billing", Connections: []string{"c1", "c2"},
		Tabs: Session{Tabs: []SessionTab{{Kind: SessionObject, ConnectionID: "c1", Label: "invoices"}}}}
	if err := db.PutWorkspace(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, err := db.Workspaces(ctx)
	if err != nil || len(got) != 1 {
		t.Fatalf("read %d workspaces: %v", len(got), err)
	}
	w := got[0]
	if w.Name != "Billing" || len(w.Connections) != 2 {
		t.Errorf("the workspace reads %+v", w)
	}
	if len(w.Tabs.Tabs) != 1 || w.Tabs.Tabs[0].Label != "invoices" {
		t.Errorf("it was left at %+v", w.Tabs.Tabs)
	}
}

// A workspace over no connections in particular is over all of them,
// which is what one nobody has narrowed has to mean.
func TestAWorkspaceOverNothingIsOverEverything(t *testing.T) {
	all := Workspace{ID: "w", Name: "Everything"}
	if !all.Holds("anything") {
		t.Error("a workspace over no connections holds none")
	}
	some := Workspace{ID: "w", Name: "Billing", Connections: []string{"c1"}}
	if !some.Holds("c1") {
		t.Error("a workspace does not hold its own connection")
	}
	if some.Holds("c2") {
		t.Error("a workspace holds a connection it was not given")
	}
}

// They come back by name, so that a list of them reads the same every
// time.
func TestWorkspacesComeBackByName(t *testing.T) {
	db, _ := open(t)
	ctx := context.Background()
	for i, name := range []string{"Zed", "Anne", "Mary"} {
		if err := db.PutWorkspace(ctx, Workspace{ID: string(rune('a' + i)), Name: name}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := db.Workspaces(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, w := range got {
		names = append(names, w.Name)
	}
	if want := []string{"Anne", "Mary", "Zed"}; !equal(names, want) {
		t.Errorf("the workspaces read %v, want %v", names, want)
	}
}

// Forgetting one loses nothing it grouped: a workspace holds no connection
// and no query of its own.
func TestForgettingAWorkspaceLosesNothing(t *testing.T) {
	db, _ := open(t)
	ctx := context.Background()
	if err := db.PutWorkspace(ctx, Workspace{ID: "w1", Name: "Billing", Connections: []string{"c1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveQuery(ctx, SavedQuery{Name: "totals", ConnectionID: "c1", Body: "select 1"}); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteWorkspace(ctx, "w1"); err != nil {
		t.Fatal(err)
	}
	if got, err := db.Workspaces(ctx); err != nil || len(got) != 0 {
		t.Errorf("%d workspaces left: %v", len(got), err)
	}
	qs, err := db.SavedQueries(ctx)
	if err != nil || len(qs) != 1 {
		t.Errorf("forgetting the workspace lost its queries: %d left (%v)", len(qs), err)
	}
}

// A workspace with nothing to be called is refused: it could never be
// picked from a list.
func TestAWorkspaceNeedsANameAndAnID(t *testing.T) {
	db, _ := open(t)
	ctx := context.Background()
	for _, w := range []Workspace{
		{Name: "no id"},
		{ID: "w"},
		{ID: "w", Name: "   "},
	} {
		if err := db.PutWorkspace(ctx, w); err == nil {
			t.Errorf("%+v was saved", w)
		}
	}
}

// A workspace that cannot be read is named in the error and the rest still
// come back: one unreadable workspace is not a reason to have none.
func TestAnUnreadableWorkspaceDoesNotLoseTheOthers(t *testing.T) {
	db, _ := open(t)
	ctx := context.Background()
	if err := db.PutWorkspace(ctx, Workspace{ID: "w1", Name: "Billing"}); err != nil {
		t.Fatal(err)
	}
	if err := db.Put(ctx, workspacePrefix+"w2", []byte("{not json")); err != nil {
		t.Fatal(err)
	}
	got, err := db.Workspaces(ctx)
	if len(got) != 1 || got[0].Name != "Billing" {
		t.Errorf("the readable workspaces came back as %+v", got)
	}
	if err == nil || !strings.Contains(err.Error(), "w2") {
		t.Errorf("the unreadable one was not named: %v", err)
	}
}
