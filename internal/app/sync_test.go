package app

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/diff"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Writing a script for the differences somebody chose (FR-7.3).

func column(name, native string) model.Column {
	return model.Column{Name: name, Position: 1,
		Type: model.DataType{Class: model.TypeString, Native: native, Length: -1}}
}

// twoSides is a database as it is and as it is wanted, with a difference of
// every shape between them.
func twoSides() (live, wanted *model.Database) {
	live = &model.Database{Name: "db", Schemas: []model.Schema{{Name: "public",
		Tables: []model.Table{
			{Name: "people", RowsEstimate: -1, Columns: []model.Column{
				column("id", "integer"), column("name", "text"), column("gone", "text")}},
			{Name: "old", RowsEstimate: -1, Columns: []model.Column{column("id", "integer")}},
		},
		Views: []model.View{{Name: "recent", Definition: "SELECT 1"}},
	}}}
	wanted = &model.Database{Name: "db", Schemas: []model.Schema{{Name: "public",
		Tables: []model.Table{
			{Name: "people", RowsEstimate: -1, Columns: []model.Column{
				column("id", "integer"), column("name", "varchar(40)"), column("note", "text")}},
			{Name: "new", RowsEstimate: -1, Columns: []model.Column{column("id", "integer")}},
		},
		Views: []model.View{{Name: "recent", Definition: "SELECT 2"}},
	}}}
	return live, wanted
}

// picked is a selection of the nodes whose names are given, at whatever
// depth they are.
func picked(t *testing.T, tree diff.Node, names ...string) Selection {
	t.Helper()
	out := Selection{}
	tree.Walk(func(id string, n diff.Node) {
		if slices.Contains(names, n.Name) {
			out[id] = true
		}
	})
	if len(out) != len(names) {
		t.Fatalf("chose %d of %v", len(out), names)
	}
	return out
}

// syncSource writes down what it was asked to render, so a test reads the
// script as the changes it holds rather than as SQL grammar, which is proved
// in the driver that has to speak it.
type syncSource struct{ schemaSource }

func (*syncSource) DropObject(ref model.ObjectRef, cascade bool) ([]source.Statement, error) {
	return []source.Statement{{SQL: "DROP " + strings.ToUpper(string(ref.Kind)) + " " + ref.Name()}}, nil
}

func (*syncSource) AlterObject(ref model.ObjectRef, from, to any) ([]source.Statement, error) {
	was, okFrom := from.(*model.Table)
	now, okTo := to.(*model.Table)
	if !okFrom || !okTo {
		return nil, errors.New("syncsql: only a table is altered")
	}
	var parts []string
	for _, c := range now.Columns {
		at := slices.IndexFunc(was.Columns, func(o model.Column) bool { return o.Name == c.Name })
		switch {
		case at < 0:
			parts = append(parts, "ADD COLUMN "+c.Name)
		case was.Columns[at].Type.Native != c.Type.Native:
			parts = append(parts, "ALTER COLUMN "+c.Name+" TYPE "+c.Type.Native)
		}
	}
	for _, c := range was.Columns {
		if !slices.ContainsFunc(now.Columns, func(o model.Column) bool { return o.Name == c.Name }) {
			parts = append(parts, "DROP COLUMN "+c.Name)
		}
	}
	for _, f := range now.ForeignKeys {
		if !slices.ContainsFunc(was.ForeignKeys, func(o model.ForeignKey) bool { return o.Name == f.Name }) {
			parts = append(parts, "ADD CONSTRAINT "+f.Name)
		}
	}
	if len(parts) == 0 {
		return nil, nil
	}
	return []source.Statement{{SQL: "ALTER TABLE " + ref.Name() + " " + strings.Join(parts, ", ")}}, nil
}

func scriptOf(t *testing.T, chosen Selection) []string {
	t.Helper()
	live, wanted := twoSides()
	stmts, err := SyncScript(&syncSource{}, live, wanted, diff.Compare(live, wanted), chosen)
	if err != nil {
		t.Fatalf("writing: %v", err)
	}
	out := make([]string, len(stmts))
	for i, s := range stmts {
		out[i] = s.SQL
	}
	return out
}

// Choosing one column writes one change, and nothing about the seven other
// differences beside it.
func TestChoosingOneColumnWritesOneChange(t *testing.T) {
	live, wanted := twoSides()
	got := scriptOf(t, picked(t, diff.Compare(live, wanted), "note"))
	if len(got) != 1 {
		t.Fatalf("it wrote %d statements: %v", len(got), got)
	}
	if !strings.Contains(got[0], "ADD COLUMN note") {
		t.Errorf("it wrote %q", got[0])
	}
}

// Choosing several columns of one table writes them as one change to it,
// because that is what a statement altering a table is.
func TestChoosingSeveralColumnsOfOneTable(t *testing.T) {
	live, wanted := twoSides()
	got := scriptOf(t, picked(t, diff.Compare(live, wanted), "note", "gone"))
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, "ADD COLUMN note") || !strings.Contains(joined, "DROP COLUMN gone") {
		t.Errorf("it wrote %v", got)
	}
	// And not the column that was not chosen.
	if strings.Contains(joined, "name") {
		t.Errorf("it wrote a change to a column nobody chose: %v", got)
	}
}

// A whole object chosen is made or dropped whole.
func TestAWholeObjectChosen(t *testing.T) {
	live, wanted := twoSides()
	tree := diff.Compare(live, wanted)
	got := scriptOf(t, picked(t, tree, "new", "old"))
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, "CREATE") || !strings.Contains(joined, "DROP") {
		t.Errorf("it wrote %v", got)
	}
	// What goes out goes first: a table on its way out can be what stops
	// one on its way in from being made.
	if at, then := lineWith(got, "DROP"), lineWith(got, "CREATE"); at < 0 || then < 0 || at > then {
		t.Errorf("it drops at %d and creates at %d: %v", at, then, got)
	}
}

func lineWith(lines []string, what string) int {
	for i, l := range lines {
		if strings.Contains(l, what) {
			return i
		}
	}
	return -1
}

// A view is its own source, so a chosen view is sent whole rather than
// altered in parts.
func TestAChangedViewIsSentWhole(t *testing.T) {
	live, wanted := twoSides()
	got := scriptOf(t, picked(t, diff.Compare(live, wanted), "recent"))
	if len(got) != 1 || !strings.Contains(got[0], "CREATE") {
		t.Errorf("it wrote %v", got)
	}
}

// Choosing nothing writes nothing, rather than everything.
func TestChoosingNothingWritesNothing(t *testing.T) {
	live, wanted := twoSides()
	stmts, err := SyncScript(&syncSource{}, live, wanted, diff.Compare(live, wanted), nil)
	if err != nil || len(stmts) != 0 {
		t.Errorf("it wrote %+v, %v", stmts, err)
	}
}

// A column chosen inside a table that the same script is about to make
// whole cannot be written, and says which two.
func TestAChoiceInsideSomethingBeingMadeWholeIsRefused(t *testing.T) {
	live, wanted := twoSides()
	tree := diff.Compare(live, wanted)
	chosen := picked(t, tree, "new")
	// And a column inside it. The tree holds no such node — an object added
	// is one difference, with no children (ADR-0119) — so this is a
	// selection nothing in the window can make, and the check is what keeps
	// it that way.
	var at string
	tree.Walk(func(id string, n diff.Node) {
		if n.Name == "new" {
			at = id
		}
	})
	inside := diff.Node{Kind: model.KindColumn, Name: "id", Status: diff.Added}
	tree = withChild(tree, at, inside)
	chosen[diff.ID(at, inside)] = true

	_, err := SyncScript(&syncSource{}, live, wanted, tree, chosen)
	var bad *Incoherent
	if !errors.As(err, &bad) {
		t.Fatalf("it said %v", err)
	}
	if !strings.Contains(err.Error(), "new") || !strings.Contains(err.Error(), "made whole") {
		t.Errorf("it said %v", err)
	}
}

// A connection that cannot render says so.
func TestASyncScriptOnAConnectionThatRendersNothing(t *testing.T) {
	live, wanted := twoSides()
	tree := diff.Compare(live, wanted)
	_, err := SyncScript(&noRenderer{}, live, wanted, tree, picked(t, tree, "note"))
	if !errors.Is(err, ErrNoDDL) {
		t.Errorf("it said %v", err)
	}
}

// A constraint chosen is taken from whichever list holds it, because the
// three kinds share one namespace.
func TestAChosenConstraintIsFoundInWhicheverListHoldsIt(t *testing.T) {
	from := model.Table{Name: "t", Columns: []model.Column{column("id", "integer")},
		PrimaryKey: &model.PrimaryKey{Name: "t_pkey", Columns: []string{"id"}},
		Uniques:    []model.UniqueConstraint{{Name: "t_u", Columns: []string{"id"}}},
		Checks:     []model.CheckConstraint{{Name: "t_c", Expression: "id > 0"}}}
	to := model.Table{Name: "t", Columns: from.Columns,
		PrimaryKey: &model.PrimaryKey{Name: "t_pkey", Columns: []string{"id", "note"}},
		Uniques:    []model.UniqueConstraint{{Name: "t_u", Columns: []string{"note"}}},
		Checks:     []model.CheckConstraint{{Name: "t_c", Expression: "id > 10"}}}

	for _, c := range []struct {
		name  string
		check func(model.Table) bool
	}{
		{"t_pkey", func(g model.Table) bool { return len(g.PrimaryKey.Columns) == 2 }},
		{"t_u", func(g model.Table) bool { return g.Uniques[0].Columns[0] == "note" }},
		{"t_c", func(g model.Table) bool { return g.Checks[0].Expression == "id > 10" }},
	} {
		got := applyChosen(from, to, []diff.Node{{Kind: model.KindConstraint, Name: c.name}})
		if !c.check(got) {
			t.Errorf("choosing %s gave %+v", c.name, got)
		}
		// And only that one: the other two are still as they were.
		if c.name != "t_pkey" && len(got.PrimaryKey.Columns) != 1 {
			t.Errorf("choosing %s changed the primary key too", c.name)
		}
		if c.name != "t_c" && got.Checks[0].Expression != "id > 0" {
			t.Errorf("choosing %s changed the check too", c.name)
		}
	}
}

// An id says where an object is, and the parts of one are told from the
// object itself.
func TestReadingWhereANodeIs(t *testing.T) {
	schema, object, owner, ok := place("/database:db/schema:public/table:orders")
	if !ok || schema != "public" || object != "/database:db/schema:public/table:orders" || owner != nil {
		t.Errorf("an object read as %q, %q, %+v, %v", schema, object, owner, ok)
	}
	schema, object, owner, ok = place("/database:db/schema:public/table:orders/column:id")
	if !ok || schema != "public" || object != "/database:db/schema:public/table:orders" {
		t.Errorf("a column read as %q, %q, %v", schema, object, ok)
	}
	if owner == nil || owner.kind != model.KindTable || owner.name != "orders" {
		t.Errorf("its object read as %+v", owner)
	}
	// A schema and the database itself name no object.
	for _, id := range []string{"/database:db", "/database:db/schema:public", ""} {
		if _, _, _, ok := place(id); ok {
			t.Errorf("%q read as naming an object", id)
		}
	}
}

// A driver that falls over writing a script is contained (ADR-0017).
type syncPanicker struct{ syncSource }

func (*syncPanicker) AlterObject(model.ObjectRef, any, any) ([]source.Statement, error) {
	panic("fakesql: writing fell over")
}

func TestADriverThatFallsOverWritingASyncScriptIsContained(t *testing.T) {
	live, wanted := twoSides()
	tree := diff.Compare(live, wanted)
	_, err := SyncScript(&syncPanicker{}, live, wanted, tree, picked(t, tree, "note"))
	if err == nil || !strings.Contains(err.Error(), "writing a sync script") {
		t.Errorf("it said %v", err)
	}
}

// withChild puts a node under the one an id names, for a selection nothing
// in the window could make.
func withChild(n diff.Node, at string, kid diff.Node) diff.Node {
	var rebuild func(diff.Node, string) diff.Node
	rebuild = func(n diff.Node, parent string) diff.Node {
		id := diff.ID(parent, n)
		if id == at {
			n.Children = append(slices.Clone(n.Children), kid)
			return n
		}
		kids := make([]diff.Node, len(n.Children))
		for i, c := range n.Children {
			kids[i] = rebuild(c, id)
		}
		n.Children = kids
		return n
	}
	return rebuild(n, "")
}
