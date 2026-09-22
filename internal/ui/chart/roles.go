package chart

import (
	"math"
	"strconv"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// Working out which columns are the axes (FR-11.2).
//
// A result is a table, and a chart is not: something has to say which column
// is along the bottom, which are drawn, and which splits the drawing into
// several. Asking before showing anything is a form to fill in before seeing
// whether the chart was worth having, so this guesses — and everything it
// guesses can be changed, because it will be wrong sometimes and a chart
// nobody can correct is a chart nobody trusts.

// Role is what a column is for.
type Role uint8

const (
	// RoleNone is a column the chart does not use.
	RoleNone Role = iota
	// RoleX is along the bottom.
	RoleX
	// RoleY is drawn.
	RoleY
	// RoleSeries splits the drawing into one per value.
	RoleSeries
)

// RowNumber is the X that is not a column: the position of the row in the
// result, which is what a chart falls back to when nothing else orders it.
const RowNumber = -1

// Roles says what each column is for. Y is in the order the columns are
// drawn, which is the order they were selected in.
type Roles struct {
	X      int
	Y      []int
	Series int
}

// Plottable reports whether a column holds numbers a chart can draw.
func Plottable(c model.ColumnDef) bool {
	switch c.Type.Class {
	case model.TypeInteger, model.TypeFloat, model.TypeDecimal:
		return true
	}
	return false
}

// orderly reports a column whose values put the rows in an order: a moment
// in time, or a date. These make an X without anybody saying so.
func orderly(c model.ColumnDef) bool {
	switch c.Type.Class {
	case model.TypeDate, model.TypeTime, model.TypeTimestamp:
		return true
	}
	return false
}

// labelled reports a column that can name something — a category along the
// bottom, or the name of a series.
func labelled(c model.ColumnDef) bool {
	switch c.Type.Class {
	case model.TypeString, model.TypeEnum, model.TypeBool:
		return true
	}
	return false
}

// Guess works out what to draw from what the columns are and what is in
// them.
//
// The order is what somebody would do by eye: a moment in time goes along
// the bottom, numbers are drawn, and a column of a few repeated labels
// splits them. Everything else is left out rather than pressed into a role
// it does not fit — a chart of an identifier is a chart of nothing.
func Guess(cols []model.ColumnDef, rows []model.Row) Roles {
	r := Roles{X: RowNumber, Series: RowNumber}

	// A moment in time is the most likely X there is: a result ordered by
	// one is almost always a result about change.
	for i, c := range cols {
		if orderly(c) {
			r.X = i
			break
		}
	}
	// Failing that, a column of labels: the categories of a bar chart.
	if r.X == RowNumber {
		for i, c := range cols {
			if labelled(c) && !manyValued(rows, i) {
				r.X = i
				break
			}
		}
	}
	// A column of few repeated labels splits the numbers into series. It is
	// looked for after the X so that the two are never the same column.
	for i, c := range cols {
		if i != r.X && labelled(c) && !manyValued(rows, i) {
			r.Series = i
			break
		}
	}
	for i, c := range cols {
		if i != r.X && i != r.Series && Plottable(c) {
			r.Y = append(r.Y, i)
		}
	}
	// A result of one numeric column needs no special case: nothing orders
	// it and nothing labels it, so the X stays the row number and the
	// column is drawn. The two sets never overlap — a column that orders is
	// a time and a column that labels is text — so an X is never a column
	// somebody wanted drawn.
	return r
}

// seriesLimit is how many distinct values a column may hold and still split a
// chart into series.
//
// Past this it is not a category but an identifier, and a chart with fifty
// lines on it is a chart of nothing. It is also the point past which the
// colours would have to repeat.
const seriesLimit = 12

// manyValued reports a column holding more distinct values than a category
// has, which is how an identifier is told from a label without knowing what
// either is called.
func manyValued(rows []model.Row, col int) bool {
	seen := map[string]bool{}
	for _, row := range rows {
		if col >= len(row) {
			continue
		}
		seen[label(row[col])] = true
		if len(seen) > seriesLimit {
			return true
		}
	}
	return false
}

// Number reads a cell as a number a chart can draw, and says whether it is
// one.
//
// A decimal is carried as text so that no precision is lost between the
// server and the display, and is read back here: a chart is drawn in pixels,
// so the precision that matters is already gone by the time it is drawn.
func Number(v any) (float64, bool) {
	switch n := v.(type) {
	case nil:
		return 0, false
	case int:
		return float64(n), true
	case int8:
		return float64(n), true
	case int16:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint8:
		return float64(n), true
	case uint16:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	case float32:
		return float64(n), true
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) {
			// Neither is a place on an axis, and drawing one would move
			// every other point to make room for it.
			return 0, false
		}
		return n, true
	case bool:
		if n {
			return 1, true
		}
		return 0, true
	case model.Decimal:
		f, err := strconv.ParseFloat(string(n), 64)
		return f, err == nil
	case time.Time:
		// A moment is drawn as the seconds since the epoch, which is what
		// makes an axis of times the same kind of axis as one of numbers.
		return float64(n.UnixNano()) / 1e9, true
	}
	return 0, false
}

// label reads a cell as the text a chart names it by.
func label(v any) string {
	switch s := v.(type) {
	case nil:
		// A chart cannot leave a gap in a legend, and "" would read as a
		// name somebody chose.
		return "(null)"
	case string:
		return s
	case model.Decimal:
		return string(s)
	case time.Time:
		return s.Format(time.RFC3339)
	case bool:
		return strconv.FormatBool(s)
	}
	if f, ok := Number(v); ok {
		return strconv.FormatFloat(f, 'g', -1, 64)
	}
	return ""
}
