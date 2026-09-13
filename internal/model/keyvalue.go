package model

import "time"

// What a key-value store is made of (FR-12.2, ADR-0078).
//
// A database here is a keyspace and a key is one value in it. Neither has
// columns, so neither is a Table; what there is to say about them is what the
// server itself reports, under the names it reports them by.

// Keyspace is a numbered database, and what the server is spending on it.
type Keyspace struct {
	Name string

	// Keys and Expiring are -1 where the server did not say.
	Keys     int64
	Expiring int64

	// Figures are the server's own numbers, under the headings it gives
	// them. They keep the server's names — used_memory_human is what a
	// person searching for it will search for, and a friendlier name would
	// only be one more thing to translate.
	Figures []FigureGroup
}

// FigureGroup is a heading and the numbers under it.
type FigureGroup struct {
	Title  string
	Values []Figure
}

// Figure is one of a server's numbers.
type Figure struct {
	Name  string
	Value string
}

// StoredKey is one key: what it holds, how long it has left, and what it
// costs the server to hold.
type StoredKey struct {
	Name string

	// Kind is the server's own word for what the key holds: string, hash,
	// list, set, zset, stream, or a module's own.
	Kind string

	// TTL is how long the key has left, and 0 where it never expires.
	TTL time.Duration

	// Bytes and Length are -1 where the server did not say. Length is in
	// whatever the kind is counted in: characters, fields, elements,
	// members, entries.
	Bytes  int64
	Length int64

	// Encoding is how the server is holding the value — listpack, skiplist,
	// embstr — which is its own word and worth showing as it is.
	Encoding string
}
