//go:build duckdb

package main

// DuckDB is behind a tag of its own here for the same reason it is in the
// application: cgo over a copy of the engine built for each platform
// (ADR-0146). The two binaries carry the same drivers, including this one's
// absence.
import _ "github.com/ikigai-db/ikigai-db/internal/source/drivers/duckdb"
