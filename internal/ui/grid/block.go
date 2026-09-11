package grid

import (
	"encoding/csv"
	"strings"
)

// ParseBlock reads text pasted into the grid as rows of cells (FR-4.10,
// ADR-0037). Text with a tab anywhere is TSV, as spreadsheets and Copy
// Cells write it, quoted as CSV is. Lines without a tab are CSV only when
// there are several and every one has the same number of fields, more than
// one: otherwise a comma is part of a value, and each line is one cell. One
// line break at the end is none.
func ParseBlock(text string) [][]string {
	text = strings.TrimSuffix(strings.TrimSuffix(text, "\n"), "\r")
	if text == "" {
		return nil
	}
	if strings.Contains(text, "\t") {
		if rows, ok := delimited(text, '\t'); ok {
			return rows
		}
	} else if rows, ok := delimited(text, ','); ok && len(rows) > 1 && rectangular(rows) {
		return rows
	}
	var out [][]string
	for _, line := range strings.Split(text, "\n") {
		out = append(out, []string{strings.TrimSuffix(line, "\r")})
	}
	return out
}

// delimited reads text as CSV, or as TSV quoted as CSV is.
func delimited(text string, comma rune) ([][]string, bool) {
	r := csv.NewReader(strings.NewReader(text))
	r.Comma, r.LazyQuotes, r.FieldsPerRecord = comma, true, -1
	rows, err := r.ReadAll()
	return rows, err == nil && len(rows) > 0
}

// rectangular reports whether every row has the same number of fields, more
// than one.
func rectangular(rows [][]string) bool {
	for _, r := range rows {
		if len(r) < 2 || len(r) != len(rows[0]) {
			return false
		}
	}
	return true
}
