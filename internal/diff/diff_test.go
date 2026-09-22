package diff

import (
	"slices"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// RISK-8 names this package: a sync script is generated from what this
// answers, and a sync script that is wrong destroys data. So the tests are
// the point, and they are written against the cases where being wrong would
// be silent rather than loud.

func col(name, native string) model.Column {
	return model.Column{Name: name, Position: 1,
		Type: model.DataType{Class: model.TypeString, Native: native, Length: -1}}
}

// db is one schema with one table, as a starting point to change one thing
// about and compare.
func db(tables ...model.Table) *model.Database {
	return &model.Database{Name: "sales", Schemas: []model.Schema{{Name: "public", Tables: tables}}}
}

func people(cols ...model.Column) model.Table {
	if len(cols) == 0 {
		cols = []model.Column{col("id", "integer"), col("name", "text")}
	}
	return model.Table{Name: "people", Columns: cols, RowsEstimate: -1}
}

// find walks to a node by the names on the way down.
func find(t *testing.T, n Node, path ...string) Node {
	t.Helper()
	for _, want := range path {
		i := slices.IndexFunc(n.Children, func(c Node) bool { return c.Name == want })
		if i < 0 {
			t.Fatalf("no %q under %s %q; it holds %v", want, n.Kind, n.Name, names(n.Children))
		}
		n = n.Children[i]
	}
	return n
}

func names(ns []Node) []string {
	var out []string
	for _, n := range ns {
		out = append(out, n.Name)
	}
	return out
}

func detail(n Node, name string) (Field, bool) {
	for _, f := range n.Detail {
		if f.Name == name {
			return f, true
		}
	}
	return Field{}, false
}

// Two schemas that say the same thing differ in nothing, and say so all the
// way down rather than by holding nothing.
func TestTwoSchemasThatAgree(t *testing.T) {
	got := Compare(db(people()), db(people()))
	if got.Differs() {
		t.Errorf("identical schemas compared as %s", got.Status)
	}
	if find(t, got, "public", "people", "id").Status != Same {
		t.Error("a column that did not change is not in the tree as unchanged")
	}
	if c := got.Count(); c[Changed]+c[Added]+c[Removed] != 0 {
		t.Errorf("it counted %v", c)
	}
}

// The decision this package turns on: two snapshots cannot say which column
// became which, so a rename is a removal and an addition. Guessing would be
// the one mistake that loses data.
func TestARenameIsARemovalAndAnAddition(t *testing.T) {
	to := people(col("id", "integer"), col("full_name", "text"))
	got := find(t, Compare(db(people()), db(to)), "public", "people")
	if got.Status != Changed {
		t.Fatalf("the table compared as %s", got.Status)
	}
	if s := find(t, got, "name").Status; s != Removed {
		t.Errorf("the old name compared as %s", s)
	}
	if s := find(t, got, "full_name").Status; s != Added {
		t.Errorf("the new name compared as %s", s)
	}
}

// Order is the server's business. The same objects listed differently are
// the same objects, or rebuilding a table would read as rewriting a schema.
func TestTheOrderTwoServersListThingsInIsNotADifference(t *testing.T) {
	from := db(people(col("id", "integer"), col("name", "text")))
	to := db(people(col("name", "text"), col("id", "integer")))
	// Position is compared, so make them agree on it: what is under test is
	// the order of the list, not what each column says about itself.
	to.Schemas[0].Tables[0].Columns[0].Position = 1
	to.Schemas[0].Tables[0].Columns[1].Position = 1
	if got := Compare(from, to); got.Differs() {
		t.Errorf("a re-ordered list compared as %s: %+v", got.Status, find(t, got, "public", "people"))
	}
	// And the tree comes out in one order whichever way round it was given.
	got := find(t, Compare(from, to), "public", "people")
	if want := []string{"id", "name"}; !slices.Equal(names(got.Children), want) {
		t.Errorf("the tree is in the order %v, want %v", names(got.Children), want)
	}
}

// What is unknown is not a difference. A model read from a file has no row
// counts, and comparing a statistic against an absence would report every
// table in the database as changed.
func TestARowCountIsNotAStructuralDifference(t *testing.T) {
	from, to := db(people()), db(people())
	from.Schemas[0].Tables[0].RowsEstimate = 12000
	to.Schemas[0].Tables[0].RowsEstimate = -1
	if got := Compare(from, to); got.Differs() {
		t.Errorf("a row count compared as %s", got.Status)
	}
}

// A column with no default and a column whose default is an empty string are
// different things, and would otherwise read alike.
func TestNoDefaultIsNotTheSameAsAnEmptyDefault(t *testing.T) {
	from := people(col("id", "integer"))
	to := people(col("id", "integer"))
	to.Columns[0].HasDefault = true
	got := find(t, Compare(db(from), db(to)), "public", "people", "id")
	if got.Status != Changed {
		t.Fatalf("it compared as %s", got.Status)
	}
	if f, ok := detail(got, "default"); !ok || f.From != "" || f.To != "= " {
		t.Errorf("it says %+v", got.Detail)
	}
}

// A sequence with no minimum and one bounded at zero are different, which
// comparing the numbers alone would miss.
func TestASequenceWithNoBoundIsNotOneBoundedAtZero(t *testing.T) {
	zero := int64(0)
	from := model.Sequence{Name: "s", Start: 1, Increment: 1}
	to := model.Sequence{Name: "s", Start: 1, Increment: 1, MinValue: &zero}
	a := &model.Database{Schemas: []model.Schema{{Name: "public", Sequences: []model.Sequence{from}}}}
	b := &model.Database{Schemas: []model.Schema{{Name: "public", Sequences: []model.Sequence{to}}}}
	got := find(t, Compare(a, b), "public", "s")
	if f, ok := detail(got, "minimum"); !ok || f.From != "none" || f.To != "0" {
		t.Errorf("it says %+v", got.Detail)
	}
}

// An index turned round answers a different question, so the direction
// travels with the column rather than being dropped.
func TestAnIndexTurnedRoundIsADifferentIndex(t *testing.T) {
	ix := func(desc bool) model.Table {
		t := people()
		t.Indexes = []model.Index{{Name: "people_name_ix",
			Columns: []model.IndexColumn{{Name: "name", Descending: desc}}}}
		return t
	}
	got := find(t, Compare(db(ix(false)), db(ix(true))), "public", "people", "people_name_ix")
	if got.Status != Changed {
		t.Fatalf("it compared as %s", got.Status)
	}
	if f, _ := detail(got, "columns"); f.From != "name" || f.To != "name DESC" {
		t.Errorf("it says %+v", got.Detail)
	}
}

// A key on (a, b) is not a key on (b, a), so the order of a list of columns
// is compared and not the set.
func TestTheOrderOfAKeysColumnsIsPartOfIt(t *testing.T) {
	key := func(cols ...string) model.Table {
		t := people()
		t.PrimaryKey = &model.PrimaryKey{Name: "people_pkey", Columns: cols}
		return t
	}
	got := find(t, Compare(db(key("a", "b")), db(key("b", "a"))), "public", "people", "people_pkey")
	if got.Status != Changed {
		t.Errorf("a key turned round compared as %s", got.Status)
	}
}

// A constraint renamed is a constraint dropped and a constraint made, for
// the same reason a column renamed is: nothing here watched it happen.
func TestAKeyRenamedIsDroppedAndMade(t *testing.T) {
	key := func(name string) model.Table {
		t := people()
		t.PrimaryKey = &model.PrimaryKey{Name: name, Columns: []string{"id"}}
		return t
	}
	got := find(t, Compare(db(key("people_pkey")), db(key("people_pk"))), "public", "people")
	if s := find(t, got, "people_pkey").Status; s != Removed {
		t.Errorf("the old key compared as %s", s)
	}
	if s := find(t, got, "people_pk").Status; s != Added {
		t.Errorf("the new key compared as %s", s)
	}
}

// A table that gains a key, and one that loses one.
func TestAKeyAddedAndAKeyDropped(t *testing.T) {
	with := people()
	with.PrimaryKey = &model.PrimaryKey{Name: "people_pkey", Columns: []string{"id"}}
	added := find(t, Compare(db(people()), db(with)), "public", "people", "people_pkey")
	if added.Status != Added {
		t.Errorf("a key added compared as %s", added.Status)
	}
	removed := find(t, Compare(db(with), db(people())), "public", "people", "people_pkey")
	if removed.Status != Removed {
		t.Errorf("a key dropped compared as %s", removed.Status)
	}
	// A primary key is called one. The three kinds of constraint share a
	// namespace, and calling them all "constraint" gave two different
	// differences one name between them.
	for _, n := range []Node{added, removed} {
		if n.Kind != model.KindPrimaryKey {
			t.Errorf("a primary key is called a %s", n.Kind)
		}
	}
}

// Two routines of the same name and different arguments are two routines,
// which is how every engine that has overloads sees them.
func TestRoutinesAreToldApartByTheirArguments(t *testing.T) {
	r := func(args ...string) model.Routine {
		out := model.Routine{Name: "total", Definition: "..."}
		for _, a := range args {
			out.Parameters = append(out.Parameters, model.Parameter{Type: model.DataType{Native: a}})
		}
		return out
	}
	a := &model.Database{Schemas: []model.Schema{{Name: "public", Routines: []model.Routine{r("integer")}}}}
	b := &model.Database{Schemas: []model.Schema{{Name: "public", Routines: []model.Routine{r("integer"), r("text")}}}}
	got := find(t, Compare(a, b), "public")
	if s := find(t, got, "total(integer)").Status; s != Same {
		t.Errorf("the routine that did not change compared as %s", s)
	}
	if s := find(t, got, "total(text)").Status; s != Added {
		t.Errorf("the overload compared as %s", s)
	}
}

// A change anywhere makes everything above it changed, so a collapsed tree
// still says there is something inside worth opening.
func TestAChangeIsVisibleAllTheWayUp(t *testing.T) {
	to := people(col("id", "bigint"), col("name", "text"))
	root := Compare(db(people()), db(to))
	for _, n := range []Node{root, find(t, root, "public"), find(t, root, "public", "people")} {
		if n.Status != Changed {
			t.Errorf("%s %q compared as %s", n.Kind, n.Name, n.Status)
		}
	}
	if f, _ := detail(find(t, root, "public", "people", "id"), "type"); f.From != "integer" || f.To != "bigint" {
		t.Errorf("the column says %+v", find(t, root, "public", "people", "id").Detail)
	}
}

// An object added is one difference, not one per column: the sync script
// creates it whole, and a column of a table that does not exist is not
// something anybody can choose separately (FR-7.3).
func TestAnObjectAddedIsOneDifference(t *testing.T) {
	got := find(t, Compare(db(), db(people())), "public", "people")
	if got.Status != Added {
		t.Fatalf("it compared as %s", got.Status)
	}
	if len(got.Children) != 0 {
		t.Errorf("an added table holds %v", names(got.Children))
	}
	if len(got.Detail) != 0 {
		t.Errorf("an added table details %+v", got.Detail)
	}
}

// A materialized view is not a view, and says which it is.
func TestAViewAndAMaterializedViewAreDifferentThings(t *testing.T) {
	v := func(mat bool) model.View {
		return model.View{Name: "totals", Materialized: mat, Definition: "SELECT 1"}
	}
	a := &model.Database{Schemas: []model.Schema{{Name: "public", Views: []model.View{v(false)}}}}
	b := &model.Database{Schemas: []model.Schema{{Name: "public", Views: []model.View{v(true)}}}}
	got := find(t, Compare(a, b), "public", "totals")
	if got.Status != Changed {
		t.Fatalf("it compared as %s", got.Status)
	}
	if got.Kind != model.KindMaterializedView {
		t.Errorf("it is listed as a %s", got.Kind)
	}
	if f, ok := detail(got, "materialized"); !ok || f.From != "no" || f.To != "yes" {
		t.Errorf("it says %+v", got.Detail)
	}
}

// The engine-specific properties are compared too, and a key on one side
// only reads as a difference against nothing.
func TestEngineSpecificPropertiesAreCompared(t *testing.T) {
	from, to := people(), people()
	from.Attrs = map[string]string{"fillfactor": "70"}
	to.Attrs = map[string]string{"fillfactor": "90", "unlogged": "true"}
	got := find(t, Compare(db(from), db(to)), "public", "people")
	if f, ok := detail(got, "fillfactor"); !ok || f.From != "70" || f.To != "90" {
		t.Errorf("it says %+v", got.Detail)
	}
	if f, ok := detail(got, "unlogged"); !ok || f.From != "" || f.To != "true" {
		t.Errorf("a property on one side only says %+v", got.Detail)
	}
}

// A referential action is part of what a key does, so changing one is a
// change even though the columns are untouched.
func TestAChangedReferentialActionIsAChange(t *testing.T) {
	fk := func(a model.ReferentialAction) model.Table {
		t := people()
		t.ForeignKeys = []model.ForeignKey{{Name: "people_org_fkey", Columns: []string{"org"},
			RefSchema: "public", RefTable: "orgs", RefColumns: []string{"id"}, OnDelete: a}}
		return t
	}
	got := find(t, Compare(db(fk(model.ActionNoAction)), db(fk(model.ActionCascade))), "public", "people", "people_org_fkey")
	if f, ok := detail(got, "on delete"); !ok || f.To != "CASCADE" {
		t.Errorf("it says %+v", got.Detail)
	}
	if _, ok := detail(got, "columns"); ok {
		t.Error("the columns are reported as changed when they did not change")
	}
}

// A key pointing somewhere else is a change, even with the same name and
// the same columns.
func TestAKeyPointingSomewhereElse(t *testing.T) {
	fk := func(table string) model.Table {
		t := people()
		t.ForeignKeys = []model.ForeignKey{{Name: "k", Columns: []string{"org"},
			RefSchema: "public", RefTable: table, RefColumns: []string{"id"}}}
		return t
	}
	got := find(t, Compare(db(fk("orgs")), db(fk("teams"))), "public", "people", "k")
	if f, ok := detail(got, "references"); !ok || f.From != "public.orgs (id)" || f.To != "public.teams (id)" {
		t.Errorf("it says %+v", got.Detail)
	}
}

// An enum's values are ordered, and the order is part of the type.
func TestAnEnumsValuesAreOrdered(t *testing.T) {
	e := func(vals ...string) *model.Database {
		return &model.Database{Schemas: []model.Schema{{Name: "public",
			UserTypes: []model.UserType{{Name: "mood", Category: "enum", EnumValues: vals}}}}}
	}
	if got := find(t, Compare(e("sad", "ok"), e("ok", "sad")), "public", "mood"); got.Status != Changed {
		t.Errorf("an enum reordered compared as %s", got.Status)
	}
	if got := Compare(e("sad", "ok"), e("sad", "ok")); got.Differs() {
		t.Errorf("an enum unchanged compared as %s", got.Status)
	}
}

// Nothing compared against nothing is nothing, and one side missing is the
// whole of the other side added or removed.
func TestComparingAgainstNothing(t *testing.T) {
	if got := Compare(nil, nil); got.Differs() {
		t.Errorf("two empty models compared as %s", got.Status)
	}
	got := Compare(nil, db(people()))
	if s := find(t, got, "public").Status; s != Added {
		t.Errorf("a schema against nothing compared as %s", s)
	}
	if got.Name != "sales" {
		t.Errorf("the root is called %q", got.Name)
	}
	if s := find(t, Compare(db(people()), nil), "public").Status; s != Removed {
		t.Errorf("nothing against a schema compared as %s", s)
	}
}

// A duplicate name is a server's business. Reporting it as a difference in
// the other database would be reporting the wrong database's problem.
func TestADuplicateNameIsNotADifferenceInTheOtherDatabase(t *testing.T) {
	twice := people(col("id", "integer"), col("id", "text"))
	if got := Compare(db(twice), db(people(col("id", "integer")))); got.Differs() {
		t.Errorf("it compared as %s: %+v", got.Status, find(t, got, "public", "people"))
	}
}

// The counts are what the header line is written from.
func TestCountingWhatIsInTheTree(t *testing.T) {
	to := people(col("id", "bigint"), col("email", "text"))
	c := Compare(db(people()), db(to)).Count()
	if c[Added] != 1 || c[Removed] != 1 {
		t.Errorf("it counted %v; one column added and one removed", c)
	}
	// database, schema, table and the changed column.
	if c[Changed] != 4 {
		t.Errorf("it counted %d changed, want 4: %v", c[Changed], c)
	}
}

// Direction: from is what is there, to is what is wanted, so Added is what a
// sync script would create. Getting this round the wrong way would generate
// a script that drops what somebody meant to add.
func TestWhichWayRoundTheComparisonReads(t *testing.T) {
	got := Compare(db(), db(people()))
	if s := find(t, got, "public", "people").Status; s != Added {
		t.Errorf("a table only in the wanted model compared as %s", s)
	}
	back := Compare(db(people()), db())
	if s := find(t, back, "public", "people").Status; s != Removed {
		t.Errorf("a table only in the existing model compared as %s", s)
	}
}

// A trigger's events are ordered as the engine reports them, and everything
// about it that is text is compared as text.
func TestATriggerIsComparedByWhatItSaysAndWhenItFires(t *testing.T) {
	trg := func(timing string, events ...string) model.Table {
		t := people()
		t.Triggers = []model.Trigger{{Name: "audit", Timing: timing, Events: events,
			ForEachRow: true, Definition: "CREATE TRIGGER audit"}}
		return t
	}
	got := find(t, Compare(db(trg("BEFORE", "INSERT")), db(trg("AFTER", "INSERT", "UPDATE"))),
		"public", "people", "audit")
	if f, _ := detail(got, "timing"); f.From != "BEFORE" || f.To != "AFTER" {
		t.Errorf("it says %+v", got.Detail)
	}
	if f, _ := detail(got, "events"); f.To != "INSERT, UPDATE" {
		t.Errorf("it says %+v", got.Detail)
	}
}

// Text is compared as text, whitespace and all. Two servers print the same
// view differently and FR-7.5's ignore rules are where that is answered —
// not here, by quietly deciding two different strings are the same.
func TestTextIsComparedAsText(t *testing.T) {
	v := func(def string) *model.Database {
		return &model.Database{Schemas: []model.Schema{{Name: "public",
			Views: []model.View{{Name: "recent", Definition: def}}}}}
	}
	got := find(t, Compare(v("SELECT id FROM people"), v("SELECT  id  FROM people")), "public", "recent")
	if got.Status != Changed {
		t.Errorf("two spellings of the same view compared as %s", got.Status)
	}
}

// Everything a node knows about a change is on the node, so whatever draws
// the tree never has to go back to the models for it (FR-7.2).
func TestADifferenceCarriesBothOfItsValues(t *testing.T) {
	to := people(col("id", "integer"), col("name", "varchar(40)"))
	got := find(t, Compare(db(people()), db(to)), "public", "people", "name")
	f, ok := detail(got, "type")
	if !ok {
		t.Fatalf("it details %+v", got.Detail)
	}
	if f.From != "text" || f.To != "varchar(40)" {
		t.Errorf("it says %q became %q", f.From, f.To)
	}
	// And nothing that agreed is carried, or the tree would be mostly noise.
	for _, d := range got.Detail {
		if d.From == d.To {
			t.Errorf("it carries %q, which did not change", d.Name)
		}
	}
}

// A schema in one database and not the other, with everything under it.
func TestASchemaOnOneSideOnly(t *testing.T) {
	from := &model.Database{Name: "sales", Schemas: []model.Schema{{Name: "public"}, {Name: "archive"}}}
	to := &model.Database{Name: "sales", Schemas: []model.Schema{{Name: "public"}}}
	got := Compare(from, to)
	if s := find(t, got, "archive").Status; s != Removed {
		t.Errorf("the schema compared as %s", s)
	}
	if s := find(t, got, "public").Status; s != Same {
		t.Errorf("the schema that is in both compared as %s", s)
	}
	if !strings.EqualFold(string(got.Kind), "database") {
		t.Errorf("the root is a %s", got.Kind)
	}
}

// rich is a model with something of every kind in it, for the properties
// below to hold over rather than over one table with two columns.
func rich(suffix string) *model.Database {
	zero := int64(0)
	return &model.Database{
		Name: "sales" + suffix, Charset: "UTF8", Collate: "C", Comment: "the one" + suffix,
		Schemas: []model.Schema{{
			Name: "public", Owner: "ada" + suffix, Comment: "public" + suffix,
			Attrs: map[string]string{"replication": "1" + suffix},
			Tables: []model.Table{{
				// The suffix goes in values, never in names: a name that
				// changed would be a rename, which is a removal and an
				// addition, and this is about the properties.
				Name: "orders", Comment: "orders" + suffix, RowsEstimate: 5,
				Columns: []model.Column{
					{Name: "id", Position: 1, Identity: true,
						Type: model.DataType{Native: "integer" + suffix, Length: -1}},
					{Name: "total", Position: 2, HasDefault: true, Default: "0" + suffix,
						Type:  model.DataType{Native: "numeric(10,2)", Nullable: true, Length: -1},
						Attrs: map[string]string{"storage": "main" + suffix}},
				},
				PrimaryKey: &model.PrimaryKey{Name: "orders_pkey", Columns: []string{"id"}},
				Uniques:    []model.UniqueConstraint{{Name: "orders_ref_key", Columns: []string{"total"}}},
				Checks:     []model.CheckConstraint{{Name: "orders_positive", Expression: "total >= 0" + suffix}},
				ForeignKeys: []model.ForeignKey{{Name: "orders_who_fkey", Columns: []string{"id"},
					RefSchema: "public", RefTable: "people" + suffix, RefColumns: []string{"id"},
					OnDelete: model.ActionCascade}},
				Indexes: []model.Index{{Name: "orders_total_ix", Method: "btree" + suffix,
					Columns: []model.IndexColumn{{Name: "total", Descending: true}},
					Include: []string{"id"}, Predicate: "total > 0" + suffix}},
				Triggers: []model.Trigger{{Name: "audit", Timing: "AFTER", Events: []string{"INSERT"},
					ForEachRow: true, Definition: "CREATE TRIGGER audit" + suffix}},
				Attrs: map[string]string{"fillfactor": "70" + suffix},
			}},
			Views: []model.View{{Name: "recent", Definition: "SELECT 1" + suffix,
				Columns: []model.Column{{Name: "id", Position: 1, Type: model.DataType{Native: "integer", Length: -1}}}}},
			Routines: []model.Routine{{Name: "total", Kind: model.RoutineFunction, Language: "sql",
				Parameters: []model.Parameter{{Name: "a", Type: model.DataType{Native: "integer"}}},
				Returns:    &model.DataType{Native: "bigint" + suffix}, Definition: "SELECT 1" + suffix}},
			Sequences: []model.Sequence{{Name: "orders_id_seq", DataType: "bigint", Start: 1,
				Increment: 1, MinValue: &zero, Comment: "seq" + suffix}},
			UserTypes: []model.UserType{{Name: "mood", Category: "enum",
				EnumValues: []string{"ok", "sad" + suffix}}},
		}},
	}
}

// Whatever is in a model, comparing it with itself finds nothing. This is
// the property that catches a comparison written the wrong way round, or one
// that reads a pointer's address rather than what it points at.
func TestAnythingComparedWithItselfIsUnchanged(t *testing.T) {
	got := Compare(rich(""), rich(""))
	if got.Differs() {
		t.Fatalf("a model compared with itself as %s: %s", got.Status, changedIn(got))
	}
	c := got.Count()
	if c[Changed]+c[Added]+c[Removed] != 0 {
		t.Errorf("it counted %v", c)
	}
	// And it did look at everything, rather than finding nothing by
	// comparing nothing.
	if c[Same] < 15 {
		t.Errorf("it only compared %d things", c[Same])
	}
}

// Every property of every kind is compared. Change one thing in each and the
// count of differences is the count of things changed — nothing missed and
// nothing counted twice.
func TestEveryPropertyOfEveryKindIsCompared(t *testing.T) {
	got := Compare(rich(""), rich("x"))
	if !got.Differs() {
		t.Fatal("a model with something changed in every kind compared as the same")
	}
	for _, path := range [][]string{
		{"public"},
		{"public", "orders"},
		{"public", "orders", "id"},
		{"public", "orders", "total"},
		{"public", "orders", "orders_positive"},
		{"public", "orders", "orders_who_fkey"},
		{"public", "orders", "orders_total_ix"},
		{"public", "orders", "audit"},
		{"public", "recent"},
		{"public", "total(integer)"},
		{"public", "orders_id_seq"},
		{"public", "mood"},
	} {
		if n := find(t, got, path...); n.Status != Changed {
			t.Errorf("%v compared as %s", path, n.Status)
		}
	}
	// Nothing was added or removed: every one of those is a property that
	// changed, not a name.
	if c := got.Count(); c[Added] != 0 || c[Removed] != 0 {
		t.Errorf("it counted %v; changing a property is not adding or removing one", c)
	}
}

// Turning the comparison round turns every difference round with it, and
// changes nothing else. A comparison that was not symmetric would generate a
// sync script that does the opposite of what was asked in one direction.
func TestTurningTheComparisonRoundTurnsEveryDifferenceRound(t *testing.T) {
	there := Compare(rich(""), rich("x"))
	back := Compare(rich("x"), rich(""))
	mirror := map[Status]Status{Added: Removed, Removed: Added, Changed: Changed, Same: Same}

	// The root is skipped for its name alone: it is the target database's,
	// and the target is the other one this way round.
	if there.Status != back.Status {
		t.Errorf("the root is %s one way and %s the other", there.Status, back.Status)
	}
	var walk func(a, b Node)
	walk = func(a, b Node) {
		if a.Name != b.Name {
			t.Fatalf("the trees hold different things: %q and %q", a.Name, b.Name)
		}
		if mirror[a.Status] != b.Status {
			t.Errorf("%s %q is %s one way and %s the other", a.Kind, a.Name, a.Status, b.Status)
		}
		if len(a.Children) != len(b.Children) {
			t.Fatalf("%q holds %v one way and %v the other", a.Name, names(a.Children), names(b.Children))
		}
		for i := range a.Children {
			walk(a.Children[i], b.Children[i])
		}
	}
	if len(there.Children) != len(back.Children) {
		t.Fatalf("the roots hold %v and %v", names(there.Children), names(back.Children))
	}
	for i := range there.Children {
		walk(there.Children[i], back.Children[i])
	}

	// And each difference's two values swap, rather than one of them being
	// carried over from the wrong side.
	f, _ := detail(find(t, there, "public", "orders", "id"), "type")
	g, _ := detail(find(t, back, "public", "orders", "id"), "type")
	if f.From != g.To || f.To != g.From || f.From == f.To {
		t.Errorf("one way says %q to %q, the other %q to %q", f.From, f.To, g.From, g.To)
	}
}

// changedIn names what differs, for a failure message that says where to
// look rather than that something, somewhere, is wrong.
func changedIn(n Node) string {
	var out []string
	var walk func(Node, string)
	walk = func(n Node, path string) {
		at := strings.TrimPrefix(path+"/"+n.Name, "/")
		if n.Status != Same && len(n.Children) == 0 {
			for _, d := range n.Detail {
				out = append(out, at+" "+d.Name+": "+d.From+" -> "+d.To)
			}
			if len(n.Detail) == 0 {
				out = append(out, at+" "+string(n.Status))
			}
		}
		for _, c := range n.Children {
			walk(c, at)
		}
	}
	walk(n, "")
	return strings.Join(out, "; ")
}

// The databases' own names are not compared. Comparing dev against
// production means comparing two databases with different names, every time,
// and a difference nobody can act on at the top of every report is a
// difference people learn to scroll past.
func TestTheDatabasesOwnNamesAreNotADifference(t *testing.T) {
	from := &model.Database{Name: "sales_dev", Schemas: []model.Schema{{Name: "public"}}}
	to := &model.Database{Name: "sales", Schemas: []model.Schema{{Name: "public"}}}
	got := Compare(from, to)
	if got.Differs() {
		t.Errorf("two names compared as %s: %s", got.Status, changedIn(got))
	}
	// The root is named after the target, which is what the comparison is
	// asking to look like.
	if got.Name != "sales" {
		t.Errorf("the root is called %q", got.Name)
	}
}
