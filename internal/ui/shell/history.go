package shell

import (
	"context"
	"fmt"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/store/localdb"
	"github.com/ikigai-db/ikigai-db/internal/ui/uithread"
)

// historyLimit bounds one search. The panel is for finding, and the latest
// few hundred matches are more than anyone scrolls through.
const historyLimit = 200

// historySearchDelay lets a burst of typing become one search.
const historySearchDelay = 150 * time.Millisecond

// historyPanel is the searchable record of what has been run (FR-5.8), and
// how J3 finds last week's query: type a few words of it, open it again.
type historyPanel struct {
	s       *Shell
	search  *widget.Entry
	list    *widget.List
	status  *widget.Label
	entries []localdb.HistoryEntry
	dlg     *dialog.CustomDialog
	gen     int // drops a search that a newer one overtook
	load    func()
	now     func() time.Time
}

func (s *Shell) showHistory() *historyPanel {
	if s.d.History == nil {
		return nil
	}
	p := &historyPanel{s: s, search: widget.NewEntry(), status: widget.NewLabel(""), now: time.Now}
	p.search.SetPlaceHolder("Search statements")
	p.status.Importance = widget.LowImportance
	p.list = widget.NewList(
		func() int { return len(p.entries) },
		func() fyne.CanvasObject {
			stmt := widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Monospace: true})
			stmt.Truncation = fyne.TextTruncateEllipsis
			meta := widget.NewLabel("")
			meta.Truncation = fyne.TextTruncateEllipsis
			return container.NewVBox(stmt, meta)
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			if i >= len(p.entries) {
				return
			}
			e, box := p.entries[i], o.(*fyne.Container)
			stmt, meta := box.Objects[0].(*widget.Label), box.Objects[1].(*widget.Label)
			stmt.SetText(firstLine(e.Statement))
			meta.Importance = widget.LowImportance
			if e.Error != "" {
				meta.Importance = widget.DangerImportance
			}
			meta.SetText(p.describe(e))
		})
	p.list.OnSelected = func(i widget.ListItemID) {
		if i < len(p.entries) {
			p.open(p.entries[i])
		}
	}
	delay := historySearchDelay
	if s.d.Delay == 0 {
		delay = 0 // tests run without delays (Deps)
	}
	p.load = uithread.Coalesce(s.d.Run, delay, p.query)
	p.search.OnChanged = func(string) { p.load() }
	p.search.OnSubmitted = func(string) {
		if len(p.entries) > 0 {
			p.open(p.entries[0])
		}
	}
	p.dlg = dialog.NewCustom("Query History", "Close", container.NewBorder(p.search, p.status, nil, nil, p.list), s.win)
	p.dlg.Resize(fyne.NewSize(720, 480))
	p.dlg.Show()
	s.win.Canvas().Focus(p.search)
	p.query()
	return p
}

// query searches off the UI goroutine. Called on it, so gen needs no lock.
func (p *historyPanel) query() {
	p.gen++
	gen, text, hist := p.gen, p.search.Text, p.s.d.History
	go func() {
		ctx, cancel := context.WithTimeout(p.s.ctx, 5*time.Second)
		defer cancel()
		entries, err := hist.SearchHistory(ctx, localdb.HistoryQuery{Text: text, Limit: historyLimit})
		p.s.d.Run(func() {
			if gen != p.gen {
				return // typing "or" then "ord" must not end on the list for "or"
			}
			if err != nil {
				p.status.SetText("Could not search history: " + err.Error())
				return
			}
			p.entries = entries
			p.list.UnselectAll()
			p.list.Refresh()
			p.status.SetText(historyCount(len(entries), text))
		})
	}()
}

func (p *historyPanel) describe(e localdb.HistoryEntry) string {
	conn := "a deleted connection"
	if c, ok := p.s.d.Conns.Get(e.ConnectionID); ok {
		conn = c.Name
	}
	parts := []string{conn, ago(e.StartedAt, p.now()), took(e.Duration)}
	switch {
	case e.Error != "":
		parts = append(parts, "failed: "+firstLine(e.Error))
	case e.Rows >= 0:
		parts = append(parts, rowCount(e.Rows, true))
	}
	return strings.Join(parts, " · ")
}

// open reopens an entry in a new query tab on the connection it ran on.
// History keeps statements redacted, so a password it held comes back
// masked; that is the point of redacting it.
func (p *historyPanel) open(e localdb.HistoryEntry) {
	if _, ok := p.s.d.Conns.Get(e.ConnectionID); !ok {
		p.status.SetText("The connection this ran on no longer exists.")
		p.list.UnselectAll()
		return
	}
	p.dlg.Hide()
	if t := p.s.OpenQuery(e.ConnectionID); t != nil {
		t.query.editor.Document().SetText(e.Statement)
		t.query.editor.Refresh()
	}
}

func historyCount(n int, text string) string {
	switch {
	case n == 0 && strings.TrimSpace(text) == "":
		return "Nothing has been run yet."
	case n == 0:
		return "No statements match."
	case n >= historyLimit:
		return fmt.Sprintf("The latest %d matches", n)
	case n == 1:
		return "1 statement"
	}
	return fmt.Sprintf("%d statements", n)
}

// firstLine is a statement's first non-blank line, for a one-line list row.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i]) + " …"
	}
	return s
}

// ago words a past time relative to now, as a list of recent things would.
func ago(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 24*time.Hour && t.YearDay() == now.YearDay():
		return t.Format("15:04")
	case d < 7*24*time.Hour:
		return t.Format("Mon 15:04")
	}
	return t.Format("2 Jan 2006")
}
