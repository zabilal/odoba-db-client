package model

import "time"

// Types in this file describe key-value structure.
//
// A key-value store has no schema at all: the unit of structure is the
// individual key, and its type determines which editor applies (FR-12.2).

// ValueKind is the type of a key-value entry. Each maps to a dedicated editor.
type ValueKind string

const (
	ValueString    ValueKind = "string"
	ValueList      ValueKind = "list"
	ValueSet       ValueKind = "set"
	ValueSortedSet ValueKind = "zset"
	ValueHash      ValueKind = "hash"
	ValueStreamLog ValueKind = "stream"
	ValueJSONDoc   ValueKind = "json"
	ValueOther     ValueKind = "other"
)

// KeyEntry is one key in a key-value store, as listed by the key browser.
//
// Values are never loaded during listing — a listing may cover millions of
// keys, so the browser shows metadata and fetches a value only on selection.
type KeyEntry struct {
	Key  string
	Kind ValueKind

	// TTL is the remaining lifetime. Nil means the key does not expire.
	TTL *time.Duration

	// Size is the element count for containers, or byte length for strings.
	// -1 when not cheaply available.
	Size int64

	// MemoryBytes is the engine's own memory accounting, -1 when unknown.
	MemoryBytes int64
}

// KeyPattern is a saved or active scan pattern in the key browser.
type KeyPattern struct {
	// Match is the glob pattern applied server-side during the scan.
	Match string

	// Kind filters results to one value kind. Empty means all kinds.
	Kind ValueKind

	// Count is the server-side scan batch hint. Scans are always incremental
	// and cancellable: a blocking full-keyspace enumeration is never issued.
	Count int64
}
