package sqlscript

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

// Firebird's scripts change their terminator with SET TERM, which is isql's
// directive and not a statement any server has ever seen:
//
//	SET TERM ^ ;
//	CREATE PROCEDURE p RETURNS (n INTEGER) AS BEGIN n = 1; SUSPEND; END^
//	SET TERM ; ^
//
// The line names the new terminator and then closes itself with the one in
// force, which is the opposite way round from MySQL's DELIMITER and is why
// this is its own function rather than a parameter of that one.

var setTermLine = regexp.MustCompile(`(?i)^\s*set\s+term\s+(\S.*?)\s*$`)

// FirebirdBlocks opens at the BEGIN of a PSQL body — a procedure, a trigger,
// a function, a package or an EXECUTE BLOCK — whose semicolons are its own
// and do not end the statement.
//
// It is what reads a script written without SET TERM, which Firebird 3 and
// later accept from a client that can tell where the statement ends. A bare
// BEGIN opens nothing: Firebird starts a transaction with SET TRANSACTION,
// so a BEGIN at the top of a statement is not a body.
func FirebirdBlocks(words []string, w string) bool {
	if w != "begin" {
		return false
	}
	for _, x := range words {
		switch x {
		case "procedure", "trigger", "function", "package", "block":
			return true
		}
	}
	return false
}

// SplitTerminated splits a script that may change its terminator with SET
// TERM. Regions still terminated by ';' are split with blocks, which may be
// nil; a region with a terminator of its own needs none, that being the whole
// point of changing it.
func SplitTerminated(d *sqllex.Dialect, script string, blocks Blocks) []source.ScriptStatement {
	type region struct {
		start, end int // bytes
		term       string
	}
	var regions []region
	lx := sqllex.NewLexer(d)
	var st sqllex.State
	term, start := ";", 0
	for lineStart := 0; lineStart <= len(script); {
		rel := strings.IndexByte(script[lineStart:], '\n')
		lineEnd := len(script)
		if rel >= 0 {
			lineEnd = lineStart + rel
		}
		line := script[lineStart:lineEnd]
		// Only outside a string or comment: a SET TERM inside either is text.
		if st.Equal(sqllex.State{}) {
			if next, ok := newTerm(line, term); ok {
				regions = append(regions, region{start, lineStart, term})
				term, start = next, lineEnd+1
			}
		}
		_, st = lx.LexLine(line, st)
		if rel < 0 {
			break
		}
		lineStart = lineEnd + 1
	}
	regions = append(regions, region{min(start, len(script)), len(script), term})

	var out []source.ScriptStatement
	for _, r := range regions {
		if r.start >= r.end {
			continue
		}
		text := script[r.start:r.end]
		base := utf8.RuneCountInString(script[:r.start])
		var stmts []source.ScriptStatement
		if r.term == ";" {
			stmts = Split(d, text, blocks)
		} else {
			stmts = splitOn(d, text, r.term)
		}
		for _, s := range stmts {
			s.Offset += base
			out = append(out, s)
		}
	}
	return out
}

// newTerm reads the terminator a SET TERM line asks for, given the one in
// force, which the line itself ends with.
//
// A line naming nothing after that is stripped — "SET TERM ;" — is not a
// directive: there is no terminator in it, and taking it as one would set the
// terminator to the empty string, which ends every statement everywhere. It is
// left where it is instead, so the server refuses it and says which line,
// which is what somebody who mistyped a directive needs to see.
//
// A line naming the terminator already in force is a directive and a no-op.
// It is taken as one, so that the line is dropped rather than sent: it is not
// a statement, whatever else it is.
func newTerm(line, current string) (string, bool) {
	m := setTermLine.FindStringSubmatch(line)
	if m == nil {
		return "", false
	}
	rest := strings.TrimSpace(strings.TrimSuffix(m[1], current))
	if rest == "" {
		return "", false
	}
	return rest, true
}
