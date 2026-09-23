package shell

import (
	"context"
	"fmt"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/ui/chart"
	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
)

// What a column holds (FR-3.14).
//
// A page of rows is what somebody can already see. The question this answers
// is about the rest: how many there are, how many of them hold nothing, how
// many different values, the smallest and the largest — over the rows the
// grid's own filters select, so that narrowing the grid narrows the answer.
//
// Measuring means counting, which on a large table is a wait. So it is asked
// for rather than kept up to date, and the panel says how long the server
// took, which is the other half of deciding whether to ask again.

// statsTimeout bounds measuring a column. Counting the different values of a
// large table is a full pass, which is the thing itself rather than a stall.
const statsTimeout = 5 * time.Minute

// statsPanel is a column's figures, beside the grid.
type statsPanel struct {
	s   *Shell
	t   *tab
	col int
	def model.ColumnDef

	pop    *widget.PopUp
	body   *fyne.Container
	status *widget.Label
	// seq numbers the readings, so that only the latest lands: somebody
	// clicking down a row of headers asks several times over.
	seq int
}

// canMeasureColumn reports whether the selected column can be measured.
func (s *Shell) canMeasureColumn() bool {
	t := s.activeTab()
	return t != nil && t.browse != nil && t.grid != nil &&
		t.browse.CanMeasure() && t.grid.SelectedColumn() >= 0
}

func (s *Shell) measureSelectedColumn() {
	t := s.activeTab()
	if t == nil || t.grid == nil {
		return
	}
	s.showColumnStats(t, t.grid.SelectedColumn(), s.underHeader(t))
}

// showColumnStats measures a column and shows what it found, at a point on
// screen.
func (s *Shell) showColumnStats(t *tab, col int, at fyne.Position) *statsPanel {
	if t.browse == nil || t.model == nil || !t.browse.CanMeasure() {
		return nil
	}
	cols := t.model.Columns()
	if col < 0 || col >= len(cols) {
		return nil
	}
	p := &statsPanel{s: s, t: t, col: col, def: cols[col]}
	p.status = widget.NewLabel("Measuring " + p.def.Name + "…")
	p.status.Importance = widget.LowImportance
	p.body = container.NewVBox(p.status)

	head := widget.NewLabelWithStyle(p.def.Name, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	p.pop = widget.NewPopUp(container.NewVBox(head, p.body), s.win.Canvas())
	p.pop.ShowAtPosition(at)
	t.stats = p
	p.measure()
	return p
}

// measure asks the server, off the UI goroutine.
func (p *statsPanel) measure() {
	p.seq++
	seq := p.seq
	b, def := p.t.browse, p.def
	go func() {
		ctx, cancel := context.WithTimeout(p.t.ctx, statsTimeout)
		defer cancel()
		st, err := b.Measure(ctx, def)
		var rows []model.Row
		if err == nil && chart.Plottable(def) {
			// How the values are spread, from a sample: the picture of ten
			// thousand values is the picture of a hundred thousand.
			rows, _ = b.Sample(ctx, def, app.SampleLimit)
		}
		p.s.d.Run(func() {
			if p.t.ctx.Err() != nil || seq != p.seq {
				return
			}
			if err != nil {
				p.status.SetText("Could not measure " + def.Name + ": " + err.Error())
				return
			}
			p.show(st, rows)
		})
	}()
}

// show puts the figures up, and the spread under them where there is one.
func (p *statsPanel) show(st *source.ColumnStats, rows []model.Row) {
	p.body.Objects = nil
	for _, f := range figuresOfColumn(st, p.def) {
		p.body.Add(container.NewBorder(nil, nil,
			widget.NewLabel(f.name), widget.NewLabel(f.value)))
	}
	if c, ok := p.spread(rows); ok {
		w := chart.New(c)
		holder := container.NewStack(w)
		holder.Resize(fyne.NewSize(spreadWidth, spreadHeight))
		p.body.Add(container.NewGridWrap(fyne.NewSize(spreadWidth, spreadHeight), holder))
	}
	took := widget.NewLabel("The server took " + st.Duration.Round(time.Millisecond).String() + ".")
	took.Importance = widget.LowImportance
	p.body.Add(took)
	p.body.Refresh()
	p.pop.Resize(p.pop.MinSize())
}

// spreadWidth and spreadHeight are how large the picture of a column's
// values is. Wide enough to read and small enough to sit in a popover
// beside the figures it belongs to.
const (
	spreadWidth  = 320
	spreadHeight = 160
)

// spread is the picture of how a column's values are spread, where it has
// one: a histogram of a sample (ADR-0132).
func (p *statsPanel) spread(rows []model.Row) (chart.Chart, bool) {
	if !chart.Plottable(p.def) {
		return chart.Chart{}, false
	}
	var s chart.Series
	s.Name = p.def.Name
	for i, row := range rows {
		if v, ok := chart.Number(firstOf(row)); ok {
			s.Points = append(s.Points, chart.Point{X: float64(i), Y: v, Index: i})
		}
	}
	if len(s.Points) == 0 {
		// A sample of nothing, or of nothing that is a number: there is no
		// picture of that, and an empty one would look like an answer.
		return chart.Chart{}, false
	}
	pal := p.s.colours()
	return chart.Chart{
		Kind: chart.Histogram, Series: []chart.Series{s}, XTitle: p.def.Name,
		Colours:    chart.SeriesColours(p.s.d.Theme.IsDark(p.s.app.Settings().ThemeVariant())),
		Axis:       pal.Label,
		Grid:       pal.Separator,
		Background: pal.ContentBackground,
	}, true
}

// firstOf is a one-column row's value.
func firstOf(row model.Row) any {
	if len(row) == 0 {
		return nil
	}
	return row[0]
}

// figure is one line of the panel.
type figure struct{ name, value string }

// figuresOfColumn is what was measured, in the order it is read: how much
// there is, then how much of it is missing, then what it looks like.
func figuresOfColumn(st *source.ColumnStats, def model.ColumnDef) []figure {
	out := []figure{{"Rows", group(st.Rows)}}
	switch {
	case st.Rows == 0:
	case st.Nulls == 0:
		out = append(out, figure{"Empty", "none"})
	default:
		out = append(out, figure{"Empty", fmt.Sprintf("%s (%s)", group(st.Nulls), percent(st.Nulls, st.Rows))})
	}
	if st.Distinct >= 0 {
		out = append(out, figure{"Different values", group(st.Distinct)})
	}
	if st.Min != nil {
		out = append(out, figure{"Smallest", shortly(st.Min, def)})
	}
	if st.Max != nil {
		out = append(out, figure{"Largest", shortly(st.Max, def)})
	}
	if st.HasMean {
		out = append(out, figure{"Average", fmt.Sprintf("%.4g", st.Mean)})
	}
	return out
}

// percent is how much of a whole, for a reader rather than for arithmetic.
func percent(part, whole int64) string {
	if whole <= 0 {
		return "0%"
	}
	return fmt.Sprintf("%.1f%%", float64(part)/float64(whole)*100)
}

// shortly is a value in a line of a panel, written the way the grid writes
// it so that the smallest value reads as the cell it came from — long enough
// to recognise and short enough not to make the panel the width of the
// window.
func shortly(v any, def model.ColumnDef) string {
	s := strings.TrimSpace(grid.Format(v, def, time.Local).Text)
	if len([]rune(s)) > 40 {
		return string([]rune(s)[:39]) + "…"
	}
	return s
}
