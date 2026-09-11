package shell

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2/test"
)

// A driver that panics fails what it was doing; the window stays up, and the
// connection can be disconnected and opened afresh (NFR-R1).
func TestADriversPanicIsSaidAndItsConnectionCanBeDropped(t *testing.T) {
	fx, tb := openItems(t)
	b := showWhere(t, fx, tb)
	b.entry.SetText("panic")
	b.apply()
	pump(t, fx.q, func() bool { return fx.s.errors.shown() && fx.s.errors.action.Visible() })
	if msg := fx.s.errors.message.Text; !strings.Contains(msg, "the driver failed while opening the rows") {
		t.Errorf("the band says %q", msg)
	}
	if fx.s.errors.action.Text != "Disconnect" {
		t.Errorf("the band offers %q", fx.s.errors.action.Text)
	}
	test.Tap(fx.s.errors.action)
	if len(fx.s.open) != 0 {
		t.Error("disconnecting should close the connection's tabs")
	}
	if _, open := fx.ws.Get(tb.connID); open {
		t.Error("the connection should be closed, to open afresh")
	}
}
