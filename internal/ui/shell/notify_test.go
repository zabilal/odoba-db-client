package shell

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"fyne.io/fyne/v2"

	"github.com/ikigai-db/ikigai-db/internal/export"
	"github.com/ikigai-db/ikigai-db/internal/model"
)

// notedApp is the test app, keeping every notification sent.
type notedApp struct {
	fyne.App
	mu   sync.Mutex
	sent []fyne.Notification
}

func (a *notedApp) SendNotification(n *fyne.Notification) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sent = append(a.sent, *n)
}

func (a *notedApp) notes() []fyne.Notification {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]fyne.Notification(nil), a.sent...)
}

// inBackground puts the app behind the others, or back in front, as the
// driver's lifecycle does when the window loses or gains the focus.
func inBackground(t *testing.T, fx *fixture, away bool) {
	t.Helper()
	lc, ok := fx.s.app.Lifecycle().(interface {
		OnEnteredForeground() func()
		OnExitedForeground() func()
	})
	if !ok {
		t.Fatal("the test app's lifecycle cannot be driven")
	}
	hook := lc.OnEnteredForeground()
	if away {
		hook = lc.OnExitedForeground()
	}
	if hook == nil {
		t.Fatal("the shell set no lifecycle hook")
	}
	hook()
}

// waitingRows sends nothing until the export is stopped.
type waitingRows struct{}

func (waitingRows) Columns() []model.ColumnDef { return []model.ColumnDef{{Name: "n"}} }
func (waitingRows) Close() error               { return nil }
func (waitingRows) Next(ctx context.Context) (model.Row, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func exportAll(t *testing.T, fx *fixture, tb *tab, src *exportSrc, dest string, stop bool) {
	t.Helper()
	j := fx.s.runExport(tb, src, export.Options{Format: export.CSV}, &sink{}, dest, nil)
	if stop {
		fx.s.stopTask(j.task)
	}
	pump(t, fx.q, func() bool { return j.done })
}

func onlyNote(t *testing.T, fx *fixture) fyne.Notification {
	t.Helper()
	got := fx.app.notes()
	if len(got) != 1 {
		t.Fatalf("%d notifications, want 1: %+v", len(got), got)
	}
	return got[0]
}

func TestAnExportEndedInTheBackgroundIsNotified(t *testing.T) {
	fx, tb := openItems(t)
	inBackground(t, fx, true)
	exportAll(t, fx, tb, fx.s.exportSource(), "items.csv", false)
	if n := onlyNote(t, fx); n.Title != "Export to items.csv" || n.Content != "Exported 250 rows to items.csv" {
		t.Errorf("notified %q: %q", n.Title, n.Content)
	}
}

func TestWorkEndedInFrontIsNotNotified(t *testing.T) {
	fx, tb := openItems(t)
	exportAll(t, fx, tb, fx.s.exportSource(), "a.csv", false)
	inBackground(t, fx, true)
	inBackground(t, fx, false)
	exportAll(t, fx, tb, fx.s.exportSource(), "b.csv", false)
	if got := fx.app.notes(); len(got) != 0 {
		t.Errorf("the window says it already; notified %+v", got)
	}
}

func TestAStoppedExportIsNotNotified(t *testing.T) {
	fx, tb := openItems(t)
	inBackground(t, fx, true)
	exportAll(t, fx, tb, &exportSrc{name: "w", total: -1, rows: func() model.RowStream { return waitingRows{} }}, "w.csv", true)
	if got := fx.app.notes(); len(got) != 0 {
		t.Errorf("whoever stopped it knows; notified %+v", got)
	}
}

func TestAFailedExportIsNotifiedWithoutItsError(t *testing.T) {
	fx, tb := openItems(t)
	inBackground(t, fx, true)
	exportAll(t, fx, tb, &exportSrc{name: "b", total: -1, rows: func() model.RowStream { return brokenRows{} }}, "b.csv", false)
	n := onlyNote(t, fx)
	if n.Title != "Export to b.csv" || n.Content != "Failed. The Tasks panel says why." {
		t.Errorf("notified %q: %q", n.Title, n.Content)
	}
}

// runInBackground runs a script in a query tab, the app in the background
// if away, with notifyAfter set to after.
func runInBackground(t *testing.T, script string, away bool, after time.Duration, stop bool) (*fixture, *tab) {
	t.Helper()
	was := notifyAfter
	notifyAfter = after
	t.Cleanup(func() { notifyAfter = was })
	fx := newFixture(t)
	tb, q := openQuery(t, fx, "")
	if away {
		inBackground(t, fx, true)
	}
	q.editor.Document().SetText(script)
	began := waiting.Load()
	fx.s.run(cmdQueryRunAll)
	if stop {
		// Stopped inside the script, not after it: rows still arriving once
		// it has ended are no part of the run.
		pump(t, fx.q, func() bool { return waiting.Load() > began })
		fx.s.run(cmdQueryStop)
	}
	pump(t, fx.q, func() bool { return !q.executing })
	return fx, tb
}

func TestALongQueryEndedInTheBackgroundIsNotified(t *testing.T) {
	fx, tb := runInBackground(t, "rows 1; update t;", true, 0, false)
	n := onlyNote(t, fx)
	if n.Title != tb.item.Text || n.Content != tb.footer.Text || !strings.HasPrefix(n.Content, "Ran 2 statements") {
		t.Errorf("notified %q: %q; the tab is %q, its footer %q", n.Title, n.Content, tb.item.Text, tb.footer.Text)
	}
	if strings.Contains(n.Content, "update") {
		t.Error("a notification must not carry the statement")
	}
}

func TestAQueryIsNotNotifiedInFrontQuickOrStopped(t *testing.T) {
	for name, c := range map[string]struct {
		script      string
		away, stop  bool
		notifyAfter time.Duration
	}{
		"in front": {"rows 1;", false, false, 0},
		"quick":    {"rows 1;", true, false, time.Hour},
		"stopped":  {"rows 1; wait;", true, true, 0},
	} {
		t.Run(name, func(t *testing.T) {
			fx, _ := runInBackground(t, c.script, c.away, c.notifyAfter, c.stop)
			if got := fx.app.notes(); len(got) != 0 {
				t.Errorf("notified %+v", got)
			}
		})
	}
}
