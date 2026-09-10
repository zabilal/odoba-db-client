// Package model defines Ikigai DB's canonical, source-agnostic data model.
//
// Four paradigms are represented — relational, document, key-value and
// stream/log — behind one uniform surface so that UI code never branches on
// the concrete source type (REQ-DB-3, REQ-DB-4).
//
// This package must not import any UI package (ARCH-1).
package model

// Paradigm classifies how a source organises data. It determines which of the
// structural types in this package apply, and which view components the UI
// offers, but it never appears as a branch inside shared UI logic — that is
// what capability descriptors are for.
type Paradigm string

const (
	// ParadigmRelational covers engines with schemas, tables and a SQL-family
	// query language: PostgreSQL, MySQL, SQLite, SQL Server, Cassandra, ...
	ParadigmRelational Paradigm = "relational"

	// ParadigmDocument covers collection-of-documents engines whose shape is
	// inferred from the data rather than declared: MongoDB, Firestore, ...
	ParadigmDocument Paradigm = "document"

	// ParadigmKeyValue covers flat keyspaces of typed values: Redis, ...
	ParadigmKeyValue Paradigm = "keyvalue"

	// ParadigmStream covers partitioned append-only logs, which have no rows,
	// no server-owned schema and offsets in place of primary keys: Kafka, ...
	ParadigmStream Paradigm = "stream"
)

// Valid reports whether p is a known paradigm.
func (p Paradigm) Valid() bool {
	switch p {
	case ParadigmRelational, ParadigmDocument, ParadigmKeyValue, ParadigmStream:
		return true
	}
	return false
}

func (p Paradigm) String() string { return string(p) }
