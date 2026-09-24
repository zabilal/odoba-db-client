// Package duckdb is the DuckDB driver (T3.34).
//
// It is built only with the "duckdb" tag, and without that tag this file
// is all there is of it.
//
// Every other database this application speaks to is reached in Go alone.
// DuckDB has no Go implementation and no wire protocol to speak: it is a
// library, and the only way to it is cgo over a copy of the engine built
// for each platform. That is a C toolchain to build with and about a
// hundred megabytes of prebuilt object code per platform, which is a
// price the rest of the application should not pay for a driver most
// people will not use.
//
// So it is behind a tag. `go build ./...` compiles this file and nothing
// else, links no C, and cross-compiles as it did before;
// `go build -tags duckdb` builds the driver.
package duckdb
