package source

import "github.com/ikigai-db/ikigai-db/internal/model"

// Dialect generates and classifies statement text for one engine.
//
// It is OPTIONAL, and paired with Queryer: a source with no query language has
// neither. ARCH-2 requires that all statement text originate here — UI code
// never builds a statement, and no driver concatenates user values into one.
type Dialect interface {
	// QuoteIdentifier renders an identifier safely, applying the engine's
	// quoting rules and escaping embedded quote characters. This is the only
	// sanctioned way an identifier reaches statement text (NFR-S6).
	QuoteIdentifier(name string) string

	// QualifyRef renders a fully-qualified object name from a ref.
	QualifyRef(ref model.ObjectRef) string

	// Placeholder renders the parameter placeholder for the 1-based index i,
	// covering the difference between $1, ?, :1 and @p1.
	Placeholder(i int) string

	// Classify determines what a statement does, so the Guard can refuse it
	// before it reaches the server (NFR-S4).
	//
	// Classification must be conservative: anything not confidently identified
	// as read-only is reported as mutating. A misclassified write on a
	// read-only production connection is the failure mode this exists to
	// prevent, so the safe direction is to over-restrict.
	Classify(statement string) Access

	// SplitScript divides script text into statements with their offsets,
	// respecting the engine's string literals, comments and delimiter rules
	// (FR-5.4, FR-5.10).
	SplitScript(script string) []ScriptStatement

	// BuildBrowse renders the statement a Browse would execute.
	//
	// Exposed rather than kept internal because UX principle 6 requires the
	// grid to show the user the SQL behind their filters (FR-3.6).
	BuildBrowse(ref model.ObjectRef, opt BrowseOptions) (Statement, error)
}

// RowScripter is an optional Dialect refinement: it writes rows as the
// INSERT statements that would add them, for Copy as INSERT (FR-3.7).
//
// Values are written as literals, not bound: the text is for a person to
// read, keep, or run elsewhere, so it cannot carry its values beside it the
// way a statement this application runs does (NFR-S6). Each literal must
// read back, on the same engine, as the value it was written from. A value
// that cannot be written so is an error, never an approximation.
type RowScripter interface {
	InsertRows(ref model.ObjectRef, cols []model.ColumnDef, rows []model.Row) (string, error)
}

// ScriptStatement is one statement located within a script.
type ScriptStatement struct {
	Text string

	// Offset is the 0-based character offset of Text within the script, used
	// to map server errors back to an editor position.
	Offset int
}

// DDLGenerator is an optional Dialect refinement producing structural
// statements (FR-6.4, FR-6.7, FR-7.3).
//
// Every method returns statement text rather than executing it, because the
// preview is mandatory: UX principle 6 and FR-6.4 require the user to see the
// exact DDL before anything runs.
type DDLGenerator interface {
	// CreateObject renders the DDL that would recreate an object. The concrete
	// type matches Introspector.Describe for the same kind.
	//
	// The ref names it. A model.Table carries a bare name and nothing about
	// the schema it is in, so rendering from the object alone would produce
	// statements for whichever table of that name the search path reaches —
	// which is the right one until it is not.
	CreateObject(ref model.ObjectRef, obj any) ([]Statement, error)

	// AlterObject renders the DDL transforming from into to. Both are the same
	// concrete type. An empty result means the two are equivalent.
	//
	// Columns are matched by name. Two tables cannot say which column was
	// renamed to which — a rename and a drop-with-an-add look identical —
	// and guessing wrong drops a column's data. Whatever does know renames
	// them first, with RenameColumn, and hands over a table already using
	// the new names.
	AlterObject(ref model.ObjectRef, from, to any) ([]Statement, error)

	// RenameColumn renders renaming one of a table's columns.
	//
	// Apart from AlterObject because only the caller knows a rename
	// happened: a designer does, a schema diff does not (ADR-0114).
	RenameColumn(ref model.ObjectRef, from, to string) ([]Statement, error)

	// DropObject renders the DDL removing an object.
	DropObject(ref model.ObjectRef, cascade bool) ([]Statement, error)
}
