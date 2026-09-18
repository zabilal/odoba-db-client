package cassandra

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
// CQL is a small language, which makes this a short list — and the safe
// direction is the same as everywhere else: a verb nobody listed writes.

// roleObjects are what a CREATE, ALTER or DROP changes when it is about who
// may connect rather than about what is stored. Those are the cluster's, not
// a keyspace's.
var roleObjects = map[string]bool{"role": true, "user": true}

func classify(stmt string) source.Access {
	stmts := sqlscript.SplitWith(sqllex.CQL, stmt, sqlscript.BatchBlocks, sqlscript.BatchCloses)
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

// words are a statement's words, lower-cased, without its strings, comments
// and punctuation.
func words(stmt string) []string {
	lx := sqllex.NewLexer(sqllex.CQL)
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

func classifyOne(stmt string) source.Access {
	ws := words(stmt)
	if len(ws) == 0 {
		return source.AccessRead
	}
	second := ""
	if len(ws) > 1 {
		second = ws[1]
	}
	switch ws[0] {
	case "select":
		return source.AccessRead
	case "use", "describe", "desc", "list":
		// LIST ROLES and LIST PERMISSIONS read what the cluster was told;
		// DESCRIBE reads the schema; USE moves the session and nothing else.
		return source.AccessRead
	case "insert", "update", "delete", "begin", "apply", "copy":
		// A batch is the writes inside it, whatever they are.
		return source.AccessWrite
	case "truncate":
		// Every row of a table, gone: that is a structural change in every
		// other driver here, and is one here.
		return source.AccessDDL
	case "create", "alter", "drop":
		if roleObjects[second] {
			return source.AccessAdmin
		}
		return source.AccessDDL
	case "grant", "revoke":
		return source.AccessAdmin
	}
	return source.AccessWrite
}
