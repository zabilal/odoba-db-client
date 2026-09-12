package sqlcomplete

import "github.com/ikigai-db/ikigai-db/internal/model"

// Catalog is what completion knows about a server's objects.
//
// Every method answers from memory. Completion runs on the keystroke path,
// where NFR-P5 allows 16 ms for everything the editor does, so there is no
// context and no error: a catalog that does not yet know a schema's tables
// returns none, and the keywords still come. Filling it, and emptying it
// again when DDL runs, is the schema cache's work (T2.29).
type Catalog interface {
	// Databases the connection can see.
	Databases() []string

	// Schemas within a database. The empty database means the session's own.
	Schemas(database string) []string

	// Objects are the tables and views of a schema.
	Objects(database, schema string) []Object

	// Columns of one table or view.
	Columns(database, schema, object string) []Column

	// Routines are the schema's own functions and procedures, which are not
	// the dialect's built-in ones.
	Routines(database, schema string) []string
}

// Object is a table or a view.
type Object struct {
	Name string

	// Kind is model.KindTable, KindView or KindMaterializedView. The zero
	// value reads as a table.
	Kind model.ObjectKind
}

// Column is one column of a table or view.
type Column struct {
	Name string

	// Type is the column's type as the server names it, shown beside the
	// name in the popup.
	Type string
}

// Static is a Catalog held in a map, built by hand.
//
// It is what the tests complete against, and what the schema cache builds
// from an introspection pass (T2.29).
type Static struct {
	databases []string
	schemas   map[string][]string
	objects   map[string][]Object
	columns   map[string][]Column
	routines  map[string][]string
}

// NewStatic builds an empty catalog.
func NewStatic() *Static {
	return &Static{
		schemas:  map[string][]string{},
		objects:  map[string][]Object{},
		columns:  map[string][]Column{},
		routines: map[string][]string{},
	}
}

// AddSchema records a schema of a database, and the database with it.
func (s *Static) AddSchema(database, schema string) {
	if !contains(s.databases, database) {
		s.databases = append(s.databases, database)
	}
	if !contains(s.schemas[database], schema) {
		s.schemas[database] = append(s.schemas[database], schema)
	}
}

// AddObject records a table or view and its columns.
func (s *Static) AddObject(database, schema string, obj Object, cols ...Column) {
	s.AddSchema(database, schema)
	key := database + "\x00" + schema
	s.objects[key] = append(s.objects[key], obj)
	s.columns[key+"\x00"+obj.Name] = cols
}

// AddRoutine records a function or procedure of a schema.
func (s *Static) AddRoutine(database, schema, name string) {
	s.AddSchema(database, schema)
	key := database + "\x00" + schema
	s.routines[key] = append(s.routines[key], name)
}

func (s *Static) Databases() []string { return s.databases }

func (s *Static) Schemas(database string) []string { return s.schemas[database] }

func (s *Static) Objects(database, schema string) []Object {
	return s.objects[database+"\x00"+schema]
}

func (s *Static) Columns(database, schema, object string) []Column {
	return s.columns[database+"\x00"+schema+"\x00"+object]
}

func (s *Static) Routines(database, schema string) []string {
	return s.routines[database+"\x00"+schema]
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
