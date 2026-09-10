package shell

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/store"
	"github.com/ikigai-db/ikigai-db/internal/store/localdb"
	"github.com/ikigai-db/ikigai-db/internal/store/secrets"
	"github.com/ikigai-db/ikigai-db/internal/ui/uithread"
)

func searchHistory(fx *fixture, text string) []localdb.HistoryEntry {
	got, _ := fx.hist.SearchHistory(context.Background(), localdb.HistoryQuery{Text: text})
	return got
}

func TestRunningAStatementRecordsIt(t *testing.T) {
	fx := newFixture(t)
	tb, q := openQuery(t, fx, "")
	q.editor.Document().SetText("rows 2;")
	fx.s.run(cmdQueryRun)
	var got []localdb.HistoryEntry
	pump(t, fx.q, func() bool { got = searchHistory(fx, "rows"); return len(got) == 1 })
	if e := got[0]; e.Statement != "rows 2;" || e.Rows != 2 || e.ConnectionID != tb.connID {
		t.Errorf("recorded %+v", e)
	}
}

func TestHistoryFindsAndReopensAStatement(t *testing.T) {
	fx := newFixture(t)
	_, q := openQuery(t, fx, "")
	q.editor.Document().SetText("rows 7;")
	fx.s.run(cmdQueryRun)
	pump(t, fx.q, func() bool { return len(searchHistory(fx, "")) == 1 })

	p := fx.s.showHistory()
	test.Type(p.search, "row")
	pump(t, fx.q, func() bool { return len(p.entries) == 1 && p.search.Text == "row" })
	p.list.Select(0)
	if len(fx.s.open) != 2 {
		t.Fatalf("%d tabs; the entry should open in a new query tab", len(fx.s.open))
	}
	if got := fx.s.open[1].query.editor.Document().Text(); got != "rows 7;" {
		t.Errorf("reopened %q", got)
	}
	if fx.s.win.Canvas().Overlays().Top() != nil {
		t.Error("the history panel should close once an entry opens")
	}
}

func TestHistorySearchSaysWhenNothingMatches(t *testing.T) {
	fx := newFixture(t)
	p := fx.s.showHistory()
	pump(t, fx.q, func() bool { return p.status.Text != "" })
	if p.status.Text != "Nothing has been run yet." {
		t.Errorf("empty history says %q", p.status.Text)
	}
	test.Type(p.search, "zzz")
	pump(t, fx.q, func() bool { return p.status.Text == "No statements match." })
}

func TestHistoryIsOffWithoutAStore(t *testing.T) {
	a := test.NewTempApp(t)
	sf, _, err := store.OpenSettings(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	conns := app.NewConnections(sf, app.NewVault(secrets.NewMemory(), nil), nil)
	ws := app.NewWorkspace(conns, app.MonitorConfig{Interval: time.Hour})
	s := New(a, Deps{Conns: conns, WS: ws, Run: (&uithread.Queue{}).Run})
	t.Cleanup(s.shutdown)
	if !s.menuItems[cmdHistory].Disabled || s.showHistory() != nil {
		t.Error("with no history store, Query History should be unavailable")
	}
}

func TestAgoWording(t *testing.T) {
	now := time.Date(2026, 9, 10, 15, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		t    time.Time
		want string
	}{
		{now.Add(-10 * time.Second), "just now"},
		{now.Add(-5 * time.Minute), "5 min ago"},
		{now.Add(-3 * time.Hour), "12:00"},
		{now.Add(-3 * 24 * time.Hour), "Mon 15:00"},
		{now.Add(-40 * 24 * time.Hour), "1 Aug 2026"},
	} {
		if got := ago(c.t, now); got != c.want {
			t.Errorf("ago(%v) = %q, want %q", c.t, got, c.want)
		}
	}
}
