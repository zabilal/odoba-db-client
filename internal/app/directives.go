package app

import (
	"strings"
)

// One script for several connections (FR-5.17).
//
// A line that is nothing but an at sign and a name —
//
//	@staging
//	SELECT count(*) FROM orders;
//	@"Production (read replica)"
//	SELECT count(*) FROM orders;
//
// — says that what follows is for the saved connection of that name, until
// the next such line. What comes before the first is for the connection the
// tab is on. The name is a saved connection's: letters, digits, hyphens,
// underscores and dots as they are, anything else between double quotes.
//
// A directive is honoured only between statements: where nothing has been
// written since the last one ended, with its semicolon. So a line of a string
// that happens to read "@home", or a variable alone on a line in the middle
// of a statement, is left to the statement it is part of. That is decided by
// the text alone, without a dialect, because the script is for more than one:
// a semicolon inside a string that ends its line is the case it gets wrong,
// and is the price of not parsing three dialects to find a line break.

// Section is a run of a script that is for one connection.
type Section struct {
	// Connection is the name its directive gave, or empty for the
	// connection the script's tab is on.
	Connection string
	// Text is what to run there, and Offset where it starts in the script,
	// in bytes, so that an error in it is pointed at where it is (FR-5.10).
	Text   string
	Offset int
}

// Sections splits a script at its connection directives. A script with none
// is one section, for the tab's own connection, and is the script. A section
// with nothing to run in it is left out.
func Sections(script string) []Section { return SectionsFrom(script, "", 0) }

// SectionsFrom is Sections of a piece of a script: text that begins at offset
// in it, in a part that is for the connection named, which the piece's own
// directives then change.
func SectionsFrom(text, connection string, offset int) []Section {
	var out []Section
	start := 0 // where the section being read begins
	flush := func(end int) {
		if strings.TrimSpace(text[start:end]) != "" {
			out = append(out, Section{Connection: connection, Text: text[start:end], Offset: offset + start})
		}
	}
	between := true // nothing written since the last statement ended
	for at := 0; at < len(text); {
		end := strings.IndexByte(text[at:], '\n')
		next := len(text)
		if end >= 0 {
			next = at + end + 1
		}
		line := strings.TrimSpace(text[at:next])
		if name, ok := directive(line); ok && between {
			flush(at)
			connection, start = name, next
		} else if line != "" && !strings.HasPrefix(line, "--") {
			between = endsStatement(line)
		}
		at = next
	}
	flush(len(text))
	return out
}

// endsStatement reports whether a line ends a statement: with its semicolon,
// and after it nothing but a comment to the end of the line.
func endsStatement(line string) bool {
	i := strings.LastIndexByte(line, ';')
	if i < 0 {
		return false
	}
	rest := strings.TrimSpace(line[i+1:])
	return rest == "" || strings.HasPrefix(rest, "--")
}

// directive is the connection a line names, if it is a directive and nothing
// else.
func directive(line string) (string, bool) {
	if !strings.HasPrefix(line, "@") {
		return "", false
	}
	name := line[1:]
	if len(name) >= 2 && strings.HasPrefix(name, `"`) && strings.HasSuffix(name, `"`) {
		name = name[1 : len(name)-1]
		return name, strings.TrimSpace(name) != "" && !strings.Contains(name, `"`)
	}
	if name == "" {
		return "", false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.', r > 0x7F:
		default:
			return "", false
		}
	}
	return name, true
}

// Across reports whether a script's sections are for more than the tab's own
// connection: whether it has a directive that is honoured.
func Across(sections []Section) bool {
	for _, s := range sections {
		if s.Connection != "" {
			return true
		}
	}
	return false
}

// SectionAt is the section a byte offset of the script is in, or the one
// before it where it is between two, so that a caret on a directive's own
// line, or just past a section's end, is in what it follows.
func SectionAt(sections []Section, offset int) (Section, bool) {
	var found Section
	ok := false
	for _, s := range sections {
		if s.Offset > offset && ok {
			break
		}
		found, ok = s, true
	}
	return found, ok
}
