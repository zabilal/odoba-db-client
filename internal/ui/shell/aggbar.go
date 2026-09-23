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
	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
)

// What is selected, added up (FR-3.15).
//
// A spreadsheet answers this the moment a selection changes, and so does
// this: it is arithmetic over values already on the screen and it asks the
// server nothing, which is what lets it follow a selection down a column
// without anybody waiting.
//
// What it cannot do is answer for rows nobody has read. A selection to the
// end of a large table reaches rows that are not here, so the bar says how
// many it looked at, and the whole column is a separate asking — the same
// one the statistics panel makes, over the same rows, exactly (FR-3.14).

// aggRows is how many rows the bar will add up on its own.
//
// It reads only what is already in memory, so this is a bound on the work
// rather than on the waiting: a selection larger than this is summed as far
// as it goes and says so.
const aggRows = 50_000

// aggBar is the line under a grid that adds up what is selected.
type aggBar struct {
	s *Shell
	t *tab

	box   *fyne.Container
	said  *widget.Label
	whole *widget.Button
	// col is the column the figures are of, and over says whether they are
	// the server's answer for the whole column rather than the selection's.
	col  int
	over bool
	// seq numbers the askings of the server, so only the latest lands.
	seq int
}

// canAggregate reports whether there is a grid to add up.
func (s *Shell) canAggregate() bool {
	t := s.activeTab()
	return t != nil && t.query == nil && t.grid != nil && t.center != nil
}

// toggleAggregates shows the line under the grid, or takes it away.
func (s *Shell) toggleAggregates() {
	if !s.canAggregate() {
		return
	}
	t := s.activeTab()
	if t.agg != nil {
		t.agg.hide()
		t.agg = nil
		s.sync()
		return
	}
	t.agg = s.newAggBar(t)
	t.agg.show()
	s.sync()
}

func (s *Shell) newAggBar(t *tab) *aggBar {
	a := &aggBar{s: s, t: t, col: -1}
	a.said = widget.NewLabel("")
	a.said.Importance = widget.LowImportance
	a.said.Truncation = fyne.TextTruncateEllipsis
	a.whole = widget.NewButton("The Whole Column", a.measureWhole)
	a.whole.Importance = widget.LowImportance
	a.box = container.NewBorder(nil, nil, nil, a.whole, a.said)
	return a
}

// show puts the line under the grid and fills it in.
func (a *aggBar) show() {
	a.t.center.Objects = []fyne.CanvasObject{
		container.NewBorder(nil, a.box, nil, nil, a.t.body),
	}
	a.t.center.Refresh()
	a.follow()
}

// hide takes it away, leaving the grid where it was.
func (a *aggBar) hide() {
	a.t.center.Objects = []fyne.CanvasObject{a.t.body}
	a.t.center.Refresh()
}

// follow adds up whatever is selected now.
func (a *aggBar) follow() {
	a.seq++
	a.over = false
	g, m := a.t.grid, a.t.model
	if g == nil || m == nil {
		a.said.SetText("")
		return
	}
	sel := g.Selection()
	if sel.Empty() {
		a.col = -1
		a.said.SetText("Nothing selected.")
		a.whole.Disable()
		return
	}
	a.col = g.SelectedColumn()
	a.enableWhole()

	values, read, all := selectedValues(m, g, sel)
	agg := app.Aggregates(values)
	a.said.SetText(describeAggregate(agg, a.columnName(), read, all))
}

// enableWhole turns on the way to ask the server, where there is one.
func (a *aggBar) enableWhole() {
	if a.t.browse != nil && a.t.browse.CanMeasure() && a.col >= 0 {
		a.whole.Enable()
		return
	}
	a.whole.Disable()
}

// columnName is what the figures are of.
func (a *aggBar) columnName() string {
	if a.t.model == nil || a.col < 0 {
		return ""
	}
	cols := a.t.model.Columns()
	if a.col >= len(cols) {
		return ""
	}
	return cols[a.col].Name
}

// held is the rows a grid has in memory, which is all a bar will add up.
//
// An interface rather than the model itself, because the claim worth making
// is about which rows are asked for: a bar that asked for a row it did not
// have would queue a fetch for every row of a selection somebody dragged to
// the end of a large table.
type held interface {
	Resident(i int64) bool
	Row(ctx context.Context, i int64) (model.Row, bool)
}

// selectedValues is every selected cell's value, from the rows already
// read. all is false where the selection reached rows that are not here.
func selectedValues(m held, g *grid.TableGrid, sel grid.Selection) (values []any, read int, all bool) {
	first, last := sel.Rows()
	all = true
	for row := first; row <= last && row-first < aggRows; row++ {
		if !m.Resident(int64(row)) {
			all = false
			continue
		}
		r, ok := m.Row(context.Background(), int64(row))
		if !ok {
			all = false
			continue
		}
		read++
		for _, col := range sel.Columns() {
			if !sel.Contains(row, col) || col >= len(r) {
				continue
			}
			values = append(values, r[col])
		}
	}
	if last-first >= aggRows {
		// A selection longer than this was not looked at to its end, which
		// a selection to the last row of a large table always is.
		all = false
	}
	return values, read, all
}

// describeAggregate writes the figures out.
//
// Only what applies: a column of dates has cells and no total, and saying
// "sum 0" of one would be a number nobody's rows put there.
func describeAggregate(agg app.Aggregate, name string, read int, all bool) string {
	var parts []string
	parts = append(parts, nounCount(agg.Cells, "cell"))
	if agg.Filled != agg.Cells {
		parts = append(parts, nounCount(agg.Filled, "value"))
	}
	if agg.Numbers > 0 {
		parts = append(parts,
			"sum "+number(agg.Sum),
			"average "+number(agg.Mean),
			"smallest "+number(agg.Min),
			"largest "+number(agg.Max))
	}
	said := strings.Join(parts, " · ")
	if name != "" {
		said = name + ": " + said
	}
	if !all {
		said += fmt.Sprintf(" — over the %s read so far", nounCount(read, "row"))
	}
	return said
}

// number writes a figure for reading rather than for arithmetic.
func number(v float64) string {
	if v == float64(int64(v)) && v < 1e15 && v > -1e15 {
		return group(int64(v))
	}
	return fmt.Sprintf("%.4g", v)
}

// measureWhole asks the server about the whole column, which is exact and
// is a wait.
func (a *aggBar) measureWhole() {
	if a.t.browse == nil || a.t.model == nil || a.col < 0 {
		return
	}
	cols := a.t.model.Columns()
	if a.col >= len(cols) {
		return
	}
	def := cols[a.col]
	a.seq++
	seq := a.seq
	a.said.SetText("Measuring " + def.Name + "…")
	go func() {
		ctx, cancel := context.WithTimeout(a.t.ctx, statsTimeout)
		defer cancel()
		st, err := a.t.browse.Measure(ctx, def)
		a.s.d.Run(func() {
			if a.t.ctx.Err() != nil || seq != a.seq {
				return
			}
			if err != nil {
				a.said.SetText("Could not measure " + def.Name + ": " + err.Error())
				return
			}
			a.over = true
			a.said.SetText(describeWhole(st, def))
		})
	}()
}

// describeWhole writes the server's own answer for a whole column.
func describeWhole(st *source.ColumnStats, def model.ColumnDef) string {
	parts := []string{nounCount(int(st.Rows), "row")}
	if st.Nulls > 0 {
		parts = append(parts, nounCount(int(st.Rows-st.Nulls), "value"))
	}
	if st.HasSum {
		parts = append(parts, "sum "+number(st.Sum))
	}
	if st.HasMean {
		parts = append(parts, "average "+number(st.Mean))
	}
	if st.Min != nil {
		parts = append(parts, "smallest "+shortly(st.Min, def))
	}
	if st.Max != nil {
		parts = append(parts, "largest "+shortly(st.Max, def))
	}
	return def.Name + ", whole column: " + strings.Join(parts, " · ") +
		" — the server took " + st.Duration.Round(time.Millisecond).String()
}
