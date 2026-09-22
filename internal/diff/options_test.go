package diff

import (
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// Leaving differences out (FR-7.5).
//
// A rule is applied while the comparison is made, so what is left out is not
// a difference at all. Every test here checks that twice over: that the
// object does not read as changed, and that it is not counted.

func withComment(name, comment string) model.Table {
	return model.Table{Name: name, Comment: comment, RowsEstimate: -1,
		Columns: []model.Column{col("id", "integer")}}
}

func twoDatabases(a, b model.Schema) (*model.Database, *model.Database) {
	return &model.Database{Name: "sales", Schemas: []model.Schema{a}},
		&model.Database{Name: "sales", Schemas: []model.Schema{b}}
}

// A comment on one side and not the other is the difference most often
// deliberate, and is left out when asked.
func TestLeavingCommentsOut(t *testing.T) {
	from, to := twoDatabases(
		model.Schema{Name: "public", Tables: []model.Table{withComment("people", "the old note")}},
		model.Schema{Name: "public", Tables: []model.Table{withComment("people", "a new one")}})

	if got := Compare(from, to); !got.Differs() {
		t.Error("a changed comment compared as no change at all")
	}
	got := CompareWith(from, to, Options{Comments: true})
	if got.Differs() {
		t.Errorf("it compared as %s: %s", got.Status, changedIn(got))
	}
	if c := got.Count(); c[Changed] != 0 {
		t.Errorf("it counted %v; a difference left out is not a difference", c)
	}
}

// Two servers print the same definition differently, which is what this is
// for — and it is text, so nothing decides it quietly.
func TestLeavingWhitespaceOut(t *testing.T) {
	view := func(def string) model.Schema {
		return model.Schema{Name: "public", Views: []model.View{{Name: "recent", Definition: def}}}
	}
	from, to := twoDatabases(view("SELECT id FROM people"), view("SELECT  id\n  FROM people"))

	if got := Compare(from, to); !got.Differs() {
		t.Error("two spellings compared as the same without being asked")
	}
	if got := CompareWith(from, to, Options{Whitespace: true}); got.Differs() {
		t.Errorf("it compared as %s: %s", got.Status, changedIn(got))
	}
	// A definition that really differs still does.
	from, to = twoDatabases(view("SELECT id FROM people"), view("SELECT id FROM folk"))
	if got := CompareWith(from, to, Options{Whitespace: true}); !got.Differs() {
		t.Error("a real change was read as whitespace")
	}
}

// A charset or a collation differs between two servers far more often than
// anybody means it to.
func TestLeavingCollationOut(t *testing.T) {
	from := &model.Database{Name: "sales", Charset: "UTF8", Collate: "en_GB.UTF-8",
		Schemas: []model.Schema{{Name: "public", Tables: []model.Table{
			{Name: "people", RowsEstimate: -1, Columns: []model.Column{col("id", "integer")},
				Attrs: map[string]string{"collation": "C", "fillfactor": "70"}}}}}}
	to := &model.Database{Name: "sales", Charset: "LATIN1", Collate: "C",
		Schemas: []model.Schema{{Name: "public", Tables: []model.Table{
			{Name: "people", RowsEstimate: -1, Columns: []model.Column{col("id", "integer")},
				Attrs: map[string]string{"collation": "en_GB", "fillfactor": "70"}}}}}}

	if got := Compare(from, to); !got.Differs() {
		t.Error("a changed charset compared as no change")
	}
	if got := CompareWith(from, to, Options{Collation: true}); got.Differs() {
		t.Errorf("it compared as %s: %s", got.Status, changedIn(got))
	}
	// And what is not a collation is still compared.
	to.Schemas[0].Tables[0].Attrs["fillfactor"] = "90"
	if got := CompareWith(from, to, Options{Collation: true}); !got.Differs() {
		t.Error("a property that is not a collation was left out with them")
	}
}

// A schema nobody deploys is left out entirely, and is not a schema somebody
// has dropped.
func TestLeavingASchemaOut(t *testing.T) {
	from := &model.Database{Name: "sales", Schemas: []model.Schema{
		{Name: "public", Tables: []model.Table{withComment("people", "")}},
		{Name: "audit", Tables: []model.Table{withComment("log", "")}}}}
	to := &model.Database{Name: "sales", Schemas: []model.Schema{
		{Name: "public", Tables: []model.Table{withComment("people", "")}}}}

	if got := Compare(from, to); !got.Differs() {
		t.Error("a schema on one side only compared as no change")
	}
	got := CompareWith(from, to, Options{Schemas: []string{"audit"}})
	if got.Differs() {
		t.Errorf("it compared as %s: %s", got.Status, changedIn(got))
	}
	for _, n := range got.Children {
		if n.Name == "audit" {
			t.Error("the schema left out is still in the tree, where it can be chosen")
		}
	}
}

// A name pattern leaves an object out wherever it is.
func TestLeavingNamesOut(t *testing.T) {
	from := &model.Database{Name: "sales", Schemas: []model.Schema{{Name: "public",
		Tables: []model.Table{withComment("people", ""), withComment("people_tmp", ""),
			withComment("orders_tmp", "")}}}}
	to := &model.Database{Name: "sales", Schemas: []model.Schema{{Name: "public",
		Tables: []model.Table{withComment("people", "")}}}}

	got := CompareWith(from, to, Options{Names: []string{"*_tmp"}})
	if got.Differs() {
		t.Errorf("it compared as %s: %s", got.Status, changedIn(got))
	}
	// And a name that does not match is still compared.
	from.Schemas[0].Tables = append(from.Schemas[0].Tables, withComment("kept", ""))
	if got := CompareWith(from, to, Options{Names: []string{"*_tmp"}}); !got.Differs() {
		t.Error("a name that matches no pattern was left out")
	}
}

// A rule names objects, not the parts they are made of: ignoring a table
// called audit must not ignore a column of that name in every table there is.
func TestANameRuleDoesNotReachInsideAnObject(t *testing.T) {
	from := &model.Database{Name: "sales", Schemas: []model.Schema{{Name: "public",
		Tables: []model.Table{{Name: "people", RowsEstimate: -1,
			Columns: []model.Column{col("id", "integer"), col("audit", "text")}}}}}}
	to := &model.Database{Name: "sales", Schemas: []model.Schema{{Name: "public",
		Tables: []model.Table{{Name: "people", RowsEstimate: -1,
			Columns: []model.Column{col("id", "integer")}}}}}}

	got := CompareWith(from, to, Options{Names: []string{"audit"}})
	if !got.Differs() {
		t.Error("a column was left out by a rule that names objects")
	}
}

// A pattern that is not one is refused before a comparison is made with it.
func TestAPatternThatIsNotOne(t *testing.T) {
	if err := (Options{Names: []string{"*_tmp", "orders"}}).Check(); err != nil {
		t.Errorf("a good pattern said %v", err)
	}
	err := (Options{Names: []string{"[unclosed"}}).Check()
	if err == nil || !strings.Contains(err.Error(), "[unclosed") {
		t.Errorf("it said %v", err)
	}
	// And a bad pattern that reached a comparison anyway leaves objects in
	// rather than dropping them silently.
	from := &model.Database{Name: "s", Schemas: []model.Schema{{Name: "public",
		Tables: []model.Table{withComment("people", "")}}}}
	to := &model.Database{Name: "s", Schemas: []model.Schema{{Name: "public"}}}
	if got := CompareWith(from, to, Options{Names: []string{"[unclosed"}}); !got.Differs() {
		t.Error("a pattern that will not parse dropped an object")
	}
}

// Whatever draws a comparison has to say that rules are in force, because a
// rule can hide a dropped column.
func TestSayingWhatIsLeftOut(t *testing.T) {
	if got := (Options{}).Describe(); got != "" {
		t.Errorf("no rules said %q", got)
	}
	if (Options{}).Any() {
		t.Error("no rules says something is left out")
	}
	o := Options{Schemas: []string{"audit"}, Names: []string{"*_tmp"},
		Whitespace: true, Collation: true, Comments: true}
	if !o.Any() {
		t.Error("rules say nothing is left out")
	}
	said := o.Describe()
	for _, want := range []string{"audit", "*_tmp", "whitespace", "collation", "comments"} {
		if !strings.Contains(said, want) {
			t.Errorf("it says %q, with no %q in it", said, want)
		}
	}
}

// No rules compares exactly as Compare does, so nothing changes for anybody
// who sets none.
func TestNoRulesChangesNothing(t *testing.T) {
	from, to := twoDatabases(
		model.Schema{Name: "public", Tables: []model.Table{withComment("people", "one")}},
		model.Schema{Name: "public", Tables: []model.Table{withComment("people", "two")}})
	plain, with := Compare(from, to), CompareWith(from, to, Options{})
	if plain.Status != with.Status || len(plain.Children) != len(with.Children) {
		t.Errorf("they answered %s and %s", plain.Status, with.Status)
	}
	if changedIn(plain) != changedIn(with) {
		t.Errorf("they found %q and %q", changedIn(plain), changedIn(with))
	}
}
