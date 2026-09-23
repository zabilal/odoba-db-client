package sqlserver

import (
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

// Statement classification, for read-only mode and production confirmation
// (NFR-S4, FR-4.9). As Dialect.Classify requires, anything not confidently
// read-only is mutating: a wrong guess costs a prompt, never a write.
//
// SQL Server has no session setting that makes a connection read-only — no
// default_transaction_read_only, no query_only pragma — so unlike every
// other relational driver here this classification is the only defence
// there is. That is a reason to be stricter rather than cleverer: a word
// this has not been taught is a write.

// readProcedures report and change nothing. sp_help and its family are the
// catalogue read by another name, and somebody browsing a database is as
// likely to type them as to click.
var readProcedures = map[string]bool{
	"sp_help": true, "sp_helptext": true, "sp_helpindex": true, "sp_helpconstraint": true,
	"sp_columns": true, "sp_tables": true, "sp_databases": true, "sp_who": true,
	"sp_who2": true, "sp_spaceused": true, "sp_helpdb": true, "sp_server_info": true,
}

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
	case "select", "with":
		// A CTE can front an INSERT, UPDATE, DELETE or MERGE, and SELECT …
		// INTO makes a table out of a query.
		for _, w := range ws {
			switch w {
			case "insert", "update", "delete", "merge":
				return source.AccessWrite
			case "into":
				return source.AccessDDL
			}
		}
		return source.AccessRead
	case "print", "declare", "set", "use":
		// None of these reaches data. SET is session state — a row count
		// limit, a transaction level — and a variable is the caller's own.
		return source.AccessRead
	case "begin", "commit", "rollback", "save":
		return source.AccessRead // transaction control changes no data by itself
	case "exec", "execute", "sp_executesql":
		// Whatever it runs, this cannot see. What it is named tells it
		// apart only where the name is one of the catalogue's own.
		if len(ws) > 1 && readProcedures[ws[1]] {
			return source.AccessRead
		}
		return source.AccessAdmin
	case "insert", "update", "delete", "merge", "truncate", "bulk":
		return source.AccessWrite
	case "create", "alter", "drop":
		return source.AccessDDL
	case "grant", "revoke", "deny", "backup", "restore", "dbcc", "kill", "shutdown",
		"reconfigure", "checkpoint":
		return source.AccessAdmin
	}
	return source.AccessWrite
}

// words is the statement's keywords and identifiers, lower-cased.
func words(stmt string) []string {
	lx := sqllex.NewLexer(sqllex.SQLServer)
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
