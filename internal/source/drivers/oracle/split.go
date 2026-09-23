package oracle

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/sqlscript"
	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

// Where a statement ends in Oracle's SQL (FR-5.4, FR-5.10).
//
// A script is cut twice. First at a slash on a line of its own, which is
// not SQL at all: the server has never heard of it, and what it separates
// is what the client sends — the same job GO does for SQL Server. Then
// within that at a semicolon, except inside a PL/SQL block, whose
// semicolons are the block's own.
//
// A block is sent whole, and with the semicolon that ends it: PL/SQL is not
// SQL, and `END` without its semicolon is not a block.

// slashLine is the separator: a slash alone on a line.
var slashLine = regexp.MustCompile(`^[\t ]*/[\t ]*$`)

// SplitScript divides a script into the statements to send.
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
	lx := sqllex.NewLexer(sqllex.Oracle)
	var st sqllex.State
	start := 0
	for lineStart := 0; lineStart <= len(script); {
		rel := strings.IndexByte(script[lineStart:], '\n')
		lineEnd := len(script)
		if rel >= 0 {
			lineEnd = lineStart + rel
		}
		line := script[lineStart:lineEnd]
		// Only outside a string or a block comment: a slash on a line of
		// its own inside either is text somebody wrote.
		if st.Equal(sqllex.State{}) && slashLine.MatchString(line) {
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

// batch is one batch's statements: a PL/SQL block whole, and anything else
// divided at its semicolons.
func batch(text string) []source.ScriptStatement {
	if isBlock(text) {
		return sqlscript.Split(sqllex.Oracle, text, wholeBatch)
	}
	return sqlscript.Split(sqllex.Oracle, text, blocks)
}

// isBlock reports whether this batch is PL/SQL rather than SQL.
func isBlock(text string) bool {
	w := words(text)
	if len(w) == 0 {
		return false
	}
	switch w[0] {
	case "begin", "declare":
		return true
	case "create", "alter":
		return definesRoutine(w)
	}
	return false
}

// wholeBatch opens a body at the batch's first word, so that nothing in the
// batch ends a statement. A block inside it still opens and closes, so what
// the first word opened is still open at the end — and the semicolon that
// ends the block is part of it.
func wholeBatch(words []string, w string) bool {
	return len(words) == 0 || blocks(words, w)
}

// blocks opens a body whose semicolons do not end the statement.
//
// It opens at the word after DECLARE or BEGIN rather than at them, because
// nothing says which of the two a word is until the word after it is read.
//
// A routine's AS or IS opens a body too, and is not read here: a batch that
// defines a routine is a block already, and is kept whole from its first
// word by wholeBatch.
func blocks(words []string, w string) bool {
	if len(words) == 0 {
		return false
	}
	switch words[len(words)-1] {
	case "declare", "begin":
		return true
	}
	return false
}

// routineWords are the things whose AS or IS opens a body.
var routineWords = map[string]bool{
	"procedure": true, "function": true, "package": true, "trigger": true, "type": true,
}

// definesRoutine reports whether the words so far are defining one.
func definesRoutine(words []string) bool {
	for _, w := range words {
		if routineWords[w] {
			return true
		}
	}
	return false
}
