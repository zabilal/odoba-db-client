package shell

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	fcanvas "fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/source"
	uitheme "github.com/ikigai-db/ikigai-db/internal/ui/theme"
)

// The way into a query plan (FR-5.13).
//
// A plan is read to find the step to change, so the step that did the most
// work is what the window puts first: every line carries its own share of
// the work as a bar and as a number, and the hottest is marked. Colour is
// never the only thing saying so, because about one man in twelve cannot
// rely on it (NFR-A1).
//
// Asking the planner what it would do runs nothing. Measuring runs the
// statement, which on a production connection is asked about first and on a
// read-only one is refused — by the same guard everything else goes through.

// explainTimeout bounds asking for a plan. Measuring runs the statement, so
// it is the statement's own patience that matters, not the planner's.
const explainTimeout = 5 * time.Minute

// planPanel is a plan's state.
type planPanel struct {
	s *Shell
	t *tab

	sql     string
	analyze bool
	plan    *source.Plan
	reading app.PlanReading

	body *fyne.Container
	raw  *widget.Entry
}

func planKey(connID, sql string) string { return "plan:" + connID + ":" + sql }

// canExplain reports whether the query in front can be planned.
func (s *Shell) canExplain() bool {
	_, sql, ok := s.explainTarget()
	return ok && sql != ""
}

// canAnalyse reports whether it can also be run and measured.
func (s *Shell) canAnalyse() bool {
	t, sql, ok := s.explainTarget()
	if !ok || sql == "" {
		return false
	}
	live, open := s.d.WS.Get(t.connID)
	return open && app.CanAnalyse(live.Source)
}

// explainTarget is the query tab in front and the statement to plan: what is
// selected, or the one the caret is in.
func (s *Shell) explainTarget() (*tab, string, bool) {
	t, q := s.activeQuery()
	if q == nil || q.session == nil {
		return nil, "", false
	}
	live, open := s.d.WS.Get(t.connID)
	if !open || !app.CanExplain(live.Source) {
		return nil, "", false
	}
	doc := q.editor.Document()
	script := doc.Text()
	if from, _, sel := doc.Selection(); sel {
		_ = from
		script = doc.SelectedText()
	} else if stmt, _, ok := q.session.StatementAt(script, doc.Offset(doc.Caret())); ok {
		script = stmt
	}
	return t, strings.TrimSpace(script), true
}

func (s *Shell) explainActive(analyze bool) {
	if t, sql, ok := s.explainTarget(); ok && sql != "" {
		s.OpenPlan(t, sql, analyze, false)
	}
}

// OpenPlan asks for a plan and shows it, or brings the tab already on it
// forward.
func (s *Shell) OpenPlan(of *tab, sql string, analyze, confirmed bool) *tab {
	key := planKey(of.connID, sql)
	t := s.tabFor(key)
	if t == nil {
		ctx, cancel := context.WithCancel(s.ctx)
		t = &tab{key: key, connID: of.connID, label: planLabel(sql), structure: true, ctx: ctx, cancel: cancel,
			body: container.NewStack(quiet("Asking for the plan…")), footer: widget.NewLabel("")}
		t.footer.Importance = widget.LowImportance
		t.item = container.NewTabItem("Plan: "+t.label, container.NewBorder(nil, t.footer, nil, nil, t.body))
		s.open = append(s.open, t)
		s.addTab(t)
	} else {
		s.selectTab(t)
		t.body.Objects = []fyne.CanvasObject{quiet("Asking for the plan…")}
		t.body.Refresh()
	}
	s.sync()

	go func() {
		ctx, cancel := context.WithTimeout(t.ctx, explainTimeout)
		defer cancel()
		live, err := s.d.WS.Connect(ctx, of.connID)
		var plan *source.Plan
		if err == nil {
			plan, err = app.Explain(ctx, live.Source,
				source.Statement{SQL: sql, Confirmed: confirmed}, analyze)
		}
		s.d.Run(func() {
			if t.ctx.Err() != nil {
				return
			}
			if err != nil {
				s.planRefused(of, t, sql, analyze, err)
				return
			}
			s.showPlan(t, sql, analyze, plan)
		})
	}()
	return t
}

// planRefused says why, and asks where asking is what is missing.
func (s *Shell) planRefused(of, t *tab, sql string, analyze bool, err error) {
	switch {
	case errors.Is(err, source.ErrConfirmationRequired):
		s.askToType(t.connID, "Run This on Production?",
			productionBody("statement changes data on", s.connName(t.connID),
				"Measuring a plan runs the statement. Nothing has run yet."),
			"Run",
			func() { s.OpenPlan(of, sql, analyze, true) },
			func() { s.tabFailed(t, fmt.Errorf("not run")) })
	case errors.Is(err, source.ErrReadOnly):
		s.tabFailed(t, fmt.Errorf(
			"measuring a plan runs the statement, which changes data, and this connection is read-only"))
	default:
		s.tabFailed(t, fmt.Errorf("could not read the plan: %w", err))
	}
}

// planLabel is what to call a plan, which is the start of the statement it
// is of: a plan of one query among several has to be told apart from the
// others.
func planLabel(sql string) string {
	one := strings.Join(strings.Fields(sql), " ")
	if len(one) > 40 {
		return one[:39] + "…"
	}
	return one
}

// showPlan draws the tree.
func (s *Shell) showPlan(t *tab, sql string, analyze bool, plan *source.Plan) {
	p := &planPanel{s: s, t: t, sql: sql, analyze: analyze, plan: plan, reading: app.ReadPlan(plan)}
	t.plan = p
	p.body = container.NewVBox()
	p.raw = widget.NewMultiLineEntry()
	p.raw.SetText(plan.Text)
	p.raw.Wrapping = fyne.TextWrapOff

	rows := container.NewVScroll(p.body)
	words := widget.NewAccordion(widget.NewAccordionItem("The server's own words",
		container.NewVScroll(p.raw)))

	t.body.Objects = []fyne.CanvasObject{
		container.NewBorder(p.toolbar(), words, nil, nil, rows),
	}
	p.draw()
	t.body.Refresh()
}

// toolbar is the few things a plan can be told to do.
func (p *planPanel) toolbar() fyne.CanvasObject {
	again := widget.NewButton("Ask Again", func() { p.s.OpenPlan(p.t, p.sql, p.analyze, false) })
	measure := widget.NewButton("Run and Measure", func() { p.s.OpenPlan(p.t, p.sql, true, false) })
	live, open := p.s.d.WS.Get(p.t.connID)
	if !open || !app.CanAnalyse(live.Source) || p.analyze {
		measure.Disable()
	}
	return container.NewHBox(again, measure)
}

// draw builds one line per step.
func (p *planPanel) draw() {
	pal := p.s.colours()
	p.body.Objects = nil
	for i, row := range p.reading.Rows {
		p.body.Add(p.line(pal, i, row))
	}
	p.body.Refresh()
	p.say()
}

// line is one step: how much of the work it did, what it is, and what it
// returned.
func (p *planPanel) line(pal uitheme.Palette, i int, row app.PlanRow) fyne.CanvasObject {
	bar := heatBar(pal, row.Share, p.reading.By)

	share := widget.NewLabel(sharePercent(row.Share, p.reading.By))
	share.Alignment = fyne.TextAlignTrailing
	share.TextStyle = fyne.TextStyle{Monospace: true}

	name := widget.NewLabel(strings.Repeat("    ", row.Depth) + row.Node.Operation)
	name.Truncation = fyne.TextTruncateEllipsis
	if i == p.reading.Hottest {
		// The step that did the most work is what a plan is opened to find,
		// and weight says so where colour alone would not.
		name.TextStyle = fyne.TextStyle{Bold: true}
	}

	figures := widget.NewLabel(figuresOf(row.Node))
	figures.Importance = widget.LowImportance
	figures.Alignment = fyne.TextAlignTrailing

	head := container.NewBorder(nil, nil, container.NewHBox(bar, share), figures, name)
	if row.Node.Detail == "" {
		return head
	}
	detail := widget.NewLabel(strings.Repeat("    ", row.Depth+1) + row.Node.Detail)
	detail.Importance = widget.LowImportance
	// Cut rather than wrapped: a condition can be a page long, and a step
	// that took three lines to describe would push the next step off the
	// screen. The whole of it is in the server's own words below.
	detail.Truncation = fyne.TextTruncateEllipsis
	return container.NewVBox(head, detail)
}

// heatWidth is how wide a share's bar is at its fullest, and heatMin how
// wide the smallest share that did any work at all is drawn: a step that is
// there is drawn as being there.
const (
	heatWidth = 48
	heatMin   = 2
)

// heatBar is how much of the work a step did, drawn as a length in a track
// so that the rows line up and the bars can be compared down the column.
func heatBar(pal uitheme.Palette, share float64, by app.Measured) fyne.CanvasObject {
	filled := float32(0)
	if by != app.ByNothing && share > 0 {
		filled = max(float32(share*heatWidth), heatMin)
	}
	bar := fcanvas.NewRectangle(heatColour(pal, share))
	bar.SetMinSize(fyne.NewSize(filled, 12))
	bar.CornerRadius = 2
	rest := fcanvas.NewRectangle(pal.Separator)
	rest.SetMinSize(fyne.NewSize(heatWidth-filled, 12))
	rest.CornerRadius = 2
	return container.NewHBox(bar, rest)
}

// heatColour is how hot a step is.
//
// Cold to hot through the palette's own status colours, which is what every
// other warning in the window is drawn in. The bar is never the only thing
// saying how hot a step is: the percentage is beside it and the hottest step
// is emboldened.
func heatColour(pal uitheme.Palette, share float64) color.NRGBA {
	switch {
	case share >= 0.5:
		return pal.Danger
	case share >= 0.2:
		return pal.Warning
	case share > 0:
		return pal.ControlAccent
	}
	return pal.Separator
}

// sharePercent writes a share, or says there is nothing to write.
func sharePercent(share float64, by app.Measured) string {
	if by == app.ByNothing {
		return "    "
	}
	return strconv.FormatFloat(share*100, 'f', 0, 64) + "%"
}

// figuresOf is what a step returned, and what it was expected to return.
func figuresOf(n *source.PlanNode) string {
	var parts []string
	switch {
	case n.ActualRows >= 0 && n.EstimatedRows >= 0:
		parts = append(parts, fmt.Sprintf("%s (%s expected)",
			nounCount(int(n.ActualRows), "row"), group(n.EstimatedRows)))
	case n.ActualRows >= 0:
		parts = append(parts, nounCount(int(n.ActualRows), "row"))
	case n.EstimatedRows >= 0:
		parts = append(parts, nounCount(int(n.EstimatedRows), "row")+" expected")
	}
	if n.ActualTime > 0 {
		parts = append(parts, n.ActualTime.Round(time.Microsecond).String())
	} else if n.EstimatedCost >= 0 {
		parts = append(parts, "cost "+strconv.FormatFloat(n.EstimatedCost, 'f', -1, 64))
	}
	return strings.Join(parts, " · ")
}

// say tells the footer what the plan is, and what its heat means.
func (p *planPanel) say() {
	said := nounCount(len(p.reading.Rows), "step") + "."
	switch p.reading.By {
	case app.ByTime:
		said += fmt.Sprintf(" Measured: %s in all. The bars are each step's share of that time.",
			p.reading.Duration.Round(time.Microsecond))
	case app.ByCost:
		said += " Estimated: the bars are each step's share of the planner's cost, not of time taken."
	default:
		said += " This server publishes no estimates, so nothing is measured here."
	}
	if wrong := app.PlanMisestimates(p.plan); len(wrong) > 0 {
		// A plan goes wrong where the estimate was wrong, so the step that
		// was furthest out is worth naming.
		said += fmt.Sprintf(" %s expected %d rows and returned %d.",
			wrong[0].Node.Operation, wrong[0].Node.EstimatedRows, wrong[0].Node.ActualRows)
	}
	p.t.footer.SetText(said)
}
