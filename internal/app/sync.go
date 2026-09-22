package app

import (
	"fmt"
	"slices"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/diff"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Writing the statements that would close a difference (FR-7.3).
//
// A comparison says what differs; this says what to do about the parts of it
// somebody chose. The choosing is the requirement — a schema comparison is
// read and then argued with, and a script that takes everything or nothing
// is one nobody can use on a database they share.
//
// Nothing here runs anything. What it answers is statements, read before
// they run like every other structural change (ADR-0115).

// Selection is the differences to close, by the names the comparison gives
// them (diff.ID).
type Selection map[string]bool

// Incoherent is a selection that cannot be written as a script: something
// chosen inside an object that is not.
type Incoherent struct {
	// Needs is what would have to be chosen too, in the words somebody
	// reading the tree would recognise.
	Needs []string
}

func (e *Incoherent) Error() string {
	return "app: this selection cannot be written on its own: " + strings.Join(e.Needs, "; ")
}

// SyncScript writes the statements that would make live look like wanted,
// for the differences chosen.
//
// The order is the one a whole schema is written in (ADR-0118), for the same
// reason: a script is a sequence, and the wrong sequence fails halfway. What
// is dropped goes first, because a table on its way out may be what stops a
// table on its way in from being made.
func SyncScript(src source.Source, live, wanted *model.Database, tree diff.Node, chosen Selection) (_ []source.Statement, err error) {
	defer panics.Recover(&err, "writing a sync script")
	gen, ok := ddl(src)
	if !ok {
		return nil, ErrNoDDL
	}
	// Choosing nothing is not short-circuited: gathering nothing answers
	// nothing, and a guard for it would be a line no test could tell the
	// absence of.
	picked, err := gather(tree, chosen)
	if err != nil {
		return nil, err
	}
	return picked.render(gen, live, wanted)
}

// choice is one object a script will touch, and what to do about it.
type choice struct {
	id     string
	schema string
	kind   model.ObjectKind
	name   string
	status diff.Status
	// parts are the differences chosen inside it, where the object itself
	// was not chosen whole.
	parts []diff.Node
}

// chosenSet is every object a selection touches, in the order the comparison
// listed them.
type chosenSet struct {
	order []string
	byID  map[string]*choice
}

// gather turns a selection of nodes into the objects a script has to touch.
//
// Anything chosen inside an object is gathered onto that object, because a
// column is not altered on its own: the statement is an ALTER of the table
// it is in, and the generator writes it by comparing two tables.
func gather(tree diff.Node, chosen Selection) (*chosenSet, error) {
	out := &chosenSet{byID: map[string]*choice{}}
	var missing []string

	tree.Walk(func(id string, n diff.Node) {
		if !chosen[id] {
			return
		}
		schema, object, owner, ok := place(id)
		if !ok {
			// The database and the schemas themselves: a schema comparison
			// does not create or drop those, and choosing one means the
			// things under it.
			return
		}
		if owner == nil {
			// The object itself.
			c := out.at(object, schema)
			c.status, c.kind, c.name = n.Status, n.Kind, n.Name
			return
		}
		// Something inside an object. Its statement is an ALTER of the
		// object, so the object must be one that will be there to alter —
		// not one the same script is about to make or drop whole.
		if held, ok := out.byID[object]; ok && held.status != "" && held.status != diff.Changed {
			missing = append(missing, fmt.Sprintf("%s is chosen inside %s, which is being %s whole",
				n.Name, held.name, wordFor(held.status)))
			return
		}
		c := out.at(object, schema)
		c.parts = append(c.parts, n)
		c.kind, c.name = owner.kind, owner.name
	})

	if len(missing) > 0 {
		slices.Sort(missing)
		return nil, &Incoherent{Needs: slices.Compact(missing)}
	}
	return out, nil
}

func wordFor(st diff.Status) string {
	switch st {
	case diff.Added:
		return "made"
	case diff.Removed:
		return "dropped"
	}
	return "changed"
}

func (s *chosenSet) at(id, schema string) *choice {
	if c, held := s.byID[id]; held {
		return c
	}
	c := &choice{id: id, schema: schema}
	s.byID[id] = c
	s.order = append(s.order, id)
	return c
}

// named is an object's kind and name, read out of an id.
type named struct {
	kind model.ObjectKind
	name string
}

// place reads an id as the schema it is in, the object it names or is
// inside, and — where the id is inside an object rather than being one —
// that object's kind and name.
//
// An id is /database:x/schema:public/table:orders/column:id, so the schema
// is the second step and the object is the third. Anything deeper is a part
// of that object.
func place(id string) (schema, object string, owner *named, ok bool) {
	steps := strings.Split(strings.TrimPrefix(id, "/"), "/")
	if len(steps) < 3 {
		return "", "", nil, false
	}
	_, schema, _ = strings.Cut(steps[1], ":")
	object = "/" + strings.Join(steps[:3], "/")
	kind, name, _ := strings.Cut(steps[2], ":")
	if len(steps) == 3 {
		return schema, object, nil, true
	}
	return schema, object, &named{kind: model.ObjectKind(kind), name: name}, true
}
