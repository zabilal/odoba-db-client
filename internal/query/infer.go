package query

import (
	"slices"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// Joins the catalogue implies (FR-9.1).
//
// A foreign key is a join the database has already declared, so a designer
// that made somebody draw it again would be asking them to retype what the
// schema says. What it does not do is guess: two columns of the same name in
// two tables are not a relationship, they are two columns, and a designer that
// joined on them would produce a query nobody wrote and present it as inferred
// from the schema. Only declared keys are used.
//
// An inferred join is marked as one, so the canvas can draw it differently and
// so a person can tell what the schema said from what they decided (Join.
// Inferred). Changing one or taking it away is theirs to do; nothing here
// puts back a join somebody removed.

// Suggestion is a join the catalogue implies between two tables on the canvas.
type Suggestion struct {
	Join Join

	// Key names the foreign key it came from, for the designer to say which.
	Key string
}

// Suggest is the joins the foreign keys imply between the tables on the canvas
// that are not already joined.
//
// Every pair is considered in both directions, because which of two tables was
// dragged on first says nothing about which holds the key. A pair already
// joined is left alone, however it came to be joined: a suggestion is for a
// gap, and a join somebody has is not a gap.
func Suggest(d *Design, db *model.Database) []Suggestion {
	if db == nil {
		return nil
	}
	tables := make([]*model.Table, len(d.Tables))
	for i, t := range d.Tables {
		tables[i] = findTable(db, t.Ref)
	}
	joined := map[[2]int]bool{}
	for _, j := range d.Joins {
		joined[pairKey(j.Left, j.Right)] = true
	}
	var out []Suggestion
	for left := range d.Tables {
		for right := range d.Tables {
			if left == right || joined[pairKey(left, right)] {
				continue
			}
			s, ok := suggestionFor(d, tables, left, right)
			if !ok {
				continue
			}
			out = append(out, s)
			// One join per pair of tables. A pair with two foreign keys
			// between them — two dates, two addresses — is a choice somebody
			// has to make, and making it here would be making it silently.
			joined[pairKey(left, right)] = true
		}
	}
	return out
}

// Apply adds suggestions to the design. It is separate from Suggest so that
// the designer can show what it found and let somebody take it or leave it,
// which is what "editable" means in FR-9.1.
func Apply(d *Design, suggestions []Suggestion) {
	for _, s := range suggestions {
		d.Joins = append(d.Joins, s.Join)
	}
}

// suggestionFor is the join a foreign key on the left table implies to the
// right one, if there is one.
func suggestionFor(d *Design, tables []*model.Table, left, right int) (Suggestion, bool) {
	from, to := tables[left], tables[right]
	if from == nil || to == nil {
		return Suggestion{}, false
	}
	for _, fk := range from.ForeignKeys {
		if !pointsAt(fk, d.Tables[right].Ref, to.Name) {
			continue
		}
		if len(fk.Columns) == 0 || len(fk.Columns) != len(fk.RefColumns) {
			// A key whose two sides do not line up is one this cannot write a
			// condition from; the catalogue said something it does not
			// understand, and inventing the pairing would be worse.
			continue
		}
		on := make([]Pair, len(fk.Columns))
		for i := range fk.Columns {
			on[i] = Pair{Left: fk.Columns[i], Right: fk.RefColumns[i]}
		}
		return Suggestion{
			Key: fk.Name,
			// INNER, because that is what a foreign key means: a row with a
			// key has the row it points at. A person who wants the rows
			// without one changes it to LEFT, which is one of the commonest
			// things they will do and is why the kind is theirs to set.
			Join: Join{Kind: JoinInner, Left: left, Right: right, On: on, Inferred: true},
		}, true
	}
	return Suggestion{}, false
}

// pointsAt reports whether a foreign key refers to the table a ref names.
//
// The comparison is on the schema and the name, and the schema only where the
// key says one: a catalogue may leave it empty for a key within the same
// schema, and treating that as a different schema would find no joins at all
// in half the databases there are.
func pointsAt(fk model.ForeignKey, ref model.ObjectRef, name string) bool {
	if !strings.EqualFold(fk.RefTable, name) {
		return false
	}
	if fk.RefSchema == "" {
		return true
	}
	return strings.EqualFold(fk.RefSchema, schemaOf(ref))
}

// schemaOf is the schema a ref names, or "" where it names none.
//
// A ref is database, schema, object — or database, object, where the engine has
// no schemas, which is SQLite's shape and MySQL's and Firebird's. So two
// elements means no schema: reading the first as one would look for a schema
// called "main" and find nothing at all in those databases.
func schemaOf(ref model.ObjectRef) string {
	if len(ref.Path) < 3 {
		return ""
	}
	return ref.Path[len(ref.Path)-2]
}

// findTable is the table a ref names, or nil.
func findTable(db *model.Database, ref model.ObjectRef) *model.Table {
	if db == nil {
		return nil
	}
	want, schema := ref.Name(), schemaOf(ref)
	for si := range db.Schemas {
		s := &db.Schemas[si]
		if schema != "" && !strings.EqualFold(s.Name, schema) {
			continue
		}
		for ti := range s.Tables {
			if strings.EqualFold(s.Tables[ti].Name, want) {
				return &s.Tables[ti]
			}
		}
	}
	return nil
}

// Columns are the columns of a table on the canvas, in the order the table
// declares them, for the designer to offer.
func Columns(db *model.Database, ref model.ObjectRef) []string {
	t := findTable(db, ref)
	if t == nil {
		// A view, or something not in the snapshot. Its columns are not
		// nothing, but they are not here; the designer asks the connection.
		return nil
	}
	out := make([]string, 0, len(t.Columns))
	for _, c := range t.Columns {
		out = append(out, c.Name)
	}
	return out
}

// pairKey names a pair of tables regardless of which way round it is given: a
// join is between two tables, and having one in each direction would be the
// same join twice.
func pairKey(a, b int) [2]int {
	if a > b {
		a, b = b, a
	}
	return [2]int{a, b}
}

// TableRefs are the tables of a database, as refs, for the designer to offer.
// Views come too where asked for: a view is a thing to select from, and a
// query over one is a query.
func TableRefs(db *model.Database, views bool) []model.ObjectRef {
	if db == nil {
		return nil
	}
	var out []model.ObjectRef
	for _, s := range db.Schemas {
		for _, t := range s.Tables {
			out = append(out, refOf(db.Name, s.Name, t.Name, model.KindTable))
		}
		if !views {
			continue
		}
		for _, v := range s.Views {
			out = append(out, refOf(db.Name, s.Name, v.Name, model.KindView))
		}
	}
	slices.SortFunc(out, func(a, b model.ObjectRef) int {
		return strings.Compare(strings.Join(a.Path, "."), strings.Join(b.Path, "."))
	})
	return out
}

// refOf addresses an object the way its own driver would: with its schema
// where the database has one, and without where the schema has no name — which
// is how a snapshot of MySQL or SQLite holds its tables.
func refOf(database, schema, name string, kind model.ObjectKind) model.ObjectRef {
	if schema == "" {
		return model.NewRef(kind, database, name)
	}
	return model.NewRef(kind, database, schema, name)
}
