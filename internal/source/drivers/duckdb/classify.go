//go:build duckdb

package duckdb

import (
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

// Statement classification, for read-only mode and production
// confirmation (NFR-S4, FR-4.9).
//
// Anything not confidently read-only is reported as mutating: a false
// positive costs a confirmation, a false negative is a write to a
// connection somebody marked read-only.
//
// There are two defences here rather than three. The first is this; the
// second is the file itself, opened read-only, which no statement can
// undo — access_mode is settled when the database is opened and DuckDB
// will not have it changed afterwards. That is a stronger second line
// than the session setting every server engine here offers, and it is why
// there is no third.

// adminFunctions change what the connection can reach from inside what
// looks like an ordinary SELECT.
//
// An extension is the one that matters: DuckDB extensions are native code
// and can read and write anything the process can, so a statement that
// loads one is not something to run because it began with SELECT.
var adminFunctions = map[string]bool{
	"install_extension": true,
	"load_extension":    true,
	"duckdb_settings":   true,
}

// writeFunctions modify data from inside a SELECT.
var writeFunctions = map[string]bool{
	"nextval": true,
	"setval":  true,
}

// classify returns the most dangerous Access among a string's statements.
func classify(statement string) source.Access {
	worst := source.AccessRead
	for _, s := range splitScript(statement) {
		if a := classifyOne(s.Text); a > worst {
			worst = a
		}
	}
	return worst
}

func classifyOne(text string) source.Access {
	words, names := scan(text)
	if len(words) == 0 {
		if len(names) > 0 {
			// Only quoted names and literals: not something the engine can
			// run as written, and not something a lexer can vouch for.
			return source.AccessWrite
		}
		return source.AccessRead
	}
	for _, n := range names {
		if adminFunctions[n] {
			return source.AccessAdmin
		}
	}
	return classifyWords(words, names)
}

func classifyWords(words, names []string) source.Access {
	switch words[0] {
	// FROM begins a statement here: "FROM people" is how DuckDB spells a
	// SELECT over everything in it.
	case "select", "from", "values", "table", "with", "pivot", "unpivot", "prepare":
		return readUnlessWrites(words, names)

	case "show", "describe", "summarize", "deallocate",
		"commit", "end", "rollback", "abort", "savepoint", "release":
		return source.AccessRead

	case "explain":
		// EXPLAIN ANALYZE runs the statement for real, so it is exactly as
		// dangerous as what it explains.
		if !contains(words, "analyze") {
			return source.AccessRead
		}
		for i, w := range words[1:] {
			if statementStarts[w] {
				return classifyWords(words[1+i:], names)
			}
		}
		return source.AccessWrite

	case "begin", "start":
		return source.AccessRead

	case "insert", "update", "delete", "call", "execute",
		// COPY reads a file into a table or writes one out of it, and
		// "COPY (SELECT …) TO" contains FROM, so telling the two apart
		// lexically is unreliable. Either way it touches the filesystem.
		"copy":
		return source.AccessWrite

	case "create", "alter", "drop", "truncate", "comment", "grant", "revoke",
		"import", "update_extensions":
		return source.AccessDDL

	case "analyze", "checkpoint", "force", "vacuum", "export",
		// An extension is native code with the run of the process.
		"install", "load", "force_install",
		// What the connection holds, and where an unqualified name looks.
		"attach", "detach", "use",
		// Both can change a setting that decides what may be reached.
		"set", "reset", "pragma":
		return source.AccessAdmin
	}
	// A statement this classifier does not recognise is not confidently
	// read-only.
	return source.AccessWrite
}

// statementStarts are the keywords that can begin the statement an
// EXPLAIN wraps.
var statementStarts = map[string]bool{
	"select": true, "from": true, "insert": true, "update": true, "delete": true,
	"with": true, "values": true, "table": true, "execute": true, "create": true,
	"pivot": true, "unpivot": true,
}

// readUnlessWrites classifies a read-shaped statement, which can still
// hide a write:
//
//	WITH d AS (DELETE FROM t RETURNING *) SELECT …   data-modifying CTE
//	SELECT nextval('s')                              advances a sequence
func readUnlessWrites(words, names []string) source.Access {
	worst := source.AccessRead
	raise := func(a source.Access) {
		if a > worst {
			worst = a
		}
	}
	for i, w := range words {
		switch w {
		case "insert", "update", "delete":
			raise(source.AccessWrite)
		case "into":
			// INSERT INTO is already a write; any other INTO in a
			// read-shaped statement is not something this can vouch for.
			if i == 0 || words[i-1] != "insert" {
				raise(source.AccessDDL)
			}
		}
	}
	for _, n := range names {
		if writeFunctions[n] {
			raise(source.AccessWrite)
		}
	}
	return worst
}

// scan returns a statement's keyword-like words, lowercased, and every
// name in it, including quoted names.
func scan(text string) (words, names []string) {
	lx := sqllex.NewLexer(sqllex.PostgreSQL)
	var st sqllex.State
	for _, line := range strings.Split(text, "\n") {
		toks, next := lx.LexLine(line, st)
		for _, tk := range toks {
			raw := line[tk.Start:tk.End]
			switch tk.Kind {
			case sqllex.TokKeyword, sqllex.TokIdentifier, sqllex.TokFunction, sqllex.TokType:
				w := strings.ToLower(raw)
				words = append(words, w)
				names = append(names, w)
			case sqllex.TokQuotedIdent:
				names = append(names, unquoteIdent(raw))
			}
		}
		st = next
	}
	return words, names
}

func unquoteIdent(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return strings.ReplaceAll(s[1:len(s)-1], `""`, `"`)
	}
	return s
}

func contains(words []string, w string) bool {
	for _, x := range words {
		if x == w {
			return true
		}
	}
	return false
}

// unboundedIn reports a DELETE or UPDATE in this text that names no WHERE
// (FR-4.9).
func unboundedIn(text string) *source.UnboundedError {
	for _, s := range splitScript(text) {
		words, _ := scan(s.Text)
		if u := source.UnboundedIn(words); u != nil {
			return u
		}
	}
	return nil
}
