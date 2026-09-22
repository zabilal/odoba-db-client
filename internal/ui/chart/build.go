package chart

import (
	"sort"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// Turning a result into the series a kind draws.
//
// Nothing here decides anything: the roles say which columns are which, and
// the kinds say what can be drawn. This is the reading, and its only job is
// to read faithfully — a row that cannot be drawn is left out rather than
// guessed at, and how many were left out is said.

// Built is the series a result gave, and what could not be read.
type Built struct {
	Series []Series

	// Skipped is the rows left out because a value could not be read as a
	// number: a null, or text in a numeric column. A chart drawn over gaps
	// nobody was told about is a chart that lies by omission.
	Skipped int

	// Labels names each X where they are categories rather than numbers, in
	// the order the X values run.
	Labels []string
}

// Build reads a result into series, by the roles given.
func Build(cols []model.ColumnDef, rows []model.Row, r Roles) Built {
	var out Built
	if r.Series != RowNumber {
		return buildSplit(cols, rows, r)
	}
	xs, labels := axisValues(rows, r.X)
	out.Labels = labels

	for _, y := range r.Y {
		s := Series{Name: nameOf2(cols, y)}
		for i, row := range rows {
			v, ok := cell(row, y)
			if !ok {
				out.Skipped++
				continue
			}
			s.Points = append(s.Points, Point{X: xs[i], Y: v, Index: i})
		}
		out.Series = append(out.Series, s)
	}
	return out
}

// buildSplit reads a result whose rows carry which series they belong to,
// which is how a query that returns one row per category per day is drawn.
func buildSplit(cols []model.ColumnDef, rows []model.Row, r Roles) Built {
	var out Built
	xs, labels := axisValues(rows, r.X)
	out.Labels = labels

	// One series per distinct label, kept in the order they first appear:
	// the order of a result is the order somebody asked for.
	at := map[string]int{}
	var order []string
	for i, row := range rows {
		name := cellLabel(row, r.Series)
		if _, held := at[name]; !held {
			at[name] = len(order)
			order = append(order, name)
			out.Series = append(out.Series, Series{Name: name})
		}
		// With a series column, the first drawn column is the value: a
		// second would need a series of its own and a name nobody gave it.
		if len(r.Y) == 0 {
			continue
		}
		v, ok := cell(row, r.Y[0])
		if !ok {
			out.Skipped++
			continue
		}
		s := &out.Series[at[name]]
		s.Points = append(s.Points, Point{X: xs[i], Y: v, Index: i})
	}
	return out
}

// axisValues is the X of every row, and the labels where the X is a column
// of categories rather than of numbers.
//
// A category is given a position — first, second, third — because an axis is
// a line and a word is not a place on one. The words come back as labels so
// that the axis can be drawn with them instead of the numbers.
func axisValues(rows []model.Row, col int) ([]float64, []string) {
	xs := make([]float64, len(rows))
	if col == RowNumber {
		for i := range rows {
			xs[i] = float64(i)
		}
		return xs, nil
	}
	numeric := true
	for _, row := range rows {
		if _, ok := cell(row, col); !ok {
			numeric = false
			break
		}
	}
	if numeric {
		for i, row := range rows {
			xs[i], _ = cell(row, col)
		}
		return xs, nil
	}

	at := map[string]float64{}
	var labels []string
	for i, row := range rows {
		name := cellLabel(row, col)
		if _, held := at[name]; !held {
			at[name] = float64(len(labels))
			labels = append(labels, name)
		}
		xs[i] = at[name]
	}
	return xs, labels
}

// cell reads one value as a number.
func cell(row model.Row, col int) (float64, bool) {
	if col < 0 || col >= len(row) {
		return 0, false
	}
	return Number(row[col])
}

// cellLabel reads one value as text.
func cellLabel(row model.Row, col int) string {
	if col < 0 || col >= len(row) {
		return "(null)"
	}
	return label(row[col])
}

func nameOf2(cols []model.ColumnDef, i int) string {
	if i < 0 || i >= len(cols) {
		return ""
	}
	return cols[i].Name
}

// Values is one column read as the numbers a histogram counts.
func Values(rows []model.Row, col int) []float64 {
	out := make([]float64, 0, len(rows))
	for _, row := range rows {
		if v, ok := cell(row, col); ok {
			out = append(out, v)
		}
	}
	return out
}

// Sorted puts a series in order along its axis.
//
// A line drawn in the order rows arrived zigzags wherever the result was not
// sorted, which reads as data going backwards in time. Every other kind is
// left alone: a bar chart's order is the order somebody asked for.
func Sorted(s Series) Series {
	out := Series{Name: s.Name, Points: append([]Point(nil), s.Points...)}
	sort.SliceStable(out.Points, func(i, j int) bool { return out.Points[i].X < out.Points[j].X })
	return out
}
