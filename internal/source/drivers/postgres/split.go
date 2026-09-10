package postgres

import (
	"strings"
	"unicode/utf8"

	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

// splitScript divides a script into statements (FR-5.4).
//
// Splitting on ';' is only correct outside strings, comments, dollar-quoted
// bodies and SQL-standard function bodies. The first three are handled by
// the lexer: a ';' inside them is part of a string or comment token, never
// punctuation. The fourth is handled here. A function written with a
// BEGIN ATOMIC ... END body (PostgreSQL 14+) contains ';' between its
// statements, and splitting there would send half a function definition to
// the server.
//
// Statements that are empty or contain only comments are dropped: sending
// them produces an empty result that the user would read as a statement
// having silently done nothing.
func splitScript(d *sqllex.Dialect, script string) []source.ScriptStatement {
	type span struct{ start, end int }
	var spans []span

	lx := sqllex.NewLexer(d)
	var st sqllex.State

	start, end := -1, 0
	atomic, caseDepth := 0, 0
	prev := ""

	for lineStart := 0; ; {
		rel := strings.IndexByte(script[lineStart:], '\n')
		last := rel < 0
		lineEnd := len(script)
		if !last {
			lineEnd = lineStart + rel
		}
		line := script[lineStart:lineEnd]

		toks, next := lx.LexLine(line, st)
		for _, tk := range toks {
			if tk.Kind == sqllex.TokText || tk.Kind == sqllex.TokComment {
				continue
			}
			text := line[tk.Start:tk.End]

			if tk.Kind == sqllex.TokPunctuation && text == ";" && atomic == 0 {
				if start >= 0 {
					spans = append(spans, span{start, end})
				}
				start, prev, caseDepth = -1, "", 0
				continue
			}

			if start < 0 {
				start = lineStart + int(tk.Start)
			}
			end = lineStart + int(tk.End)

			if tk.Kind == sqllex.TokKeyword || tk.Kind == sqllex.TokIdentifier {
				w := strings.ToLower(text)
				switch {
				case w == "atomic" && prev == "begin":
					atomic++
				case w == "case":
					// CASE ... END appears inside atomic bodies; without
					// counting it, its END would close the body early.
					caseDepth++
				case w == "end":
					if caseDepth > 0 {
						caseDepth--
					} else if atomic > 0 {
						atomic--
					}
				}
				prev = w
			}
		}
		st = next
		if last {
			break
		}
		lineStart = lineEnd + 1
	}
	if start >= 0 {
		spans = append(spans, span{start, end})
	}

	// The contract counts offsets in characters, because PostgreSQL reports
	// error positions in characters (FR-5.10). Convert every span in a single
	// pass; doing it per statement would be quadratic in the script length.
	out := make([]source.ScriptStatement, len(spans))
	runes, at := 0, 0
	for i, sp := range spans {
		runes += utf8.RuneCountInString(script[at:sp.start])
		at = sp.start
		out[i] = source.ScriptStatement{Text: script[sp.start:sp.end], Offset: runes}
	}
	return out
}
