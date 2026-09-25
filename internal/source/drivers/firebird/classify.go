package firebird

import (
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/sqlscript"
	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

// Statement classification, for read-only mode and production confirmation
// (NFR-S4, FR-4.9). As Dialect.Classify requires, anything not confidently
// read-only is mutating: a wrong guess costs a prompt, never a write.
//
// Firebird has no engine-side read-only mode this driver can lean on — a
// transaction can be declared read-only, but a query tab holds its own
// transaction and the browse path holds none, so the guard is the defence
// and this is what tells it what a statement is.

func classify(stmt string) source.Access {
	stmts := sqlscript.SplitTerminated(sqllex.Firebird, stmt, sqlscript.FirebirdBlocks)
	if len(stmts) == 0 {
		return source.AccessRead
	}
	worst := source.AccessRead
	for _, s := range stmts {
		if a := classifyOne(s.Text); a > worst {
			worst = a
		}
	}
	return worst
}

func classifyOne(stmt string) source.Access {
	words := statementWords(stmt)
	if len(words) == 0 {
		return source.AccessRead
	}
	switch words[0] {
	case "select", "with":
		// A CTE can front an INSERT, UPDATE, DELETE or MERGE in Firebird 4
		// and later, so a statement opening with either word is read through
		// rather than taken at its first word.
		//
		// This is also what holds a browse's typed WHERE to reading (FR-3.6):
		// the condition is rendered inside a SELECT and the whole statement
		// classified, so the scan has to find a write wherever it is. Being
		// conservative is the contract — a SELECT with the word DELETE in it
		// somewhere harmless costs a prompt, and the other way round costs a
		// table.
		for _, w := range words {
			switch w {
			case "insert", "update", "delete", "merge", "execute":
				return source.AccessWrite
			}
		}
		return source.AccessRead
	case "set":
		// SET TRANSACTION, SET STATISTICS and the rest change no data; SET
		// GENERATOR moves a generator, which something else's keys depend on.
		if len(words) > 1 && words[1] == "generator" {
			return source.AccessWrite
		}
		return source.AccessRead
	case "commit", "rollback", "savepoint", "release":
		return source.AccessRead // transaction control changes no data by itself
	case "insert", "update", "delete", "merge", "execute":
		// EXECUTE BLOCK and EXECUTE PROCEDURE run somebody's PSQL, which may
		// do anything; EXECUTE STATEMENT runs text this cannot see at all.
		return source.AccessWrite
	case "create", "alter", "drop", "recreate", "comment", "declare":
		return source.AccessDDL
	case "grant", "revoke", "connect", "shadow":
		return source.AccessAdmin
	}
	return source.AccessWrite
}

// statementWords is the statement's keywords and identifiers, lower-cased.
func statementWords(stmt string) []string {
	lx := sqllex.NewLexer(sqllex.Firebird)
	var st sqllex.State
	var out []string
	for _, line := range strings.Split(stmt, "\n") {
		toks, next := lx.LexLine(line, st)
		for _, tk := range toks {
			switch tk.Kind {
			case sqllex.TokKeyword, sqllex.TokIdentifier, sqllex.TokFunction, sqllex.TokType:
				out = append(out, strings.ToLower(line[tk.Start:tk.End]))
			}
		}
		st = next
	}
	return out
}

// unboundedIn reports a DELETE or UPDATE in this text that names no WHERE
// (FR-4.9).
func unboundedIn(stmt string) *source.UnboundedError {
	for _, s := range sqlscript.SplitTerminated(sqllex.Firebird, stmt, sqlscript.FirebirdBlocks) {
		if u := source.UnboundedIn(statementWords(s.Text)); u != nil {
			return u
		}
	}
	return nil
}
