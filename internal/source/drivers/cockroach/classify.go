package cockroach

import (
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

// Statement classification, for read-only mode and production confirmation
// (NFR-S4, FR-4.9).
//
// The rule is Dialect.Classify's: anything not confidently read-only is
// reported as mutating. A false positive costs a confirmation prompt; a
// false negative is a write to a connection somebody marked read-only. So
// every doubtful case errs toward mutating.
//
// This is the first of three defences, not the only one. The others are an
// explicit READ ONLY transaction around every statement on a read-only
// connection, and default_transaction_read_only set as it connects, which
// CockroachDB honours and enforces with 25006.

// adminFunctions change server or session state from inside what looks
// like an ordinary SELECT.
//
// set_config matters most:
//
//	SELECT set_config('default_transaction_read_only', 'off', false)
//
// turns read-only mode off for the session without ever issuing a SET.
var adminFunctions = map[string]bool{
	"set_config":           true,
	"crdb_internal":        true,
	"pg_terminate_backend": true,
	"pg_cancel_backend":    true,
}

// writeFunctions modify data from inside a SELECT.
var writeFunctions = map[string]bool{
	"nextval": true,
	"setval":  true,
}

// classify returns the most dangerous Access among a string's statements.
//
// The input is split first. A parameterless statement goes over the simple
// protocol, which executes every statement in the string, so
// "SELECT 1; DROP TABLE t" handed over as one really does drop the table.
func classify(statement string) source.Access {
	worst := source.AccessRead
	for _, s := range splitScript(statement) {
		if a := classifyOne(s.Text); a > worst {
			worst = a
		}
	}
	return worst
}

func classifyOne(text string) source.Access {
	words, names := scan(text)
	if len(words) == 0 {
		if len(names) > 0 {
			// Only quoted names and literals: not something the server can
			// execute as written, and not something a lexer can vouch for.
			return source.AccessWrite
		}
		return source.AccessRead
	}
	for _, n := range names {
		if adminFunctions[n] {
			return source.AccessAdmin
		}
	}
	return classifyWords(words, names)
}

func classifyWords(words, names []string) source.Access {
	switch words[0] {
	case "select", "values", "table", "with", "prepare":
		return readUnlessWrites(words, names)

	case "show", "deallocate", "commit", "end", "rollback", "savepoint", "release":
		return source.AccessRead

	case "explain":
		// Plain EXPLAIN plans without running. EXPLAIN ANALYZE runs the
		// statement for real, so it is exactly as dangerous as what it
		// explains. Any mention of analyze counts, which also catches
		// "EXPLAIN (ANALYZE, VERBOSE)" as a harmless false positive.
		if !contains(words, "analyze") {
			return source.AccessRead
		}
		for i, w := range words[1:] {
			if statementStarts[w] {
				return classifyWords(words[1+i:], names)
			}
		}
		return source.AccessWrite

	case "begin", "start":
		// BEGIN READ WRITE opens a writable transaction on a connection
		// that is meant to be read-only.
		if contains(words, "write") {
			return source.AccessAdmin
		}
		return source.AccessRead

	case "set", "reset":
		if touchesReadOnly(words) {
			return source.AccessAdmin
		}
		return source.AccessRead

	case "insert", "update", "delete", "call", "do", "execute",
		// UPSERT is CockroachDB's own, and writes.
		"upsert",
		// COPY TO only reads, but "COPY (SELECT … FROM …) TO" contains
		// FROM, so telling the two apart lexically is unreliable.
		"copy":
		return source.AccessWrite

	case "create", "alter", "drop", "truncate", "comment", "grant", "revoke",
		"import", "refresh", "restore":
		return source.AccessDDL

	case "analyze", "export", "backup",
		// USE changes which database an unqualified name means, for every
		// statement after it in the session.
		"use",
		// The cluster's own controls: a job, a session or a statement
		// somebody else is running.
		"cancel", "pause", "resume":
		return source.AccessAdmin
	}
	// A statement this classifier does not recognise is not confidently
	// read-only.
	return source.AccessWrite
}

// statementStarts are the keywords that can begin the statement an EXPLAIN
// wraps.
var statementStarts = map[string]bool{
	"select": true, "insert": true, "update": true, "delete": true, "upsert": true,
	"with": true, "values": true, "table": true, "execute": true, "create": true,
}

// readUnlessWrites classifies a read-shaped statement, which can still
// hide a write:
//
//	WITH d AS (DELETE FROM t RETURNING *) SELECT …   data-modifying CTE
//	SELECT … FOR UPDATE / FOR SHARE                   row locks that block writers
//	SELECT nextval('seq')                             advances a sequence
func readUnlessWrites(words, names []string) source.Access {
	worst := source.AccessRead
	raise := func(a source.Access) {
		if a > worst {
			worst = a
		}
	}
	for i, w := range words {
		switch w {
		case "insert", "update", "delete", "upsert":
			raise(source.AccessWrite)
		case "share":
			if i > 0 && (words[i-1] == "for" || words[i-1] == "key") {
				raise(source.AccessWrite)
			}
		case "into":
			// INSERT INTO and UPSERT INTO are already writes. Any other
			// INTO in a read-shaped statement is not something this can
			// vouch for.
			if i == 0 || (words[i-1] != "insert" && words[i-1] != "upsert") {
				raise(source.AccessDDL)
			}
		}
	}
	for _, n := range names {
		if writeFunctions[n] {
			raise(source.AccessWrite)
		}
	}
	return worst
}

// touchesReadOnly reports whether a SET or RESET can switch read-only mode
// off, change who the session is, or change the cluster.
func touchesReadOnly(words []string) bool {
	for _, w := range words {
		switch w {
		case "default_transaction_read_only", "transaction_read_only",
			"write",   // SET TRANSACTION / SESSION CHARACTERISTICS … READ WRITE
			"role",    // SET ROLE
			"cluster", // SET CLUSTER SETTING, which is the whole cluster
			"authorization",
			"all": // RESET ALL restores the server defaults
			return true
		}
	}
	return false
}

// scan returns a statement's keyword-like words, lowercased, and every
// name in it, including quoted names.
//
// words are what structure is judged by, so comments, strings, numbers and
// quoted identifiers are excluded: "DELETE" in a comment or a string is not
// a delete, and a column quoted as "delete" is not either.
//
// names include quoted identifiers, unquoted exactly, because
// "set_config"(…) calls the same function as set_config(…). A quoted name
// is case-sensitive here, so "SET_CONFIG" names a different function and is
// correctly left alone.
func scan(text string) (words, names []string) {
	lx := sqllex.NewLexer(sqllex.PostgreSQL)
	var st sqllex.State
	for _, line := range strings.Split(text, "\n") {
		toks, next := lx.LexLine(line, st)
		for _, tk := range toks {
			raw := line[tk.Start:tk.End]
			switch tk.Kind {
			case sqllex.TokKeyword, sqllex.TokIdentifier, sqllex.TokFunction, sqllex.TokType:
				w := strings.ToLower(raw)
				words = append(words, w)
				names = append(names, w)
			case sqllex.TokQuotedIdent:
				names = append(names, unquoteIdent(raw))
			}
		}
		st = next
	}
	return words, names
}

func unquoteIdent(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return strings.ReplaceAll(s[1:len(s)-1], `""`, `"`)
	}
	return s
}

func contains(words []string, w string) bool {
	for _, x := range words {
		if x == w {
			return true
		}
	}
	return false
}

// unboundedIn reports a DELETE or UPDATE in this text that names no WHERE
// (FR-4.9). The script is split first, for the same reason classify splits
// it: every statement in the string really does run.
func unboundedIn(text string) *source.UnboundedError {
	for _, s := range splitScript(text) {
		words, _ := scan(s.Text)
		if u := source.UnboundedIn(words); u != nil {
			return u
		}
	}
	return nil
}
