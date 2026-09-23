package sqlserver

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/sqlscript"
	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

// Where a statement ends in T-SQL (FR-5.4, FR-5.10).
//
// A script is cut twice. First at a GO line, which is not SQL at all: the
// server has never heard of GO, and what it separates is a batch, the unit
// a client sends. Then within a batch at a semicolon — except inside a
// BEGIN … END body, whose semicolons are the body's own, and except in a
// batch that defines a routine, which the server requires to hold nothing
// else and which is therefore sent whole.

// goLine is the batch separator: GO alone on a line, with the repeat count
// sqlcmd allows after it and a trailing comment.
var goLine = regexp.MustCompile(`(?i)^[\t ]*go[\t ]*[0-9]*[\t ]*(--.*)?$`)

// SplitScript divides a script into the statements to send.
//
// The number sqlcmd allows after GO tells the client to send the batch
// again, and is not part of it. It is not obeyed here: a window that ran a
// write five times because a digit followed a word would be hard to forgive,
// so the batch runs once.
func (dialect) SplitScript(script string) []source.ScriptStatement { return splitScript(script) }

// splitScript is SplitScript, where a dialect value is not to hand.
func splitScript(script string) []source.ScriptStatement {
	var out []source.ScriptStatement
	emit := func(from, to int) {
		if from >= to {
			return
		}
		base := utf8.RuneCountInString(script[:from])
		for _, s := range batch(script[from:to]) {
			s.Offset += base
			out = append(out, s)
		}
	}
	lx := sqllex.NewLexer(sqllex.SQLServer)
	var st sqllex.State
	start := 0
	for lineStart := 0; lineStart <= len(script); {
		rel := strings.IndexByte(script[lineStart:], '\n')
		lineEnd := len(script)
		if rel >= 0 {
			lineEnd = lineStart + rel
		}
		line := script[lineStart:lineEnd]
		// Only outside a string or a block comment: GO on a line of its own
		// inside either is text somebody wrote, not a separator.
		if st.Equal(sqllex.State{}) && goLine.MatchString(line) {
			emit(start, lineStart)
			start = lineEnd + 1
		}
		_, st = lx.LexLine(line, st)
		if rel < 0 {
			break
		}
		lineStart = lineEnd + 1
	}
	emit(start, len(script))
	return out
}

// batch is one batch's statements.
func batch(text string) []source.ScriptStatement {
	if !ownsItsBatch(text) {
		return glue(text, sqlscript.Split(sqllex.SQLServer, text, blocks))
	}
	stmts := sqlscript.Split(sqllex.SQLServer, text, wholeBatch)
	for i := range stmts {
		// The semicolon a batch ends with terminated the last statement of
		// the definition, and what is sent is the definition. Every other
		// statement's terminator is already left behind by the split.
		stmts[i].Text = strings.TrimRight(stmts[i].Text, "; \t\r\n")
	}
	return stmts
}

// blocks opens a body whose semicolons do not end the statement: T-SQL's
// BEGIN … END, which is a routine's body, an IF's arm, a loop's and a TRY's.
//
// It opens at the word after BEGIN rather than at BEGIN itself, because
// BEGIN TRANSACTION opens nothing and has no END to match — and which of the
// two a BEGIN is cannot be told until the word after it is read. Nothing but
// whitespace and comments may sit between the two, so opening a word late
// loses nothing.
func blocks(words []string, w string) bool {
	if len(words) == 0 || words[len(words)-1] != "begin" {
		return false
	}
	switch w {
	case "transaction", "tran", "distributed":
		return false
	}
	return true
}

// wholeBatch opens a body at the batch's first word, so that nothing in the
// batch ends a statement. BEGIN … END inside it still opens and closes, so
// what the first word opened is still open at the end.
func wholeBatch(words []string, w string) bool {
	return len(words) == 0 || blocks(words, w)
}

// ownsBatch are the things SQL Server requires to be the only statement in
// their batch.
var ownsBatch = map[string]bool{
	"procedure": true, "proc": true, "function": true,
	"trigger": true, "view": true, "schema": true,
}

// ownsItsBatch reports whether this batch defines one of them. Everything
// after the name is then the definition, however it is punctuated — which is
// what lets a procedure body be read without guessing where it ends.
func ownsItsBatch(text string) bool {
	w := words(text)
	if len(w) < 2 || (w[0] != "create" && w[0] != "alter") {
		return false
	}
	if w[1] == "or" && len(w) > 3 {
		return ownsBatch[w[3]] // CREATE OR ALTER PROCEDURE
	}
	return ownsBatch[w[1]]
}

// glue joins a statement beginning with ELSE to the one before it.
//
// An IF whose arms are single statements ends its first arm with a
// semicolon, and that semicolon ends the arm rather than the IF. Which
// semicolon that is cannot be known until the word after it has been read,
// so it is put back together afterwards.
func glue(text string, stmts []source.ScriptStatement) []source.ScriptStatement {
	var out []source.ScriptStatement
	var from []int // each kept statement's byte offset in text
	at, seen := 0, 0
	for _, s := range stmts {
		for seen < s.Offset {
			_, w := utf8.DecodeRuneInString(text[at:])
			at, seen = at+w, seen+1
		}
		if w := words(s.Text); len(out) > 0 && len(w) > 0 && w[0] == "else" {
			last := len(out) - 1
			out[last].Text = text[from[last] : at+len(s.Text)]
			continue
		}
		out = append(out, s)
		from = append(from, at)
	}
	return out
}
