package shell

import (
	"context"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer/view"
)

// newestQuery waits for a query tab beyond the first n and returns its text.
func newestQuery(t *testing.T, fx *fixture, n int) string {
	t.Helper()
	pump(t, fx.q, func() bool { return len(fx.s.open) > n })
	tb := fx.s.open[len(fx.s.open)-1]
	if tb.query == nil {
		t.Fatalf("the newest tab %q is not a query", tb.item.Text)
	}
	if !tb.query.dirty {
		t.Error("a script is unsaved text, and should say so")
	}
	return tb.query.editor.Document().Text()
}

func TestScriptAsOpensTheStatementInAQueryTab(t *testing.T) {
	fx := newFixture(t)
	selectItems(t, fx)
	fx.s.run(cmdScriptSelect)
	if got := newestQuery(t, fx, 0); !strings.HasPrefix(got, "SELECT ") || !strings.Contains(got, "name") || !strings.Contains(got, "id") {
		t.Errorf("SELECT script %q", got)
	}
	fx.s.run(cmdScriptInsert)
	got := newestQuery(t, fx, 1)
	if !strings.HasPrefix(got, "INSERT INTO ") || !strings.Contains(got, "name") {
		t.Errorf("INSERT script %q", got)
	}
	if head, _, _ := strings.Cut(got, "VALUES"); strings.Contains(head, "id") {
		t.Errorf("INSERT script %q gives the identity column a value", got)
	}
	fx.s.run(cmdScriptUpdate)
	if got := newestQuery(t, fx, 2); !strings.HasPrefix(got, "UPDATE ") || !strings.Contains(got, "WHERE") {
		t.Errorf("UPDATE script %q", got)
	}
}

func TestOnlyATableIsScriptedAsInsertOrUpdate(t *testing.T) {
	fx := newFixture(t)
	c := selectItems(t, fx)
	if fx.s.menuItems[cmdScriptInsert].Disabled || fx.s.menuItems[cmdScriptUpdate].Disabled {
		t.Error("a table can be scripted as INSERT and UPDATE")
	}
	fx.s.Explorer.Tree.Select(view.NodeID(c.ID, model.NewRef(model.KindDatabase, "main")))
	fx.s.sync()
	for _, id := range []string{cmdScriptSelect, cmdScriptInsert, cmdScriptUpdate} {
		if !fx.s.menuItems[id].Disabled {
			t.Errorf("%s should be disabled for a database", id)
		}
	}
}

func TestAScriptThatCannotBeWrittenIsSaid(t *testing.T) {
	fx := newFixture(t)
	c := fx.create(t, "db1", nil)
	fx.s.scriptAs(c.ID, model.Node{Ref: model.NewRef(model.KindTable, "main", "boom"), Label: "boom", Browsable: true}, app.ScriptSelect)
	// The describe panicked, so the band ends on the crash and its offer to
	// disconnect, which names what fell over, as for a structure tab.
	pump(t, fx.q, func() bool { return strings.Contains(fx.s.errors.text, "describing fell over") })
	if findButton(fx.s.errors.slot, "Disconnect") == nil {
		t.Error("a driver that panicked should be offered for disconnecting")
	}
	if len(fx.s.open) != 0 {
		t.Error("no tab should open for a script that could not be written")
	}
}

// Writing DDL, for one object or a whole schema (FR-6.7).

func TestScriptAsCreateOpensTheDDLAndRunsNothing(t *testing.T) {
	forgetStatements()
	fx := newFixture(t)
	selectItems(t, fx)
	if fx.s.menuItems[cmdScriptCreate].Disabled {
		t.Fatal("a table cannot be scripted as CREATE")
	}
	fx.s.run(cmdScriptCreate)
	got := newestQuery(t, fx, 0)
	if !strings.Contains(got, "CREATE") {
		t.Errorf("the CREATE script is %q", got)
	}
	if n := len(ranStatements()); n != 0 {
		t.Errorf("%d statements ran; a script is written, never run", n)
	}
}

// The kinds that have DDL to write are the ones offered it, and a column,
// which is part of a table rather than an object of its own, is not.
func TestWhichObjectsAreOfferedACreateScript(t *testing.T) {
	for kind, want := range map[model.ObjectKind]bool{
		model.KindTable:            true,
		model.KindView:             true,
		model.KindMaterializedView: true,
		model.KindRoutine:          true,
		model.KindTrigger:          true,
		model.KindSequence:         true,
		model.KindColumn:           false,
		model.KindDatabase:         false,
	} {
		if got := createKinds[kind]; got != want {
			t.Errorf("%s has a CREATE to write: %v, want %v", kind, got, want)
		}
	}
	// A whole schema is asked for on the node its objects are listed under,
	// which on MySQL and SQLite is the database itself.
	if !holdsAClass[model.KindSchema] || !holdsAClass[model.KindDatabase] {
		t.Error("a whole schema cannot be asked for where its objects are listed")
	}
	if holdsAClass[model.KindTable] {
		t.Error("a table is offered a whole-schema script")
	}
}

// A schema script says why it is in the order it is in, because the order is
// the only part of it somebody cannot see for themselves.
func TestAWholeSchemaScriptSaysWhyItIsInThatOrder(t *testing.T) {
	forgetStatements()
	fx := newFixture(t)
	c := selectItems(t, fx)
	fx.s.writeDDL(c.ID, "public", func(context.Context, source.Source) ([]source.Statement, error) {
		return []source.Statement{
			{SQL: "CREATE TABLE people (id integer)"},
			{SQL: "ALTER TABLE people ADD CONSTRAINT people_who_fkey FOREIGN KEY (id) REFERENCES orders (id)"},
		}, nil
	}, schemaHeader("public"))

	got := newestQuery(t, fx, 0)
	if !strings.HasPrefix(got, "-- public, in an order this can be run in.") {
		t.Errorf("the script opens %q", got)
	}
	if !strings.Contains(got, "refer\n-- to each other") {
		t.Errorf("it does not say why the keys are last: %q", got)
	}
	if at := strings.Index(got, "CREATE TABLE"); at < 0 || at > strings.Index(got, "ADD CONSTRAINT") {
		t.Errorf("the statements are not in the order they were given: %q", got)
	}
	if !strings.Contains(got, "Nothing here has run") {
		t.Errorf("it does not say that nothing has run: %q", got)
	}
	if n := len(ranStatements()); n != 0 {
		t.Errorf("%d statements ran; a script is written, never run", n)
	}
}

// A schema with nothing in it is said, rather than opening an empty tab.
func TestAnEmptySchemaSaysSoRatherThanOpeningATab(t *testing.T) {
	fx := newFixture(t)
	c := selectItems(t, fx)
	before := len(fx.s.open)
	fx.s.writeDDL(c.ID, "empty", func(context.Context, source.Source) ([]source.Statement, error) {
		return nil, nil
	}, schemaHeader("empty"))
	pump(t, fx.q, func() bool { return strings.Contains(fx.s.status.Text, "nothing in empty") })
	if got := len(fx.s.open); got != before {
		t.Errorf("%d tabs are open, was %d", got, before)
	}
}
