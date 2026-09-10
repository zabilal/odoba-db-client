package postgres

import (
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

// Statement classification, for read-only mode and production confirmation
// (NFR-S4, FR-4.9).
//
// The rule is the one Dialect.Classify documents: anything not confidently
// read-only is reported as mutating. A false positive costs a confirmation
// prompt. A false negative is a write to a connection the user marked
// read-only. So every doubtful case errs toward mutating, and every case that
// looks harmless but is not has a test: a SELECT that switches read-only mode
// off, a comment containing DELETE, two statements in one string.
//
// A lexer cannot see inside a function body, so this is the first of three
// defences, not the only one. The others are an explicit READ ONLY
// transaction around every statement on a read-only connection, and
// default_transaction_read_only set at connect time.

// adminFunctions change server or session state from inside what looks like
// an ordinary SELECT.
//
// set_config matters most:
//
//	SELECT set_config('default_transaction_read_only', 'off', false)
//
// turns read-only mode off for the session without ever issuing a SET. dblink
// runs SQL over a second connection that none of this session's read-only
// settings reach.
var adminFunctions = map[string]bool{
	"set_config":                          true,
	"dblink":                              true,
	"dblink_exec":                         true,
	"dblink_connect":                      true,
	"dblink_connect_u":                    true,
	"dblink_open":                         true,
	"dblink_send_query":                   true,
	"pg_terminate_backend":                true,
	"pg_cancel_backend":                   true,
	"pg_reload_conf":                      true,
	"pg_rotate_logfile":                   true,
	"pg_switch_wal":                       true,
	"pg_promote":                          true,
	"pg_create_restore_point":             true,
	"pg_backup_start":                     true,
	"pg_backup_stop":                      true,
	"pg_create_logical_replication_slot":  true,
	"pg_create_physical_replication_slot": true,
	"pg_drop_replication_slot":            true,
	"lo_import":                           true,
	"lo_export":                           true,
	"lo_unlink":                           true,
	"pg_file_write":                       true,
	"pg_file_rename":                      true,
	"pg_file_unlink":                      true,
	"pg_advisory_lock":                    true,
	"pg_advisory_xact_lock":               true,
	"pg_try_advisory_lock":                true,
}

// writeFunctions modify data from inside a SELECT.
var writeFunctions = map[string]bool{
	"nextval": true,
	"setval":  true,
}

// classify returns the most dangerous Access among a string's statements.
//
// The input is split first. pgx runs a parameterless statement over the simple
// protocol, which executes every statement in the string, so
// "SELECT 1; DROP TABLE t" handed over as one statement really does drop the
// table. Classifying only the first statement would call it a read.
func classify(d *sqllex.Dialect, statement string) source.Access {
	worst := source.AccessRead
	for _, s := range splitScript(d, statement) {
		if a := classifyOne(d, s.Text); a > worst {
			worst = a
		}
	}
	return worst
}

func classifyOne(d *sqllex.Dialect, text string) source.Access {
	words, names := scan(d, text)
	if len(words) == 0 {
		if len(names) > 0 {
			// Only quoted names and literals. Not something PostgreSQL can
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
	case "select", "values", "table", "with", "declare", "prepare":
		return readUnlessWrites(words, names)

	case "show", "fetch", "move", "listen", "unlisten", "close", "deallocate",
		"commit", "end", "rollback", "abort", "savepoint", "release":
		return source.AccessRead

	case "explain":
		// Plain EXPLAIN plans without running. EXPLAIN ANALYZE runs the
		// statement for real, so it is exactly as dangerous as what it
		// explains. Any mention of analyze counts, which also catches
		// "EXPLAIN (ANALYZE false)" as a harmless false positive.
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
		// BEGIN READ WRITE opens a writable transaction on a connection that
		// is meant to be read-only.
		if contains(words, "write") {
			return source.AccessAdmin
		}
		return source.AccessRead

	case "set", "reset":
		if touchesReadOnly(words) {
			return source.AccessAdmin
		}
		return source.AccessRead

	case "insert", "update", "delete", "merge", "call", "do", "execute", "notify",
		// COPY TO only reads, but "COPY (SELECT ... FROM ...) TO" contains
		// FROM, so telling the two apart lexically is unreliable. All COPY is
		// treated as a write.
		"copy":
		return source.AccessWrite

	case "create", "alter", "drop", "truncate", "comment", "grant", "revoke",
		"security", "import", "refresh", "reindex", "cluster":
		return source.AccessDDL

	case "vacuum", "analyze", "checkpoint", "load", "lock", "reassign",
		// DISCARD ALL includes RESET ALL, which restores
		// default_transaction_read_only to the server default: off.
		"discard":
		return source.AccessAdmin
	}
	// A statement this classifier does not recognise is not confidently
	// read-only.
	return source.AccessWrite
}

// statementStarts are the keywords that can begin the statement an EXPLAIN
// wraps.
var statementStarts = map[string]bool{
	"select": true, "insert": true, "update": true, "delete": true, "merge": true,
	"with": true, "values": true, "table": true, "execute": true, "declare": true,
	"create": true,
}

// readUnlessWrites classifies a read-shaped statement, which can still hide a
// write:
//
//	WITH d AS (DELETE FROM t RETURNING *) SELECT ...   data-modifying CTE
//	SELECT ... FOR UPDATE / FOR SHARE                   row locks that block writers
//	SELECT ... INTO new_table                           creates a table
//	SELECT nextval('seq')                               advances a sequence
func readUnlessWrites(words, names []string) source.Access {
	worst := source.AccessRead
	raise := func(a source.Access) {
		if a > worst {
			worst = a
		}
	}
	for i, w := range words {
		switch w {
		case "insert", "update", "delete", "merge":
			raise(source.AccessWrite)
		case "share":
			if i > 0 && (words[i-1] == "for" || words[i-1] == "key") {
				raise(source.AccessWrite)
			}
		case "into":
			// INSERT INTO and MERGE INTO are already writes. Any other INTO
			// in a read-shaped statement is SELECT INTO, which creates a table.
			if i == 0 || (words[i-1] != "insert" && words[i-1] != "merge") {
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
// off or change who the session is.
func touchesReadOnly(words []string) bool {
	for _, w := range words {
		switch w {
		case "default_transaction_read_only", "transaction_read_only",
			"write",         // SET TRANSACTION / SESSION CHARACTERISTICS ... READ WRITE
			"role",          // SET ROLE
			"authorization", // SET SESSION AUTHORIZATION
			"all":           // RESET ALL restores the server default
			return true
		}
	}
	return false
}

// scan returns a statement's keyword-like words, lowercased, and every name in
// it, including quoted names.
//
// words are what structure is judged by, so comments, strings, numbers and
// quoted identifiers are excluded: "DELETE" in a comment or string is not a
// delete, and a column quoted as "delete" is not either.
//
// names include quoted identifiers, unquoted exactly, because
// "set_config"(...) calls the same function as set_config(...). Quoted names
// are case-sensitive in PostgreSQL, so "SET_CONFIG" names a different
// function and is correctly left alone.
func scan(d *sqllex.Dialect, text string) (words, names []string) {
	lx := sqllex.NewLexer(d)
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
