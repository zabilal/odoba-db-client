package postgres

import (
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/sqlscript"
	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

// splitScript divides a script into statements (FR-5.4). A function written
// with a BEGIN ATOMIC … END body (PostgreSQL 14+) holds ';' between its
// statements, and splitting there would send half a definition to the server.
func splitScript(d *sqllex.Dialect, script string) []source.ScriptStatement {
	return sqlscript.Split(d, script, sqlscript.PostgresBlocks)
}
