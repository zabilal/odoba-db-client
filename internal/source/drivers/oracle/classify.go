package oracle

import (
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

// Statement classification, for read-only mode and production confirmation
// (NFR-S4, FR-4.9). As Dialect.Classify requires, anything not confidently
// read-only is mutating: a wrong guess costs a prompt, never a write.
//
// Oracle has read-only sessions and read-only transactions, but both are
// the connection's own settings rather than something this program can rely
// on a server to have set, so what is decided here is what stands between
// somebody and their data.

func classify(stmt string) source.Access {
	worst := source.AccessRead
	for _, s := range splitScript(stmt) {
		if a := classifyOne(s.Text); a > worst {
			worst = a
		}
	}
	return worst
}

func classifyOne(stmt string) source.Access {
	ws := words(stmt)
	if len(ws) == 0 {
		return source.AccessRead
	}
	switch ws[0] {
	case "select", "with", "explain", "describe", "desc":
		return source.AccessRead
	case "set", "alter":
		// ALTER SESSION is this connection's own setting; every other ALTER
		// changes something everybody sees.
		if ws[0] == "set" || (len(ws) > 1 && ws[1] == "session") {
			return source.AccessRead
		}
		return source.AccessDDL
	case "commit", "rollback", "savepoint":
		return source.AccessRead // transaction control changes no data by itself
	case "insert", "update", "delete", "merge":
		return source.AccessWrite
	case "truncate":
		// TRUNCATE is a definition here: it cannot be rolled back.
		return source.AccessDDL
	case "create", "drop", "rename", "comment", "purge", "flashback":
		return source.AccessDDL
	case "grant", "revoke", "audit", "noaudit", "lock", "analyze", "administer":
		return source.AccessAdmin
	case "begin", "declare":
		// A PL/SQL block: what it runs cannot be seen from here.
		return source.AccessAdmin
	case "call", "exec", "execute":
		return source.AccessAdmin
	}
	return source.AccessWrite
}

// words is the statement's keywords and identifiers, lower-cased.
func words(stmt string) []string {
	lx := sqllex.NewLexer(sqllex.Oracle)
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
	for _, s := range splitScript(stmt) {
		if u := source.UnboundedIn(words(s.Text)); u != nil {
			return u
		}
	}
	return nil
}
