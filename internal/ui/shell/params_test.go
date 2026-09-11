package shell

import (
	"fmt"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"github.com/ikigai-db/ikigai-db/internal/store"
)

// lastSession is the fake session the newest query tab runs on.
func lastSession(t *testing.T) *fakeSession {
	t.Helper()
	fakeSessions.Lock()
	defer fakeSessions.Unlock()
	if len(fakeSessions.list) == 0 {
		t.Fatal("no session opened")
	}
	return fakeSessions.list[len(fakeSessions.list)-1]
}

// ranWith is the values the fake session's last script ran with.
func ranWith(fs *fakeSession) string {
	p := fs.named.Load()
	if p == nil {
		return "nothing ran"
	}
	return fmt.Sprint(*p)
}

// askedFor runs a script and returns the Parameters panel it opens.
func askedFor(t *testing.T, fx *fixture, q *queryTab, script string) *paramsPanel {
	t.Helper()
	q.editor.Document().SetText(script)
	fx.s.run(cmdQueryRunAll)
	if !fx.s.panelIs(panelParams) {
		t.Fatal("a script with parameters should ask for them before it runs")
	}
	return fx.s.lastParams
}

func TestAScriptWithParametersAsksForThemFirst(t *testing.T) {
	fx := newFixture(t)
	_, q := openQuery(t, fx, "")
	fs := lastSession(t)
	p := askedFor(t, fx, q, "rows 1; rows :n; update :id, :n")
	if fmt.Sprint(p.names) != "[n id]" || q.executing || ranWith(fs) != "nothing ran" {
		t.Fatalf("asked for %q; running %v, ran %s", p.names, q.executing, ranWith(fs))
	}
	test.Type(p.values[0], "3")
	test.Type(p.values[1], "7")
	test.Tap(p.run)
	pump(t, fx.q, func() bool { return !q.executing })
	if got := ranWith(fs); got != "map[id:7 n:3]" {
		t.Errorf("ran with %s", got)
	}
	if fx.s.panelIs(panelParams) {
		t.Error("the panel should close as the script runs")
	}
}

func TestReturnInAValueRunsTheScript(t *testing.T) {
	fx := newFixture(t)
	_, q := openQuery(t, fx, "")
	fs := lastSession(t)
	p := askedFor(t, fx, q, "rows :n")
	test.Type(p.values[0], "4")
	p.values[0].TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	pump(t, fx.q, func() bool { return !q.executing && ranWith(fs) != "nothing ran" })
	if got := ranWith(fs); got != "map[n:4]" {
		t.Errorf("ran with %s", got)
	}
}

func TestParameterValuesComeBackForTheirConnection(t *testing.T) {
	fx := newFixture(t)
	tb, q := openQuery(t, fx, "")
	p := askedFor(t, fx, q, "rows :n")
	test.Type(p.values[0], "5")
	test.Tap(p.run)
	pump(t, fx.q, func() bool { return !q.executing })
	if p := askedFor(t, fx, q, "rows :n; update :other"); p.values[0].Text != "5" || p.values[1].Text != "" {
		t.Errorf("asked again with %q and %q; the value given comes back, a new name is empty", p.values[0].Text, p.values[1].Text)
	}
	fx.s.closePanel()
	c, err := fx.conns.Create(store.SavedConnection{Name: "db2", Driver: "postgres", Host: "db2"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t2 := fx.s.OpenQuery(c.ID)
	pump(t, fx.q, func() bool { return t2.query.session != nil })
	if p := askedFor(t, fx, t2.query, "rows :n"); p.values[0].Text != "" {
		t.Errorf("another connection is offered %q; values are kept by connection", p.values[0].Text)
	}
	_ = tb
}

func TestNullIsSentAsNull(t *testing.T) {
	fx := newFixture(t)
	_, q := openQuery(t, fx, "")
	fs := lastSession(t)
	p := askedFor(t, fx, q, "rows :n")
	test.Type(p.values[0], "ignored")
	p.nulls[0].SetChecked(true)
	if !p.values[0].Disabled() {
		t.Error("with NULL ticked, the value cannot be typed")
	}
	test.Tap(p.run)
	pump(t, fx.q, func() bool { return !q.executing })
	if got := ranWith(fs); got != "map[n:<nil>]" {
		t.Errorf("ran with %s, want n as NULL", got)
	}
	if p := askedFor(t, fx, q, "rows :n"); !p.nulls[0].Checked {
		t.Error("NULL is remembered too")
	}
}

func TestCancelRunsNothing(t *testing.T) {
	fx := newFixture(t)
	_, q := openQuery(t, fx, "")
	fs := lastSession(t)
	p := askedFor(t, fx, q, "rows :n")
	test.Tap(p.cancel)
	if fx.s.panelIs(panelParams) || q.executing || ranWith(fs) != "nothing ran" {
		t.Errorf("Cancel should close the panel and run nothing; ran %s", ranWith(fs))
	}
}

func TestAScriptWithoutParametersRunsAtOnce(t *testing.T) {
	fx := newFixture(t)
	_, q := openQuery(t, fx, "")
	fs := lastSession(t)
	q.editor.Document().SetText("rows 1; 'a :string' ")
	fx.s.run(cmdQueryRunAll)
	if fx.s.panelIs(panelParams) {
		t.Error("nothing to ask for")
	}
	pump(t, fx.q, func() bool { return !q.executing })
	if got := ranWith(fs); got != "map[]" {
		t.Errorf("ran with %s", got)
	}
}

func TestAConfirmedProductionRunKeepsItsValues(t *testing.T) {
	fx := newFixture(t)
	_, q := openQuery(t, fx, "production")
	fs := lastSession(t)
	p := askedFor(t, fx, q, "update :id")
	test.Type(p.values[0], "7")
	test.Tap(p.run)
	pump(t, fx.q, func() bool { return fx.s.win.Canvas().Overlays().Top() != nil })
	test.Tap(findButton(fx.s.win.Canvas().Overlays().Top(), "Run"))
	pump(t, fx.q, func() bool { return !q.executing })
	if got := ranWith(fs); got != "map[id:7]" {
		t.Errorf("confirmed, the script ran with %s; the values given should go with it", got)
	}
}
