package shell

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/model"
)

// Searching a database's structure (FR-2.7): the column somewhere among
// two hundred tables, the table named in a procedure's body, the default
// nobody can account for.
//
// The explorer's filter narrows what is loaded by its name. This is the
// other question — what is inside — and it cannot be answered without
// asking the server, so it is a task: it says how it is getting on, the
// matches arrive as they are found, and it can be stopped.

// searchLimit caps how many matches are kept. Five hundred is past where
// anybody reads a list, and stopping there is better than walking a whole
// database to fill one nobody wants.
const searchLimit = 500

// searchBatch is how many matches arrive before the list is drawn again.
// One redraw per match would spend the search's time on frames.
const searchBatch = 25

// canSearchStructure reports whether something is selected to search
// under. It asks the search's own question, so that what is offered and
// what is searched cannot come apart.
func (s *Shell) canSearchStructure() bool {
	conn, n, ok := s.Explorer.SelectedNode()
	if !ok || !app.HoldsObjects(n.Ref) {
		return false
	}
	_, open := s.d.WS.Get(conn)
	return open
}

// searchPanel searches the structure under one node.
type searchPanel struct {
	s      *Shell
	find   *panelEntry
	exact  *widget.Check
	whole  *widget.Check
	list   *widget.List
	status *widget.Label

	connID string
	root   model.ObjectRef
	scope  string

	hits []app.Hit
	// stop cancels the search running now, and task is its entry in the
	// Tasks panel. Both are nil while nothing is running.
	stop func()
	task *task
}

// searchStructure opens the panel over what is selected.
func (s *Shell) searchStructure() {
	conn, n, ok := s.Explorer.SelectedNode()
	if !ok || !app.HoldsObjects(n.Ref) {
		return
	}
	p := &searchPanel{s: s, find: s.newPanelEntry(), status: widget.NewLabel(""),
		connID: conn, root: n.Ref, scope: scopeName(n)}
	s.searchView = p
	p.find.SetPlaceHolder("Find in columns, bodies and definitions")
	p.status.Importance = widget.LowImportance
	p.status.Truncation = fyne.TextTruncateEllipsis
	p.exact = widget.NewCheck("Match case", func(bool) { p.run() })
	p.whole = widget.NewCheck("Whole word", func(bool) { p.run() })
	p.list = widget.NewList(
		func() int { return len(p.hits) },
		func() fyne.CanvasObject {
			where := widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
			where.Truncation = fyne.TextTruncateEllipsis
			line := widget.NewLabel("")
			line.Importance = widget.LowImportance
			line.Truncation = fyne.TextTruncateEllipsis
			return container.NewVBox(where, line)
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			if i >= len(p.hits) {
				return
			}
			h, box := p.hits[i], o.(*fyne.Container)
			box.Objects[0].(*widget.Label).SetText(h.Node.Label + " · " + h.In)
			box.Objects[1].(*widget.Label).SetText(h.Line)
		})
	p.list.OnSelected = func(i widget.ListItemID) {
		if i < len(p.hits) {
			p.open(p.hits[i])
		}
	}
	p.find.OnSubmitted = func(string) { p.run() }
	top := container.NewVBox(p.find, container.NewHBox(p.exact, p.whole))
	s.openPanel(panelSearch, "Search "+p.scope,
		container.NewBorder(top, p.status, nil, nil, p.list), p.find)
	p.say("Type what to look for and press Return.")
}

// scopeName is what the panel is over, as the tree names it.
func scopeName(n model.Node) string {
	if n.Label != "" {
		return n.Label
	}
	return n.Ref.Name()
}

func (p *searchPanel) say(text string) { p.status.SetText(text) }

// run starts a search, stopping whatever was running.
func (p *searchPanel) run() {
	p.cancel()
	q := app.Query{Text: strings.TrimSpace(p.find.Text), Case: p.exact.Checked, Whole: p.whole.Checked}
	p.hits = nil
	p.list.UnselectAll()
	p.list.Refresh()
	if q.Empty() {
		p.say("Type what to look for and press Return.")
		return
	}
	ctx, cancel := context.WithCancel(p.s.ctx)
	p.stop = cancel
	p.task = p.s.startTask(nil, "Searching "+p.scope+" for "+q.Text, cancel)
	p.say("Searching " + p.scope + "…")

	s, task := p.s, p.task
	go func() {
		defer cancel()
		var (
			mu    sync.Mutex
			batch []app.Hit
			found int
		)
		// The matches are pushed in batches, so that a search over a big
		// database fills the list as it goes without a redraw each time.
		push := func() {
			mu.Lock()
			out := batch
			batch = nil
			mu.Unlock()
			if len(out) == 0 {
				return
			}
			s.d.Run(func() { p.add(task, out) })
		}
		live, err := s.d.WS.Connect(ctx, p.connID)
		if err == nil {
			err = app.Search(ctx, live.Source, p.root, q, func(h app.Hit) bool {
				mu.Lock()
				batch = append(batch, h)
				found++
				n := len(batch)
				mu.Unlock()
				if n >= searchBatch {
					push()
				}
				return found < searchLimit
			})
		}
		push()
		s.d.Run(func() { p.done(task, found, err) })
	}()
}

// add puts a batch of matches on the list, if it is still the search the
// panel is showing.
func (p *searchPanel) add(k *task, hits []app.Hit) {
	if p.task != k {
		return // a later search has taken over
	}
	p.hits = append(p.hits, hits...)
	p.list.Refresh()
	p.say(matchCount(len(p.hits)) + " so far…")
	p.s.progressTask(k, matchCount(len(p.hits)), -1)
}

// done says how the search ended.
func (p *searchPanel) done(k *task, found int, err error) {
	switch {
	case errors.Is(err, context.Canceled):
		p.s.endTask(k, taskCancelled, matchCount(found)+" before it was stopped")
	case err != nil:
		p.s.endTask(k, taskFailed, err.Error())
	default:
		p.s.endTask(k, taskDone, matchCount(found))
	}
	if p.task != k {
		return
	}
	p.task, p.stop = nil, nil
	switch {
	case errors.Is(err, context.Canceled):
		p.say("Stopped. " + matchCount(len(p.hits)) + ".")
	case err != nil:
		p.say("Could not search " + p.scope + ": " + err.Error())
		p.s.showError(fmt.Errorf("could not search %s: %w", p.scope, err))
	case found >= searchLimit:
		p.say(matchCount(len(p.hits)) + ", and it stopped looking there. Narrow what you are looking for.")
	case len(p.hits) == 0:
		p.say("Nothing in " + p.scope + " holds that.")
	default:
		p.say(matchCount(len(p.hits)) + " in " + p.scope + ".")
	}
}

// matchCount says how many, in words that read.
func matchCount(n int) string { return nounCount(n, "match") }

// cancel stops the search running now, if there is one.
func (p *searchPanel) cancel() {
	if p.stop == nil {
		return
	}
	p.stop()
	p.stop, p.task = nil, nil
}

// open shows what a match is in, the way the tree would have opened it.
func (p *searchPanel) open(h app.Hit) {
	if h.Node.Browsable {
		p.s.OpenObject(p.connID, h.Node)
		return
	}
	p.s.OpenStructure(p.connID, h.Node)
}
