package clickhouse

import (
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

// Statement classification, for read-only mode and production confirmation
// (NFR-S4, FR-4.9). As Dialect.Classify requires, anything not confidently
// read-only is mutating: a wrong guess costs a prompt, never a write.
//
// ClickHouse has a readonly setting a connection can be opened with, but it
// belongs to the server's user profile and not to this connection, so what
// is decided here is what stands between somebody and their production
// data.

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
	case "select", "with", "show", "describe", "desc", "exists", "explain", "check":
		return source.AccessRead
	case "set", "use":
		// Session state: a setting, or which database a bare name means.
		return source.AccessRead
	case "begin", "commit", "rollback":
		return source.AccessRead // transaction control changes no data by itself
	case "insert", "delete", "update", "truncate":
		return source.AccessWrite
	case "alter":
		// ALTER TABLE … UPDATE and … DELETE are how ClickHouse changed rows
		// before it had the statements for it, and they are writes wearing a
		// definition's clothes.
		for _, w := range ws {
			switch w {
			case "update", "delete":
				return source.AccessWrite
			}
		}
		return source.AccessDDL
	case "create", "drop", "attach", "detach", "rename", "exchange", "undrop", "replace":
		return source.AccessDDL
	case "optimize", "system", "kill", "grant", "revoke", "backup", "restore", "move":
		return source.AccessAdmin
	}
	return source.AccessWrite
}

// words is the statement's keywords and identifiers, lower-cased.
func words(stmt string) []string {
	lx := sqllex.NewLexer(sqllex.ClickHouse)
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
