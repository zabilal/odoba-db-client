package model

import "strings"

// ObjectKind identifies what an object in the explorer tree is.
//
// Kinds are shared across paradigms wherever the semantics genuinely match.
// A Cassandra keyspace is a KindSchema because it contains tables; a Redis
// numbered database is a KindDatabase. Introducing per-engine kinds for
// concepts that already exist would push engine knowledge into the UI, which
// REQ-DB-4 forbids.
type ObjectKind string

const (
	KindServer   ObjectKind = "server"
	KindDatabase ObjectKind = "database"
	KindSchema   ObjectKind = "schema"
	KindFolder   ObjectKind = "folder" // pure grouping node, not a real object

	// Relational
	KindTable            ObjectKind = "table"
	KindView             ObjectKind = "view"
	KindMaterializedView ObjectKind = "materialized_view"
	KindColumn           ObjectKind = "column"
	KindIndex            ObjectKind = "index"
	KindConstraint       ObjectKind = "constraint"
	KindForeignKey       ObjectKind = "foreign_key"
	KindRoutine          ObjectKind = "routine" // procedure or function
	KindTrigger          ObjectKind = "trigger"
	KindSequence         ObjectKind = "sequence"
	KindUserType         ObjectKind = "user_type"

	// Document
	KindCollection ObjectKind = "collection"
	KindField      ObjectKind = "field"

	// Key-value
	KindKey ObjectKind = "key"

	// Stream
	KindCluster       ObjectKind = "cluster"
	KindTopic         ObjectKind = "topic"
	KindPartition     ObjectKind = "partition"
	KindConsumerGroup ObjectKind = "consumer_group"
	KindSubject       ObjectKind = "subject" // schema-registry subject
)

// ObjectRef addresses an object stably across every paradigm.
//
// Path is ordered outermost-first, e.g. {"sales", "public", "orders"} for a
// relational table or {"orders-topic"} for a Kafka topic. Path-based addressing
// is what lets the explorer, tab manager and session-restore code operate
// without knowing which paradigm produced the object.
type ObjectRef struct {
	Kind ObjectKind
	Path []string
}

// NewRef builds an ObjectRef.
func NewRef(kind ObjectKind, path ...string) ObjectRef {
	return ObjectRef{Kind: kind, Path: path}
}

// Name returns the object's own name — the last path element.
func (r ObjectRef) Name() string {
	if len(r.Path) == 0 {
		return ""
	}
	return r.Path[len(r.Path)-1]
}

// Parent returns the containing object's path with the given kind. The zero
// ObjectRef is returned when r is already at the root.
func (r ObjectRef) Parent(kind ObjectKind) ObjectRef {
	if len(r.Path) <= 1 {
		return ObjectRef{}
	}
	return ObjectRef{Kind: kind, Path: r.Path[:len(r.Path)-1]}
}

// IsZero reports whether r addresses nothing.
func (r ObjectRef) IsZero() bool { return r.Kind == "" && len(r.Path) == 0 }

// Equal reports whether two refs address the same object.
func (r ObjectRef) Equal(o ObjectRef) bool {
	if r.Kind != o.Kind || len(r.Path) != len(o.Path) {
		return false
	}
	for i := range r.Path {
		if r.Path[i] != o.Path[i] {
			return false
		}
	}
	return true
}

// String renders a stable, human-readable address, used in tab titles, logs
// and session state.
func (r ObjectRef) String() string {
	if r.IsZero() {
		return ""
	}
	return string(r.Kind) + ":" + strings.Join(r.Path, ".")
}

// Node is one entry in the object explorer tree.
//
// Every paradigm produces Nodes, so the tree widget has no per-source logic
// (FR-2.1, FR-2.2). Children are fetched lazily via the source's Introspector.
type Node struct {
	Ref   ObjectRef
	Label string

	// HasChildren drives the expand affordance without forcing a fetch.
	HasChildren bool

	// Browsable reports whether opening this node yields rows — a table,
	// collection, key pattern or topic. Drives the "open data" action.
	Browsable bool

	// Badge carries a lazily-fetched, cancellable count or size (FR-2.5).
	// Nil until loaded; loading must never block tree expansion.
	Badge *Badge

	// Attrs carries display-only metadata the UI renders generically, such as
	// a column's type, a topic's partition count or a Redis key's TTL.
	Attrs map[string]string
}

// Badge is a lazily-loaded count or size annotation on a tree node.
type Badge struct {
	Text  string
	Exact bool // false when the value is an estimate, rendered with a ~ prefix
}
