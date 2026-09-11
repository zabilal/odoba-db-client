package shell

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/sqllex"
	"github.com/ikigai-db/ikigai-db/internal/store/localdb"
	"github.com/ikigai-db/ikigai-db/internal/ui/editor"
	"github.com/ikigai-db/ikigai-db/internal/ui/editor/view"
	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
	"github.com/ikigai-db/ikigai-db/internal/ui/uithread"
)

// queryTab is what a query tab has beyond any tab (T1.61–T1.64): an editor
// above its results.
type queryTab struct {
	editor   *view.Editor
	session  *app.QuerySession // nil until connected
	results  *container.AppTabs
	messages *widget.Label
	sets     []*app.ResultSet
	grids    []*grid.TableGrid
	// executing is true while a script runs. run ends the latest run, and
	// outlives it: a script finishes when every statement has returned a
	// result, not when every row has arrived, and the rows keep streaming
	// until Stop, the next run or closing the tab (ADR-0012).
	executing bool
	run       context.CancelFunc
	// runRev is the document's revision when the latest run began. Error
	// positions map onto the text only if it has not changed since.
	runRev uint64

	// saved is the saved query this tab edits; a zero ID means none yet.
	saved localdb.SavedQuery
	title string // the tab's name, before any unsaved-edits mark
	dirty bool   // edited since opened or last saved

	find *findBar
}

// OpenQuery opens a query tab on a connection. Typing can start at once; the
// session connects in the background, and the editor takes the source's
// dialect once it has.
func (s *Shell) OpenQuery(connID string) *tab {
	if _, ok := s.d.Conns.Get(connID); !ok {
		return nil
	}
	s.queries++
	ctx, cancel := context.WithCancel(s.ctx)
	q := &queryTab{
		editor:   view.New(editor.NewDocument("", sqllex.DialectFor("")), s.colours()),
		messages: widget.NewLabel(""),
	}
	q.messages.Wrapping = fyne.TextWrapWord
	q.results = container.NewAppTabs(container.NewTabItem("Messages", container.NewVScroll(q.messages)))
	q.results.OnSelected = func(*container.TabItem) { s.sync() } // Export follows the result on show

	t := &tab{key: fmt.Sprintf("query:%d", s.queries), connID: connID, ctx: ctx, cancel: cancel,
		footer: widget.NewLabel("Connecting…"), query: q}
	t.footer.Importance = widget.LowImportance
	q.find = newFindBar(s, q)
	top := container.NewBorder(q.find.box, nil, nil, nil, q.editor)
	q.find.parent = top
	split := container.NewVSplit(top, q.results)
	split.Offset = 0.55
	t.body = container.NewStack(split)
	q.title = fmt.Sprintf("Query %d", s.queries)
	t.item = container.NewTabItem(q.title, container.NewBorder(nil, t.footer, nil, nil, t.body))
	q.editor.OnChanged = func() {
		if !q.dirty {
			q.dirty = true
			s.retitle(t)
		}
		if q.find.visible() {
			q.find.refresh() // the matches moved with the text
		}
	}
	s.open = append(s.open, t)
	s.showTabs(true)
	s.tabs.Append(t.item)
	s.tabs.Select(t.item)
	q.editor.Focus()
	s.sync()

	go func() {
		qs, lang, err := s.querySession(ctx, connID)
		s.d.Run(func() {
			if ctx.Err() != nil {
				if qs != nil {
					go qs.Close()
				}
				return
			}
			if err != nil {
				t.footer.SetText("")
				s.note(q, "Could not connect: "+err.Error())
				return
			}
			q.session = qs
			q.editor.Document().Highlighter().SetDialect(sqllex.DialectFor(lang))
			q.editor.Refresh()
			t.footer.SetText("Ready")
			s.sync()
		})
	}()
	return t
}

// querySession connects a query tab. lang is the source's query language, for
// the editor's highlighting (capability.Query.Language).
func (s *Shell) querySession(ctx context.Context, connID string) (*app.QuerySession, string, error) {
	live, err := s.d.WS.Connect(ctx, connID)
	if err != nil {
		return nil, "", err
	}
	c, _ := s.d.Conns.Get(connID)
	qs, err := app.NewQuerySession(ctx, live, app.QueryOptions{History: s.d.History, Database: c.Database})
	return qs, live.Source.Capabilities().Query.Language, err
}

func (s *Shell) activeQuery() (*tab, *queryTab) {
	t := s.activeTab()
	if t == nil || t.query == nil {
		return nil, nil
	}
	return t, t.query
}

func (s *Shell) canRun() bool {
	_, q := s.activeQuery()
	return q != nil && q.session != nil && !q.executing
}

// running reports work Stop can end: a script still executing, or a result
// whose rows are still arriving after the script itself has finished.
func (s *Shell) running() bool {
	_, q := s.activeQuery()
	if q == nil {
		return false
	}
	if q.executing {
		return true
	}
	for _, rs := range q.sets {
		if _, done := rs.Progress(); !done {
			return true
		}
	}
	return false
}

func (s *Shell) stopQuery() {
	_, q := s.activeQuery()
	if q == nil {
		return
	}
	if q.run != nil {
		q.run()
	}
	for _, rs := range q.sets {
		rs.Close()
	}
}

// runQuery runs the selection, or else the statement at the caret; with all,
// the whole script (FR-5.3).
func (s *Shell) runQuery(all bool) {
	t, q := s.activeQuery()
	if q == nil || q.session == nil || q.executing {
		return
	}
	doc := q.editor.Document()
	script, base := doc.Text(), 0
	if !all {
		if from, _, sel := doc.Selection(); sel {
			script, base = doc.SelectedText(), doc.Offset(from)
		} else if stmt, start, ok := q.session.StatementAt(script, doc.Offset(doc.Caret())); ok {
			script, base = stmt, start
		}
	}
	if strings.TrimSpace(script) == "" {
		return
	}
	s.execute(t, script, base, false)
}

// execute runs a script and shows each statement's result as it arrives.
// base is where the script starts in the editor, for mapping errors back to
// it (FR-5.10, T1.68).
func (s *Shell) execute(t *tab, script string, base int, confirmed bool) {
	q := t.query
	if q.run != nil {
		q.run() // the previous run's rows, if any are still arriving
	}
	ctx, cancel := context.WithCancel(t.ctx)
	q.run, q.executing = cancel, true
	q.runRev = q.editor.Document().Revision()
	s.clearResults(q)
	t.footer.SetText("Running…")
	s.sync()
	start := time.Now()
	go func() {
		ch, err := q.session.Run(ctx, script, confirmed)
		if err != nil {
			s.d.Run(func() {
				q.executing = false
				cancel() // nothing ran, so nothing streams
				if t.ctx.Err() == nil {
					s.runRefused(t, script, base, err)
					s.sync()
				}
			})
			return
		}
		n, failed := 0, false
		for r := range ch {
			n++
			failed = failed || r.Err != nil
			s.d.Run(func() {
				if t.ctx.Err() == nil {
					s.showResult(t, r, base)
				}
			})
		}
		elapsed, stopped := time.Since(start), ctx.Err() != nil
		s.d.Run(func() {
			// Not cancel(): results may still be streaming. Cancelling here
			// once cut every large SELECT off after its first rows.
			q.executing = false
			if t.ctx.Err() == nil {
				t.footer.SetText(runSummary(n, elapsed, stopped, failed))
				s.sync()
			}
		})
	}()
}

// runRefused explains a script the session would not start. A production
// write is not an error but a question: nothing has run, so asking and then
// running it confirmed is safe (FR-4.9).
func (s *Shell) runRefused(t *tab, script string, base int, err error) {
	t.footer.SetText("")
	switch {
	case errors.Is(err, source.ErrConfirmationRequired):
		c, _ := s.d.Conns.Get(t.connID)
		d := dialog.NewConfirm("Change Data on Production?",
			fmt.Sprintf("This script changes data on “%s”, which is marked Production. Nothing has run yet.", c.Name),
			func(yes bool) {
				if yes {
					s.execute(t, script, base, true)
				} else {
					t.footer.SetText("Not run")
				}
			}, s.win)
		d.SetConfirmText("Run")
		d.SetDismissText("Cancel")
		d.SetConfirmImportance(widget.DangerImportance)
		d.Show()
	case errors.Is(err, source.ErrReadOnly):
		s.note(t.query, "Not run: the script changes data, and this connection is read-only.")
	default:
		s.note(t.query, "Could not run: "+err.Error())
	}
}

// showResult shows one statement's outcome. base is where the script that
// ran starts in the editor's text, for pointing at an error (FR-5.10).
func (s *Shell) showResult(t *tab, r app.StatementResult, base int) {
	q, n := t.query, r.Index+1
	switch {
	case r.Err != nil:
		msg := fmt.Sprintf("Statement %d failed: %v", n, r.Err)
		doc := q.editor.Document()
		if off, ok := r.ErrorOffset(); ok && doc.Revision() == q.runRev {
			p := doc.PosAt(base + off)
			msg = fmt.Sprintf("Statement %d failed at line %d, column %d: %v", n, p.Line+1, doc.Column(p)+1, r.Err)
			q.editor.MarkError(p)
		}
		s.note(q, msg)
		q.results.SelectIndex(0)
	case r.Rows != nil:
		s.addResult(t, n, r.Rows)
	default:
		s.note(q, fmt.Sprintf("Statement %d: %s in %s.", n, affectedText(r.Affected), took(r.Duration)))
	}
	for _, m := range r.Messages {
		s.note(q, m.Text)
	}
}

// addResult shows a result set in a grid of its own tab. The rows stream in
// behind it; the count underneath says how many have arrived.
func (s *Shell) addResult(t *tab, n int, rs *app.ResultSet) {
	q := t.query
	m := grid.NewModel(rs)
	g := grid.NewTableGridWith(t.ctx, m, s.colours(), s.d.Run, s.d.Delay)
	g.OnSelectCell = func() { s.sync(); t.viewerFollow(g) } // the Edit menu and the viewer follow it
	g.OnCopy = func() { s.copyCells(t.ctx, g) }
	g.OnSpace = func() { s.toggleViewerFor(t, g) }
	g.OnHeaderMenu = func(col int, at fyne.Position) { s.showHeaderMenu(t, g, col, at) }
	count := widget.NewLabel("Loading rows…")
	count.Importance = widget.LowImportance
	update := uithread.Coalesce(s.d.Run, s.d.Delay, func() {
		if t.ctx.Err() == nil {
			count.SetText(resultCount(rs))
		}
	})
	m.OnPageLoaded = func(int64) {
		g.ScheduleRefresh()
		update()
	}
	m.OnError = func(err error) {
		s.d.Run(func() { count.SetText("Could not load rows: " + err.Error()) })
	}
	holder := container.NewStack(g.View())
	t.hold(g, holder)
	item := container.NewTabItem(fmt.Sprintf("Result %d", n), container.NewBorder(nil, count, nil, nil, holder))
	q.results.Append(item)
	if len(q.sets) == 0 {
		q.results.Select(item)
	}
	q.sets, q.grids = append(q.sets, rs), append(q.grids, g)
	go func() {
		select {
		case <-rs.Done():
		case <-t.ctx.Done():
			return
		}
		_ = m.LoadCount(t.ctx)
		update()
		g.ScheduleRefresh()
		s.d.Run(s.sync) // Stop may no longer apply
	}()
}

func (s *Shell) clearResults(q *queryTab) {
	for len(q.results.Items) > 1 {
		q.results.Remove(q.results.Items[len(q.results.Items)-1])
	}
	q.results.SelectIndex(0)
	q.messages.SetText("")
	q.sets, q.grids = nil, nil
}

func (s *Shell) note(q *queryTab, text string) {
	q.messages.SetText(strings.TrimPrefix(q.messages.Text+"\n"+text, "\n"))
}

func resultCount(rs *app.ResultSet) string {
	n, done := rs.Progress()
	rows := rowCount(int64(n), true)
	switch err := rs.Err(); {
	case !done:
		return group(int64(n)) + "+ rows"
	case errors.Is(err, context.Canceled):
		return "Stopped after " + rows
	case err != nil:
		return fmt.Sprintf("%s, then an error: %v", rows, err)
	case rs.Truncated():
		return "First " + rows
	}
	return rows
}

func affectedText(n int64) string {
	switch {
	case n < 0:
		return "done"
	case n == 1:
		return "1 row affected"
	}
	return group(n) + " rows affected"
}

func runSummary(n int, elapsed time.Duration, stopped, failed bool) string {
	switch {
	case stopped:
		return "Stopped after " + took(elapsed)
	case failed:
		return "Stopped at an error after " + took(elapsed)
	case n == 1:
		return "Ran 1 statement in " + took(elapsed)
	}
	return fmt.Sprintf("Ran %d statements in %s", n, took(elapsed))
}

func took(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%d ms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1f s", d.Seconds())
}
