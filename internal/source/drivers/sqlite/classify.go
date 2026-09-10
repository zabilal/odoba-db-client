package sqlite

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
// It is the first of two defences. A read-only connection also opens the
// file read-only with query_only set, so the engine itself refuses writes a
// classifier might miss.

// readPragmas report and change nothing when called without a value.
var readPragmas = map[string]bool{
	"table_info": true, "table_xinfo": true, "table_list": true, "index_list": true,
	"index_info": true, "index_xinfo": true, "foreign_key_list": true, "database_list": true,
	"collation_list": true, "function_list": true, "module_list": true, "pragma_list": true,
	"compile_options": true, "integrity_check": true, "quick_check": true,
	"foreign_key_check": true, "user_version": true, "schema_version": true,
	"application_id": true, "page_count": true, "page_size": true, "freelist_count": true,
	"encoding": true, "journal_mode": true, "data_version": true,
}

// adminFunctions do more than compute: load_extension runs native code.
var adminFunctions = map[string]bool{"load_extension": true}

func classify(stmt string) source.Access {
	stmts := sqlscript.Split(sqllex.SQLite, stmt, sqlscript.TriggerBlocks)
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
	words, hasAssign := words(stmt)
	if len(words) == 0 {
		return source.AccessRead
	}
	for _, w := range words {
		if adminFunctions[w] {
			return source.AccessAdmin
		}
	}
	switch words[0] {
	case "select", "with", "values":
		// A CTE can front an INSERT, UPDATE or DELETE in SQLite.
		for _, w := range words {
			switch w {
			case "insert", "update", "delete", "replace":
				return source.AccessWrite
			}
		}
		return source.AccessRead
	case "explain":
		return source.AccessRead // describes a statement without running it
	case "begin", "commit", "end", "rollback", "savepoint", "release":
		return source.AccessRead // transaction control changes no data by itself
	case "pragma":
		if len(words) > 1 && readPragmas[words[1]] && !hasAssign {
			return source.AccessRead
		}
		return source.AccessAdmin
	case "insert", "update", "delete", "replace", "analyze":
		return source.AccessWrite
	case "create", "alter", "drop":
		return source.AccessDDL
	case "attach", "detach", "vacuum", "reindex":
		return source.AccessAdmin // ATTACH can open another file for writing
	}
	return source.AccessWrite
}

// words is the statement's keywords and identifiers, lower-cased, and
// whether it assigns with "=" at the top level, as "PRAGMA x = 1" does.
func words(stmt string) ([]string, bool) {
	lx := sqllex.NewLexer(sqllex.SQLite)
	var st sqllex.State
	var out []string
	assign := false
	for _, line := range strings.Split(stmt, "\n") {
		toks, next := lx.LexLine(line, st)
		for _, tk := range toks {
			text := line[tk.Start:tk.End]
			switch tk.Kind {
			case sqllex.TokKeyword, sqllex.TokIdentifier, sqllex.TokFunction, sqllex.TokType:
				out = append(out, strings.ToLower(text))
			case sqllex.TokOperator:
				if text == "=" {
					assign = true
				}
			}
		}
		st = next
	}
	return out, assign
}
