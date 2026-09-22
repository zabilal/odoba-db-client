package shell

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/app/filterexpr"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/ui/chart"
	"github.com/ikigai-db/ikigai-db/internal/ui/filedlg"
	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
)

// The way into a chart (FR-11.1, FR-11.2, FR-11.3).
//
// A chart is of a result, so it opens from the result: the rows are read
// once, into a tab of their own, and every change of kind or of axis redraws
// from those rows rather than asking the server again. A chart that changed
// under somebody's hands as they tried three kinds of it would not be a
// chart of anything.

// chartRows is the most rows a chart reads.
//
// Enough for the hundred thousand points the drawing was measured against
// (ADR-0004), and a limit rather than everything because a chart of ten
// million rows is a wait, not a picture. How many were read is always said.
//
// A variable so that a test can show a result being cut short without
// making one of a hundred thousand rows.
var chartRows = int64(100_000)

// chartRead bounds reading them, which on a large result is many fetches.
const chartReadTimeout = 2 * time.Minute

// chartPanel is a chart's state.
type chartPanel struct {
	s *Shell
	t *tab

	// cols and rows are the result as it was read, kept so that changing
	// the kind or an axis redraws instead of re-reading.
	cols []model.ColumnDef
	rows []model.Row
	// short says the result had more rows than were read.
	short bool

	kind  chart.Kind
	roles chart.Roles
	built chart.Built

	w      *chart.Widget
	holder *fyne.Container
	kinds  *widget.Select
	xs     *widget.Select
	ys     *widget.Select
	splits *widget.Select
	name   string
}

func chartKey(of string) string { return "chart:" + of }

// chartLabel is what to call a chart, which is what the thing it is of is
// called: a chart named after its table is one somebody can find again.
//
// What the tab is called, rather than the object it is of, because a query
// has no object and is still something worth charting.
func chartLabel(of *tab) string {
	if of.item != nil && of.item.Text != "" {
		return of.item.Text
	}
	return "Result"
}

// canChart reports whether the tab in front has rows to draw.
func (s *Shell) canChart() bool {
	t := s.activeTab()
	return t != nil && t.model != nil && len(t.model.Columns()) > 0
}

func (s *Shell) chartActive() {
	if t := s.activeTab(); t != nil && t.model != nil {
		s.OpenChart(t)
	}
}

// OpenChart draws the rows of a tab, or brings the chart of them forward.
func (s *Shell) OpenChart(of *tab) *tab {
	if of.model == nil {
		return nil
	}
	key := chartKey(of.key)
	if t := s.tabFor(key); t != nil {
		s.selectTab(t)
		return t
	}
	label := chartLabel(of)
	ctx, cancel := context.WithCancel(s.ctx)
	t := &tab{key: key, connID: of.connID, ref: of.ref, label: label, structure: true, ctx: ctx, cancel: cancel,
		body: container.NewStack(quiet("Reading the rows…")), footer: widget.NewLabel("")}
	t.footer.Importance = widget.LowImportance
	t.item = container.NewTabItem("Chart: "+label, container.NewBorder(nil, t.footer, nil, nil, t.body))
	s.open = append(s.open, t)
	s.addTab(t)
	s.sync()

	m := of.model
	cols := m.Columns()
	go func() {
		ctx, cancel := context.WithTimeout(ctx, chartReadTimeout)
		defer cancel()
		rows, err := m.Read(ctx, 0, chartRows)
		s.d.Run(func() {
			if t.ctx.Err() != nil {
				return
			}
			if err != nil {
				s.tabFailed(t, fmt.Errorf("could not read the rows to chart: %w", err))
				return
			}
			s.showChart(t, label, cols, rows)
		})
	}()
	return t
}

// showChart guesses what to draw and draws it.
func (s *Shell) showChart(t *tab, label string, cols []model.ColumnDef, rows []model.Row) {
	p := &chartPanel{s: s, t: t, cols: cols, rows: rows, name: label,
		short: int64(len(rows)) >= chartRows, roles: chart.Guess(cols, rows)}
	p.kind = chart.Suits(cols, rows, p.roles)
	t.chart = p
	p.holder = container.NewStack()
	t.body.Objects = []fyne.CanvasObject{
		container.NewBorder(p.toolbar(), nil, nil, nil, p.holder),
	}
	p.draw()
	t.body.Refresh()
}

// draw builds the picture from the rows already read.
func (p *chartPanel) draw() {
	p.built = chart.Build(p.cols, p.rows, p.roles)
	c := p.s.chartOf(p.kind, p.built, p.roles, p.cols)
	if p.w == nil {
		p.w = chart.New(c)
		p.w.OnPick = p.pick
		p.holder.Objects = []fyne.CanvasObject{p.w}
		p.holder.Refresh()
	} else {
		p.w.SetChart(c)
	}
	p.say()
}

// chartOf dresses a reading in the window's colours.
//
// The chart carries them rather than reading a theme when it draws, so that
// an exported file is the chart that was on the screen.
func (s *Shell) chartOf(k chart.Kind, b chart.Built, r chart.Roles, cols []model.ColumnDef) chart.Chart {
	pal := s.colours()
	return chart.Chart{
		Kind: k, Series: b.Series, Labels: b.Labels,
		Times:      chart.TimesAlong(cols, r.X),
		XTitle:     chart.ColumnName(cols, r.X),
		YTitle:     chart.SeriesTitle(b.Series),
		Colours:    chart.SeriesColours(s.d.Theme.IsDark(s.app.Settings().ThemeVariant())),
		Axis:       pal.Label,
		Grid:       pal.Separator,
		Background: pal.ContentBackground,
	}
}

// say tells the footer what is drawn and what is not.
func (p *chartPanel) say() {
	said := fmt.Sprintf("%s, %s.", nounCount(len(p.rows), "row"), nounCount(len(p.built.Series), "series"))
	if p.short {
		// A chart of the first hundred thousand rows of a million is not a
		// chart of the million, and somebody reading it has to know that.
		said = "The first " + said
	}
	if p.built.Skipped > 0 {
		said += fmt.Sprintf(" %s left out: the value could not be read as a number.",
			nounCount(p.built.Skipped, "row"))
	}
	if err := p.w.Err(); err != nil {
		said += " " + err.Error()
	}
	p.t.footer.SetText(said)
}

// toolbar is what a chart can be told to draw.
func (p *chartPanel) toolbar() fyne.CanvasObject {
	names := make([]string, 0, len(chart.Kinds))
	for _, k := range chart.Kinds {
		names = append(names, chart.KindName(k))
	}
	p.kinds = widget.NewSelect(names, func(name string) {
		p.kind = chart.KindNamed(name)
		p.draw()
	})
	p.kinds.Selected = chart.KindName(p.kind)

	p.xs = widget.NewSelect(p.columnChoices(true), func(name string) {
		p.roles.X = p.columnAt(name)
		p.draw()
	})
	p.xs.Selected = p.columnName(p.roles.X, true)

	p.ys = widget.NewSelect(p.valueChoices(), func(name string) {
		p.roles.Y = p.valuesNamed(name)
		p.draw()
	})
	p.ys.Selected = p.valuesName()

	p.splits = widget.NewSelect(p.columnChoices(false), func(name string) {
		p.roles.Series = p.columnAt(name)
		p.draw()
	})
	p.splits.Selected = p.columnName(p.roles.Series, false)

	return container.NewHBox(
		widget.NewLabel("Chart"), p.kinds,
		widget.NewLabel("of"), p.ys,
		widget.NewLabel("by"), p.xs,
		widget.NewLabel("split by"), p.splits,
		widget.NewButton("Export…", p.export),
	)
}

// rowNumberName and nothingName are the two choices that are not columns.
const (
	rowNumberName = "Row number"
	nothingName   = "Nothing"
	everyValue    = "Every numeric column"
)

func (p *chartPanel) columnChoices(axis bool) []string {
	first := nothingName
	if axis {
		first = rowNumberName
	}
	out := []string{first}
	for _, c := range p.cols {
		out = append(out, c.Name)
	}
	return out
}

func (p *chartPanel) columnAt(name string) int {
	for i, c := range p.cols {
		if c.Name == name {
			return i
		}
	}
	return chart.RowNumber
}

func (p *chartPanel) columnName(i int, axis bool) string {
	if i >= 0 && i < len(p.cols) {
		return p.cols[i].Name
	}
	if axis {
		return rowNumberName
	}
	return nothingName
}

// valueChoices are the columns that can be drawn, and all of them together.
func (p *chartPanel) valueChoices() []string {
	out := []string{everyValue}
	for _, c := range p.cols {
		if chart.Plottable(c) {
			out = append(out, c.Name)
		}
	}
	return out
}

func (p *chartPanel) valuesNamed(name string) []int {
	if name == everyValue {
		return chart.Guess(p.cols, p.rows).Y
	}
	if i := p.columnAt(name); i >= 0 {
		return []int{i}
	}
	return nil
}

func (p *chartPanel) valuesName() string {
	if len(p.roles.Y) == 1 {
		return p.columnName(p.roles.Y[0], true)
	}
	return everyValue
}

// export writes the chart as a picture (FR-11.3).
//
// The format follows the name, as a diagram's does: somebody who types .svg
// means SVG, and asking again in a second dialog would be asking a question
// they have answered.
func (p *chartPanel) export() {
	p.s.d.Files.Save(p.s.win, filedlg.Options{
		Message:    "Export " + p.name,
		Name:       chartFileName(p.name),
		Extensions: []string{"png", "svg"},
		Kind:       "picture",
		Accept:     "Export",
	}, func(path string, err error) {
		switch {
		case err != nil:
			p.s.showError(fmt.Errorf("could not export the chart: %w", err))
		case path == "":
			// Cancelled, which is an answer.
		default:
			p.write(path)
		}
	})
}

func (p *chartPanel) write(path string) {
	if err := p.encode(path); err != nil {
		p.s.showError(fmt.Errorf("could not export the chart: %w", err))
		return
	}
	p.t.footer.SetText("Exported to " + filepath.Base(path) + ".")
}

// encode writes the file, in the format the name asks for.
func (p *chartPanel) encode(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	c := p.w.Chart()
	if strings.EqualFold(filepath.Ext(path), ".svg") {
		err = chart.SVG(f, c, chart.ExportWidth, chart.ExportHeight)
	} else {
		err = chart.PNG(f, c, chart.ExportWidth, chart.ExportHeight, p.s.d.Theme)
	}
	if err != nil {
		return err
	}
	return f.Sync()
}

// chartFileName is what an exported chart is called by default.
func chartFileName(label string) string {
	name := strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == ':' {
			return '-'
		}
		return r
	}, label)
	return name + ".png"
}

// What a click on a chart does (FR-11.4).
//
// A mark that stands for one row can be traced back to it, so clicking it
// narrows the result to the value on the bottom axis at that point — which
// is what somebody who has just seen a spike wants to do next.
//
// A mark that stands for many rows cannot. A histogram's bar counts an
// interval that is open at its top, and the filter row's range is closed at
// both ends, so filtering to it would show rows the bar did not count. It
// says so rather than filtering to something near enough.

// pick acts on a click.
func (p *chartPanel) pick(r chart.Reading) {
	switch {
	case p.kind == chart.Histogram:
		p.t.footer.SetText("That bar counts everything from " + chart.Along(p.w.Chart(), r.X) +
			" up to the next one, which the filter row cannot say exactly.")
	case r.Row < 0 || r.Row >= len(p.rows):
		p.t.footer.SetText("That stands for more than one row, so there is nothing to narrow to.")
	case p.roles.X == chart.RowNumber:
		p.show(r.Row)
	default:
		p.narrow(r.Row)
	}
}

// of is the tab the rows came from, if it is still open.
func (p *chartPanel) of() *tab {
	return p.s.tabFor(strings.TrimPrefix(p.t.key, "chart:"))
}

// narrow filters the result to the value the mark stands for.
//
// The value is taken from the row rather than from the chart, because a
// chart holds numbers and a filter has to hold what the source gave: a
// moment, a decimal, a string.
func (p *chartPanel) narrow(row int) {
	of := p.of()
	if of == nil || of.grid == nil {
		p.t.footer.SetText("The rows this was drawn from are no longer open.")
		return
	}
	col := p.roles.X
	v := p.rows[row][col]
	pk := pick{values: []any{v}, text: filterexpr.Pick([]any{v}, false)}
	if of.picked == nil {
		of.picked = map[int]pick{}
	}
	of.picked[col] = pk
	of.grid.SetFilterText(col, pk.text)
	of.grid.ApplyFilters()
	p.s.selectTab(of)
	p.t.footer.SetText("Narrowed " + chart.ColumnName(p.cols, col) + " to " + pk.text + ".")
}

// show brings the row itself forward, which is what a click means where
// nothing on the bottom axis names anything to filter by.
func (p *chartPanel) show(row int) {
	of := p.of()
	if of == nil || of.grid == nil {
		p.t.footer.SetText("The rows this was drawn from are no longer open.")
		return
	}
	of.grid.GoTo(grid.CellID{Row: row, Col: 0})
	p.s.selectTab(of)
}
