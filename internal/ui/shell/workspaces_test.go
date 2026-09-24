package shell

import (
	"context"
	"strings"
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/ikigai-db/ikigai-db/internal/store"
	"github.com/ikigai-db/ikigai-db/internal/store/localdb"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer/view"
)

// Workspaces (FR-15.9): a named piece of work — the connections it is
// over, the tabs it was left at, and the saved queries in it.

// twoConnections is a window with two connections saved and showing.
func twoConnections(t *testing.T) (*fixture, store.SavedConnection, store.SavedConnection) {
	t.Helper()
	fx := newFixture(t)
	billing := fx.create(t, "db1", nil)
	reporting := fx.create(t, "db2", nil)
	fx.s.Explorer.Refresh(explorer.RootID)
	pump(t, fx.q, func() bool { return len(fx.s.Explorer.Model.Children(explorer.RootID)) == 2 })
	return fx, billing, reporting
}

func workspaces(fx *fixture) []localdb.Workspace {
	got, _ := fx.hist.Workspaces(context.Background())
	return got
}

// The explorer shows the workspace's connections and not the others, and
// the window says which workspace it is in, because two windows in two
// workspaces are otherwise the same window.
func TestAWorkspaceNarrowsTheExplorer(t *testing.T) {
	fx, billing, _ := twoConnections(t)
	fx.q.Run(func() { fx.s.keepWorkspace("Billing", []string{billing.ID}) })
	pump(t, fx.q, func() bool {
		kids := fx.s.Explorer.Model.Children(explorer.RootID)
		return len(kids) == 1 && kids[0] == view.ConnectionID(billing.ID)
	})
	if got := fx.s.win.Title(); got != "Ikigai DB — Billing" {
		t.Errorf("the window is called %q", got)
	}
	fx.q.Run(func() { fx.s.switchWorkspace(localdb.Workspace{}) })
	pump(t, fx.q, func() bool { return len(fx.s.Explorer.Model.Children(explorer.RootID)) == 2 })
	if got := fx.s.win.Title(); got != "Ikigai DB" {
		t.Errorf("out of the workspace the window is called %q", got)
	}
}

// Going to a workspace comes back to the tabs it was left at, and leaving
// it takes them away again.
func TestAWorkspaceComesBackToItsTabs(t *testing.T) {
	fx, billing, _ := twoConnections(t)
	fx.s.OpenObject(billing.ID, itemsNode)
	pump(t, fx.q, func() bool { return len(fx.s.open) == 1 })
	fx.q.Run(func() { fx.s.keepWorkspace("Billing", []string{billing.ID}) })
	var saved []localdb.Workspace
	pump(t, fx.q, func() bool {
		saved = workspaces(fx)
		return len(saved) == 1 && len(saved[0].Tabs.Tabs) == 1
	})

	fx.q.Run(func() { fx.s.switchWorkspace(localdb.Workspace{}) })
	pump(t, fx.q, func() bool { return len(fx.s.open) == 0 })

	fx.q.Run(func() { fx.s.switchWorkspace(saved[0]) })
	pump(t, fx.q, func() bool { return len(fx.s.open) == 1 })
	if tb := fx.s.open[0]; tb.connID != billing.ID || tb.ref.Name() != itemsNode.Ref.Name() {
		t.Errorf("the tab that came back is %q on %q", tb.ref.Name(), tb.connID)
	}
	// And the window is not still putting itself back: what is opened
	// after a switch is saved like anything else.
	fx.s.OpenQuery(billing.ID)
	pump(t, fx.q, func() bool { return len(fx.s.open) == 2 })
	fx.q.Run(fx.s.saveSession)
	pump(t, fx.q, func() bool {
		ss, _, _ := fx.hist.Session(context.Background())
		return len(ss.Tabs) == 2 && ss.Workspace == saved[0].ID
	})
}

// Going to the workspace already in use is going nowhere: the tabs are
// not closed and opened again under somebody.
func TestGoingToTheWorkspaceAlreadyInUseDoesNothing(t *testing.T) {
	fx, billing, _ := twoConnections(t)
	fx.s.OpenObject(billing.ID, itemsNode)
	pump(t, fx.q, func() bool { return len(fx.s.open) == 1 })
	fx.q.Run(func() { fx.s.keepWorkspace("Billing", []string{billing.ID}) })
	pump(t, fx.q, func() bool { return len(workspaces(fx)) == 1 })

	tb, here := fx.s.open[0], fx.s.workspace
	fx.q.Run(func() { fx.s.switchWorkspace(here) })
	fx.q.Flush()
	// The tab itself, not one that looks like it: closing a tab stops it,
	// and this one was never closed.
	if tb.ctx.Err() != nil || len(fx.s.open) != 1 || fx.s.open[0] != tb {
		t.Errorf("%d tabs open after going nowhere, and this one stopped: %v", len(fx.s.open), tb.ctx.Err())
	}
}

// Leaving a workspace shows the folders it had emptied again: they are
// shelves somebody made, and only the workspace was hiding them.
func TestLeavingAWorkspaceShowsTheFoldersAgain(t *testing.T) {
	fx, billing, _ := twoConnections(t)
	later := inFolder(t, fx, "Later", "")
	fx.q.Run(func() { fx.s.keepWorkspace("Billing", []string{billing.ID}) })
	pump(t, fx.q, func() bool {
		kids := fx.s.Explorer.Model.Children(explorer.RootID)
		return len(kids) == 1 && kids[0] == view.ConnectionID(billing.ID)
	})
	fx.q.Run(func() { fx.s.switchWorkspace(localdb.Workspace{}) })
	pump(t, fx.q, func() bool {
		kids := fx.s.Explorer.Model.Children(explorer.RootID)
		return len(kids) == 3 && kids[0] == view.FolderID(later.ID)
	})
}

// A workspace remembers the tabs as they are on the way out of it, not as
// they were when it was saved.
func TestAWorkspaceRemembersTheTabsAsTheyAre(t *testing.T) {
	fx, billing, _ := twoConnections(t)
	fx.s.OpenObject(billing.ID, itemsNode)
	pump(t, fx.q, func() bool { return len(fx.s.open) == 1 })
	fx.q.Run(func() { fx.s.keepWorkspace("Billing", []string{billing.ID}) })
	pump(t, fx.q, func() bool { return len(workspaces(fx)) == 1 })

	fx.s.OpenQuery(billing.ID) // a second tab, after it was saved
	pump(t, fx.q, func() bool { return len(fx.s.open) == 2 })
	fx.q.Run(func() { fx.s.switchWorkspace(localdb.Workspace{}) })
	pump(t, fx.q, func() bool {
		saved := workspaces(fx)
		return len(saved) == 1 && len(saved[0].Tabs.Tabs) == 2
	})
}

// Leaving a workspace closes its tabs, and unsaved query text goes on
// being kept: the application is closing them, not the user.
func TestLeavingAWorkspaceKeepsUnsavedText(t *testing.T) {
	fx := newFixture(t)
	tb, q := openQuery(t, fx, "")
	test.Type(q.editor.Focusable(), "rows 3;")
	pump(t, fx.q, func() bool { l := scratches(fx); return len(l) == 1 && l[0].Body == "rows 3;" })
	fx.q.Run(func() { fx.s.keepWorkspace("Billing", []string{tb.connID}) })
	var saved []localdb.Workspace
	pump(t, fx.q, func() bool { saved = workspaces(fx); return len(saved) == 1 })

	fx.q.Run(func() { fx.s.switchWorkspace(localdb.Workspace{}) })
	pump(t, fx.q, func() bool { return len(fx.s.open) == 0 })
	if l := scratches(fx); len(l) != 1 || l[0].Body != "rows 3;" {
		t.Fatalf("the unsaved text was thrown away: %+v", l)
	}
	fx.q.Run(func() { fx.s.switchWorkspace(saved[0]) })
	pump(t, fx.q, func() bool { return len(fx.s.open) == 1 && fx.s.open[0].query != nil })
	if got := fx.s.open[0].query.editor.Document().Text(); got != "rows 3;" {
		t.Errorf("the query came back holding %q", got)
	}
}

// The saved queries panel shows the workspace's: the ones on its
// connections, and the ones on no connection, which are in every one.
func TestAWorkspaceNarrowsTheSavedQueries(t *testing.T) {
	fx, billing, reporting := twoConnections(t)
	ctx := context.Background()
	for _, q := range []localdb.SavedQuery{
		{Name: "invoices", ConnectionID: billing.ID, Body: "rows 1;"},
		{Name: "revenue", ConnectionID: reporting.ID, Body: "rows 1;"},
		{Name: "anywhere", Body: "rows 1;"},
	} {
		if _, err := fx.hist.SaveQuery(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	fx.q.Run(func() { fx.s.keepWorkspace("Billing", []string{billing.ID}) })
	p := fx.s.showSaved()
	pump(t, fx.q, func() bool { return p.loaded })
	var names []string
	for _, q := range p.all {
		names = append(names, q.Name)
	}
	if len(names) != 2 || names[0] != "anywhere" || names[1] != "invoices" {
		t.Errorf("the panel lists %v", names)
	}
	// And the panel open while the workspace changes is not left showing
	// the last one's.
	fx.q.Run(func() { fx.s.switchWorkspace(localdb.Workspace{}) })
	pump(t, fx.q, func() bool { return len(p.all) == 3 })
}

// A connection made inside a workspace is part of it. Without that it
// would be made and then not be there.
func TestAConnectionMadeInAWorkspaceIsInIt(t *testing.T) {
	fx, billing, _ := twoConnections(t)
	fx.q.Run(func() { fx.s.keepWorkspace("Billing", []string{billing.ID}) })
	pump(t, fx.q, func() bool { return len(fx.s.Explorer.Model.Children(explorer.RootID)) == 1 })
	made := fx.create(t, "db3", nil)
	fx.q.Run(func() { fx.s.connectionSaved(made.ID, false) })
	pump(t, fx.q, func() bool { return len(fx.s.Explorer.Model.Children(explorer.RootID)) == 2 })
	if !fx.s.workspace.Holds(made.ID) || len(fx.s.workspace.Connections) != 2 {
		t.Errorf("the new connection is not in the workspace: %v", fx.s.workspace.Connections)
	}
	// Saved again, it joins no second time.
	fx.q.Run(func() { fx.s.connectionSaved(made.ID, false) })
	fx.q.Flush()
	if len(fx.s.workspace.Connections) != 2 {
		t.Errorf("the connection joined twice: %v", fx.s.workspace.Connections)
	}
}

// A workspace over every connection stays over every connection: a new
// one must not be the one thing it is suddenly narrowed to.
func TestAConnectionMadeInAWorkspaceOverEverything(t *testing.T) {
	fx, _, _ := twoConnections(t)
	fx.q.Run(func() { fx.s.keepWorkspace("Everything here", nil) })
	pump(t, fx.q, func() bool { return fx.s.workspace.Name == "Everything here" })
	made := fx.create(t, "db3", nil)
	fx.q.Run(func() { fx.s.connectionSaved(made.ID, false) })
	pump(t, fx.q, func() bool { return len(fx.s.Explorer.Model.Children(explorer.RootID)) == 3 })
	if len(fx.s.workspace.Connections) != 0 {
		t.Errorf("the workspace narrowed itself to %v", fx.s.workspace.Connections)
	}
}

// Forgetting a workspace loses nothing it grouped, and widens the window
// it was in back to everything.
func TestForgettingAWorkspaceLosesNothing(t *testing.T) {
	fx, billing, _ := twoConnections(t)
	ctx := context.Background()
	if _, err := fx.hist.SaveQuery(ctx, localdb.SavedQuery{Name: "invoices", ConnectionID: billing.ID, Body: "rows 1;"}); err != nil {
		t.Fatal(err)
	}
	fx.q.Run(func() { fx.s.keepWorkspace("Billing", []string{billing.ID}) })
	var saved []localdb.Workspace
	pump(t, fx.q, func() bool { saved = workspaces(fx); return len(saved) == 1 })
	pump(t, fx.q, func() bool { return len(fx.s.Explorer.Model.Children(explorer.RootID)) == 1 })

	fx.q.Run(func() { fx.s.forgetWorkspace(saved[0]) })
	pump(t, fx.q, func() bool { return len(workspaces(fx)) == 0 })
	if len(fx.conns.List()) != 2 {
		t.Errorf("forgetting the workspace took its connections with it: %d left", len(fx.conns.List()))
	}
	qs, err := fx.hist.SavedQueries(ctx)
	if err != nil || len(qs) != 1 {
		t.Errorf("forgetting the workspace took its saved queries with it: %d left (%v)", len(qs), err)
	}
	pump(t, fx.q, func() bool { return len(fx.s.Explorer.Model.Children(explorer.RootID)) == 2 })
}

// The window comes back in the workspace it was left in (NFR-R3).
func TestAWindowComesBackInItsWorkspace(t *testing.T) {
	fx, billing, _ := twoConnections(t)
	fx.q.Run(func() { fx.s.keepWorkspace("Billing", []string{billing.ID}) })
	pump(t, fx.q, func() bool { return len(workspaces(fx)) == 1 })
	fx.q.Run(fx.s.saveSession)
	pump(t, fx.q, func() bool {
		ss, _, _ := fx.hist.Session(context.Background())
		return ss.Workspace != ""
	})

	s := fx.relaunch(t)
	pump(t, fx.q, func() bool { return s.workspace.Name == "Billing" })
	if got := s.win.Title(); !strings.HasSuffix(got, "Billing") {
		t.Errorf("the window came back called %q", got)
	}
	pump(t, fx.q, func() bool { return len(s.Explorer.Model.Children(explorer.RootID)) == 1 })
}

// Going to a workspace opens the tabs it was left at, and not every
// buffer of unsaved text a crash has left lying about.
func TestGoingToAWorkspaceOpensItsTabsAlone(t *testing.T) {
	fx := newFixture(t)
	tb, q := openQuery(t, fx, "")
	test.Type(q.editor.Focusable(), "rows 3;")
	pump(t, fx.q, func() bool { return len(scratches(fx)) == 1 })
	fx.q.Run(func() { fx.s.keepWorkspace("Billing", []string{tb.connID}) })
	var saved []localdb.Workspace
	pump(t, fx.q, func() bool { saved = workspaces(fx); return len(saved) == 1 })

	fx.q.Run(func() { fx.s.switchWorkspace(localdb.Workspace{}) })
	pump(t, fx.q, func() bool { return len(fx.s.open) == 0 })
	// Text in no workspace, typed after leaving that one.
	fx.s.OpenQuery(tb.connID)
	other := fx.onlyTab(t)
	pump(t, fx.q, func() bool { return other.query.session != nil })
	test.Type(other.query.editor.Focusable(), "rows 1;")
	pump(t, fx.q, func() bool { return len(scratches(fx)) == 2 })

	// Wait for the workspace's own tab, not for any one tab: the tab open
	// now is one too, and waiting for a count would be waiting for what is
	// already true.
	fx.q.Run(func() { fx.s.switchWorkspace(saved[0]) })
	pump(t, fx.q, func() bool {
		return len(fx.s.open) == 1 && fx.s.open[0].query != nil &&
			fx.s.open[0].query.editor.Document().Text() == "rows 3;"
	})
	fx.q.Flush()
	if n := len(fx.s.open); n != 1 {
		t.Fatalf("%d tabs opened for a workspace left with one", n)
	}
}

// The window remembers, as it closes, the tabs the workspace was left at.
func TestClosingTheWindowRemembersTheWorkspace(t *testing.T) {
	fx, billing, _ := twoConnections(t)
	fx.q.Run(func() { fx.s.keepWorkspace("Billing", []string{billing.ID}) })
	pump(t, fx.q, func() bool { return len(workspaces(fx)) == 1 })
	fx.s.OpenObject(billing.ID, itemsNode)
	pump(t, fx.q, func() bool { return len(fx.s.open) == 1 })

	fx.s.shutdown()
	saved := workspaces(fx)
	if len(saved) != 1 || len(saved[0].Tabs.Tabs) != 1 {
		t.Fatalf("the workspace was left at %+v", saved)
	}
	if saved[0].Tabs.Tabs[0].ConnectionID != billing.ID {
		t.Errorf("it remembers a tab on %q", saved[0].Tabs.Tabs[0].ConnectionID)
	}
}

// A workspace in the list says what it is over and what it was left at, by
// the names somebody would look for.
func TestAWorkspaceSaysWhatItIsOver(t *testing.T) {
	fx, billing, _ := twoConnections(t)
	w := localdb.Workspace{ID: "w1", Name: "Billing", Connections: []string{billing.ID},
		Tabs: localdb.Session{Tabs: []localdb.SessionTab{{Kind: localdb.SessionObject}}}}
	if got := describeWorkspace(fx.s, w); got != "db1 · 1 tab" {
		t.Errorf("it reads %q", got)
	}
	if got := describeWorkspace(fx.s, localdb.Workspace{ID: "w2", Name: "All"}); got != "every connection" {
		t.Errorf("a workspace over nothing in particular reads %q", got)
	}
	gone := localdb.Workspace{ID: "w3", Name: "Old", Connections: []string{"deleted"}}
	if got := describeWorkspace(fx.s, gone); !strings.Contains(got, "none of them still saved") {
		t.Errorf("a workspace whose connections have gone reads %q", got)
	}
}
