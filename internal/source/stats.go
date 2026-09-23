package source

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// Measuring a column, in the words every SQL engine shares (FR-3.14).
//
// What is asked for depends on what the column is, and that judgement is the
// same on every engine: an average of text is nothing, and the smallest and
// largest of a JSON document are an order nobody meant. So the decision is
// made once here and each driver renders it in its own quoting.

// StatsQuery is what to ask of a column, and how to read the answer back.
type StatsQuery struct {
	// Select is the list of aggregates, in the order they come back.
	Select []string

	// Distinct, Extremes and Mean say which of them were asked for, so that
	// a row of answers can be read without counting columns.
	Distinct, Extremes, Mean bool
}

// StatsFor decides what can be measured about a column.
//
// count(*) and count(column) are asked of everything: how many rows there
// are and how many of them hold something is the question a statistics
// panel is opened for, and no engine minds being asked it about anything.
func StatsFor(quoted string, def model.ColumnDef) StatsQuery {
	q := StatsQuery{Select: []string{"count(*)", "count(" + quoted + ")"}}
	q.Distinct = ManyValued(def)
	q.Extremes = Ordered(def)
	q.Mean = Numeric(def)
	if q.Distinct {
		q.Select = append(q.Select, "count(DISTINCT "+quoted+")")
	}
	if q.Extremes {
		q.Select = append(q.Select, "min("+quoted+")", "max("+quoted+")")
	}
	if q.Mean {
		q.Select = append(q.Select, "avg("+quoted+")")
	}
	return q
}

// SQL is the whole list, ready to follow a SELECT.
func (q StatsQuery) SQL() string { return strings.Join(q.Select, ", ") }

// Read turns a row of answers into what it says.
//
// A row of the wrong width is a defect rather than a datum, so it is an
// error: a panel showing figures from the wrong columns would be worse than
// one showing none.
func (q StatsQuery) Read(row model.Row) (*ColumnStats, error) {
	if len(row) != len(q.Select) {
		return nil, fmt.Errorf("source: a column's figures came back in %d columns, asked for %d",
			len(row), len(q.Select))
	}
	out := &ColumnStats{Distinct: -1}
	rows, ok := Int64(row[0])
	if !ok {
		return nil, fmt.Errorf("source: how many rows came back as %T", row[0])
	}
	filled, ok := Int64(row[1])
	if !ok {
		return nil, fmt.Errorf("source: how many rows hold something came back as %T", row[1])
	}
	out.Rows, out.Nulls = rows, rows-filled

	at := 2
	if q.Distinct {
		if n, ok := Int64(row[at]); ok {
			out.Distinct = n
		}
		at++
	}
	if q.Extremes {
		out.Min, out.Max = row[at], row[at+1]
		at += 2
	}
	if q.Mean {
		out.Mean, out.HasMean = Float64(row[at])
	}
	return out, nil
}

// ManyValued reports a column whose different values can be counted.
//
// Everything an engine can group by, which is everything but the kinds it
// has no equality for. A binary column is left out because counting the
// distinct values of a column of pictures is a long way to no answer.
func ManyValued(def model.ColumnDef) bool {
	switch def.Type.Class {
	case model.TypeBytes, model.TypeGeometry:
		return false
	}
	return true
}

// Ordered reports a column with a smallest and a largest.
func Ordered(def model.ColumnDef) bool {
	switch def.Type.Class {
	case model.TypeInteger, model.TypeFloat, model.TypeDecimal, model.TypeString,
		model.TypeDate, model.TypeTime, model.TypeTimestamp, model.TypeInterval,
		model.TypeEnum, model.TypeBool, model.TypeUUID:
		return true
	}
	return false
}

// Numeric reports a column with an average.
func Numeric(def model.ColumnDef) bool {
	switch def.Type.Class {
	case model.TypeInteger, model.TypeFloat, model.TypeDecimal:
		return true
	}
	return false
}

// decimalFloat reads the text a decimal travels in.
//
// A decimal is carried as text so that no digit is lost on the way
// (ADR-0018). An average is drawn and compared rather than stored, so
// reading it as a float here loses nothing that was kept.
func decimalFloat(d model.Decimal) (float64, bool) {
	f, err := strconv.ParseFloat(string(d), 64)
	return f, err == nil
}

// Int64 reads a count, whatever width the driver gave it.
func Int64(v any) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case int32:
		return int64(n), true
	case int:
		return int64(n), true
	case uint64:
		return int64(n), true
	case float64:
		return int64(n), true
	case model.Decimal:
		f, ok := decimalFloat(n)
		return int64(f), ok
	}
	return 0, false
}

// Float64 reads an average, which arrives as a number or as the text a
// decimal travels in.
func Float64(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int64:
		return float64(n), true
	case int32:
		return float64(n), true
	case model.Decimal:
		return decimalFloat(n)
	}
	return 0, false
}
