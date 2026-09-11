package app

import (
	"context"
	"errors"
	"io"
	"sort"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/sqlscript"
	"github.com/ikigai-db/ikigai-db/internal/sqllex"
	"github.com/ikigai-db/ikigai-db/internal/store/localdb"
)

// ErrNoQueryLanguage is returned for a source that cannot run text queries,
// such as Kafka. The UI offers no editor for those (capability.Query).
var ErrNoQueryLanguage = errors.New("this connection has no query language")

// MaxResultRows caps the rows kept from one result. Past it the result is
// marked truncated instead of growing memory without bound (NFR-P11);
// browsing the table, which pages, is how to see the rest.
const MaxResultRows = 100_000

// HistoryStore keeps what has been run (FR-5.8). *localdb.DB is one.
type HistoryStore interface {
	AddHistory(ctx context.Context, e localdb.HistoryEntry) (int64, error)
	SearchHistory(ctx context.Context, q localdb.HistoryQuery) ([]localdb.HistoryEntry, error)
}

// historyTimeout bounds one history write. It runs off the UI goroutine and
// never under the run's context: stopping a query must not stop it being
// remembered.
const historyTimeout = 5 * time.Second

// QueryOptions configures a query session. The zero value records nothing.
type QueryOptions struct {
	History  HistoryStore
	Database string // recorded with each statement
}

// QuerySession runs scripts for one editor tab (FR-5.3–FR-5.6). Where the
// source has sessions it pins one, so SET, temporary tables and an open
// transaction survive from one run to the next (source.Sessioner).
type QuerySession struct {
	q       source.Queryer
	session source.Session // nil when the source has no sessions
	dialect source.Dialect // nil when the source cannot split scripts
	hist    HistoryStore
	entry   localdb.HistoryEntry // what every history entry shares

	mu      sync.Mutex
	results []*ResultSet // the current run's, closed when the next starts
	closed  bool
}

// NewQuerySession prepares a live connection for running scripts.
func NewQuerySession(ctx context.Context, live *Live, opt QueryOptions) (*QuerySession, error) {
	return newQuerySession(ctx, live.Source, live.ID, opt)
}

func newQuerySession(ctx context.Context, src source.Source, connID string, opt QueryOptions) (_ *QuerySession, err error) {
	defer panics.Recover(&err, "opening a session")
	qs := &QuerySession{}
	switch s := src.(type) {
	case source.Sessioner:
		sess, err := s.Session(ctx)
		if err != nil {
			return nil, err
		}
		qs.session, qs.q = sess, sess
	case source.Queryer:
		qs.q = s
	default:
		return nil, ErrNoQueryLanguage
	}
	qs.dialect, _ = src.(source.Dialect)
	qs.hist = opt.History
	qs.entry = localdb.HistoryEntry{ConnectionID: connID, Database: opt.Database,
		Language: src.Capabilities().Query.Language}
	return qs, nil
}

// StatementResult is the outcome of one statement of a script.
type StatementResult struct {
	Index int
	// Offset is the byte offset in the script where the statement starts,
	// for mapping its errors back to the editor (FR-5.10).
	Offset    int
	Statement string
	Rows      *ResultSet // nil for a statement that returns no rows
	Affected  int64      // -1 when the engine does not say
	Duration  time.Duration
	Messages  []source.Message
	Err       error
}

// ErrorOffset is where in the script a failed statement's error points, as a
// byte offset, when the server said (FR-5.10). Servers give a 1-based
// character position within the statement.
func (r StatementResult) ErrorOffset() (int, bool) {
	var se *source.StatementError
	if !errors.As(r.Err, &se) || se.Message.Position <= 0 {
		return 0, false
	}
	return r.Offset + byteOffset(r.Statement, se.Message.Position-1), true
}

// Run executes a script, delivering each statement's result as it completes;
// the channel closes when the script ends. It first closes the previous run's
// results: a session holds one open result at a time.
//
// ctx governs the rows as well as the statements: each result keeps reading
// after the script has finished, until ctx is cancelled or the next Run.
//
// opts.Confirmed is the user's consent to change data on a production
// connection. Without it such a script is refused before any statement runs,
// with source.ErrConfirmationRequired, so the caller can ask and run it
// again. opts.Named are the values of its named parameters (Params).
func (qs *QuerySession) Run(ctx context.Context, script string, opts source.ScriptOptions) (_ <-chan StatementResult, err error) {
	defer panics.Recover(&err, "running the script")
	qs.closeResults()
	in, err := qs.q.QueryMulti(ctx, script, opts)
	if err != nil {
		return nil, err
	}
	out := make(chan StatementResult, 1)
	go func() {
		defer close(out)
		defer panics.Catch("handing on results", func(err error) {
			select {
			case out <- StatementResult{Err: err, Affected: -1}:
			case <-ctx.Done():
			}
		})
		for r := range in {
			arrived := time.Now()
			sr := StatementResult{Index: r.Index, Offset: byteOffset(script, r.Offset),
				Statement: r.Statement, Err: r.Err, Affected: -1}
			if res := r.Result; res != nil {
				sr.Affected, sr.Duration, sr.Messages = res.Affected, res.Duration, res.Messages
				if res.Rows != nil {
					sr.Rows = newResultSet(ctx, res.Rows, MaxResultRows)
					qs.track(sr.Rows)
				}
			}
			qs.record(sr, arrived)
			select {
			case out <- sr:
			case <-ctx.Done():
				// Nobody is listening any more. Keep draining so the driver can
				// finish, and release whatever each result holds.
				if sr.Rows != nil {
					sr.Rows.Close()
				}
			}
		}
	}()
	return out, nil
}

// record writes a statement to history once its outcome is known: at once
// for a failure or a write, and for a query once its rows are in, so the
// entry carries the real row count. Best effort: a history line that fails to
// save must not disturb the run.
func (qs *QuerySession) record(sr StatementResult, arrived time.Time) {
	if qs.hist == nil {
		return
	}
	e := qs.entry
	e.Statement, e.Duration, e.Rows = sr.Statement, sr.Duration, sr.Affected
	e.StartedAt = arrived.Add(-sr.Duration)
	if sr.Err != nil {
		e.Error = sr.Err.Error()
	}
	rs := sr.Rows
	go func() {
		if rs != nil {
			<-rs.Done()
			n, _ := rs.Progress()
			e.Rows = int64(n)
			if err := rs.Err(); err != nil && !errors.Is(err, context.Canceled) {
				e.Error = err.Error() // stopping is the user's choice, not a failure
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), historyTimeout)
		defer cancel()
		_, _ = qs.hist.AddHistory(ctx, e)
	}()
}

// Params are a script's named parameters, :name, in the order each is first
// used: what to ask for before it runs (FR-5.7). A name inside a string or a
// comment, or a PostgreSQL cast, is none. History keeps the script as it was
// written, so the values asked for are never recorded.
func (qs *QuerySession) Params(script string) []string {
	return sqlscript.Names(sqllex.DialectFor(qs.entry.Language), script)
}

// StatementAt is the statement of a script that contains a byte offset: what
// ⌘↵ runs when nothing is selected. Between statements it is the one before,
// so a caret just past a semicolon runs what the semicolon ended. start is a
// byte offset too. ok is false when the source cannot split scripts, or the
// script has no statements.
func (qs *QuerySession) StatementAt(script string, offset int) (text string, start int, ok bool) {
	defer panics.Catch("splitting the script", func(error) { text, start, ok = "", 0, false })
	if qs.dialect == nil {
		return "", 0, false
	}
	stmts := qs.dialect.SplitScript(script)
	if len(stmts) == 0 {
		return "", 0, false
	}
	at := utf8.RuneCountInString(script[:min(max(offset, 0), len(script))])
	i := sort.Search(len(stmts), func(i int) bool { return stmts[i].Offset > at }) - 1
	if i < 0 {
		i = 0
	}
	return stmts[i].Text, byteOffset(script, stmts[i].Offset), true
}

// byteOffset converts a character offset, which is what sources report
// (source.ScriptStatement), to a byte offset into s, which is what the editor
// positions by. Mixing the two picks the wrong statement as soon as anything
// before the caret is not ASCII.
func byteOffset(s string, chars int) int {
	for i := range s {
		if chars == 0 {
			return i
		}
		chars--
	}
	return len(s)
}

// Close ends the session and releases every result. Closing twice is
// harmless, and must be: closing a tab and quitting can both reach it, and
// releasing a pooled connection twice is not.
func (qs *QuerySession) Close() (err error) {
	defer panics.Recover(&err, "closing the session")
	qs.mu.Lock()
	if qs.closed {
		qs.mu.Unlock()
		return nil
	}
	qs.closed = true
	old := qs.results
	qs.results = nil
	qs.mu.Unlock()
	for _, rs := range old {
		rs.Close()
	}
	if qs.session != nil {
		return qs.session.Close()
	}
	return nil
}

func (qs *QuerySession) track(rs *ResultSet) {
	qs.mu.Lock()
	defer qs.mu.Unlock()
	if qs.closed {
		rs.Close()
		return
	}
	qs.results = append(qs.results, rs)
}

func (qs *QuerySession) closeResults() {
	qs.mu.Lock()
	old := qs.results
	qs.results = nil
	qs.mu.Unlock()
	for _, rs := range old {
		rs.Close()
	}
}

// notifyEvery is how many rows arrive between wake-ups of waiting fetches:
// one grid page, so a fetch wakes once per page rather than once per row.
const notifyEvery = 256

// ResultSet keeps a query result's rows as they arrive, for the grid. It
// drains the stream on a goroutine of its own, so the connection is free as
// soon as the rows are in: a later statement cannot be held up by a grid
// nobody has scrolled.
//
// It serves the grid's Fetcher contract. Fetch waits until the rows it asks
// for have arrived, so a short page reliably means the end of the data.
type ResultSet struct {
	cols   []model.ColumnDef
	stream model.RowStream
	cancel context.CancelFunc

	mu        sync.Mutex
	rows      []model.Row
	done      bool
	truncated bool
	err       error
	changed   chan struct{} // closed and replaced whenever rows arrive or reading ends
	finished  chan struct{} // closed once, when reading ends
}

func newResultSet(ctx context.Context, rs model.RowStream, max int) *ResultSet {
	ctx, cancel := context.WithCancel(ctx)
	r := &ResultSet{cols: rs.Columns(), stream: rs, cancel: cancel,
		changed: make(chan struct{}), finished: make(chan struct{})}
	go r.read(ctx, max)
	return r
}

func (r *ResultSet) read(ctx context.Context, max int) {
	defer panics.Catch("reading rows", r.fail) // outermost: it catches Close too
	defer r.stream.Close()
	batch := 0
	for {
		row, err := r.stream.Next(ctx)
		r.mu.Lock()
		switch {
		case errors.Is(err, io.EOF):
			r.done = true
		case err != nil:
			r.err, r.done = err, true
		default:
			r.rows = append(r.rows, row)
			batch++
			if len(r.rows) >= max {
				r.done, r.truncated = true, true
			}
		}
		finished := r.done
		if finished || batch >= notifyEvery {
			close(r.changed)
			r.changed = make(chan struct{})
			batch = 0
		}
		if finished {
			close(r.finished)
		}
		r.mu.Unlock()
		if finished {
			return
		}
	}
}

// Done is closed when reading ends, whether at the end of the rows, at the
// cap, on an error or on Close.
func (r *ResultSet) Done() <-chan struct{} { return r.finished }

// Columns describes every row.
func (r *ResultSet) Columns() []model.ColumnDef { return r.cols }

// Identity is how the result's rows are told apart, to be edited (FR-4.8,
// ADR-0035): a table's key, where the source found every column in that one
// table and its key among them; otherwise none.
func (r *ResultSet) Identity() model.RowIdentity {
	if id, ok := r.stream.(model.Identified); ok {
		return id.Identity()
	}
	return model.RowIdentity{Kind: model.IdentityNone}
}

// Fetch returns up to limit rows from offset, waiting for them to arrive.
// Fewer than limit means the result ended there.
func (r *ResultSet) Fetch(ctx context.Context, offset, limit int64) ([]model.Row, error) {
	for {
		r.mu.Lock()
		have := int64(len(r.rows))
		if have >= offset+limit || r.done {
			defer r.mu.Unlock()
			if r.err != nil && have < offset+limit {
				return nil, r.err
			}
			if offset >= have {
				return nil, nil
			}
			end := min(have, offset+limit)
			return append([]model.Row(nil), r.rows[offset:end]...), nil
		}
		ch := r.changed
		r.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// Count is the number of rows once reading has finished, else -1: the grid
// then grows as rows arrive and learns the total from the short last page.
func (r *ResultSet) Count(context.Context) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.done || r.err != nil {
		return -1, nil
	}
	return int64(len(r.rows)), nil
}

// Progress reports how many rows have arrived and whether reading finished.
func (r *ResultSet) Progress() (rows int, done bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.rows), r.done
}

// Truncated reports that the result stopped at MaxResultRows.
func (r *ResultSet) Truncated() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.truncated
}

// Err is the error that ended reading early, if one did.
func (r *ResultSet) Err() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

// Close stops reading and releases the stream. Rows already read stay
// available.
func (r *ResultSet) Close() { r.cancel() }

// fail ends the read with an error, as a failed Next would, unless it had
// ended already.
func (r *ResultSet) fail(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.done {
		return
	}
	r.err, r.done = err, true
	close(r.changed)
	r.changed = make(chan struct{})
	close(r.finished)
}
