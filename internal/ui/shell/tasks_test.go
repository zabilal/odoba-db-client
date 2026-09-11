package shell

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/export"
	"github.com/ikigai-db/ikigai-db/internal/model"
)

// slowExport starts exporting a query result that takes minutes to arrive,
// so the export is still running while a test looks at it.
func slowExport(t *testing.T, fx *fixture, discard func()) (*tab, *exportJob) {
	t.Helper()
	tb, q := openQuery(t, fx, "")
	q.editor.Document().SetText("slow 100000;")
	fx.s.run(cmdQueryRun)
	pump(t, fx.q, func() bool { return len(q.sets) == 1 })
	return tb, fx.s.runExport(tb, fx.s.exportSource(), export.Options{Format: export.NDJSON}, &sink{}, "r.ndjson", discard)
}

func TestAnExportRunsInTheTaskCentreNotADialog(t *testing.T) {
	fx := newFixture(t)
	_, j := slowExport(t, fx, nil)
	if top := fx.s.win.Canvas().Overlays().Top(); top != nil {
		t.Fatal("an export must not put anything over the window")
	}
	fx.s.run(cmdQueryNew)
	if len(fx.s.open) != 2 {
		t.Errorf("%d tabs; the window should stay usable while an export runs", len(fx.s.open))
	}
	if !fx.s.taskButton.Visible() || fx.s.taskButton.Text != "Export to r.ndjson…" {
		t.Errorf("status bar says %q, shown %v", fx.s.taskButton.Text, fx.s.taskButton.Visible())
	}

	test.Tap(fx.s.taskButton)
	if !fx.s.panelIs(panelTasks) || !fx.s.menuItems[cmdTasks].Checked {
		t.Fatal("the status bar's word on tasks opens the Tasks panel")
	}
	if got := labelText(fx.s.Panel()); !strings.Contains(got, "Export to r.ndjson") {
		t.Errorf("panel reads %q", got)
	}
	stop := findButton(fx.s.Panel(), "Cancel")
	if stop == nil || !stop.Visible() {
		t.Fatal("a running task can be cancelled from the panel")
	}
	clear := findButton(fx.s.Panel(), "Clear Finished")
	if !clear.Disabled() {
		t.Error("nothing has finished to clear")
	}
	test.Tap(stop)
	pump(t, fx.q, func() bool { return j.done })
	if !errors.Is(j.err, context.Canceled) || j.task.state != taskCancelled {
		t.Errorf("err %v, task %v", j.err, j.task.state)
	}
	if got := labelText(fx.s.Panel()); !strings.Contains(got, "Cancelled") {
		t.Errorf("panel reads %q", got)
	}
	if stop.Visible() || fx.s.taskButton.Visible() {
		t.Error("nothing is running now")
	}

	test.Tap(clear)
	if len(fx.s.tasks) != 0 || !fx.s.taskView.empty.Visible() || !clear.Disabled() {
		t.Errorf("after Clear Finished: %d tasks, panel %q", len(fx.s.tasks), labelText(fx.s.Panel()))
	}
}

func TestAFinishedExportSaysSoInTheTaskCentre(t *testing.T) {
	fx, tb := openItems(t)
	fx.s.showTasks()
	j := fx.s.runExport(tb, fx.s.exportSource(), export.Options{Format: export.CSV}, &sink{}, "items.csv", nil)
	pump(t, fx.q, func() bool { return j.done })
	if j.task.state != taskDone || j.task.status != "Exported 250 rows to items.csv" {
		t.Errorf("task %v, %q", j.task.state, j.task.status)
	}
	if got := labelText(fx.s.Panel()); !strings.Contains(got, "Exported 250 rows to items.csv") {
		t.Errorf("panel reads %q", got)
	}
	if fx.s.taskButton.Visible() {
		t.Error("the status bar speaks only while a task runs")
	}
}

func TestATaskRowShowsItsState(t *testing.T) {
	fx := newFixture(t)
	p := fx.s.showTasks()
	k := fx.s.startTask(nil, "Export to a.csv", func() {})
	r := p.rows[k]
	if !r.spin.Visible() || r.bar.Visible() || !r.stop.Visible() {
		t.Error("with no total, a running task shows a bar that moves without measuring")
	}
	fx.s.progressTask(k, "5 of 10 rows", 0.5)
	if r.spin.Visible() || !r.bar.Visible() || r.bar.Value != 0.5 || r.status.Text != "5 of 10 rows" {
		t.Errorf("half done: bar %v at %v, status %q", r.bar.Visible(), r.bar.Value, r.status.Text)
	}
	fx.s.stopTask(k)
	if !r.stop.Disabled() || r.status.Text != "Stopping…" {
		t.Errorf("stopping: button disabled %v, status %q", r.stop.Disabled(), r.status.Text)
	}
	fx.s.endTask(k, taskFailed, "Failed: disk full")
	if r.stop.Visible() || r.bar.Visible() || r.spin.Visible() || r.status.Importance != widget.DangerImportance {
		t.Error("a failed task shows neither bar nor Cancel, and says so in red")
	}
	fx.s.progressTask(k, "late", 0.9)
	if k.status != "Failed: disk full" {
		t.Error("a report arriving after the end must not undo it")
	}
	newer := fx.s.startTask(nil, "Export to b.csv", func() {})
	if p.list.Objects[0] != p.rows[newer].box {
		t.Error("the newest task comes first")
	}
	fx.s.clearTasks()
	if len(fx.s.tasks) != 1 || fx.s.tasks[0] != newer {
		t.Error("Clear Finished leaves a running task")
	}
}

func TestAnExportWithAKnownTotalMeasures(t *testing.T) {
	fx, tb := openItems(t)
	src := &exportSrc{name: "big", total: 100000, rows: func() model.RowStream { return &slowStream{n: 100000, slow: true} }}
	j := fx.s.runExport(tb, src, export.Options{Format: export.CSV}, &sink{}, "big.csv", nil)
	pump(t, fx.q, func() bool { return j.task.frac > 0 })
	if !strings.Contains(j.task.status, " of 100,000 rows") {
		t.Errorf("status %q", j.task.status)
	}
	if want := fmt.Sprintf("Export to big.csv · %d%%", int(j.task.frac*100)); fx.s.taskButton.Text != want {
		t.Errorf("status bar %q, want %q", fx.s.taskButton.Text, want)
	}
	j.cancel()
	pump(t, fx.q, func() bool { return j.done })
}

func TestWaitingForTasksGivesUp(t *testing.T) {
	fx := newFixture(t)
	k := fx.s.startTask(nil, "Export to a.csv", func() {})
	if fx.s.waitTasks(10 * time.Millisecond) {
		t.Error("a task that never cleans up must not hold quitting")
	}
	close(k.finished)
	if !fx.s.waitTasks(time.Second) {
		t.Error("a task that has cleaned up is not waited for")
	}
}

func TestTheStatusBarSaysWhatIsRunning(t *testing.T) {
	a := &task{title: "Export to a.csv", frac: -1}
	b := &task{title: "Export to b.csv", frac: 0.425}
	for _, c := range []struct {
		run  []*task
		want string
	}{
		{nil, ""},
		{[]*task{a}, "Export to a.csv…"},
		{[]*task{b}, "Export to b.csv · 42%"},
		{[]*task{a, b}, "2 tasks running"},
	} {
		if got := taskSummary(c.run); got != c.want {
			t.Errorf("taskSummary(%d tasks) = %q, want %q", len(c.run), got, c.want)
		}
	}
}

func TestClosingATabWithARunningExportAsks(t *testing.T) {
	fx := newFixture(t)
	discarded := false
	tb, j := slowExport(t, fx, func() { discarded = true })
	fx.s.requestClose(tb.item)
	top := fx.s.win.Canvas().Overlays().Top()
	if top == nil || !strings.Contains(labelText(top), "“Export to r.ndjson” is still running") {
		t.Fatalf("closing the tab should ask first; overlay %v", top)
	}
	test.Tap(findButton(top, "Cancel"))
	if len(fx.s.open) != 1 || len(fx.s.runningTasks(nil)) != 1 {
		t.Fatal("Cancel keeps the tab and its export")
	}
	fx.s.requestClose(tb.item)
	test.Tap(findButton(fx.s.win.Canvas().Overlays().Top(), "Close"))
	pump(t, fx.q, func() bool { return j.done })
	if len(fx.s.open) != 0 || !errors.Is(j.err, context.Canceled) || !discarded {
		t.Errorf("tabs %d, err %v, discarded %v", len(fx.s.open), j.err, discarded)
	}

	busy, _ := slowExport(t, fx, nil)
	fx.s.run(cmdQueryNew) // on the same connection
	if len(fx.s.open) != 2 {
		t.Fatalf("%d tabs, want a busy one and an idle one", len(fx.s.open))
	}
	idle := fx.s.open[1]
	fx.s.requestClose(idle.item)
	if len(fx.s.open) != 1 || fx.s.open[0] != busy || fx.s.win.Canvas().Overlays().Top() != nil {
		t.Error("a tab with no task running closes without asking, whatever other tabs run")
	}
}

func TestQuittingStopsTasksAndWaitsForThemToCleanUp(t *testing.T) {
	fx := newFixture(t)
	var discarded atomic.Bool
	_, j := slowExport(t, fx, func() {
		time.Sleep(20 * time.Millisecond) // a slow disk
		discarded.Store(true)
	})
	fx.s.requestQuit()
	top := fx.s.win.Canvas().Overlays().Top()
	if top == nil || (fx.s.ctx.Err() != nil) {
		t.Fatal("quitting with an export running should ask first")
	}
	start := time.Now()
	test.Tap(findButton(top, "Quit"))
	if took := time.Since(start); took > shutdownWait/2 {
		t.Errorf("quitting took %v: it should wait for the export only until it has cleaned up", took)
	}
	if !(fx.s.ctx.Err() != nil) || !discarded.Load() {
		t.Errorf("stopped %v, partial file removed %v: quitting must wait for the export to clean up",
			(fx.s.ctx.Err() != nil), discarded.Load())
	}
	pump(t, fx.q, func() bool { return j.done })

	idle := newFixture(t)
	idle.s.requestQuit()
	if !(idle.s.ctx.Err() != nil) || idle.s.win.Canvas().Overlays().Top() != nil {
		t.Error("with nothing running, quitting does not ask")
	}
}
