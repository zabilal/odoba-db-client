//go:build duckdb

package main

// DuckDB is behind a tag of its own. It is the one engine here with no Go
// implementation: reaching it means cgo over a copy of the engine built
// for each platform, which is a C toolchain to build with and a hundred
// megabytes of object code, and the rest of the application should not
// pay that for a driver most people will not use (ADR-0146).
import _ "github.com/ikigai-db/ikigai-db/internal/source/drivers/duckdb"
