package sqlscript

import (
	"strconv"
	"testing"
)

func dollar(i int) string { return "$" + strconv.Itoa(i) }
func question(int) string { return "?" }

func TestScriptAsLaysOutWhatTheDialectQuoted(t *testing.T) {
	table, cols := `"s"."t"`, []string{`"id"`, `"name"`}
	for _, c := range []struct{ name, got, want string }{
		{"select", Select(table, cols), "SELECT \"id\",\n       \"name\"\nFROM \"s\".\"t\";\n"},
		{"insert", Insert(table, cols, dollar), "INSERT INTO \"s\".\"t\" (\"id\", \"name\")\nVALUES ($1, $2);\n"},
		{"insert with ?", Insert(table, cols, question), "INSERT INTO \"s\".\"t\" (\"id\", \"name\")\nVALUES (?, ?);\n"},
		{"update by key", Update(table, []string{`"name"`}, []string{`"id"`}, dollar),
			"UPDATE \"s\".\"t\"\nSET \"name\" = $1\nWHERE \"id\" = $2;\n"},
		{"update with no key", Update(table, cols, nil, dollar),
			"-- No primary key: this matches rows on every column.\nUPDATE \"s\".\"t\"\nSET \"id\" = $1,\n    \"name\" = $2\nWHERE \"id\" = $3\n  AND \"name\" = $4;\n"},
	} {
		if c.got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, c.got, c.want)
		}
	}
}
