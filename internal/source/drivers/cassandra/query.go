package cassandra

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/gocql/gocql"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Running CQL (FR-5.4, FR-5.5, FR-12.3, T2.50).
//
// Every statement is classified and put to the guard before any of a script
// runs, as every other driver here does: refusing the fourth after three have
// run would leave a person somewhere they did not choose (NFR-S4).
//
// A session is a keyspace to be on, which USE moves and nothing else sees.
// gocql pins a keyspace at the session rather than taking it per statement,
// so moving means a session of this console's own — opened when USE asks for
// it, and never before. The connection's own session, which the tree is read
// through, is never moved under it.

var (
	_ source.Queryer   = (*cassandraSource)(nil)
	_ source.Sessioner = (*cassandraSource)(nil)
	_ source.Session   = (*cqlSession)(nil)
)

// consoleRows is how many rows one statement reads at a time. A console
// answers a person, and a person reads a screen; the grid pages a table.
const consoleRows = 500

// Session pins a console's own keyspace, which USE moves.
func (s *cassandraSource) Session(context.Context) (source.Session, error) {
	return &cqlSession{src: s, keyspace: strings.TrimSpace(s.cfg.Database)}, nil
}

// Query runs one statement (source.Queryer).
func (s *cassandraSource) Query(ctx context.Context, stmt source.Statement) (*source.Result, error) {
	return (&cqlSession{src: s, keyspace: strings.TrimSpace(s.cfg.Database)}).Query(ctx, stmt)
}

// QueryMulti runs a script of them, a result at a time (FR-5.4).
func (s *cassandraSource) QueryMulti(ctx context.Context, script string, opts source.ScriptOptions) (<-chan source.ScriptResult, error) {
	return (&cqlSession{src: s, keyspace: strings.TrimSpace(s.cfg.Database)}).QueryMulti(ctx, script, opts)
}

// cqlSession is one query tab's state: the keyspace its statements are on,
// and the connection they run on once USE has moved it.
type cqlSession struct {
	src *cassandraSource

	mu       sync.Mutex
	keyspace string
	own      *gocql.Session // nil until USE moves this console
	closed   bool
}

// Handle names the session for a cancel. gocql cancels a statement by
// cancelling its context, which it does itself, so there is nothing to name.
func (c *cqlSession) Handle() string { return "" }

func (c *cqlSession) Close() error {
	c.mu.Lock()
	own := c.own
	c.own, c.closed = nil, true
	c.mu.Unlock()
	if own != nil {
		own.Close()
	}
	return nil
}

// on is the connection this console's statements run on: the source's own,
// or one of this console's once USE has moved it.
func (c *cqlSession) on() *gocql.Session {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.own != nil {
		return c.own
	}
	return c.src.session
}

// use moves this console to another keyspace.
//
// gocql pins a keyspace at the session, so moving means a session of this
// console's own: another tab on the same connection stays where it was, and
// the connection's own session — which the tree is read through — is never
// moved under it.
func (c *cqlSession) use(ctx context.Context, keyspace string) error {
	cfg := c.src.cfg
	cfg.Database = keyspace
	cluster, err := clusterOf(cfg)
	if err != nil {
		return err
	}
	opened, err := connect(ctx, cluster)
	if err != nil {
		return whyNot(ctx, cfg, err)
	}
	c.mu.Lock()
	previous := c.own
	c.own, c.keyspace = opened, keyspace
	c.mu.Unlock()
	if previous != nil {
		previous.Close()
	}
	return nil
}

// Query runs one statement.
func (c *cqlSession) Query(ctx context.Context, stmt source.Statement) (_ *source.Result, err error) {
	defer panics.Recover(&err, "running a statement")
	text := strings.TrimSpace(stmt.SQL)
	if text == "" {
		return nil, errors.New("cassandra: there is nothing to run")
	}
	if err := c.allow(text, stmt.Confirmed); err != nil {
		return nil, err
	}
	return c.run(ctx, text, stmt.Args)
}

// QueryMulti runs a script, delivering each statement's result as it
// finishes. Every statement is classified and put to the guard before the
// first one runs.
func (c *cqlSession) QueryMulti(ctx context.Context, script string, opts source.ScriptOptions) (<-chan source.ScriptResult, error) {
	stmts := dialect{}.SplitScript(script)
	if len(stmts) == 0 {
		return nil, errors.New("cassandra: there is nothing to run")
	}
	for _, st := range stmts {
		if err := c.allow(st.Text, opts.Confirmed); err != nil {
			return nil, err
		}
	}
	out := make(chan source.ScriptResult, len(stmts))
	go func() {
		defer close(out)
		defer panics.Catch("running a script", func(error) {})
		for i, st := range stmts {
			res, err := c.run(ctx, st.Text, nil)
			out <- source.ScriptResult{Index: i, Offset: st.Offset, Statement: st.Text, Result: res, Err: err}
			if err != nil || ctx.Err() != nil {
				// A script stops where it failed: what follows was written
				// to run after what did not (FR-5.4).
				return
			}
		}
	}()
	return out, nil
}

// allow puts a statement to the guard before it is sent (NFR-S4).
func (c *cqlSession) allow(text string, confirmed bool) error {
	return c.src.cfg.Guard.Allow(dialect{}.Classify(text), confirmed)
}

// run sends one statement, and reads what came back.
func (c *cqlSession) run(ctx context.Context, text string, args []any) (*source.Result, error) {
	if keyspace, ok := useKeyspace(text); ok {
		start := time.Now()
		if err := c.use(ctx, keyspace); err != nil {
			return nil, err
		}
		return &source.Result{Affected: -1, Duration: time.Since(start), Messages: []source.Message{
			{Level: source.MessageInfo, Text: "Now on " + keyspace + "."}}}, nil
	}
	start := time.Now()
	q := c.on().Query(text, args...).WithContext(ctx).PageSize(consoleRows)
	iter := q.Iter()
	cols := iter.Columns()
	if len(cols) == 0 {
		// A statement that returns no rows: what matters is that it ran, and
		// Cassandra does not count what it changed.
		warnings := iter.Warnings()
		if err := iter.Close(); err != nil {
			return nil, err
		}
		return &source.Result{Affected: -1, Duration: time.Since(start), Messages: messages(warnings)}, nil
	}
	rows := &cqlRows{iter: iter, cols: columnDefs(cols)}
	return &source.Result{Rows: rows, Affected: -1, Duration: time.Since(start),
		Messages: messages(iter.Warnings())}, nil
}

// useKeyspace reads USE, which gocql pins at a session rather than runs. The
// name is taken as it was written: CQL folds an unquoted name to lower case,
// and a quoted one keeps the case it was given.
func useKeyspace(text string) (string, bool) {
	fields := strings.Fields(strings.TrimSuffix(strings.TrimSpace(text), ";"))
	if len(fields) != 2 || !strings.EqualFold(fields[0], "use") {
		return "", false
	}
	name := fields[1]
	if quoted := strings.HasPrefix(name, `"`) && strings.HasSuffix(name, `"`) && len(name) > 1; quoted {
		return strings.ReplaceAll(name[1:len(name)-1], `""`, `"`), true
	}
	return strings.ToLower(name), true
}

// messages are the server's own warnings, said as it said them (FR-5.6).
func messages(warnings []string) []source.Message {
	if len(warnings) == 0 {
		return nil
	}
	out := make([]source.Message, 0, len(warnings))
	for _, w := range warnings {
		out = append(out, source.Message{Level: source.MessageWarning, Text: w})
	}
	return out
}

// columnDefs describes what a statement answered with, in the model's terms.
func columnDefs(cols []gocql.ColumnInfo) []model.ColumnDef {
	out := make([]model.ColumnDef, 0, len(cols))
	for _, c := range cols {
		def := model.ColumnDef{Name: c.Name, Type: cqlDataType(typeText(c.TypeInfo))}
		// Every column of a CQL row but a key column may be empty, and a
		// statement's result is not a table: nothing here knows which is
		// which, so nothing here says.
		def.Type.Nullable = true
		if c.Keyspace != "" && c.Table != "" {
			def.Origin = model.NewRef(model.KindTable, c.Keyspace, c.Table)
			def.OriginColumn = c.Name
		}
		out = append(out, def)
	}
	return out
}

// cqlRows reads an iterator's rows, one at a time.
type cqlRows struct {
	iter *gocql.Iter
	cols []model.ColumnDef

	mu   sync.Mutex
	done bool
}

var _ model.RowStream = (*cqlRows)(nil)

func (r *cqlRows) Columns() []model.ColumnDef { return r.cols }

func (r *cqlRows) Next(ctx context.Context) (_ model.Row, err error) {
	defer panics.Recover(&err, "reading a row")
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.done {
		return nil, io.EOF
	}
	// Fresh destinations for every row: what a scan writes into is kept, and
	// a row that reused them would change as the next one was read.
	data, err := r.iter.RowData()
	if err != nil {
		return nil, err
	}
	if !r.iter.Scan(data.Values...) {
		r.done = true
		if err := r.iter.Close(); err != nil {
			return nil, err
		}
		return nil, io.EOF
	}
	row := make(model.Row, 0, len(data.Values))
	for _, v := range data.Values {
		row = append(row, normalize(deref(v)))
	}
	return row, nil
}

func (r *cqlRows) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.done {
		return nil
	}
	r.done = true
	return r.iter.Close()
}

// deref reads what a scan wrote into a destination.
func deref(v any) any {
	switch x := v.(type) {
	case *string:
		return *x
	case *int64:
		return *x
	case *int:
		return *x
	case *int16:
		return *x
	case *int8:
		return *x
	case *float32:
		return *x
	case *float64:
		return *x
	case *bool:
		return *x
	case *[]byte:
		return *x
	case *time.Time:
		return *x
	case *time.Duration:
		return *x
	case *gocql.UUID:
		return *x
	case *gocql.Duration:
		return *x
	}
	return derefValue(v)
}

// typeText writes a type as CQL writes one. gocql prints a collection with
// brackets of its own — list(varchar) — and CQL is written list<text>, which
// is what a person reads and what classifies a type here (types.go).
func typeText(t gocql.TypeInfo) string {
	switch v := t.(type) {
	case gocql.CollectionType:
		switch v.Type() {
		case gocql.TypeList, gocql.TypeSet:
			return fmt.Sprintf("%s<%s>", cqlName(v.Type()), typeText(v.Elem))
		case gocql.TypeMap:
			return fmt.Sprintf("map<%s, %s>", typeText(v.Key), typeText(v.Elem))
		}
	case gocql.TupleTypeInfo:
		parts := make([]string, 0, len(v.Elems))
		for _, e := range v.Elems {
			parts = append(parts, typeText(e))
		}
		return "tuple<" + strings.Join(parts, ", ") + ">"
	case gocql.UDTTypeInfo:
		// A keyspace's own type, which is its name here: the fields are the
		// structure tab's business, not a column heading's.
		return v.Name
	}
	return cqlName(t.Type())
}

// cqlName is the name CQL gives a type, where gocql's own differs: varchar is
// what the protocol calls text.
func cqlName(t gocql.Type) string {
	if t == gocql.TypeVarchar {
		return "text"
	}
	return t.String()
}
