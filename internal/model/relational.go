package model

// Types in this file describe relational structure. They are the input to
// schema comparison (internal/diff) and DDL generation, so they must capture
// everything a sync script needs to reproduce an object — not merely what the
// explorer displays.

// Database is a top-level container. Engines without schemas (MySQL, SQLite)
// place tables directly in Schemas[0], a schema whose name is empty.
type Database struct {
	Name    string
	Charset string
	Collate string
	Comment string
	Schemas []Schema
}

// Schema is a namespace within a database. Cassandra keyspaces map here.
type Schema struct {
	Name    string
	Owner   string
	Comment string

	Tables    []Table
	Views     []View
	Routines  []Routine
	Sequences []Sequence
	UserTypes []UserType

	// Attrs carries engine-specific properties that survive round-tripping —
	// a Cassandra replication strategy, for instance.
	Attrs map[string]string
}

// Table is a base table.
type Table struct {
	Name    string
	Comment string

	Columns     []Column
	PrimaryKey  *PrimaryKey
	Indexes     []Index
	ForeignKeys []ForeignKey
	Uniques     []UniqueConstraint
	Checks      []CheckConstraint
	Triggers    []Trigger

	// RowsEstimate is -1 when unknown. Never fetched eagerly (FR-2.5).
	RowsEstimate int64

	Attrs map[string]string
}

// Column is a table or view column.
type Column struct {
	Name     string
	Type     DataType
	Position int

	Default    string
	HasDefault bool

	Identity      bool
	AutoIncrement bool
	Generated     string // generation expression, empty when not generated

	Comment string
	Attrs   map[string]string
}

// PrimaryKey is a table's primary key.
type PrimaryKey struct {
	Name    string
	Columns []string
}

// UniqueConstraint is a named uniqueness guarantee.
type UniqueConstraint struct {
	Name    string
	Columns []string
}

// CheckConstraint is a named row predicate.
type CheckConstraint struct {
	Name       string
	Expression string
}

// IndexColumn is one member of an index, with its sort direction.
type IndexColumn struct {
	Name       string
	Descending bool
	// Expression is set instead of Name for expression indexes.
	Expression string
}

// Index describes a secondary index.
type Index struct {
	Name    string
	Columns []IndexColumn
	Unique  bool

	// Method is the engine's access method: btree, hash, gin, gist, ...
	Method string

	// Predicate is the WHERE clause of a partial index.
	Predicate string

	// Include lists non-key payload columns.
	Include []string

	Attrs map[string]string
}

// ReferentialAction is the behaviour of a foreign key on parent change.
type ReferentialAction string

const (
	ActionNoAction   ReferentialAction = "NO ACTION"
	ActionRestrict   ReferentialAction = "RESTRICT"
	ActionCascade    ReferentialAction = "CASCADE"
	ActionSetNull    ReferentialAction = "SET NULL"
	ActionSetDefault ReferentialAction = "SET DEFAULT"
)

// ForeignKey describes a referential constraint. It drives both the ER diagram
// (FR-8.3) and grid FK navigation (FR-3.11, FR-3.12).
type ForeignKey struct {
	Name    string
	Columns []string

	RefSchema  string
	RefTable   string
	RefColumns []string

	OnDelete ReferentialAction
	OnUpdate ReferentialAction
}

// Referrer is a foreign key of another table that refers to a table: the
// table the key is in, and the key (FR-3.11).
type Referrer struct {
	From ObjectRef
	Key  ForeignKey
}

// View is a view or materialized view.
type View struct {
	Name         string
	Materialized bool
	Definition   string
	Columns      []Column
	Comment      string
	Indexes      []Index // materialized views may be indexed
}

// RoutineKind distinguishes procedures from functions.
type RoutineKind string

const (
	RoutineProcedure RoutineKind = "procedure"
	RoutineFunction  RoutineKind = "function"
)

// Parameter is a routine argument.
type Parameter struct {
	Name       string
	Type       DataType
	Mode       string // IN, OUT, INOUT, VARIADIC
	Default    string
	HasDefault bool
}

// Routine is a stored procedure or function.
type Routine struct {
	Name       string
	Kind       RoutineKind
	Language   string
	Parameters []Parameter
	Returns    *DataType
	Definition string
	Comment    string
}

// Trigger is a table trigger.
type Trigger struct {
	Name       string
	Timing     string // BEFORE, AFTER, INSTEAD OF
	Events     []string
	ForEachRow bool
	Condition  string
	Definition string
}

// Sequence is a standalone number generator.
type Sequence struct {
	Name      string
	DataType  string
	Start     int64
	Increment int64
	MinValue  *int64
	MaxValue  *int64
	Cycle     bool
	Comment   string
}

// UserType is a user-defined type: enum, composite or domain.
type UserType struct {
	Name       string
	Category   string // enum, composite, domain, range
	EnumValues []string
	Fields     []FieldDef
	BaseType   string
	Comment    string
}

// Dependent is something else in a database that names an object, reported
// before a rename so somebody can see what it costs (FR-6.6).
//
// Breaks is the whole point. On PostgreSQL almost nothing breaks: a view, a
// foreign key, an index and a trigger are all held by identity, so a rename
// carries them and pg_get_viewdef prints the new name afterwards. What does
// break is text the engine never re-resolves — a PL/pgSQL body, or a SQL
// function whose body is a string — and that can only be found by looking
// for the name in the text, which is a guess and says so in Note.
type Dependent struct {
	Ref    ObjectRef
	Label  string // how to name it to a person
	Note   string // how it depends, in words
	Breaks bool   // a rename breaks it, rather than being carried along
}
