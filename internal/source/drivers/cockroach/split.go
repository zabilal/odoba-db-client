package cockroach

import (
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/sqlscript"
	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

// splitScript divides a script into statements (FR-5.4).
//
// The same rule as PostgreSQL's: a routine written with a BEGIN ATOMIC …
// END body holds ';' between its own statements, and cutting there would
// send half a definition to the server. CockroachDB writes its user-defined
// functions that way too.
func splitScript(script string) []source.ScriptStatement {
	return sqlscript.Split(sqllex.PostgreSQL, script, sqlscript.PostgresBlocks)
}
