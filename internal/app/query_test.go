package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/capability"
	"github.com/ikigai-db/ikigai-db/internal/store/localdb"
)

// scriptSource runs scripts of ";"-separated statements:
//
//	rows N    returns N rows
//	slow N    returns N rows, one every millisecond
//	update    changes data (3 rows affected)
//	fail      fails
//
// Its guard is a real source.Guard, so confirmation behaves as a driver's.
type scriptSource struct {
	guard    source.Guard
	sessions atomic.Int64
	mu       sync.Mutex
	streams  []*countStream
}

func (s *scriptSource) Root(context.Context) ([]model.Node, error) { return nil, nil }
func (s *scriptSource) Children(context.Context, model.ObjectRef) ([]model.Node, error) {
	return nil, nil
}
func (s *scriptSource) Describe(context.Context, model.ObjectRef) (any, error) { return nil, nil }
func (s *scriptSource) Badge(context.Context, model.ObjectRef) (model.Badge, bool, error) {
	return model.Badge{}, false, nil
}
func (s *scriptSource) Browse(context.Context, model.ObjectRef, source.BrowseOptions) (model.RowStream, error) {
	return nil, errors.New("not here")
}
func (s *scriptSource) Capabilities() capability.Capabilities { return capability.Capabilities{} }
func (s *scriptSource) Info(context.Context) (source.ServerInfo, error) {
	return source.ServerInfo{}, nil
}
func (s *scriptSource) Ping(context.Context) error { return nil }
func (s *scriptSource) Close() error               { return nil }

func (s *scriptSource) Session(context.Context) (source.Session, error) {
	s.sessions.Add(1)
	return &scriptSession{src: s}, nil
}

func (s *scriptSource) SplitScript(script string) []source.ScriptStatement {
	var out []source.ScriptStatement
	at := 0
	for _, part := range strings.SplitAfter(script, ";") {
		lead := len(part) - len(strings.TrimLeft(part, " \n"))
		if text := strings.TrimSpace(part); text != "" {
			// Character offsets, as the contract says and PostgreSQL gives.
			chars := utf8.RuneCountInString(script[:at+lead])
			out = append(out, source.ScriptStatement{Text: text, Offset: chars})
		}
		at += len(part)
	}
	return out
}
func (s *scriptSource) QuoteIdentifier(n string) string   { return n }
func (s *scriptSource) QualifyRef(model.ObjectRef) string { return "" }
func (s *scriptSource) Placeholder(int) string            { return "?" }
func (s *scriptSource) Classify(stmt string) source.Access {
	if strings.HasPrefix(stmt, "update") {
		return source.AccessWrite
	}
	return source.AccessRead
}
func (s *scriptSource) BuildBrowse(model.ObjectRef, source.BrowseOptions) (source.Statement, error) {
	return source.Statement{}, nil
}

type scriptSession struct {
	src    *scriptSource
	closed atomic.Bool
}

func (ss *scriptSession) Handle() string { return "1" }
func (ss *scriptSession) Close() error   { ss.closed.Store(true); return nil }
func (ss *scriptSession) Query(context.Context, source.Statement) (*source.Result, error) {
	return nil, errors.New("not here")
}

func (ss *scriptSession) QueryMulti(ctx context.Context, script string, confirmed bool) (<-chan source.ScriptResult, error) {
	stmts := ss.src.SplitScript(script)
	for _, st := range stmts { // every statement is checked before any runs
		if err := ss.src.guard.Allow(ss.src.Classify(st.Text), confirmed); err != nil {
			return nil, err
		}
	}
	out := make(chan source.ScriptResult)
	go func() {
		defer close(out)
		for i, st := range stmts {
			r := source.ScriptResult{Index: i, Offset: st.Offset, Statement: st.Text}
			f := strings.Fields(st.Text)
			if at := strings.Index(st.Text, "oops"); at >= 0 {
				f = []string{"oops"} // a server error with a position
				r.Err = &source.StatementError{Message: source.Message{Text: `syntax error at or near "oops"`,
					Position: utf8.RuneCountInString(st.Text[:at]) + 1}}
			}
			switch f[0] {
			case "oops":
			case "rows", "slow":
				n, _ := strconv.Atoi(strings.TrimSuffix(f[1], ";"))
				cs := &countStream{n: n, slow: f[0] == "slow"}
				ss.src.mu.Lock()
				ss.src.streams = append(ss.src.streams, cs)
				ss.src.mu.Unlock()
				r.Result = &source.Result{Rows: cs, Affected: -1}
			case "update":
				r.Result = &source.Result{Affected: 3}
			default:
				r.Err = fmt.Errorf("syntax error at %q", st.Text)
			}
			select {
			case out <- r:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

type countStream struct {
	n, i   int
	slow   bool
	closed atomic.Bool
}

func (c *countStream) Columns() []model.ColumnDef { return []model.ColumnDef{{Name: "n"}} }
func (c *countStream) Next(ctx context.Context) (model.Row, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.i >= c.n {
		return nil, io.EOF
	}
	if c.slow {
		time.Sleep(time.Millisecond)
	}
	c.i++
	return model.Row{int64(c.i)}, nil
}
func (c *countStream) Close() error { c.closed.Store(true); return nil }

func collect(t *testing.T, ch <-chan StatementResult) []StatementResult {
	t.Helper()
	var out []StatementResult
	timeout := time.After(5 * time.Second)
	for {
		select {
		case r, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, r)
		case <-timeout:
			t.Fatal("the run never finished")
		}
	}
}

func TestRunDeliversEachStatementInOrder(t *testing.T) {
	src := &scriptSource{}
	qs, err := newQuerySession(context.Background(), src, "c1", QueryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := qs.Run(context.Background(), "rows 3; update t; fail", false)
	if err != nil {
		t.Fatal(err)
	}
	got := collect(t, ch)
	if len(got) != 3 {
		t.Fatalf("%d results", len(got))
	}
	if got[0].Rows == nil || got[1].Affected != 3 || got[1].Rows != nil || got[2].Err == nil {
		t.Errorf("results %+v", got)
	}
	if got[1].Offset != len("rows 3; ") {
		t.Errorf("offset %d: the editor maps errors back by it", got[1].Offset)
	}
	rows, err := got[0].Rows.Fetch(context.Background(), 0, 10)
	if err != nil || len(rows) != 3 {
		t.Errorf("fetched %v, %v", rows, err)
	}
}

func TestFetchWaitsForRowsAndShortMeansTheEnd(t *testing.T) {
	src := &scriptSource{}
	qs, _ := newQuerySession(context.Background(), src, "c1", QueryOptions{})
	ch, _ := qs.Run(context.Background(), "slow 300", false)
	rs := collect(t, ch)[0].Rows
	if n, _ := rs.Count(context.Background()); n != -1 {
		t.Errorf("count %d while rows are still arriving; it must be unknown", n)
	}
	page, err := rs.Fetch(context.Background(), 256, 256)
	if err != nil || len(page) != 44 {
		t.Fatalf("second page: %d rows, %v; want the 44 after the first 256", len(page), err)
	}
	select {
	case <-rs.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Done never closed")
	}
	if n, _ := rs.Count(context.Background()); n != 300 {
		t.Errorf("count %d once done", n)
	}
}

func TestResultsStopAtTheCap(t *testing.T) {
	rs := newResultSet(context.Background(), &countStream{n: 1000}, 100)
	rows, _ := rs.Fetch(context.Background(), 0, 1000)
	if len(rows) != 100 || !rs.Truncated() {
		t.Errorf("%d rows, truncated %v", len(rows), rs.Truncated())
	}
}

func TestANewRunReleasesThePreviousResults(t *testing.T) {
	src := &scriptSource{}
	qs, _ := newQuerySession(context.Background(), src, "c1", QueryOptions{})
	ch, _ := qs.Run(context.Background(), "slow 100000", false)
	collect(t, ch)
	ch, _ = qs.Run(context.Background(), "rows 1", false)
	collect(t, ch)
	deadline := time.Now().Add(2 * time.Second)
	for !src.streams[0].closed.Load() {
		if time.Now().After(deadline) {
			t.Fatal("the first run's stream still holds the connection")
		}
		time.Sleep(time.Millisecond)
	}
	if src.sessions.Load() != 1 {
		t.Errorf("%d sessions opened; one tab keeps one, so SET and temp tables survive", src.sessions.Load())
	}
}

func TestProductionWritesNeedConfirmationBeforeAnythingRuns(t *testing.T) {
	src := &scriptSource{guard: source.Guard{Environment: source.EnvProduction}}
	qs, _ := newQuerySession(context.Background(), src, "c1", QueryOptions{})
	if _, err := qs.Run(context.Background(), "rows 1; update t", false); !errors.Is(err, source.ErrConfirmationRequired) {
		t.Fatalf("err %v", err)
	}
	if len(src.streams) != 0 {
		t.Fatal("a statement ran before the script was confirmed")
	}
	ch, err := qs.Run(context.Background(), "rows 1; update t", true)
	if err != nil {
		t.Fatal(err)
	}
	if got := collect(t, ch); len(got) != 2 {
		t.Errorf("%d results after confirming", len(got))
	}
}

func TestCloseEndsTheSession(t *testing.T) {
	src := &scriptSource{}
	qs, _ := newQuerySession(context.Background(), src, "c1", QueryOptions{})
	ch, _ := qs.Run(context.Background(), "slow 100000", false)
	rs := collect(t, ch)[0].Rows
	qs.Close()
	if !qs.session.(*scriptSession).closed.Load() {
		t.Error("session left open")
	}
	if _, err := rs.Fetch(context.Background(), 99000, 10); err == nil {
		t.Error("a closed result should report why its rows stopped")
	}
}

func TestStatementAt(t *testing.T) {
	src := &scriptSource{}
	qs, _ := newQuerySession(context.Background(), src, "c1", QueryOptions{})
	script := "rows 1;\nrows 2;\n\nrows 3;"
	for _, c := range []struct {
		offset int
		want   string
	}{
		{0, "rows 1;"}, {3, "rows 1;"},
		{7, "rows 1;"},                  // just past the semicolon
		{8, "rows 2;"}, {16, "rows 2;"}, // the blank line belongs to the one before
		{17, "rows 3;"}, {len(script), "rows 3;"},
	} {
		if got, _, ok := qs.StatementAt(script, c.offset); !ok || got != c.want {
			t.Errorf("offset %d: %q", c.offset, got)
		}
	}
}

// plainSource has a query language but no sessions.
type plainSource struct{ scriptSource }

func (p *plainSource) Session() {} // shadows Sessioner with a different signature

func (p *plainSource) QueryMulti(ctx context.Context, script string, confirmed bool) (<-chan source.ScriptResult, error) {
	return (&scriptSession{src: &p.scriptSource}).QueryMulti(ctx, script, confirmed)
}
func (p *plainSource) Query(context.Context, source.Statement) (*source.Result, error) {
	return nil, errors.New("not here")
}

func TestSourcesWithoutSessionsStillRun(t *testing.T) {
	qs, err := newQuerySession(context.Background(), &plainSource{}, "c1", QueryOptions{})
	if err != nil || qs.session != nil {
		t.Fatalf("session %v, %v", qs.session, err)
	}
	ch, err := qs.Run(context.Background(), "rows 2", false)
	if err != nil || len(collect(t, ch)) != 1 {
		t.Fatalf("run: %v", err)
	}
}

func TestNoQueryLanguage(t *testing.T) {
	if _, err := newQuerySession(context.Background(), browseOnly{}, "c1", QueryOptions{}); !errors.Is(err, ErrNoQueryLanguage) {
		t.Errorf("err %v", err)
	}
}

type browseOnly struct{ source.Source }

func TestOffsetsAreBytesEvenAfterNonASCIIText(t *testing.T) {
	// The source reports character offsets; the editor positions by bytes.
	// "é" is one character and two bytes, and bytes always outnumber
	// characters, so an unconverted offset overshoots into the next statement.
	src := &scriptSource{}
	qs, _ := newQuerySession(context.Background(), src, "c1", QueryOptions{})
	script := "rows 1 éé;\nrows 2;"
	semi := strings.Index(script, ";")        // byte 11, character 9
	second := strings.Index(script, "rows 2") // byte 13, character 11
	if got, start, _ := qs.StatementAt(script, semi); got != "rows 1 éé;" || start != 0 {
		t.Errorf("caret on the first semicolon → %q at %d", got, start)
	}
	if got, start, _ := qs.StatementAt(script, second+1); got != "rows 2;" || start != second {
		t.Errorf("caret in the second statement → %q at %d, want byte %d", got, start, second)
	}
	ch, _ := qs.Run(context.Background(), script, false)
	if res := collect(t, ch); len(res) != 2 || res[1].Offset != second {
		t.Errorf("second result's offset is not byte %d: %+v", second, res)
	}
}

// memHistory is a HistoryStore that remembers in memory.
type memHistory struct {
	mu      sync.Mutex
	entries []localdb.HistoryEntry
}

func (m *memHistory) AddHistory(_ context.Context, e localdb.HistoryEntry) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = append(m.entries, e)
	return int64(len(m.entries)), nil
}

func (m *memHistory) SearchHistory(context.Context, localdb.HistoryQuery) ([]localdb.HistoryEntry, error) {
	return nil, nil
}

func (m *memHistory) wait(t *testing.T, n int) map[string]localdb.HistoryEntry {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		m.mu.Lock()
		got := append([]localdb.HistoryEntry(nil), m.entries...)
		m.mu.Unlock()
		if len(got) >= n {
			out := map[string]localdb.HistoryEntry{}
			for _, e := range got {
				out[e.Statement] = e
			}
			return out
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d history entries, want %d", len(got), n)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestEveryStatementIsRecordedWithItsOutcome(t *testing.T) {
	h := &memHistory{}
	qs, _ := newQuerySession(context.Background(), &scriptSource{}, "c1", QueryOptions{History: h, Database: "sales"})
	ch, _ := qs.Run(context.Background(), "rows 3; update t; fail", false)
	collect(t, ch)
	got := h.wait(t, 3)
	if e := got["rows 3;"]; e.Rows != 3 || e.ConnectionID != "c1" || e.Database != "sales" || e.Error != "" {
		t.Errorf("query entry %+v", e)
	}
	if e := got["update t;"]; e.Rows != 3 {
		t.Errorf("write entry %+v; rows affected is its row count", e)
	}
	if e := got["fail"]; e.Error == "" {
		t.Errorf("failure entry %+v has no error", e)
	}
}

func TestAStoppedQueryIsStillRecordedWithoutAnError(t *testing.T) {
	h := &memHistory{}
	qs, _ := newQuerySession(context.Background(), &scriptSource{}, "c1", QueryOptions{History: h})
	ch, _ := qs.Run(context.Background(), "slow 100000", false)
	collect(t, ch)
	qs.Close()
	e := h.wait(t, 1)["slow 100000"]
	if e.Rows >= 100000 || e.Error != "" {
		t.Errorf("entry %+v; a stop is the user's choice, not a failure", e)
	}
}

func TestErrorOffsetPointsAtTheTokenInTheScript(t *testing.T) {
	// The server counts characters within the statement; the result's
	// offset is bytes within the script. "éé" makes the two disagree.
	qs, _ := newQuerySession(context.Background(), &scriptSource{}, "c1", QueryOptions{})
	script := "rows 1; select éé oops;"
	ch, _ := qs.Run(context.Background(), script, false)
	res := collect(t, ch)
	if len(res) != 2 {
		t.Fatalf("%d results", len(res))
	}
	off, ok := res[1].ErrorOffset()
	if !ok || !strings.HasPrefix(script[off:], "oops") {
		t.Errorf("error offset %d (%v) points at %q, want \"oops\"", off, ok, script[min(off, len(script)):])
	}
	if _, ok := res[0].ErrorOffset(); ok {
		t.Error("a successful statement has no error offset")
	}
}
