package mysql

import (
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/sqlscript"
	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

// Statement classification for MySQL and MariaDB (NFR-S4, FR-4.9). Anything
// not confidently read-only is mutating. It is the first of two defences: a
// read-only connection also has its transactions set read-only by the server
// (see Open), which is why SET is guarded here — setting that variable back
// would switch the second defence off.

// adminFunctions reach outside the database.
var adminFunctions = map[string]bool{"load_file": true, "sys_exec": true, "sys_eval": true}

func classify(stmt string) source.Access {
	stmts := sqlscript.SplitDelimited(sqllex.MySQL, stmt)
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

type token struct {
	kind sqllex.TokenKind
	text string // lower-cased
}

func tokens(stmt string) []token {
	lx := sqllex.NewLexer(sqllex.MySQL)
	var st sqllex.State
	var out []token
	for _, line := range strings.Split(stmt, "\n") {
		toks, next := lx.LexLine(line, st)
		for _, tk := range toks {
			if tk.Kind == sqllex.TokText || tk.Kind == sqllex.TokComment {
				continue
			}
			out = append(out, token{tk.Kind, strings.ToLower(line[tk.Start:tk.End])})
		}
		st = next
	}
	return out
}

func classifyOne(stmt string) source.Access {
	toks := tokens(stmt)
	if len(toks) == 0 {
		return source.AccessRead
	}
	words := make([]string, 0, len(toks))
	for _, t := range toks {
		switch t.kind {
		case sqllex.TokKeyword, sqllex.TokIdentifier, sqllex.TokFunction, sqllex.TokType:
			words = append(words, t.text)
			if adminFunctions[t.text] {
				return source.AccessAdmin
			}
		}
	}
	if len(words) == 0 {
		return source.AccessWrite
	}
	has := func(ws ...string) bool {
		for i, w := range words {
			if w == "update" && i > 0 && words[i-1] == "for" {
				continue // SELECT … FOR UPDATE is a locking read, not a write
			}
			for _, x := range ws {
				if w == x {
					return true
				}
			}
		}
		return false
	}
	second := ""
	if len(words) > 1 {
		second = words[1]
	}
	switch words[0] {
	case "select", "with", "values", "table":
		if has("outfile", "dumpfile") {
			return source.AccessAdmin // writes a file on the server
		}
		if has("insert", "update", "delete", "replace") {
			return source.AccessWrite // a CTE can front a write
		}
		return source.AccessRead
	case "show", "describe", "desc", "explain", "help", "use", "handler":
		return source.AccessRead
	case "begin", "commit", "rollback", "savepoint", "release":
		return source.AccessRead
	case "start":
		if second == "transaction" {
			return source.AccessRead
		}
		return source.AccessAdmin // replication
	case "set":
		// A user variable or the connection's character set is harmless;
		// anything else may be the read-only switch itself.
		if len(toks) > 1 && strings.HasPrefix(toks[1].text, "@") && !strings.HasPrefix(toks[1].text, "@@") {
			return source.AccessRead
		}
		if second == "names" || second == "character" || second == "charset" {
			return source.AccessRead
		}
		return source.AccessAdmin
	case "analyze":
		// MariaDB's ANALYZE <statement> runs the statement; ANALYZE TABLE
		// rewrites statistics.
		if second == "table" || second == "local" || second == "no_write_to_binlog" {
			return source.AccessAdmin
		}
		return classifyOne(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(stmt)[len("analyze"):], " ")))
	case "insert", "update", "delete", "replace", "load", "call", "do", "prepare", "execute", "deallocate":
		return source.AccessWrite
	case "create", "alter", "drop", "rename", "truncate":
		return source.AccessDDL
	case "grant", "revoke", "flush", "reset", "purge", "kill", "lock", "unlock", "install", "uninstall",
		"shutdown", "restart", "change", "stop", "optimize", "repair", "check", "checksum", "xa", "cache":
		return source.AccessAdmin
	}
	return source.AccessWrite
}

// unboundedIn reports a DELETE or UPDATE in this text that names no WHERE
// (FR-4.9).
func unboundedIn(stmt string) *source.UnboundedError {
	for _, s := range sqlscript.SplitDelimited(sqllex.MySQL, stmt) {
		var ws []string
		for _, t := range tokens(s.Text) {
			switch t.kind {
			case sqllex.TokKeyword, sqllex.TokIdentifier, sqllex.TokFunction, sqllex.TokType:
				ws = append(ws, t.text)
			}
		}
		if u := source.UnboundedIn(ws); u != nil {
			return u
		}
	}
	return nil
}
