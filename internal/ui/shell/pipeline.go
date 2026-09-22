package shell

import (
	"errors"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	fynetheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// The aggregation-pipeline editor (FR-12.1, ADR-0068): the stages a person
// writes, above the grid where the WHERE clause sits for a table, and the
// documents they produce in the grid's own place.
//
// A pipeline says its own matching and its own order, so while one is shown
// the grid's filters and sorts are not the browse's any more: Show Documents
// puts the collection back.

// pipelineBar is a collection's pipeline: the stages, what they produced,
// and what went wrong with them.
type pipelineBar struct {
	s     *Shell
	t     *tab
	entry *widget.Entry
	note  *widget.Label
	notes fyne.CanvasObject
	back  *widget.Button
	box   *fyne.Container
	// browse is the rows the tab showed before a pipeline ran, put back by
	// Show Documents.
	browse *app.BrowseSource
}

// canPipeline reports whether the active tab reads by a pipeline.
func (s *Shell) canPipeline() bool {
	t := s.activeTab()
	return t != nil && t.browse != nil && t.top != nil && t.browse.CanPipeline()
}

// togglePipeline shows the active tab's pipeline editor, or hides it.
func (s *Shell) togglePipeline() {
	if !s.canPipeline() {
		return
	}
	t := s.activeTab()
	if t.pipeline == nil {
		t.pipeline = s.newPipelineBar(t)
	}
	if t.pipeline.shown() {
		t.pipeline.hide()
		return
	}
	t.pipeline.show()
}

func (s *Shell) newPipelineBar(t *tab) *pipelineBar {
	b := &pipelineBar{s: s, t: t, note: widget.NewLabel("")}
	b.entry = widget.NewMultiLineEntry()
	b.entry.TextStyle = fyne.TextStyle{Monospace: true}
	b.entry.Wrapping = fyne.TextWrapOff
	b.entry.SetPlaceHolder(`[{"$match": {…}}, {"$group": {"_id": "$field", "n": {"$sum": 1}}}]`)
	b.note.Importance = widget.DangerImportance
	b.notes = container.NewHScroll(b.note)
	b.notes.Hide()
	b.back = widget.NewButton("Show Documents", b.restore)
	b.back.Importance = widget.LowImportance
	b.back.Hide()
	run := widget.NewButtonWithIcon("Run Pipeline", fynetheme.MediaPlayIcon(), b.run)
	keyword := widget.NewLabelWithStyle("Pipeline", fyne.TextAlignLeading, fyne.TextStyle{Monospace: true, Bold: true})
	b.box = container.NewVBox(
		container.NewBorder(nil, nil, keyword, container.NewHBox(b.back, run), b.entry),
		b.notes,
		widget.NewSeparator())
	return b
}

func (b *pipelineBar) shown() bool { return len(b.t.top.Objects) > 0 }

func (b *pipelineBar) show() {
	b.t.top.Objects = []fyne.CanvasObject{b.box}
	b.t.top.Refresh()
	b.t.item.Content.Refresh()
	b.s.win.Canvas().Focus(b.entry)
}

func (b *pipelineBar) hide() {
	b.t.top.Objects = nil
	b.t.top.Refresh()
	b.t.item.Content.Refresh()
}

// say shows what went wrong, or clears it.
func (b *pipelineBar) say(problem string) {
	if problem == "" {
		b.notes.Hide()
	} else {
		b.note.SetText(problem)
		b.notes.Show()
	}
	b.t.top.Refresh()
	b.t.item.Content.Refresh()
}

// run reads the pipeline and puts what it produces in the grid. A pipeline
// that writes asks first on a production connection, as every other write
// does (FR-4.9).
func (b *pipelineBar) run() { b.runWith(false) }

func (b *pipelineBar) runWith(confirmed bool) {
	text := strings.TrimSpace(b.entry.Text)
	if text == "" {
		b.say("Write the stages to run, as an array: [{\"$match\": {…}}]")
		return
	}
	t := b.t
	t.browseSeq++
	seq := t.browseSeq
	t.footer.SetText("Running the pipeline…")
	b.say("")
	go func() {
		live, err := b.s.d.WS.Connect(t.ctx, t.connID)
		var ps *app.PipelineSource
		if err == nil {
			ps, err = app.NewPipelineSource(t.ctx, live.Source, t.ref, text, confirmed)
		}
		b.s.d.Run(func() {
			if t.ctx.Err() != nil || seq != t.browseSeq {
				return
			}
			if err != nil {
				b.failed(err, text)
				return
			}
			if b.browse == nil {
				b.browse = t.browse // what to put back
			}
			t.footer.SetText("")
			t.model.SetFetcher(ps)
			t.grid.SetSorts(nil)
			t.grid.ScheduleRefresh()
			b.back.Show()
			b.say("")
			b.s.showCount(t)
		})
	}()
}

// failed says what the server or the text made of the pipeline. A pipeline
// that writes to a production connection asks rather than failing.
func (b *pipelineBar) failed(err error, text string) {
	b.t.footer.SetText("")
	if errors.Is(err, source.ErrConfirmationRequired) {
		b.s.askToType(b.t.connID, "Run a Pipeline that Writes?",
			productionBody("pipeline writes to", b.s.connName(b.t.connID), "Nothing has been written yet."),
			"Run",
			func() { b.runWith(true) },
			func() { b.say("Not run") })
		return
	}
	b.say("Could not run the pipeline: " + err.Error())
	b.s.crashed(b.t.connID, err)
}

// restore puts the collection's own documents back in the grid.
func (b *pipelineBar) restore() {
	if b.browse == nil {
		return
	}
	t := b.t
	t.browseSeq++
	t.browse = b.browse
	b.browse = nil
	t.model.SetFetcher(t.browse)
	t.grid.ScheduleRefresh()
	b.back.Hide()
	b.say("")
	b.s.count(t)
}
