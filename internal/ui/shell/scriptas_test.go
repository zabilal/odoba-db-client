package shell

import (
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/model"
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
