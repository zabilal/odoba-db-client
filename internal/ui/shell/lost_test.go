package shell

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer/view"
)

// pingFails makes the fake stop answering its monitor's checks.
var pingFails atomic.Bool

func pingErr() error {
	if pingFails.Load() {
		return errors.New("server closed the connection")
	}
	return nil
}

func TestALostConnectionIsSaidUntilItIsBack(t *testing.T) {
	fx, tb := openItems(t)
	t.Cleanup(func() { pingFails.Store(false) })
	fx.s.Explorer.Refresh(explorer.RootID)
	loaded(t, fx, explorer.RootID)
	fx.s.Explorer.Tree.Select(view.ConnectionID(tb.connID))
	if tb.lost.Visible() || !fx.s.menuItems[cmdReconnect].Disabled {
		t.Fatal("a connection that is well has nothing to reconnect")
	}
	live, _ := fx.ws.Get(tb.connID)
	pingFails.Store(true)
	live.Check()
	pump(t, fx.q, func() bool { return tb.lost.Visible() })
	text := tb.lost.msg.Text
	for _, want := range []string{"The connection was lost at ", "server closed the connection", "Trying again automatically; 1 attempt so far."} {
		if !strings.Contains(text, want) {
			t.Errorf("the band says %q, without %q", text, want)
		}
	}
	if got := drawn(fx.s.Explorer.Tree); !has(got, "Disconnected") {
		t.Errorf("the explorer draws %q; the connection's row should say Disconnected", got)
	}
	if fx.s.menuItems[cmdReconnect].Disabled {
		t.Error("a lost connection can be tried again now")
	}
	inTab := false // the bands sit in a box at the top of the tab's content
	for _, o := range tb.item.Content.(*fyne.Container).Objects {
		inTab = inTab || holds(o, tb.lost)
	}
	if !inTab {
		t.Error("the band should be at the top of the tab's content")
	}
	pingFails.Store(false)
	start := time.Now()
	test.Tap(findButton(tb.lost, "Reconnect Now"))
	pump(t, fx.q, func() bool { return !tb.lost.Visible() })
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Errorf("back after %v; Reconnect Now should try at once, not at the next try", d)
	}
	if got := drawn(fx.s.Explorer.Tree); has(got, "Disconnected") {
		t.Errorf("the explorer still draws %q", got)
	}
	if !fx.s.menuItems[cmdReconnect].Disabled {
		t.Error("back, there is nothing to reconnect")
	}
}

func TestReconnectNowTriesAtOnce(t *testing.T) {
	fx, tb := openItems(t)
	t.Cleanup(func() { pingFails.Store(false) })
	live, _ := fx.ws.Get(tb.connID)
	pingFails.Store(true)
	live.Check()
	pump(t, fx.q, func() bool { return tb.lost.Visible() })
	pingFails.Store(false)
	start := time.Now()
	fx.s.run(cmdReconnect) // the connection in focus is the tab's
	pump(t, fx.q, func() bool { return !tb.lost.Visible() })
	if d := time.Since(start); d > 500*time.Millisecond || live.Status().State != app.StateConnected {
		t.Errorf("after Reconnect Now the connection is %s after %v; its next try was a second away", live.Status().State, d)
	}
	var menu []string
	for _, m := range fx.s.menu.Items {
		if m.Label == "Connection" {
			for _, it := range m.Items {
				menu = append(menu, it.Label)
			}
		}
	}
	var conn []string
	for _, it := range fx.s.explorerMenu(view.ConnectionID(tb.connID)).Items {
		conn = append(conn, it.Label)
	}
	if !has(menu, "Reconnect Now") || !has(conn, "Reconnect Now") {
		t.Errorf("Reconnect Now should be in the Connection menu %q and a connection's own %q", menu, conn)
	}
}

func TestTheLostBandSaysWhenWhyAndHowOften(t *testing.T) {
	at := time.Date(2026, 9, 11, 14, 2, 0, 0, time.Local)
	for _, c := range []struct {
		st   app.Status
		want string
	}{
		{app.Status{State: app.StateConnected}, ""},
		{app.Status{State: app.StateClosed}, ""},
		{app.Status{State: app.StateDisconnected, Since: at, Attempt: 1, Err: errors.New("refused")},
			"The connection was lost at 14:02: refused. Trying again automatically; 1 attempt so far."},
		{app.Status{State: app.StateDisconnected, Since: at, Attempt: 3},
			"The connection was lost at 14:02. Trying again automatically; 3 attempts so far."},
	} {
		if got := lostText(c.st); got != c.want {
			t.Errorf("%+v: %q, want %q", c.st, got, c.want)
		}
	}
}
