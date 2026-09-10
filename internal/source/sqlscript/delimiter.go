package sqlscript

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

var delimiterLine = regexp.MustCompile(`(?i)^\s*delimiter\s+(\S+)\s*$`)

// SplitDelimited splits a script that may change its statement delimiter
// with the MySQL client's DELIMITER directive, as scripts defining routines
// do, so the semicolons inside a BEGIN … END body stay inside it:
//
//	DELIMITER $$
//	CREATE PROCEDURE p() BEGIN SELECT 1; SELECT 2; END $$
//	DELIMITER ;
//
// The directive belongs to the client, not the server, so it is never sent.
// A DELIMITER line inside a string or comment is text, not a directive.
func SplitDelimited(d *sqllex.Dialect, script string) []source.ScriptStatement {
	type region struct {
		start, end int // bytes
		delim      string
	}
	var regions []region
	lx := sqllex.NewLexer(d)
	var st sqllex.State
	delim, start := ";", 0
	for lineStart := 0; lineStart <= len(script); {
		rel := strings.IndexByte(script[lineStart:], '\n')
		lineEnd := len(script)
		if rel >= 0 {
			lineEnd = lineStart + rel
		}
		line := script[lineStart:lineEnd]
		if m := delimiterLine.FindStringSubmatch(line); m != nil && st.Equal(sqllex.State{}) {
			regions = append(regions, region{start, lineStart, delim})
			delim, start = m[1], lineEnd+1
		}
		_, st = lx.LexLine(line, st)
		if rel < 0 {
			break
		}
		lineStart = lineEnd + 1
	}
	regions = append(regions, region{min(start, len(script)), len(script), delim})

	var out []source.ScriptStatement
	for _, r := range regions {
		if r.start >= r.end {
			continue
		}
		text := script[r.start:r.end]
		base := utf8.RuneCountInString(script[:r.start])
		var stmts []source.ScriptStatement
		if r.delim == ";" {
			stmts = Split(d, text, nil)
		} else {
			stmts = splitOn(d, text, r.delim)
		}
		for _, s := range stmts {
			s.Offset += base
			out = append(out, s)
		}
	}
	return out
}

// splitOn splits text at every occurrence of delim that is not inside a
// string, comment or quoted name. The delimiter may be any characters, so it
// is found in the raw text, with the lexer saying which bytes to skip.
func splitOn(d *sqllex.Dialect, text, delim string) []source.ScriptStatement {
	masked := make([]bool, len(text))
	lx := sqllex.NewLexer(d)
	var st sqllex.State
	for lineStart := 0; lineStart <= len(text); {
		rel := strings.IndexByte(text[lineStart:], '\n')
		lineEnd := len(text)
		if rel >= 0 {
			lineEnd = lineStart + rel
		}
		toks, next := lx.LexLine(text[lineStart:lineEnd], st)
		for _, tk := range toks {
			switch tk.Kind {
			case sqllex.TokString, sqllex.TokComment, sqllex.TokQuotedIdent:
				for i := lineStart + int(tk.Start); i < lineStart+int(tk.End); i++ {
					masked[i] = true
				}
			}
		}
		st = next
		if rel < 0 {
			break
		}
		lineStart = lineEnd + 1
	}
	var out []source.ScriptStatement
	emit := func(from, to int) {
		piece := text[from:to]
		lead := len(piece) - len(strings.TrimLeft(piece, " \t\r\n"))
		piece = strings.TrimSpace(piece)
		if piece == "" || !hasCode(d, piece) {
			return
		}
		out = append(out, source.ScriptStatement{Text: piece, Offset: utf8.RuneCountInString(text[:from+lead])})
	}
	from := 0
	for i := 0; i+len(delim) <= len(text); i++ {
		if !masked[i] && strings.HasPrefix(text[i:], delim) {
			emit(from, i)
			i += len(delim) - 1
			from = i + 1
		}
	}
	emit(from, len(text))
	return out
}

// hasCode reports whether text is more than whitespace and comments.
func hasCode(d *sqllex.Dialect, text string) bool {
	for _, s := range Split(d, text+"\n;", nil) {
		if s.Text != "" {
			return true
		}
	}
	return false
}
