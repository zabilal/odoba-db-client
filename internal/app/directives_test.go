package app

import (
	"fmt"
	"strings"
	"testing"
)

// sections describes a split: each section's connection, where it starts and
// what it runs, with its line breaks shown.
func sections(ss []Section) string {
	var out []string
	for _, s := range ss {
		out = append(out, fmt.Sprintf("%s@%d[%s]", s.Connection, s.Offset, strings.ReplaceAll(s.Text, "\n", "⏎")))
	}
	return strings.Join(out, " ")
}

func TestAScriptIsSplitAtItsConnections(t *testing.T) {
	for name, c := range map[string]struct{ script, want string }{
		"no directive is the script":     {"SELECT 1;\nSELECT 2;", "@0[SELECT 1;⏎SELECT 2;]"},
		"nothing":                        {"", ""},
		"only space":                     {" \n\t\n", ""},
		"one, at the top":                {"@dev\nSELECT 1;", "dev@5[SELECT 1;]"},
		"before the first is the tab's":  {"SELECT 0;\n@dev\nSELECT 1;\n@prod\nSELECT 2;\n", "@0[SELECT 0;⏎] dev@15[SELECT 1;⏎] prod@31[SELECT 2;⏎]"},
		"a name in quotes":               {"@\"My Prod (replica)\"\nSELECT 1;", "My Prod (replica)@21[SELECT 1;]"},
		"names with marks":               {"@db-1_x.y\nSELECT 1;\n@üñí\nSELECT 2;", "db-1_x.y@10[SELECT 1;⏎] üñí@28[SELECT 2;]"},
		"spaces about it":                {"  @dev  \r\nSELECT 1;", "dev@10[SELECT 1;]"},
		"the last line":                  {"SELECT 1;\n@dev", "@0[SELECT 1;⏎]"},
		"two in a row":                   {"@dev\n@prod\nSELECT 1;", "prod@11[SELECT 1;]"},
		"an empty section is left out":   {"@dev\n\n  \n@prod\nSELECT 1;", "prod@15[SELECT 1;]"},
		"back to the same":               {"@a\nSELECT 1;\n@b\nSELECT 2;\n@a\nSELECT 3;", "a@3[SELECT 1;⏎] b@16[SELECT 2;⏎] a@29[SELECT 3;]"},
		"after a comment at the end":     {"SELECT 1; -- done\n@dev\nSELECT 2;", "@0[SELECT 1; -- done⏎] dev@23[SELECT 2;]"},
		"after a line of comment":        {"SELECT 1;\n-- now the other\n@dev\nSELECT 2;", "@0[SELECT 1;⏎-- now the other⏎] dev@32[SELECT 2;]"},
		"after two statements on a line": {"SELECT 1; SELECT 2;\n@dev\nSELECT 3;", "@0[SELECT 1; SELECT 2;⏎] dev@25[SELECT 3;]"},
		"after a blank line":             {"SELECT 1;\n\n@dev\nSELECT 2;", "@0[SELECT 1;⏎⏎] dev@16[SELECT 2;]"},
		// Not between statements, it is part of one: a line of a string, a
		// variable alone on a line.
		"inside a string":       {"SELECT '\n@home\n';", "@0[SELECT '⏎@home⏎';]"},
		"inside a statement":    {"SELECT\n@x\nFROM t;\n@dev\nSELECT 1;", "@0[SELECT⏎@x⏎FROM t;⏎] dev@23[SELECT 1;]"},
		"a semicolon mid-line":  {"SELECT ';' ||\n@x\n;", "@0[SELECT ';' ||⏎@x⏎;]"},
		"a comment with one in": {"SELECT 1 -- a; b\n@x\n;", "@0[SELECT 1 -- a; b⏎@x⏎;]"},
		// What is not a directive is the script's.
		"more on the line":  {"@dev SELECT 1;", "@0[@dev SELECT 1;]"},
		"an assignment":     {"@x = 1;", "@0[@x = 1;]"},
		"an at sign alone":  {"@\nSELECT 1;", "@0[@⏎SELECT 1;]"},
		"empty quotes":      {"@\"\"\nSELECT 1;", "@0[@\"\"⏎SELECT 1;]"},
		"blank quotes":      {"@\" \"\nSELECT 1;", "@0[@\" \"⏎SELECT 1;]"},
		"a quote in quotes": {"@\"a\"b\"\nSELECT 1;", "@0[@\"a\"b\"⏎SELECT 1;]"},
		"one quote":         {"@\"\nSELECT 1;", "@0[@\"⏎SELECT 1;]"},
		"a mark in a name":  {"@a!b\nSELECT 1;", "@0[@a!b⏎SELECT 1;]"},
		"two at signs":      {"@@version\n;", "@0[@@version⏎;]"},
		"not at the start":  {"x @dev\nSELECT 1;", "@0[x @dev⏎SELECT 1;]"},
	} {
		if got := sections(Sections(c.script)); got != c.want {
			t.Errorf("%s:\n%s\nfor\n%s", name, got, c.want)
		}
	}
	// Each section's text is where it says it is.
	script := "SELECT 0;\n@dev\nSELECT 1;\n@\"p q\"\nSELECT 2;"
	for _, s := range Sections(script) {
		if script[s.Offset:s.Offset+len(s.Text)] != s.Text {
			t.Errorf("%q is not at %d", s.Text, s.Offset)
		}
	}
}

func TestAPieceOfAScriptIsForTheConnectionItIsIn(t *testing.T) {
	// A selection: text from the middle of a script, in a part that is for
	// one connection, which its own directives then change.
	got := sections(SectionsFrom("SELECT 1;\n@prod\nSELECT 2;", "dev", 100))
	if got != "dev@100[SELECT 1;⏎] prod@116[SELECT 2;]" {
		t.Errorf("%s", got)
	}
	if !Across(Sections("@dev\nSELECT 1;")) || Across(Sections("SELECT 1;")) || Across(nil) || !Across(SectionsFrom("SELECT 1;", "dev", 0)) {
		t.Error("a script is across connections when a part of it is for one named")
	}
	ss := Sections("SELECT 0;\n@dev\nSELECT 1;\n@prod\nSELECT 2;\n")
	for offset, want := range map[int]string{0: "", 9: "", 10: "", 14: "", 15: "dev", 24: "dev", 25: "dev", 30: "dev", 31: "prod", 999: "prod", -5: ""} {
		if s, ok := SectionAt(ss, offset); !ok || s.Connection != want {
			t.Errorf("offset %d is in %q, not %q", offset, s.Connection, want)
		}
	}
	if _, ok := SectionAt(nil, 3); ok {
		t.Error("a script of nothing has a section")
	}
}
