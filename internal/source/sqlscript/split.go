// Package sqlscript splits SQL scripts into statements, for every dialect the
// lexer knows (FR-5.4, FR-5.10). Drivers share it so that "where does this
// statement end" has one answer per dialect rather than one per driver.
package sqlscript

import (
	"strings"
	"unicode/utf8"

	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

// Blocks reports whether the word just read opens a body whose semicolons do
// not end the statement: PostgreSQL's BEGIN ATOMIC, SQLite's trigger bodies.
// words is the statement so far, lower-cased, not including w.
type Blocks func(words []string, w string) bool

// Closes reports whether the word just read ends a body Blocks opened. Every
// SQL dialect here ends one at END, which is what a nil Closes means; CQL
// ends a batch at APPLY BATCH instead.
type Closes func(words []string, w string) bool

// endCloses ends a body at END, as SQL does.
func endCloses(_ []string, w string) bool { return w == "end" }

// BatchBlocks opens at the BATCH of BEGIN BATCH, BEGIN UNLOGGED BATCH and
// BEGIN COUNTER BATCH: a CQL batch's statements are one statement to send,
// and the semicolons between them do not end it.
//
// What may sit between BEGIN and BATCH is a word or nothing, so that is what
// is looked for, rather than a list of the words it may be: the lexer reads
// COUNTER as a type and UNLOGGED as a name, and a rule written around which
// is which would be a rule about the lexer.
func BatchBlocks(words []string, w string) bool {
	if w != "batch" || len(words) == 0 {
		return false
	}
	if words[len(words)-1] == "begin" {
		return true
	}
	return len(words) > 1 && words[len(words)-2] == "begin"
}

// BatchCloses ends a batch at the BATCH of APPLY BATCH.
func BatchCloses(words []string, w string) bool {
	return w == "batch" && len(words) > 0 && words[len(words)-1] == "apply"
}

// PostgresBlocks opens at BEGIN ATOMIC, PostgreSQL 14's SQL-standard function
// bodies.
func PostgresBlocks(words []string, w string) bool {
	return w == "atomic" && len(words) > 0 && words[len(words)-1] == "begin"
}

// TriggerBlocks opens at the BEGIN of CREATE [TEMP|TEMPORARY] TRIGGER, whose
// body is statements of its own (SQLite). A bare BEGIN, which starts a
// transaction, opens nothing.
func TriggerBlocks(words []string, w string) bool {
	if w != "begin" || len(words) < 2 || words[0] != "create" {
		return false
	}
	return words[1] == "trigger" || len(words) > 2 && words[2] == "trigger" &&
		(words[1] == "temp" || words[1] == "temporary")
}

// Split divides a script into statements, each with its offset in characters,
// as the source contract counts (servers report error positions in
// characters).
//
// Splitting on ';' is right only outside strings, comments, quoted names and
// block bodies. The lexer takes care of the first three: a ';' inside them is
// part of a string or comment token, never punctuation. blocks takes care of
// the fourth, and may be nil. CASE … END is counted too, because it appears
// inside bodies and its END must not close the body early.
//
// Statements that are empty or only comments are dropped: sending one would
// produce an empty result, which reads as a statement silently doing nothing.
func Split(d *sqllex.Dialect, script string, blocks Blocks) []source.ScriptStatement {
	return SplitWith(d, script, blocks, nil)
}

// SplitWith divides a script whose bodies end at a word of their own. A nil
// closes ends them at END, and counts CASE … END with them; a dialect that
// ends a body otherwise has no CASE to count.
func SplitWith(d *sqllex.Dialect, script string, blocks Blocks, closes Closes) []source.ScriptStatement {
	countsCase := closes == nil
	if closes == nil {
		closes = endCloses
	}
	type span struct{ start, end int }
	var spans []span
	lx := sqllex.NewLexer(d)
	var st sqllex.State
	start, end := -1, 0
	depth, caseDepth := 0, 0
	var words []string
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
			if tk.Kind == sqllex.TokPunctuation && text == ";" && depth == 0 {
				if start >= 0 {
					spans = append(spans, span{start, end})
				}
				start, caseDepth, words = -1, 0, words[:0]
				continue
			}
			if start < 0 {
				start = lineStart + int(tk.Start)
			}
			end = lineStart + int(tk.End)
			if tk.Kind == sqllex.TokKeyword || tk.Kind == sqllex.TokIdentifier {
				w := strings.ToLower(text)
				switch {
				case blocks != nil && blocks(words, w):
					depth++
				case countsCase && w == "case":
					caseDepth++
				case closes(words, w):
					if caseDepth > 0 {
						caseDepth--
					} else if depth > 0 {
						depth--
					}
				}
				words = append(words, w)
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

	// Convert every span to characters in one pass; per statement it would
	// be quadratic in the script's length.
	out := make([]source.ScriptStatement, len(spans))
	runes, at := 0, 0
	for i, sp := range spans {
		runes += utf8.RuneCountInString(script[at:sp.start])
		at = sp.start
		out[i] = source.ScriptStatement{Text: script[sp.start:sp.end], Offset: runes}
	}
	return out
}
