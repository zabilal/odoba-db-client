package shell

import (
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/ui/commands"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer/view"
)

// A lost connection is said where it is used (FR-1.15, ADR-0011 §21). The
// monitor (app.Live) sees a connection stop answering and tries it again,
// backing off. When it does, the connection's row in the explorer says
// Disconnected, each of its tabs has a band saying when it was lost, why and
// that it is being tried again, and Reconnect Now tries it at once. All of it
// goes when the connection comes back.

const cmdReconnect = "connection.reconnect"

func (s *Shell) lostCommands() []commands.Command {
	return []commands.Command{{ID: cmdReconnect, Category: "Connection", Title: "Reconnect Now",
		Keywords: []string{"retry", "connect", "lost", "offline"},
		Enabled:  func() bool { _, ok := s.selectedLost(); return ok },
		Run: func() {
			if id, ok := s.selectedLost(); ok {
				s.reconnect(id)
			}
		}}}
}

// selectedLost is the connection in focus, if it is open and lost.
func (s *Shell) selectedLost() (string, bool) {
	id, ok := s.selectedConn()
	if !ok {
		return "", false
	}
	live, open := s.d.WS.Get(id)
	return id, open && live.Status().State == app.StateDisconnected
}

// reconnect tries a lost connection now, rather than at its next try.
func (s *Shell) reconnect(id string) {
	if live, open := s.d.WS.Get(id); open {
		live.Check()
	}
}

// connectionStatus follows a change in a connection's health: its row, its
// tabs' bands and the menus say so at once.
func (s *Shell) connectionStatus(id string) {
	s.Explorer.Redraw(view.ConnectionID(id))
	for _, t := range s.open {
		if t.connID == id && t.lost != nil {
			t.lost.update()
		}
	}
	s.sync()
}

// lostText is what a lost connection's band says, or "" for one that is well.
func lostText(st app.Status) string {
	if st.State != app.StateDisconnected {
		return ""
	}
	s := "The connection was lost at " + st.Since.Format("15:04")
	if m := st.Message(); m != "" {
		s += ": " + m
	}
	attempts := "1 attempt"
	if st.Attempt != 1 {
		attempts = fmt.Sprintf("%d attempts", st.Attempt)
	}
	return s + ". Trying again automatically; " + attempts + " so far."
}

// lostBand is across the top of a tab whose connection was lost.
type lostBand struct {
	widget.BaseWidget
	s   *Shell
	t   *tab
	msg *widget.Label
	now *widget.Button
}

func newLostBand(s *Shell, t *tab) *lostBand {
	b := &lostBand{s: s, t: t, msg: widget.NewLabel("")}
	b.msg.Wrapping = fyne.TextWrapWord
	b.msg.Importance = widget.WarningImportance
	b.now = widget.NewButton("Reconnect Now", func() { s.reconnect(t.connID) })
	b.ExtendBaseWidget(b)
	b.update()
	return b
}

func (b *lostBand) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewBorder(nil, nil, nil, container.NewCenter(b.now), b.msg))
}

// text is what the band says: nothing while the connection is well, or not
// open, as for a tab that failed to connect, which says so itself.
func (b *lostBand) text() string {
	live, open := b.s.d.WS.Get(b.t.connID)
	if !open {
		return ""
	}
	return lostText(live.Status())
}

// update shows the band while the connection is lost, saying so.
func (b *lostBand) update() {
	text := b.text()
	b.msg.SetText(text)
	setShown(b, text != "")
	b.Refresh()
}
