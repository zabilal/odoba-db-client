package shell

import (
	"fmt"
	"slices"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
)

// The task centre (FR-15.6, UX principle 5): long work runs beside the
// window, not over it. Each task says what it is doing, and can be stopped,
// in the Tasks panel; the status bar says while any is running, and opens
// the panel. Exports are its first tasks.

const panelTasks = "tasks"

type taskState int

const (
	taskRunning taskState = iota
	taskDone
	taskFailed
	taskCancelled
)

// task is one piece of long work. Its fields belong to the UI goroutine,
// except finished.
type task struct {
	title  string
	tab    *tab // the tab it works from: closing the tab stops it
	cancel func()
	// finished is closed by the worker once it has stopped and cleaned up
	// after itself. Quitting waits for it.
	finished chan struct{}
	state    taskState
	stopping bool // asked to stop, and not yet stopped
	status   string
	frac     float64 // the share done, or -1 when the total is unknown
}

// startTask puts work in the task centre. The worker closes finished when
// it has stopped, then calls endTask on the UI goroutine.
func (s *Shell) startTask(t *tab, title string, cancel func()) *task {
	k := &task{title: title, tab: t, cancel: cancel, finished: make(chan struct{}), status: "Starting…", frac: -1}
	s.tasks = append(s.tasks, k)
	s.tasksChanged()
	return k
}

// progressTask says how a running task is getting on.
func (s *Shell) progressTask(k *task, status string, frac float64) {
	if k.state != taskRunning {
		return // a last report can arrive after the end
	}
	k.status, k.frac = status, frac
	s.tasksChanged()
}

// endTask records how a task ended. It stays in the panel, saying so, until
// Clear Finished; from the background, a notification says so too.
func (s *Shell) endTask(k *task, state taskState, status string) {
	k.state, k.status = state, status
	s.tasksChanged()
	s.notifyTask(k)
}

// stopTask asks a task to stop. It says it has once it has cleaned up.
func (s *Shell) stopTask(k *task) {
	k.stopping = true
	k.cancel()
	s.tasksChanged()
}

// runningTasks is the tasks still running from one tab or, for nil, from all.
func (s *Shell) runningTasks(t *tab) []*task {
	var out []*task
	for _, k := range s.tasks {
		if k.state == taskRunning && (t == nil || k.tab == t) {
			out = append(out, k)
		}
	}
	return out
}

// waitTasks waits up to wait for the running tasks, told to stop, to clean
// up after themselves. False if one had not by then.
func (s *Shell) waitTasks(wait time.Duration) bool {
	deadline := time.After(wait)
	for _, k := range s.runningTasks(nil) {
		select {
		case <-k.finished:
		case <-deadline:
			return false
		}
	}
	return true
}

func (s *Shell) clearTasks() {
	s.tasks = slices.DeleteFunc(s.tasks, func(k *task) bool { return k.state != taskRunning })
	s.tasksChanged()
}

// tasksChanged brings the status bar and, if it is open, the Tasks panel up
// to date.
func (s *Shell) tasksChanged() {
	if text := taskSummary(s.runningTasks(nil)); text == "" {
		s.taskButton.Hide()
	} else {
		s.taskButton.SetText(text)
		s.taskButton.Show()
	}
	if s.panelIs(panelTasks) {
		s.taskView.render()
	}
}

// taskSummary is the status bar's word on the running tasks: the one task
// and how far it has got, or how many there are.
func taskSummary(run []*task) string {
	switch {
	case len(run) == 0:
		return ""
	case len(run) > 1:
		return fmt.Sprintf("%d tasks running", len(run))
	case run[0].frac >= 0:
		return fmt.Sprintf("%s · %d%%", run[0].title, int(run[0].frac*100))
	}
	return run[0].title + "…"
}

// stopWarning words what stopping the running tasks loses, for the question
// closing a tab or quitting asks first.
func stopWarning(run []*task, doing string) string {
	if len(run) == 1 {
		return fmt.Sprintf("“%s” is still running. %s stops it, and removes what it had written.", run[0].title, doing)
	}
	return fmt.Sprintf("%d tasks are still running. %s stops them, and removes what they had written.", len(run), doing)
}

// requestQuit closes the window, which quits, as the close button and Quit
// do: first asking, if tasks are running, since quitting stops them.
func (s *Shell) requestQuit() {
	run := s.runningTasks(nil)
	if len(run) == 0 {
		s.win.Close()
		return
	}
	d := dialog.NewConfirm("Stop and Quit?", stopWarning(run, "Quitting"), func(yes bool) {
		if yes {
			s.win.Close()
		}
	}, s.win)
	d.SetConfirmText("Quit")
	d.SetDismissText("Cancel")
	d.SetConfirmImportance(widget.DangerImportance)
	d.Show()
}

// tasksPanel lists the tasks, newest first.
type tasksPanel struct {
	s     *Shell
	list  *fyne.Container
	empty *widget.Label
	clear *widget.Button
	rows  map[*task]*taskRow
}

type taskRow struct {
	box    *fyne.Container
	title  *widget.Label
	status *widget.Label
	bar    *widget.ProgressBar
	spin   *widget.ProgressBarInfinite
	stop   *widget.Button
}

func (s *Shell) showTasks() *tasksPanel {
	p := &tasksPanel{s: s, list: container.NewVBox(), rows: map[*task]*taskRow{}}
	p.empty = widget.NewLabel("Nothing is running. An export shows here while it runs, and says how it ended.")
	p.empty.Wrapping = fyne.TextWrapWord
	p.empty.Importance = widget.LowImportance
	p.clear = widget.NewButton("Clear Finished", s.clearTasks)
	s.taskView = p
	s.openPanel(panelTasks, "Tasks", container.NewBorder(nil, container.NewHBox(layout.NewSpacer(), p.clear), nil, nil,
		container.NewVScroll(container.NewVBox(p.empty, p.list))), nil)
	p.render()
	return p
}

func (p *tasksPanel) render() {
	rows := make(map[*task]*taskRow, len(p.s.tasks))
	objs := make([]fyne.CanvasObject, 0, len(p.s.tasks))
	finished := false
	for i := len(p.s.tasks) - 1; i >= 0; i-- {
		k := p.s.tasks[i]
		r := p.rows[k]
		if r == nil {
			r = p.newRow(k)
		}
		r.fill(k)
		rows[k] = r
		objs = append(objs, r.box)
		finished = finished || k.state != taskRunning
	}
	p.rows = rows
	p.list.Objects = objs
	p.list.Refresh()
	setShown(p.empty, len(objs) == 0)
	if finished {
		p.clear.Enable()
	} else {
		p.clear.Disable()
	}
}

func (p *tasksPanel) newRow(k *task) *taskRow {
	r := &taskRow{
		title:  widget.NewLabelWithStyle(k.title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		status: widget.NewLabel(""),
		bar:    widget.NewProgressBar(),
		spin:   widget.NewProgressBarInfinite(),
		stop:   widget.NewButton("Cancel", func() { p.s.stopTask(k) }),
	}
	r.title.Truncation = fyne.TextTruncateEllipsis
	r.status.Wrapping = fyne.TextWrapWord
	r.box = container.NewVBox(container.NewBorder(nil, nil, nil, r.stop, r.title), r.status,
		container.NewStack(r.bar, r.spin), widget.NewSeparator())
	return r
}

func (r *taskRow) fill(k *task) {
	running := k.state == taskRunning
	r.status.Importance = widget.LowImportance
	if k.state == taskFailed {
		r.status.Importance = widget.DangerImportance
	}
	status := k.status
	if running && k.stopping {
		status = "Stopping…"
	}
	r.status.SetText(status)
	setShown(r.stop, running)
	if k.stopping {
		r.stop.Disable()
	}
	setShown(r.bar, running && k.frac >= 0)
	setShown(r.spin, running && k.frac < 0)
	if k.frac >= 0 {
		r.bar.SetValue(k.frac)
	}
}

func setShown(o fyne.CanvasObject, on bool) {
	if on {
		o.Show()
	} else {
		o.Hide()
	}
}
