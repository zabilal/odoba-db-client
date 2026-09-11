package view

import (
	"context"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer"
	"github.com/ikigai-db/ikigai-db/internal/ui/uithread"
)

// fixedStatus is how each open connection is, as a test sets it.
type fixedStatus map[string]app.Status

func (f fixedStatus) Status(id string) (app.Status, bool) {
	st, ok := f[id]
	return st, ok
}

func TestALostConnectionSaysSoOnItsRow(t *testing.T) {
	newApp(t)
	l, saved := setup(t, "prod-db")
	e := New(l, (&uithread.Queue{}).Run, 0)
	e.Model.Children(explorer.RootID)
	root := waitReal(t, e.Model, explorer.RootID)
	r := newNodeRow()
	for _, c := range []struct {
		name   string
		status fixedStatus
		badge  string
	}{
		{"lost", fixedStatus{saved[0].ID: {State: app.StateDisconnected}}, "Disconnected"},
		{"well", fixedStatus{saved[0].ID: {State: app.StateConnected}}, "PROD"},
		{"not open", fixedStatus{}, "PROD"},
	} {
		e.stater = c.status
		e.update(root[0], r)
		if r.badge.Text != c.badge || !r.badge.TextStyle.Bold {
			t.Errorf("%s: the row's badge says %q (bold %v), want %q", c.name, r.badge.Text, r.badge.TextStyle.Bold, c.badge)
		}
	}
}

func TestTheLoaderSaysHowAConnectionIs(t *testing.T) {
	newApp(t)
	l, saved := setup(t, "a")
	if e := New(l, (&uithread.Queue{}).Run, 0); e.stater == nil {
		t.Error("the explorer should ask its loader how connections are")
	}
	if _, open := l.Status(saved[0].ID); open {
		t.Error("a connection not opened has no health to report")
	}
	if _, err := l.WS.Connect(context.Background(), saved[0].ID); err != nil {
		t.Fatal(err)
	}
	if st, open := l.Status(saved[0].ID); !open || st.State != app.StateConnected {
		t.Errorf("an open connection is %+v, open %v", st, open)
	}
}
