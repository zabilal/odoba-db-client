package shell

import (
	"context"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/store/localdb"
)

// Multiple windows (FR-15.8): another window over the same connections.

// A second window is over the same connections, and is its own window.
func TestASecondWindowIsOverTheSameConnections(t *testing.T) {
	fx := newFixture(t)
	c := fx.create(t, "db1", nil)

	second := fx.s.NewWindow()
	t.Cleanup(second.shutdown)
	if second == fx.s {
		t.Fatal("a second window is the first")
	}
	if second.win == fx.s.win {
		t.Error("both windows are the same window")
	}
	// The same connections, not a copy of them: a connection saved in one
	// window is there in the other.
	if second.d.Conns != fx.s.d.Conns || second.d.WS != fx.s.d.WS {
		t.Error("a second window has connections of its own")
	}
	if _, ok := second.d.Conns.Get(c.ID); !ok {
		t.Error("the connection saved before it opened is not in it")
	}
	// And the same saved queries and history, which belong to the person
	// rather than to a window.
	if second.d.Saved != fx.s.d.Saved || second.d.History != fx.s.d.History {
		t.Error("a second window keeps its own saved queries or history")
	}
}

// One window keeps the session, and it is the first. A second saves
// nothing about itself: what a session restores is a window, and two
// windows restoring it would be the same work shown twice.
func TestOnlyTheFirstWindowKeepsTheSession(t *testing.T) {
	fx := newFixture(t)
	c := fx.create(t, "db1", nil)
	fx.s.OpenObject(c.ID, itemsNode)
	tb := fx.onlyTab(t)
	pump(t, fx.q, func() bool { return tb.browse != nil })
	fx.q.Run(fx.s.saveSession)

	before, ok, err := fx.hist.Session(context.Background())
	if err != nil || !ok {
		t.Fatalf("the first window kept no session: %v", err)
	}

	second := fx.s.NewWindow()
	t.Cleanup(second.shutdown)
	if second.d.Session != nil {
		t.Error("a second window was given the session to keep")
	}
	// It opens empty rather than with the first window's tabs.
	if len(second.open) != 0 {
		t.Errorf("a second window opened with %d tabs", len(second.open))
	}
	// And shutting it down leaves the session as the first window left it.
	second.shutdown()
	after, _, err := fx.hist.Session(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !sameTabs(before, after) {
		t.Errorf("closing a second window changed the session: %d tabs became %d",
			len(before.Tabs), len(after.Tabs))
	}
}

// Closing a second window closes it alone. The connections it was reading
// belong to the application, and the first window is still reading them.
func TestClosingASecondWindowLeavesTheConnections(t *testing.T) {
	fx := newFixture(t)
	c := fx.create(t, "db1", nil)
	fx.s.OpenObject(c.ID, itemsNode)
	tb := fx.onlyTab(t)
	pump(t, fx.q, func() bool { return tb.browse != nil })

	second := fx.s.NewWindow()
	second.shutdown()

	// The first window's tab still reads its rows.
	if _, err := tb.model.Read(context.Background(), 0, 1); err != nil {
		t.Errorf("the first window lost its connection: %v", err)
	}
	if _, open := fx.s.d.WS.Get(c.ID); !open {
		t.Error("closing a second window closed the connection")
	}
}

// New Window is always offered: it needs nothing to be selected and no
// connection to be open.
func TestNewWindowIsAlwaysOffered(t *testing.T) {
	fx := newFixture(t)
	cmd, ok := fx.s.Commands().Get(cmdNewWindow)
	if !ok {
		t.Fatal("there is no New Window command")
	}
	if cmd.Enabled != nil && !cmd.Enabled() {
		t.Error("New Window is not offered on an empty window")
	}
}

func sameTabs(a, b localdb.Session) bool {
	if len(a.Tabs) != len(b.Tabs) {
		return false
	}
	for i := range a.Tabs {
		if a.Tabs[i].Label != b.Tabs[i].Label {
			return false
		}
	}
	return true
}
