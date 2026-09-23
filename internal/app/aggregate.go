package app

import (
	"math"
	"strconv"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// Adding up what is selected (FR-3.15).
//
// A spreadsheet answers this the moment a selection changes, and so does
// this: it is arithmetic over values already on the screen, and it asks the
// server nothing. What it cannot do is answer for rows nobody has read, so
// it says how many it looked at and leaves the whole column to the panel
// that measures one (FR-3.14).

// Aggregate is what a set of cells adds up to.
type Aggregate struct {
	// Cells is how many were looked at, Filled how many held anything, and
	// Numbers how many of those could be read as a number. The three are
	// worth telling apart: a column of dates has cells and no numbers, and
	// a total over half a column is not the total.
	Cells, Filled, Numbers int

	// Sum, Mean, Min and Max are over the numbers alone, and mean nothing
	// where there were none.
	Sum, Mean, Min, Max float64
}

// Aggregates adds up a set of values.
func Aggregates(values []any) Aggregate {
	out := Aggregate{Cells: len(values), Min: math.Inf(1), Max: math.Inf(-1)}
	for _, v := range values {
		if v == nil {
			continue
		}
		out.Filled++
		n, ok := number(v)
		if !ok {
			continue
		}
		out.Numbers++
		out.Sum += n
		out.Min, out.Max = math.Min(out.Min, n), math.Max(out.Max, n)
	}
	if out.Numbers == 0 {
		out.Min, out.Max = 0, 0
		return out
	}
	out.Mean = out.Sum / float64(out.Numbers)
	return out
}

// number reads a value as one, the way a chart does: everything an engine
// counts with, and nothing else.
//
// Neither an infinity nor a not-a-number is added up. A total that came back
// as "NaN" because one row held one would say nothing about the rest.
func number(v any) (float64, bool) {
	switch n := v.(type) {
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
		return safe(float64(n))
	case float64:
		return safe(n)
	case bool:
		if n {
			return 1, true
		}
		return 0, true
	case model.Decimal:
		// A decimal travels as text so that no digit is lost on the way
		// (ADR-0018). A total is read rather than stored, so reading it as
		// a float here loses nothing that was kept.
		f, err := strconv.ParseFloat(string(n), 64)
		if err != nil {
			return 0, false
		}
		return safe(f)
	}
	return 0, false
}

func safe(f float64) (float64, bool) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}
