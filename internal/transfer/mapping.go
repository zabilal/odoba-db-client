package transfer

import (
	"errors"
	"strings"
	"time"
	"unicode"

	"github.com/ikigai-db/ikigai-db/internal/export"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/value"
)

// Which of a file's columns fills which of a table's, and each value made
// its table column's type (FR-10.5, ADR-0046).

// Pair maps one of a file's columns to one of a table's.
type Pair struct {
	From int    // the file's column, by its place
	To   string // the table's column, by its name
}

// Suggest pairs a file's columns with a table's of the same name, telling
// no difference between cases, spaces, underscores and hyphens ("Order ID"
// is order_id). A file column no table column matches is left out, and each
// table column is taken once, by the first file column to match it.
func Suggest(from, to []model.ColumnDef) []Pair {
	byKey := map[string]string{}
	for _, c := range to {
		if k := nameKey(c.Name); byKey[k] == "" {
			byKey[k] = c.Name
		}
	}
	var out []Pair
	taken := map[string]bool{}
	for i, c := range from {
		if name := byKey[nameKey(c.Name)]; name != "" && !taken[name] {
			taken[name] = true
			out = append(out, Pair{From: i, To: name})
		}
	}
	return out
}

// nameKey is a name as Suggest compares it: letters and digits alone, in
// lower case.
func nameKey(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

// CellError is a value a mapping could not make its column's.
type CellError struct {
	Column string // the table's column
	Value  string // the value as the file has it
	Err    error
}

func (e CellError) Error() string {
	return e.Column + ": " + e.Err.Error() + " (" + e.Value + ")"
}

var errNeedsValue = errors.New("needs a value")

// Coerce makes a file's row the table's values, one for each pair, in the
// pairs' order. Text is read as its column's type, as typing it into a cell
// is (value.Parse); a value the file gave a type is written as text and read
// the same way, save a date and time, which goes as it is into a column of
// dates or times. NULL stays NULL, and is an error in a column that cannot
// hold it. It says, of each value it could not make, its column and why.
func Coerce(row model.Row, from []model.ColumnDef, pairs []Pair, to map[string]model.ColumnDef) ([]any, []CellError) {
	out := make([]any, len(pairs))
	var errs []CellError
	for i, p := range pairs {
		col := to[p.To]
		var v any
		if p.From >= 0 && p.From < len(row) {
			v = row[p.From]
		}
		switch x := v.(type) {
		case nil:
			if !col.Type.Nullable {
				errs = append(errs, CellError{Column: p.To, Value: "NULL", Err: errNeedsValue})
			}
			continue
		case time.Time:
			if c := col.Type.Class; c == model.TypeDate || c == model.TypeTime || c == model.TypeTimestamp {
				out[i] = x
				continue
			}
		}
		var text string
		if s, ok := v.(string); ok {
			text = s
		} else {
			fc := model.ColumnDef{}
			if p.From < len(from) {
				fc = from[p.From]
			}
			text = export.Text(v, fc)
		}
		got, err := value.Parse(text, col, time.Local)
		if err != nil {
			errs = append(errs, CellError{Column: p.To, Value: text, Err: err})
			continue
		}
		out[i] = got
	}
	return out, errs
}
