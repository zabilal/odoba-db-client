package shell

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/ikigai-db/ikigai-db/internal/store"
	"github.com/ikigai-db/ikigai-db/internal/ui/editor"
)

// across is a query tab on db1, with two more connections saved: one the
// script may name, and one marked Production.
func across(t *testing.T) (*fixture, *tab, *queryTab) {
	t.Helper()
	fx := newFixture(t)
	for name, env := range map[string]string{"staging": "", "Live Shop": "production"} {
		if _, err := fx.conns.Create(store.SavedConnection{Name: name, Driver: "postgres", Host: name, Environment: env}, nil); err != nil {
			t.Fatal(err)
		}
	}
	tb, q := openQuery(t, fx, "")
	return fx, tb, q
}

func TestAScriptRunsEachPartOnTheConnectionItNames(t *testing.T) {
	fx, tb, q := across(t)
	from := ranSoFar()
	q.editor.Document().SetText("rows 1;\n@staging\nrows 2;\nupdate t where id = 1;\n@db1\nitems;\n@STAGING\nitems;")
	fx.s.run(cmdQueryRunAll)
	pump(t, fx.q, func() bool { return !q.executing && len(q.sets) == 4 })
	// In the order written, each where it was sent.
	want := "db1: rows 1; | staging: rows 2; | staging: update t where id = 1; | db1: items; | staging: items;"
	if got := ranSince(from); got != want {
		t.Errorf("ran\n%s\nfor\n%s", got, want)
	}
	// Numbered through the script, each part under the name of where it ran.
	for _, want := range []string{"On db1:", "On staging:", "Statement 3: 3 rows affected"} {
		if !strings.Contains(q.messages.Text, want) {
			t.Errorf("the messages do not say %q:\n%s", want, q.messages.Text)
		}
	}
	if strings.Index(q.messages.Text, "On db1:") > strings.Index(q.messages.Text, "On staging:") {
		t.Errorf("the parts are said out of order:\n%s", q.messages.Text)
	}
	if got := len(q.results.Items); got != 5 || q.results.Items[4].Text != "Result 5" {
		t.Errorf("%d result tabs, the last %q", got, q.results.Items[len(q.results.Items)-1].Text)
	}
	if !strings.HasPrefix(tb.footer.Text, "Ran 5 statements") {
		t.Errorf("footer %q", tb.footer.Text)
	}
	// The tab's own rows are edited where they are known by their key; the
	// same rows from another connection are shown and not edited, since
	// the tab writes through its own.
	if q.res[2].ed == nil {
		t.Error("the tab's own result is not edited")
	}
	if q.res[3].ed != nil || !q.foreign[q.sets[3]] || q.foreign[q.sets[2]] {
		t.Error("another connection's result can be edited through this one")
	}
	// One session a connection, kept: running again makes no more.
	if len(q.others) != 1 {
		t.Fatalf("%d sessions on other connections", len(q.others))
	}
	var kept any
	for _, qs := range q.others {
		kept = qs
	}
	fx.s.run(cmdQueryRunAll)
	pump(t, fx.q, func() bool { return !q.executing && ranSoFar() == from+10 })
	for _, qs := range q.others {
		if any(qs) != kept {
			t.Error("a second run made a second session on the same connection")
		}
	}
	// What is remembered of the results is of this run's, not every run's.
	if len(q.foreign) != 2 {
		t.Errorf("%d results are remembered as another connection's, of this run's 2", len(q.foreign))
	}
	// And closing the tab closes them.
	fx.s.closeTab(tb.item)
	pump(t, fx.q, func() bool { q.othersMu.Lock(); defer q.othersMu.Unlock(); return len(q.others) == 0 })
}

func TestTheStatementAtTheCaretRunsWhereItsPartSays(t *testing.T) {
	fx, _, q := across(t)
	doc := q.editor.Document()
	doc.SetText("rows 1;\n@staging\nrows 2;\nrows 3;\n@db1\nrows 4;")
	for _, c := range []struct {
		line, col int
		want      string
	}{
		{0, 2, "db1: rows 1;"},
		{2, 2, "staging: rows 2;"},
		{3, 3, "staging: rows 3;"},
		{1, 1, "db1: rows 1;"}, // on the directive's own line: what it follows
		{5, 0, "db1: rows 4;"},
	} {
		from := ranSoFar()
		doc.SetCaret(editor.Pos{Line: c.line, Col: c.col}, false)
		fx.s.run(cmdQueryRun)
		pump(t, fx.q, func() bool { return !q.executing && ranSoFar() > from })
		if got := ranSince(from); got != c.want {
			t.Errorf("the caret at line %d ran %q, not %q", c.line+1, got, c.want)
		}
	}
	// A selection runs on the connection it is in, and on those it names.
	from := ranSoFar()
	doc.SetCaret(editor.Pos{Line: 3, Col: 0}, false)
	doc.SetCaret(editor.Pos{Line: 5, Col: 7}, true)
	fx.s.run(cmdQueryRun)
	pump(t, fx.q, func() bool { return !q.executing && ranSoFar() == from+2 })
	if got := ranSince(from); got != "staging: rows 3; | db1: rows 4;" {
		t.Errorf("the selection ran %q", got)
	}
}

func TestAConnectionNobodyHasIsRefusedBeforeAnythingRuns(t *testing.T) {
	fx, tb, q := across(t)
	if _, err := fx.conns.Create(store.SavedConnection{Name: "Staging", Driver: "postgres", Host: "twin"}, nil); err != nil {
		t.Fatal(err)
	}
	for script, want := range map[string]string{
		"rows 1;\n@nowhere\nrows 2;": "Not run: no saved connection is called “nowhere”.",
		"rows 1;\n@staging\nrows 2;": "Not run: 2 saved connections are called “staging”, and the script does not say which.",
	} {
		from := ranSoFar()
		q.editor.Document().SetText(script)
		fx.s.run(cmdQueryRunAll)
		if ranSoFar() != from || q.executing || !strings.Contains(q.messages.Text, want) || tb.footer.Text != "Not run" {
			t.Errorf("%q: ran %q, said %q, footer %q", script, ranSince(from), q.messages.Text, tb.footer.Text)
		}
	}
}

func TestAPartThatFailsStopsTheScript(t *testing.T) {
	fx, tb, q := across(t)
	from := ranSoFar()
	q.editor.Document().SetText("rows 1;\n@staging\noops;\nrows 2;\n@db1\nupdate t where id = 1;")
	fx.s.run(cmdQueryRunAll)
	pump(t, fx.q, func() bool { return !q.executing && strings.Contains(q.messages.Text, "failed") })
	if got := ranSince(from); got != "db1: rows 1; | staging: oops;" {
		t.Errorf("ran %q: what follows a failure on another connection must not run", got)
	}
	// The error is pointed at where it is in the whole script: line 3.
	for _, want := range []string{"Statement 2 failed at line 3, column 1", "The rest of the script was not run."} {
		if !strings.Contains(q.messages.Text, want) {
			t.Errorf("the messages do not say %q:\n%s", want, q.messages.Text)
		}
	}
	if !strings.HasPrefix(tb.footer.Text, "Stopped at an error") {
		t.Errorf("footer %q", tb.footer.Text)
	}
}

func TestAPartForProductionIsAskedAboutByItsName(t *testing.T) {
	fx, tb, q := across(t)
	from := ranSoFar()
	q.editor.Document().SetText("rows 1;\n@\"Live Shop\"\nupdate t where id = 1;\n@staging\nrows 2;")
	fx.s.run(cmdQueryRunAll)
	pump(t, fx.q, func() bool { return fx.s.win.Canvas().Overlays().Top() != nil })
	// The part before it has run, and the question says so; the write has not.
	if got := ranSince(from); got != "db1: rows 1;" {
		t.Fatalf("before consent, ran %q", got)
	}
	text := labelText(fx.s.win.Canvas().Overlays().Top())
	for _, want := range []string{"part of the script changes data on “Live Shop”, which is marked Production", "What came before it in the script has already run."} {
		if !strings.Contains(text, want) {
			t.Errorf("the question does not say %q: %s", want, text)
		}
	}
	// Its name is the one to type: the tab's own is not consent for it.
	typeOnTop(t, fx, "db1")
	if run := findButton(fx.s.win.Canvas().Overlays().Top(), "Run"); !run.Disabled() {
		t.Fatal("the tab's own name was taken as consent for another connection")
	}
	tapOnTop(t, fx, "Cancel")
	if ranSoFar() != from+1 || tb.footer.Text != "Not run" || !strings.Contains(q.messages.Text, "The rest of the script was not run.") {
		t.Fatalf("declined: ran %q, footer %q, said %q", ranSince(from), tb.footer.Text, q.messages.Text)
	}
	// Asked again and answered, it goes on from that part, and the consent
	// is for that part alone.
	from = ranSoFar()
	fx.s.run(cmdQueryRunAll)
	pump(t, fx.q, func() bool { return fx.s.win.Canvas().Overlays().Top() != nil })
	typeOnTop(t, fx, "Live Shop")
	test.Tap(findButton(fx.s.win.Canvas().Overlays().Top(), "Run"))
	pump(t, fx.q, func() bool { return !q.executing && ranSoFar() == from+3 })
	if got := ranSince(from); got != "db1: rows 1; | Live Shop: update t where id = 1; | staging: rows 2;" {
		t.Errorf("ran %q", got)
	}
	if !strings.Contains(q.messages.Text, "Statement 2: 3 rows affected") || !strings.Contains(q.messages.Text, "On Live Shop:") || len(q.sets) != 2 {
		t.Errorf("the results are numbered through the script, with what ran before kept:\n%s", q.messages.Text)
	}
	// What is counted is what ran since the answer.
	if !strings.HasPrefix(tb.footer.Text, "Ran 2 statements") {
		t.Errorf("footer %q", tb.footer.Text)
	}
	// Consent is for the part it was asked about: the same connection,
	// further down the script, is asked about again.
	from = ranSoFar()
	q.editor.Document().SetText("@\"Live Shop\"\nupdate t where id = 1;\n@staging\nrows 1;\n@\"Live Shop\"\nupdate t where id = 2;")
	fx.s.run(cmdQueryRunAll)
	pump(t, fx.q, func() bool { return fx.s.win.Canvas().Overlays().Top() != nil })
	typeOnTop(t, fx, "Live Shop")
	test.Tap(findButton(fx.s.win.Canvas().Overlays().Top(), "Run"))
	pump(t, fx.q, func() bool { return ranSoFar() == from+2 && fx.s.win.Canvas().Overlays().Top() != nil })
	if got := ranSince(from); got != "Live Shop: update t where id = 1; | staging: rows 1;" {
		t.Errorf("one answer was taken for two questions: ran %q", got)
	}
	tapOnTop(t, fx, "Cancel")
	// A statement that reaches every row is asked about by where it would
	// run, and is not said to be the first thing to happen when it is not.
	q.editor.Document().SetText("rows 1;\n@staging\ndelete from orders;")
	fx.s.run(cmdQueryRunAll)
	pump(t, fx.q, func() bool { return fx.s.win.Canvas().Overlays().Top() != nil })
	if text := labelText(fx.s.win.Canvas().Overlays().Top()); !strings.Contains(text, "every row in orders, on “staging”. What came before it in the script has already run.") {
		t.Errorf("%s", text)
	}
	tapOnTop(t, fx, "Cancel")
	// A script that starts on production says nothing has run.
	q.editor.Document().SetText("@\"Live Shop\"\nupdate t where id = 1;")
	fx.s.run(cmdQueryRunAll)
	pump(t, fx.q, func() bool { return fx.s.win.Canvas().Overlays().Top() != nil })
	if text := labelText(fx.s.win.Canvas().Overlays().Top()); !strings.Contains(text, "Nothing has run yet.") {
		t.Errorf("%s", text)
	}
	tapOnTop(t, fx, "Cancel")
}

func TestAConnectionThatWillNotTakeItsPartSaysSoByName(t *testing.T) {
	fx, tb, q := across(t)
	for name, c := range map[string]store.SavedConnection{
		"locked": {Driver: "postgres", Host: "locked", ReadOnly: true},
		"away":   {Driver: "postgres", Host: "down"},
	} {
		c.Name = name
		if _, err := fx.conns.Create(c, nil); err != nil {
			t.Fatal(err)
		}
	}
	// Read-only is that connection's, whatever the tab's own allows.
	from := ranSoFar()
	q.editor.Document().SetText("update t where id = 1;\n@locked\nupdate t where id = 2;\n@staging\nrows 1;")
	fx.s.run(cmdQueryRunAll)
	pump(t, fx.q, func() bool { return !q.executing && strings.Contains(q.messages.Text, "read-only") })
	if got := ranSince(from); got != "db1: update t where id = 1;" {
		t.Errorf("ran %q", got)
	}
	for _, want := range []string{"Not run on locked: this part changes data, and that connection is read-only.", "The rest of the script was not run."} {
		if !strings.Contains(q.messages.Text, want) {
			t.Errorf("the messages do not say %q:\n%s", want, q.messages.Text)
		}
	}
	if tb.footer.Text != "Not run" {
		t.Errorf("footer %q", tb.footer.Text)
	}
	// A connection that cannot be reached is said, by its name, and what
	// was for it and after it does not run.
	from = ranSoFar()
	q.editor.Document().SetText("@away\nrows 1;\n@staging\nrows 2;")
	fx.s.run(cmdQueryRunAll)
	pump(t, fx.q, func() bool { return !q.executing && strings.Contains(q.messages.Text, "Could not run on away:") })
	// (One session is kept from above: the read-only connection's.)
	if ranSoFar() != from || len(q.others) != 1 {
		t.Errorf("ran %q, with %d sessions kept", ranSince(from), len(q.others))
	}
	// First in the script, there is no rest to speak of.
	if strings.Contains(q.messages.Text, "The rest of the script") {
		t.Errorf("nothing came before it: %s", q.messages.Text)
	}
}

func TestAScriptAcrossConnectionsIsGivenItsValuesOnce(t *testing.T) {
	fx, _, q := across(t)
	from := ranSoFar()
	// The directive is not taken for a parameter, nor asked about.
	p := askedFor(t, fx, q, "rows :n;\n@staging\nrows :m;\nrows :n;")
	if got := strings.Join(p.names, ","); got != "n,m" || ranSoFar() != from {
		t.Fatalf("asked for %q, having run %q", got, ranSince(from))
	}
	test.Type(p.values[0], "3")
	test.Type(p.values[1], "4")
	test.Tap(p.run)
	pump(t, fx.q, func() bool { return !q.executing && ranSoFar() == from+3 })
	if got := ranSince(from); got != "db1: rows :n; | staging: rows :m; | staging: rows :n;" {
		t.Errorf("ran %q", got)
	}
	// Every connection is given the values.
	sessions := fakeSessionsSoFar()
	for _, fs := range sessions[len(sessions)-2:] {
		if got := ranWith(fs); got != "map[m:4 n:3]" {
			t.Errorf("a session on %s ran with %s", fs.host, got)
		}
	}
}

func TestAPlanIsOnlyOfTheTabsOwnStatements(t *testing.T) {
	fx, _, q := across(t)
	doc := q.editor.Document()
	doc.SetText("rows 1;\n@staging\nrows 2;\n@db1\nrows 3;")
	for line, want := range map[int]string{0: "rows 1;", 4: "rows 3;"} {
		doc.SetCaret(editor.Pos{Line: line, Col: 2}, false)
		if _, sql, ok := fx.s.explainTarget(); !ok || sql != want {
			t.Errorf("line %d: a plan of %q, %v; for %q", line+1, sql, ok, want)
		}
	}
	// Another connection's statement would be planned by the wrong server.
	doc.SetCaret(editor.Pos{Line: 2, Col: 2}, false)
	if _, sql, ok := fx.s.explainTarget(); ok {
		t.Errorf("a plan of %q, which is for another connection", sql)
	}
	doc.SetCaret(editor.Pos{Line: 2, Col: 0}, false)
	doc.SetCaret(editor.Pos{Line: 2, Col: 6}, true)
	if _, sql, ok := fx.s.explainTarget(); ok {
		t.Errorf("a plan of the selection %q, which is for another connection", sql)
	}
	doc.SetCaret(editor.Pos{Line: 4, Col: 0}, false)
	doc.SetCaret(editor.Pos{Line: 4, Col: 4}, true)
	if _, sql, ok := fx.s.explainTarget(); !ok || sql != "rows" {
		t.Errorf("a plan of the tab's own selection: %q, %v", sql, ok)
	}
}

// fakeSessionsSoFar is every session the fake has opened, in order.
func fakeSessionsSoFar() []*fakeSession {
	fakeSessions.Lock()
	defer fakeSessions.Unlock()
	return append([]*fakeSession(nil), fakeSessions.list...)
}
