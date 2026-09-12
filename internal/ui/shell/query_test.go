package shell

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/store"
	"github.com/ikigai-db/ikigai-db/internal/ui/editor"
	"github.com/ikigai-db/ikigai-db/internal/ui/editor/view"
)

// The fake runs scripts of ";"-separated statements: "rows N", "slow N" (a
// row a millisecond), "items" (the items table, known by its key), "joined"
// (its rows, known by nothing), "update …" (a write), "wait" (which holds the script
// until it is stopped) and anything else, which fails. Its offsets are
// characters, as the source contract says.

var (
	executed     atomic.Int64
	waiting      atomic.Int64 // how many "wait" statements have begun
	fakeSessions struct {
		sync.Mutex
		list []*fakeSession
	}
)

func (fakeSource) QuoteIdentifier(n string) string   { return `"` + n + `"` }
func (fakeSource) QualifyRef(model.ObjectRef) string { return "" }
func (fakeSource) Placeholder(int) string            { return "?" }
func (fakeSource) BuildBrowse(_ model.ObjectRef, opt source.BrowseOptions) (source.Statement, error) {
	sql := `SELECT * FROM "items"`
	if opt.Where != "" {
		sql += " WHERE (" + opt.Where + ")"
	}
	return source.Statement{SQL: sql + " LIMIT ?", Args: []any{opt.Limit}}, nil
}
func (fakeSource) Classify(stmt string) source.Access {
	switch first, _, _ := strings.Cut(strings.TrimSpace(stmt), " "); strings.ToLower(first) {
	case "update", "insert", "delete":
		return source.AccessWrite
	case "create", "alter", "drop", "truncate":
		return source.AccessDDL
	}
	return source.AccessRead
}
func (fakeSource) SplitScript(script string) []source.ScriptStatement {
	var out []source.ScriptStatement
	at := 0
	for _, part := range strings.SplitAfter(script, ";") {
		lead := len(part) - len(strings.TrimLeft(part, " \n"))
		if text := strings.TrimSpace(part); text != "" {
			out = append(out, source.ScriptStatement{Text: text, Offset: utf8.RuneCountInString(script[:at+lead])})
		}
		at += len(part)
	}
	return out
}

func (f fakeSource) Session(context.Context) (source.Session, error) {
	fs := &fakeSession{guard: f.guard}
	fakeSessions.Lock()
	fakeSessions.list = append(fakeSessions.list, fs)
	fakeSessions.Unlock()
	return fs, nil
}

type fakeSession struct {
	guard  source.Guard
	closed atomic.Bool
	named  atomic.Pointer[map[string]any] // the values the last script was run with
}

func (fs *fakeSession) Handle() string { return "1" }
func (fs *fakeSession) Close() error {
	fs.closed.Store(true)
	logClose("session")
	return nil
}

// closeLog records the order sessions and sources close in.
var closeLog struct {
	sync.Mutex
	events []string
}

func logClose(what string) {
	closeLog.Lock()
	closeLog.events = append(closeLog.events, what)
	closeLog.Unlock()
}

// Query answers "items" alone, as a result read again once its changes are
// written: ids 1 to 999, to be told from the rows first read, and more than
// a page, so that only a count says how many. It counts them; failReread
// makes them fail.
func (fs *fakeSession) Query(_ context.Context, st source.Statement) (*source.Result, error) {
	if !strings.HasPrefix(st.SQL, "items") {
		return nil, fmt.Errorf("not in this test")
	}
	rereads.Add(1)
	if failReread.Load() {
		return nil, fmt.Errorf("fakesql: the table has gone")
	}
	return &source.Result{Rows: &sliceStream{next: 1, end: 1000, keyed: true}, Affected: -1}, nil
}

func (fs *fakeSession) QueryMulti(ctx context.Context, script string, opts source.ScriptOptions) (<-chan source.ScriptResult, error) {
	confirmed := opts.Confirmed
	fs.named.Store(&opts.Named)
	stmts := fakeSource{}.SplitScript(script)
	for _, st := range stmts {
		if err := fs.guard.Allow(fakeSource{}.Classify(st.Text), confirmed); err != nil {
			return nil, err
		}
	}
	out := make(chan source.ScriptResult)
	go func() {
		defer close(out)
		for i, st := range stmts {
			executed.Add(1)
			r := source.ScriptResult{Index: i, Offset: st.Offset, Statement: st.Text}
			f := strings.Fields(strings.TrimSuffix(st.Text, ";"))
			if at := strings.Index(st.Text, "oops"); at >= 0 {
				f = []string{"oops"}
				r.Err = &source.StatementError{Message: source.Message{Text: `syntax error at or near "oops"`,
					Position: utf8.RuneCountInString(st.Text[:at]) + 1}}
			}
			switch f[0] {
			case "oops":
			case "rows", "slow":
				n, _ := strconv.Atoi(f[1])
				r.Result = &source.Result{Rows: &slowStream{n: n, slow: f[0] == "slow"}, Affected: -1}
			case "items":
				r.Result = &source.Result{Rows: itemsResult(), Affected: -1}
			case "joined":
				r.Result = &source.Result{Rows: joinedStream{&sliceStream{end: 5}}, Affected: -1}
			case "update":
				r.Result = &source.Result{Affected: 3, Duration: 2 * time.Millisecond}
			case "wait":
				waiting.Add(1)
				<-ctx.Done()
				return
			default:
				r.Err = fmt.Errorf("syntax error at or near %q", f[0])
			}
			select {
			case out <- r:
			case <-ctx.Done():
				return
			}
			if r.Err != nil {
				return
			}
		}
	}()
	return out, nil
}

type slowStream struct {
	n, i int
	slow bool
}

func (*slowStream) Columns() []model.ColumnDef { return []model.ColumnDef{{Name: "n"}} }
func (s *slowStream) Next(ctx context.Context) (model.Row, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.i >= s.n {
		return nil, io.EOF
	}
	if s.slow {
		time.Sleep(time.Millisecond)
	}
	s.i++
	return model.Row{int64(s.i)}, nil
}
func (*slowStream) Close() error { return nil }

// openQuery opens a query tab on a new connection and waits for its session.
func openQuery(t *testing.T, fx *fixture, env string) (*tab, *queryTab) {
	t.Helper()
	c, err := fx.conns.Create(store.SavedConnection{Name: "db1", Driver: "postgres", Host: "db1", Environment: env}, nil)
	if err != nil {
		t.Fatal(err)
	}
	fx.s.OpenQuery(c.ID)
	tb := fx.onlyTab(t)
	pump(t, fx.q, func() bool { return tb.query.session != nil })
	return tb, tb.query
}

func waitDone(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("result never finished reading")
	}
}

func TestRunRunsTheStatementAtTheCaret(t *testing.T) {
	fx := newFixture(t)
	tb, q := openQuery(t, fx, "")
	doc := q.editor.Document()
	doc.SetText("rows 2;\nrows 5;")
	doc.SetCaret(editor.Pos{Line: 1, Col: 2}, false)
	fx.s.run(cmdQueryRun)
	pump(t, fx.q, func() bool { return !q.executing })
	if len(q.sets) != 1 {
		t.Fatalf("%d results; messages: %q", len(q.sets), q.messages.Text)
	}
	waitDone(t, q.sets[0].Done())
	if n, _ := q.sets[0].Progress(); n != 5 {
		t.Errorf("ran the statement with %d rows, want the one at the caret (5)", n)
	}
	if !strings.HasPrefix(tb.footer.Text, "Ran 1 statement") {
		t.Errorf("footer %q", tb.footer.Text)
	}
	if q.results.Selected().Text != "Result 1" {
		t.Errorf("selected %q; the first result should come forward", q.results.Selected().Text)
	}
}

func TestRunAllShowsEveryResult(t *testing.T) {
	fx := newFixture(t)
	_, q := openQuery(t, fx, "")
	q.editor.Document().SetText("rows 1; update t; rows 3;")
	fx.s.run(cmdQueryRunAll)
	pump(t, fx.q, func() bool { return !q.executing })
	if len(q.sets) != 2 || len(q.results.Items) != 3 {
		t.Fatalf("%d result sets, %d result tabs", len(q.sets), len(q.results.Items))
	}
	if !strings.Contains(q.messages.Text, "Statement 2: 3 rows affected") {
		t.Errorf("messages %q", q.messages.Text)
	}
}

func TestSelectionRunsOnlyWhatIsSelected(t *testing.T) {
	fx := newFixture(t)
	_, q := openQuery(t, fx, "")
	doc := q.editor.Document()
	doc.SetText("rows 1; rows 4;")
	doc.SetCaret(editor.Pos{Line: 0, Col: 8}, false)
	doc.SetCaret(editor.Pos{Line: 0, Col: 15}, true)
	fx.s.run(cmdQueryRun)
	pump(t, fx.q, func() bool { return !q.executing })
	if len(q.sets) != 1 {
		t.Fatalf("%d results", len(q.sets))
	}
	waitDone(t, q.sets[0].Done())
	if n, _ := q.sets[0].Progress(); n != 4 {
		t.Errorf("%d rows; only the selection should run", n)
	}
}

func TestAFailedStatementIsReportedByNumber(t *testing.T) {
	fx := newFixture(t)
	tb, q := openQuery(t, fx, "")
	q.editor.Document().SetText("rows 1; oops;")
	fx.s.run(cmdQueryRunAll)
	pump(t, fx.q, func() bool { return !q.executing })
	if !strings.Contains(q.messages.Text, "Statement 2 failed") || q.results.SelectedIndex() != 0 {
		t.Errorf("messages %q, selected tab %d", q.messages.Text, q.results.SelectedIndex())
	}
	if !strings.HasPrefix(tb.footer.Text, "Stopped at an error") {
		t.Errorf("footer %q", tb.footer.Text)
	}
}

func TestProductionWriteAsksBeforeAnythingRuns(t *testing.T) {
	fx := newFixture(t)
	_, q := openQuery(t, fx, "production")
	q.editor.Document().SetText("update t;")
	before := executed.Load()
	fx.s.run(cmdQueryRun)
	pump(t, fx.q, func() bool { return fx.s.win.Canvas().Overlays().Top() != nil })
	if executed.Load() != before {
		t.Fatal("the write ran before it was confirmed")
	}
	run := findButton(fx.s.win.Canvas().Overlays().Top(), "Run")
	if run == nil {
		t.Fatal("the confirmation has no Run button")
	}
	test.Tap(run)
	pump(t, fx.q, func() bool { return !q.executing && executed.Load() == before+1 })
	if !strings.Contains(q.messages.Text, "3 rows affected") {
		t.Errorf("messages %q", q.messages.Text)
	}
}

func TestStopEndsRowsStillArriving(t *testing.T) {
	fx := newFixture(t)
	_, q := openQuery(t, fx, "")
	q.editor.Document().SetText("slow 100000;")
	fx.s.run(cmdQueryRun)
	pump(t, fx.q, func() bool { return len(q.sets) == 1 })
	if !fx.s.running() {
		t.Fatal("Stop must stay available while rows are still arriving")
	}
	fx.s.run(cmdQueryStop)
	waitDone(t, q.sets[0].Done())
	if n, _ := q.sets[0].Progress(); n >= 100000 {
		t.Error("Stop did not stop")
	}
	pump(t, fx.q, func() bool { return !fx.s.running() })
}

func TestClosingAQueryTabEndsItsSession(t *testing.T) {
	fx := newFixture(t)
	tb, _ := openQuery(t, fx, "")
	fakeSessions.Lock()
	fs := fakeSessions.list[len(fakeSessions.list)-1]
	fakeSessions.Unlock()
	fx.s.closeTab(tb.item)
	deadline := time.Now().Add(2 * time.Second)
	for !fs.closed.Load() {
		if time.Now().After(deadline) {
			t.Fatal("the session outlived its tab")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestMenuShortcutsLeaveEditorChordsAlone(t *testing.T) {
	// A menu shortcut is tried before the focused widget, so one that matches
	// an editing chord takes the key from the editor. ⌘↓ was Open Data until
	// the editor arrived; on macOS it is "go to the end".
	fx := newFixture(t)
	reserved := map[string]bool{}
	for _, sc := range view.Reserved() {
		reserved[sc.ShortcutName()] = true
	}
	for id, it := range fx.s.menuItems {
		if it.Shortcut != nil && reserved[it.Shortcut.ShortcutName()] {
			t.Errorf("%s is bound to %s, which the editor needs", id, it.Shortcut.ShortcutName())
		}
	}
}

func TestRowsKeepArrivingAfterTheScriptEnds(t *testing.T) {
	// A one-statement script ends as soon as its result starts streaming.
	// Cancelling the run then also stopped the reading, so a large SELECT
	// showed its first rows and "Stopped". 300 rows at a millisecond each
	// outlive the script's end by far.
	fx := newFixture(t)
	_, q := openQuery(t, fx, "")
	q.editor.Document().SetText("slow 300;")
	fx.s.run(cmdQueryRun)
	pump(t, fx.q, func() bool { return !q.executing && len(q.sets) == 1 })
	waitDone(t, q.sets[0].Done())
	if n, _ := q.sets[0].Progress(); n != 300 || q.sets[0].Err() != nil {
		t.Errorf("%d rows, err %v; the script ending must not stop its rows", n, q.sets[0].Err())
	}
}

func TestAServerErrorPointsAtItsToken(t *testing.T) {
	// Run only the second line, so the error's position has to be carried
	// through the statement's offset in the script AND the script's offset in
	// the editor, across non-ASCII text that makes characters and bytes differ.
	fx := newFixture(t)
	_, q := openQuery(t, fx, "")
	doc := q.editor.Document()
	doc.SetText("rows 1;\nselect éé oops;")
	doc.SetCaret(editor.Pos{Line: 1, Col: 3}, false)
	fx.s.run(cmdQueryRun)
	pump(t, fx.q, func() bool { return !q.executing })
	want := editor.Pos{Line: 1, Col: strings.Index("select éé oops;", "oops")}
	if c := doc.Caret(); c != want {
		t.Errorf("caret %v, want %v at the rejected token", c, want)
	}
	if !strings.Contains(q.messages.Text, "failed at line 2, column 11") {
		t.Errorf("messages %q", q.messages.Text)
	}
}

func TestQuittingClosesQuerySessionsBeforeTheirConnections(t *testing.T) {
	// A pool will not close while a pinned session holds one of its
	// connections. PostgreSQL's pool waited forever, so quitting with a
	// query tab open hung. The fake has no pool, so the order is the test.
	fx := newFixture(t)
	openQuery(t, fx, "")
	closeLog.Lock()
	closeLog.events = nil
	closeLog.Unlock()
	fx.s.shutdown()
	closeLog.Lock()
	events := append([]string(nil), closeLog.events...)
	closeLog.Unlock()
	session, source := -1, -1
	for i, e := range events {
		if e == "session" && session < 0 {
			session = i
		}
		if e == "source" && source < 0 {
			source = i
		}
	}
	if session < 0 || source < 0 || session > source {
		t.Errorf("close order %v; the session must close before its connection", events)
	}
}

// typeKey sends one key to the editor, as the window would.
func typeKey(q *queryTab, name fyne.KeyName) {
	q.editor.Focusable().(interface{ TypedKey(*fyne.KeyEvent) }).TypedKey(&fyne.KeyEvent{Name: name})
}

func TestQueryTabCompletesAsYouType(t *testing.T) {
	fx := newFixture(t)
	_, q := openQuery(t, fx, "")
	q.editor.Focus()
	test.Type(q.editor.Focusable(), "sel")
	if !q.editor.CompletionOpen() {
		t.Fatal("nothing offered while typing a keyword")
	}
	fx.s.run(cmdQueryComplete)
	typeKey(q, fyne.KeyReturn)
	if got := q.editor.Document().Text(); got != "select" {
		t.Errorf("text %q, want the keyword written in", got)
	}
}

func TestQueryTabCompletionIsAskedForOnlyInTheEditor(t *testing.T) {
	fx := newFixture(t)
	_, q := openQuery(t, fx, "")
	q.editor.Focus()
	if !fx.s.canComplete() {
		t.Error("completion is not offered with the caret in the editor")
	}
	q.editor.Document().SetText("select 1")
	fx.s.win.Canvas().Unfocus()
	if fx.s.canComplete() {
		t.Error("completion is offered with the focus outside the editor")
	}
	// Asking for it anyway does nothing rather than panicking.
	fx.s.run(cmdQueryComplete)
	if q.editor.CompletionOpen() {
		t.Error("the popup opened with the focus outside the editor")
	}
}

// completed asks the editor's completer until a candidate appears, as the
// popup does when the schema cache says it has loaded something.
func completed(t *testing.T, fx *fixture, q *queryTab, marked string) []string {
	t.Helper()
	i := strings.IndexByte(marked, '|')
	if i < 0 {
		t.Fatalf("no cursor in %q", marked)
	}
	text, cursor := marked[:i]+marked[i+1:], utf8.RuneCountInString(marked[:i])
	deadline := time.Now().Add(3 * time.Second)
	for {
		res := q.complete(text, cursor)
		var out []string
		for _, c := range res.Candidates {
			out = append(out, c.Label)
		}
		if len(out) > 0 || time.Now().After(deadline) {
			return out
		}
		fx.q.Flush()
		time.Sleep(time.Millisecond)
	}
}

func TestQueryTabCompletesTheConnectionsTables(t *testing.T) {
	fx := newFixture(t)
	_, q := openQuery(t, fx, "")
	if got := completed(t, fx, q, "select * from it|"); !has(got, "items") {
		t.Errorf("offering %v, want the connection's table", got)
	}
	// And its columns, by the name the statement reads it under.
	if got := completed(t, fx, q, "select i.| from items i"); !has(got, "id") || !has(got, "name") {
		t.Errorf("offering %v, want the table's columns", got)
	}
}

func TestQueryTabForgetsTheSchemaAfterDDL(t *testing.T) {
	fx := newFixture(t)
	_, q := openQuery(t, fx, "")
	completed(t, fx, q, "select * from it|")
	if q.schema == nil {
		t.Fatal("the tab has no schema cache")
	}
	// A read leaves what completion knows alone.
	q.editor.Document().SetText("rows 1;")
	fx.s.run(cmdQueryRun)
	pump(t, fx.q, func() bool { return !q.executing })
	if got := q.complete("select * from it", len("select * from it")); len(got.Candidates) == 0 {
		t.Error("a read emptied what completion knows")
	}
	// DDL empties it, and it loads again.
	q.editor.Document().SetText("create table t (id int);")
	fx.s.run(cmdQueryRun)
	pump(t, fx.q, func() bool { return !q.executing })
	if got := q.complete("select * from it", len("select * from it")); len(got.Candidates) != 0 {
		t.Errorf("offering %v straight after DDL, want it emptied", got.Candidates)
	}
	if got := completed(t, fx, q, "select * from it|"); !has(got, "items") {
		t.Errorf("offering %v, want the tables read again", got)
	}
}
