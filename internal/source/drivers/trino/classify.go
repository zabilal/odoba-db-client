package trino

import (
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

// Statement classification, for read-only mode and production confirmation
// (NFR-S4, FR-4.9). As Dialect.Classify requires, anything not confidently
// read-only is mutating: a wrong guess costs a prompt, never a write.
//
// It matters more here than it looks. Trino itself may hold nothing, but what
// is behind a catalog is somebody's warehouse, and a DELETE through Trino is
// a DELETE there.

func classify(stmt string) source.Access {
	worst := source.AccessRead
	for _, s := range splitScript(stmt) {
		if a := classifyOne(s.Text); a > worst {
			worst = a
		}
	}
	return worst
}

// writes are the words that change something, wherever in a statement they
// are found. They are what EXPLAIN ANALYZE is searched for: it runs what it
// explains. MERGE is not among them and need not be: it cannot be written
// without saying which of the three it does.
var writes = map[string]source.Access{
	"insert": source.AccessWrite, "delete": source.AccessWrite, "update": source.AccessWrite,
	"truncate": source.AccessWrite,
	"create":   source.AccessDDL, "drop": source.AccessDDL, "alter": source.AccessDDL,
	"comment": source.AccessDDL, "refresh": source.AccessDDL,
	"call": source.AccessAdmin, "grant": source.AccessAdmin, "revoke": source.AccessAdmin,
	"deny": source.AccessAdmin,
}

func classifyOne(stmt string) source.Access {
	ws := words(stmt)
	if len(ws) == 0 {
		return source.AccessRead
	}
	switch ws[0] {
	case "select", "with", "show", "describe", "desc", "values", "table":
		return source.AccessRead
	case "explain":
		// A plan is read. An analysed plan is the statement run, and is
		// whatever the statement is: the worst of the words in it, so that a
		// column called "comment" costs a prompt and an INSERT is never
		// taken for a question.
		if len(ws) > 1 && ws[1] == "analyze" {
			worst := source.AccessRead
			for _, w := range ws[2:] {
				if a := writes[w]; a > worst {
					worst = a
				}
			}
			return worst
		}
		return source.AccessRead
	case "use", "set", "reset":
		// Session state: which catalog a bare name means, a property, a role.
		return source.AccessRead
	case "start", "commit", "rollback":
		return source.AccessRead // transaction control changes no data by itself
	case "prepare", "deallocate":
		return source.AccessRead // naming a statement runs nothing
	case "analyze":
		// Collects statistics and writes them where the connector keeps them.
		return source.AccessDDL
	}
	if a, ok := writes[ws[0]]; ok {
		return a
	}
	// EXECUTE runs a statement this cannot see, and everything else is
	// something this does not know.
	return source.AccessWrite
}

// words is the statement's keywords and identifiers, lower-cased.
func words(stmt string) []string {
	lx := sqllex.NewLexer(sqllex.Trino)
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
