//go:build duckdb

package duckdb

import (
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/sqlscript"
	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

// splitScript divides a script into statements (FR-5.4).
//
// PostgreSQL's rule, which is DuckDB's: a routine written with a
// BEGIN ATOMIC … END body holds ';' between its own statements, and
// cutting there would send half a definition to the engine.
func splitScript(script string) []source.ScriptStatement {
	return sqlscript.Split(sqllex.PostgreSQL, script, sqlscript.PostgresBlocks)
}
