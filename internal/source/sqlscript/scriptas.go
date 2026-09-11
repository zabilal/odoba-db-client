package sqlscript

import "strings"

// The statements "script as" writes (FR-2.4): a starting point a person
// edits and runs, never a statement this application runs itself. The
// dialect supplies every quoted name and every placeholder (ARCH-2); these
// functions only lay the statement out.

// Select writes a SELECT of the named columns, one a line.
func Select(table string, names []string) string {
	return "SELECT " + strings.Join(names, ",\n       ") + "\nFROM " + table + ";\n"
}

// Insert writes an INSERT of one row, with a placeholder for each column.
func Insert(table string, names []string, placeholder func(int) string) string {
	ph := make([]string, len(names))
	for i := range names {
		ph[i] = placeholder(i + 1)
	}
	return "INSERT INTO " + table + " (" + strings.Join(names, ", ") + ")\nVALUES (" + strings.Join(ph, ", ") + ");\n"
}

// Update writes an UPDATE of the set columns in the rows whose keys match,
// with a placeholder for each value, numbered in the order they appear.
// With no keys, a table without a primary key, it matches rows on every set
// column, and a comment says so: nothing narrower is known.
func Update(table string, sets, keys []string, placeholder func(int) string) string {
	n := 0
	next := func() string { n++; return placeholder(n) }
	var b strings.Builder
	where := keys
	if len(keys) == 0 {
		b.WriteString("-- No primary key: this matches rows on every column.\n")
		where = sets
	}
	b.WriteString("UPDATE " + table + "\nSET ")
	for i, c := range sets {
		if i > 0 {
			b.WriteString(",\n    ")
		}
		b.WriteString(c + " = " + next())
	}
	b.WriteString("\nWHERE ")
	for i, c := range where {
		if i > 0 {
			b.WriteString("\n  AND ")
		}
		b.WriteString(c + " = " + next())
	}
	b.WriteString(";\n")
	return b.String()
}
