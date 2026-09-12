// Package capability describes, as data, what a source can do.
//
// REQ-DB-2 requires the UI to hide or disable affordances a source does not
// support rather than failing at runtime, and REQ-DB-4 forbids branching on the
// concrete source type in shared code. Every such decision reads a field here.
//
// The test for whether something belongs in this struct: if UI code would
// otherwise need to know which engine it is talking to, it belongs here.
package capability

import "github.com/ikigai-db/ikigai-db/internal/model"

// Capabilities is a source's declarative feature descriptor.
//
// Zero values are deliberately the conservative answer — a field left unset
// means "not supported", so a new capability added to this struct cannot
// silently enable an untested code path in an existing driver.
type Capabilities struct {
	Paradigm model.Paradigm

	// Structure describes the object hierarchy the source presents.
	Structure Structure

	// Query describes text-query support. Sources such as Kafka leave this
	// zeroed, and the UI offers no query editor for them.
	Query Query

	// Data describes what the grid may do against this source.
	Data Data

	// Schema describes structural editing and comparison support.
	Schema Schema

	// Stream describes log-specific operations.
	Stream Stream

	// Objects enumerates which object kinds the source can produce. The
	// explorer builds its node classes from this set.
	Objects map[model.ObjectKind]bool
}

// Supports reports whether the source produces objects of the given kind.
func (c Capabilities) Supports(k model.ObjectKind) bool {
	return c.Objects[k]
}

// Structure describes the shape of the object hierarchy.
type Structure struct {
	// MultipleDatabases is false for single-database sources such as SQLite.
	MultipleDatabases bool

	// Schemas is false for engines where tables sit directly in a database,
	// such as MySQL and SQLite.
	Schemas bool

	// CreateDatabase reports whether new databases can be created.
	CreateDatabase bool

	// InferredShape reports source.ShapeInferrer: an object whose fields are
	// sampled from its data rather than declared by the server (FR-12.4).
	// The UI offers the sampling; it never runs it unasked.
	InferredShape bool
}

// Query describes text-query support.
type Query struct {
	// Supported is false for sources with no query language at all.
	Supported bool

	// Language names the query language as sqllex.DialectFor knows it:
	// "postgresql", "mysql", "sqlite", "sqlserver", "cql". It picks the
	// editor's highlighting and the rules history is redacted by, so an
	// unknown name is a defect, not a harmless default; the conformance suite
	// checks it with sqllex.Known. Document and key-value languages
	// ("mongosh", "redis") arrive with their lexers.
	Language string

	// MultiStatement reports whether one submission may contain several
	// statements producing several result sets (FR-5.4).
	MultiStatement bool

	// Cancel reports genuine driver-level cancellation (FR-5.5, NFR-P9). When
	// false, the UI must not offer a cancel button that only detaches the view.
	Cancel bool

	// Parameters reports server-side parameter binding (FR-5.7).
	Parameters bool

	// Explain reports query-plan retrieval (FR-5.13).
	Explain bool

	// Transactions reports explicit transaction control (FR-5.14).
	Transactions bool

	// EditableResults reports that a query's result says where its columns
	// were read from, and is known by a table's key when every column comes
	// from that one table and its key is among them (FR-4.8, ADR-0035).
	EditableResults bool
}

// Data describes grid capabilities.
type Data struct {
	// ServerSort and ServerFilter report whether ordering and filtering are
	// pushed to the server. When false the grid must not offer them over
	// partial results, which would silently mislead.
	ServerSort   bool
	ServerFilter bool

	// Insert, Update and Delete report write support for browsed rows.
	Insert bool
	Update bool
	Delete bool

	// TransactionalWrite reports whether a changeset commits atomically
	// (FR-4.5). When false the UI must warn that a partial failure leaves
	// earlier rows applied.
	TransactionalWrite bool

	// BulkLoad reports source.BulkLoader: rows imported many at a time
	// (FR-10.6).
	BulkLoad bool

	// DistinctValues reports whether the source can enumerate a column's
	// distinct values for the Excel-style filter picklist (FR-3.4).
	DistinctValues bool

	// ApproximateCount reports whether a cheap row estimate is available for
	// tree badges (FR-2.5).
	ApproximateCount bool

	// ExactCount reports whether an exact count is affordable. False for
	// large-scale engines where COUNT(*) is a full scan.
	ExactCount bool

	// Pipeline reports source.Aggregator: rows read by a pipeline of stages
	// the person writes, rather than by filters over an object (FR-12.1).
	Pipeline bool
}

// Schema describes structural editing and comparison.
type Schema struct {
	// DDL reports whether structure can be modified at all.
	DDL bool

	// Indexes reports source.IndexManager: indexes made and unmade on their
	// own, apart from the object they are on (FR-6.3, FR-12.1).
	Indexes bool

	// Diff reports participation in schema comparison (FR-7).
	Diff bool

	// ERDiagram reports whether relationships are declared and therefore
	// diagrammable (FR-8). False for engines without foreign keys.
	ERDiagram bool

	// ForeignKeys reports declared referential constraints, which drive both
	// the diagram and grid FK navigation (FR-3.11).
	ForeignKeys bool

	// ScriptObject reports DDL generation for an existing object (FR-6.7).
	ScriptObject bool
}

// Stream describes log-specific operations.
type Stream struct {
	// Consume reports whether records can be read.
	Consume bool

	// Produce reports whether records can be written (FR-13.11).
	Produce bool

	// SeekTimestamp reports offset lookup by time (FR-13.5).
	SeekTimestamp bool

	// Follow reports live tailing (FR-13.6).
	Follow bool

	// ConsumerGroups reports group and lag inspection (FR-13.10).
	ConsumerGroups bool

	// ResetOffsets reports group offset manipulation (FR-13.13).
	ResetOffsets bool

	// SchemaRegistry reports an attached registry for record decoding
	// (FR-13.7, FR-13.14).
	SchemaRegistry bool

	// TopicAdmin reports topic creation, deletion and reconfiguration
	// (FR-13.12).
	TopicAdmin bool
}
