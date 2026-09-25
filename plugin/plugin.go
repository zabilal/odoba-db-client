// Package plugin is how a source that is not in this application is written
// (FR-16.2).
//
// A plugin is a program. It speaks JSON lines on its standard input and output,
// one object a line, and it can be written in any language — this package is a
// convenience for writing one in Go, not the interface itself. The interface is
// the protocol, which is written down in docs/PLUGINS.md and in the comments
// here, and a plugin that speaks it is a plugin whatever built it.
//
// A program rather than a shared library, because Go's own plugin support is
// unusable for a distributed application: it works on Linux and macOS and not
// on Windows, it refuses to load anything not built with the same toolchain
// version and byte-identical dependency versions, it cannot be used with the
// -trimpath the release build uses, and a panic inside one takes the
// application down with it. A program has none of those problems, can be
// written in any language, and can be killed — which is what cancelling
// something ultimately means (ARCH-4).
//
// Nothing here imports anything else of this application's, deliberately: a
// plugin is a separate module, so it cannot import internal/..., and a protocol
// that needed the application's own types would be a protocol only this
// application's Go version could speak.
package plugin

// Protocol is the version of the conversation this package speaks. A host that
// does not know it refuses to talk, rather than guessing what a message means.
const Protocol = 1

// Kind is what a plugin provides. One for now; the field exists because a file
// format is the other half of FR-16.2 and will be one of these.
const (
	KindSource = "source"
)

// Hello is what a plugin says about itself, before anything else happens. It is
// the whole of what the application needs to offer the plugin in its
// new-connection picker: the driver's name, the fields its form has, and what it
// can do (REQ-DB-1).
type Hello struct {
	Protocol int    `json:"protocol"`
	Kind     string `json:"kind"`
	// ID is the driver's identifier, and is what a saved connection records.
	// It must not be one the application already has.
	ID   string `json:"id"`
	Name string `json:"name"`
	// Paradigm is relational, document, keyvalue or stream. Empty is
	// relational.
	Paradigm string `json:"paradigm,omitempty"`
	// Version is the plugin's own, shown beside its name so that somebody
	// reporting a fault can say which one they have.
	Version      string       `json:"version,omitempty"`
	Fields       []Field      `json:"fields,omitempty"`
	Capabilities Capabilities `json:"capabilities"`
}

// Field is one box on the connection form.
type Field struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Help  string `json:"help,omitempty"`
	// Kind is text, number, file, password or choice. Empty is text.
	Kind     string   `json:"kind,omitempty"`
	Choices  []string `json:"choices,omitempty"`
	Default  string   `json:"default,omitempty"`
	Required bool     `json:"required,omitempty"`
	// Secret sends this field's value to the keychain rather than the
	// settings file, and it reaches the plugin only when a connection opens
	// (FR-1.5, NFR-S1).
	Secret bool `json:"secret,omitempty"`
}

// Capabilities is what the source can do, as data. The application asks this
// rather than trying things and catching failures: an affordance offered and
// then refused is worse than one that was never offered (REQ-DB-2).
// A plugin takes no statements. A query editor is not a "run this" call: it is
// splitting a script, classifying each statement so a read-only connection can
// refuse the ones that write, quoting identifiers, and completing from the
// schema. A protocol that answered only "run this" would be asking the
// application to trust a plugin with the one guard it exists to keep (NFR-S4),
// so browsing is the whole of the data path here, as it is the required one
// everywhere (ADR-0005).
type Capabilities struct {
	// Schemas says there is a level between a database and its tables.
	Schemas bool `json:"schemas,omitempty"`
	// MultipleDatabases is false for a source that holds one.
	MultipleDatabases bool `json:"multiple_databases,omitempty"`
	// ServerFilter and ServerSort say the source itself can do those, rather
	// than the application doing them over what it has read.
	ServerFilter bool `json:"server_filter,omitempty"`
	ServerSort   bool `json:"server_sort,omitempty"`
	// Objects are the kinds this source has: table, view, collection, topic,
	// key, routine, sequence, index, trigger.
	Objects []string `json:"objects,omitempty"`
}

// Ref addresses an object: a kind and the path down to it, exactly as the
// application's own references work. The path starts at the database.
type Ref struct {
	Kind string   `json:"kind"`
	Path []string `json:"path"`
}

// Node is one row of the object tree.
type Node struct {
	Ref   Ref    `json:"ref"`
	Label string `json:"label"`
	// Detail is the grey text beside the label: a row count, a type, a size.
	Detail string `json:"detail,omitempty"`
	// HasChildren draws the twisty. Browsable offers the object for opening.
	HasChildren bool `json:"has_children,omitempty"`
	Browsable   bool `json:"browsable,omitempty"`
}

// Column is one column of an object, as the grid and the model read it.
type Column struct {
	Name string `json:"name"`
	// Type is the source's own name for the type, shown as it is given.
	Type     string `json:"type,omitempty"`
	Nullable bool   `json:"nullable,omitempty"`
	// Class helps the grid align and edit: string, integer, float, boolean,
	// date, time, timestamp, binary, json, uuid. Empty is string.
	Class string `json:"class,omitempty"`
	// Comment is the column's own, where the source keeps one.
	Comment string `json:"comment,omitempty"`
}

// Object is what describing something answers. One type rather than several,
// because a plugin should not have to know which of the application's types its
// object becomes: Columns makes it a table, Definition makes it a view, and
// both makes it a table with a definition.
type Object struct {
	Ref     Ref      `json:"ref"`
	Columns []Column `json:"columns,omitempty"`
	// PrimaryKey names the columns that address a row. Without it the
	// application will not offer to edit the rows, because it cannot say
	// which row an edit is about (FR-4.7).
	PrimaryKey []string `json:"primary_key,omitempty"`
	// Definition is a view's or a routine's text.
	Definition string `json:"definition,omitempty"`
	// Rows is an estimate, or -1 where the source does not keep one. Zero
	// means zero.
	Rows    int64  `json:"rows,omitempty"`
	Comment string `json:"comment,omitempty"`
}

// BrowseOptions is how an object's rows are asked for. A source that cannot do
// one of these says so in its capabilities and is not asked.
type BrowseOptions struct {
	Offset int64 `json:"offset,omitempty"`
	Limit  int64 `json:"limit,omitempty"`
	// Where is a condition in the source's own language, and reaches the
	// plugin exactly as somebody typed it. A plugin that takes it must refuse
	// anything that is not a condition — a second statement, or a comment
	// running on past it (REQ-DRV-3).
	Where   string   `json:"where,omitempty"`
	Sorts   []Sort   `json:"sorts,omitempty"`
	Filters []Filter `json:"filters,omitempty"`
}

// Sort is one ordering.
type Sort struct {
	Column     string `json:"column"`
	Descending bool   `json:"descending,omitempty"`
}

// Filter is one condition the application built from the grid's own controls.
// The value is a value, never text to be pasted into a statement (NFR-S6).
type Filter struct {
	Column string `json:"column"`
	// Op is eq, ne, lt, le, gt, ge, contains, starts, ends, null or notnull.
	Op    string `json:"op"`
	Value any    `json:"value,omitempty"`
}

// Info is what the source says about itself once it is open.
type Info struct {
	Product string `json:"product,omitempty"`
	Version string `json:"version,omitempty"`
	// Database is the one this connection is on, where the source has more
	// than one.
	Database string `json:"database,omitempty"`
}

// Config is a connection, as the application holds it. Secrets are here and
// nowhere else: they arrive when a connection opens and are not written down by
// the application anywhere but the keychain.
type Config struct {
	Host     string            `json:"host,omitempty"`
	Port     int               `json:"port,omitempty"`
	Database string            `json:"database,omitempty"`
	User     string            `json:"user,omitempty"`
	Params   map[string]string `json:"params,omitempty"`
	Secrets  map[string]string `json:"secrets,omitempty"`
	// ReadOnly is the connection's guard. A plugin must refuse anything that
	// would change data while it is set — the application refuses what it can
	// see, and what it cannot see is the plugin's to refuse (NFR-S4).
	ReadOnly bool `json:"read_only,omitempty"`
	// Environment is development, staging or production, where somebody said.
	Environment string `json:"environment,omitempty"`
}
