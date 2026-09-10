package model

// Types in this file describe document-store structure.
//
// The defining difference from relational structure is that shape is inferred
// from sampled data rather than declared by the server, so every inferred type
// carries the evidence behind it. The UI must be able to say "87% of sampled
// documents have this field" rather than presenting inference as fact.

// Collection is a set of documents.
type Collection struct {
	Name    string
	Indexes []DocumentIndex

	// Shape is the inferred field structure (FR-12.4). Empty until inference
	// has run; inference is always explicit and cancellable, never implicit.
	Shape *DocumentShape

	// DocumentsEstimate is -1 when unknown.
	DocumentsEstimate int64

	Attrs map[string]string
}

// DocumentShape is the result of sampling documents to infer structure.
type DocumentShape struct {
	// Sampled is how many documents the inference examined.
	Sampled int64
	Fields  []InferredField
}

// InferredField is one field observed while sampling a collection.
type InferredField struct {
	Name string

	// Types lists every type observed for this field, most frequent first.
	// A field holding both string and int64 is a real and common condition
	// in document stores; the model represents it rather than collapsing it.
	Types []ObservedType

	// Presence is the fraction of sampled documents containing the field,
	// in [0,1]. The UI renders this so inference is never mistaken for schema.
	Presence float64

	// Children are the members of an embedded document or array element.
	Children []InferredField
}

// ObservedType pairs a type with how often it was seen.
type ObservedType struct {
	Type  DataType
	Count int64
}

// DocumentIndex describes an index on a collection.
type DocumentIndex struct {
	Name    string
	Keys    []IndexColumn
	Unique  bool
	Sparse  bool
	TTL     int64 // seconds; 0 when not a TTL index
	Partial string
	Attrs   map[string]string
}
