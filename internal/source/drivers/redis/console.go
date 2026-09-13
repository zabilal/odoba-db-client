package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	goredis "github.com/redis/go-redis/v9"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// The console (FR-5.4, FR-12.2, T2.44): Redis's own language, at a prompt.
//
// Redis has no query language; it has commands, and the console is where they
// are typed. A command is a line — a name and its arguments, quoted as
// redis-cli quotes them — and every command is classified and put to the
// guard before any of a script runs, as a SQL script's statements are.
//
// A console holds a connection of its own, so that what a person does to it
// stays done: SELECT moves this console and nothing else, and MULTI, WATCH
// and CLIENT SETNAME are the connection's own state for as long as the tab is
// open.

var (
	_ source.Queryer   = (*redisSource)(nil)
	_ source.Sessioner = (*redisSource)(nil)
)

// Session opens a console's connection.
func (s *redisSource) Session(ctx context.Context) (_ source.Session, err error) {
	defer panics.Recover(&err, "opening a console")
	c := &console{src: s}
	switch client := s.client.(type) {
	case *goredis.ClusterClient:
		c.cluster = client
	case *goredis.Client:
		db, err := database(s.cfg)
		if err != nil {
			return nil, err
		}
		conn := client.Conn()
		if err := conn.Select(ctx, db).Err(); err != nil {
			conn.Close()
			return nil, err
		}
		c.conn = conn
	default:
		return nil, fmt.Errorf("redis: %T is not a connection commands are typed at", s.client)
	}
	return c, nil
}

// Query runs one command on a console of its own.
func (s *redisSource) Query(ctx context.Context, stmt source.Statement) (*source.Result, error) {
	c, err := s.Session(ctx)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	return c.Query(ctx, stmt)
}

// QueryMulti runs a script of commands on a console of its own (FR-5.4).
func (s *redisSource) QueryMulti(ctx context.Context, script string, opts source.ScriptOptions) (<-chan source.ScriptResult, error) {
	c, err := s.Session(ctx)
	if err != nil {
		return nil, err
	}
	res, err := c.(*console).run(ctx, script, opts, func() { c.Close() })
	if err != nil {
		c.Close()
		return nil, err
	}
	return res, nil
}

// console is one query tab's connection.
type console struct {
	src     *redisSource
	conn    *goredis.Conn
	cluster *goredis.ClusterClient

	mu     sync.Mutex
	closed bool
}

var _ source.Session = (*console)(nil)

// Handle names the session for a cancel. A command is cancelled by cancelling
// its context, which the client does itself, so there is nothing to name.
func (c *console) Handle() string { return "" }

func (c *console) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// Query runs one command.
func (c *console) Query(ctx context.Context, stmt source.Statement) (_ *source.Result, err error) {
	defer panics.Recover(&err, "running a command")
	args, err := parseArgs(stmt.SQL)
	if err != nil {
		return nil, err
	}
	if len(args) == 0 {
		return nil, errors.New("redis: there is nothing to run")
	}
	if err := c.allow(args, stmt.Confirmed); err != nil {
		return nil, err
	}
	return c.send(ctx, args)
}

// QueryMulti runs a script on this console.
func (c *console) QueryMulti(ctx context.Context, script string, opts source.ScriptOptions) (<-chan source.ScriptResult, error) {
	return c.run(ctx, script, opts, nil)
}

// run reads a whole script, puts every command in it to the guard, and only
// then runs them: refusing the fourth after three have run would leave a
// person somewhere they did not choose (NFR-S4).
func (c *console) run(ctx context.Context, script string, opts source.ScriptOptions, done func()) (<-chan source.ScriptResult, error) {
	stmts := splitCommands(script)
	if len(stmts) == 0 {
		return nil, errors.New("redis: there is nothing to run")
	}
	commands := make([][]string, 0, len(stmts))
	for _, st := range stmts {
		args, err := parseArgs(st.Text)
		if err != nil {
			return nil, err
		}
		if err := c.allow(args, opts.Confirmed); err != nil {
			return nil, err
		}
		commands = append(commands, args)
	}
	out := make(chan source.ScriptResult, len(commands))
	go func() {
		defer close(out)
		if done != nil {
			defer done()
		}
		for i, args := range commands {
			res, err := c.send(ctx, args)
			out <- source.ScriptResult{Index: i, Offset: stmts[i].Offset, Statement: stmts[i].Text, Result: res, Err: err}
			if err != nil || ctx.Err() != nil {
				return // a script stops at its first failure, as SQL's does
			}
		}
	}()
	return out, nil
}

// allow puts a command to the guard, by what it does.
func (c *console) allow(args []string, confirmed bool) error {
	if len(args) == 0 {
		return errors.New("redis: there is nothing to run")
	}
	if why, refused := refusedHere(args); refused {
		return errors.New("redis: " + why)
	}
	return c.src.cfg.Guard.Allow(commandAccess(args), confirmed)
}

// send makes the call and reads what came back.
func (c *console) send(ctx context.Context, args []string) (_ *source.Result, err error) {
	defer panics.Recover(&err, "running a command")
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return nil, errors.New("redis: this console is closed")
	}
	as := make([]any, 0, len(args))
	for _, a := range args {
		as = append(as, a)
	}
	start := time.Now()
	var reply any
	if c.cluster != nil {
		reply, err = c.cluster.Do(ctx, as...).Result()
	} else {
		reply, err = c.conn.Do(ctx, as...).Result()
	}
	if errors.Is(err, goredis.Nil) {
		// A reply of nothing is an answer: the key is not there, the list is
		// empty, the member has no score.
		return nothing(start), nil
	}
	if err != nil {
		return nil, err
	}
	return replied(start, reply), nil
}

// refusedHere says why a command is not one this console runs. Each of these
// turns the connection into something that is no longer answering commands,
// and a console that stopped answering would look like one that had hung.
func refusedHere(args []string) (string, bool) {
	switch strings.ToUpper(args[0]) {
	case "SUBSCRIBE", "PSUBSCRIBE", "SSUBSCRIBE":
		return "this console runs commands and reads their answers; a subscription is a tail, which is FR-12.5", true
	case "MONITOR":
		return "MONITOR turns a connection into a stream of everything the server does, which this console cannot read", true
	}
	return "", false
}

// splitCommands divides a console's script into its commands: one a line. A
// line beginning with # is a comment, as it is in a Redis configuration
// file; redis-cli itself has none, and a script nobody can annotate is worse
// than one whose comments the server never sees.
func splitCommands(script string) []source.ScriptStatement {
	var out []source.ScriptStatement
	offset := 0
	for _, line := range strings.SplitAfter(script, "\n") {
		text := strings.TrimSpace(line)
		if text != "" && !strings.HasPrefix(text, "#") {
			at := offset + strings.Index(line, strings.TrimLeft(line, " \t"))
			out = append(out, source.ScriptStatement{Text: text, Offset: at})
		}
		offset += len(line)
	}
	return out
}

// parseArgs splits a command into its name and arguments, as redis-cli reads
// them: words apart, "…" with the escapes it knows, and '…' where only the
// quote itself is escaped.
func parseArgs(line string) ([]string, error) {
	var args []string
	s := strings.TrimSpace(line)
	for i := 0; i < len(s); {
		switch {
		case s[i] == ' ' || s[i] == '\t':
			i++
		case s[i] == '"':
			arg, next, err := doubleQuoted(s, i)
			if err != nil {
				return nil, err
			}
			args, i = append(args, arg), next
		case s[i] == '\'':
			arg, next, err := singleQuoted(s, i)
			if err != nil {
				return nil, err
			}
			args, i = append(args, arg), next
		default:
			start := i
			for i < len(s) && s[i] != ' ' && s[i] != '\t' {
				i++
			}
			args = append(args, s[start:i])
		}
	}
	return args, nil
}

// doubleQuoted reads "…", where \n \r \t \b \a \xHH and \" are what they are
// elsewhere.
func doubleQuoted(s string, i int) (string, int, error) {
	var b strings.Builder
	for i++; i < len(s); i++ {
		switch {
		case s[i] == '"':
			i++
			if i < len(s) && s[i] != ' ' && s[i] != '\t' {
				return "", 0, errors.New("redis: a closing quote must end the word")
			}
			return b.String(), i, nil
		case s[i] == '\\' && i+3 < len(s) && s[i+1] == 'x' && isHex(s[i+2]) && isHex(s[i+3]):
			n, _ := strconv.ParseUint(s[i+2:i+4], 16, 8)
			b.WriteByte(byte(n))
			i += 3
		case s[i] == '\\' && i+1 < len(s):
			i++
			switch s[i] {
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			case 'b':
				b.WriteByte('\b')
			case 'a':
				b.WriteByte('\a')
			default:
				b.WriteByte(s[i])
			}
		default:
			b.WriteByte(s[i])
		}
	}
	return "", 0, errors.New("redis: a quote is left open")
}

// singleQuoted reads '…', where only \' is an escape.
func singleQuoted(s string, i int) (string, int, error) {
	var b strings.Builder
	for i++; i < len(s); i++ {
		switch {
		case s[i] == '\'':
			i++
			if i < len(s) && s[i] != ' ' && s[i] != '\t' {
				return "", 0, errors.New("redis: a closing quote must end the word")
			}
			return b.String(), i, nil
		case s[i] == '\\' && i+1 < len(s) && s[i+1] == '\'':
			b.WriteByte('\'')
			i++
		default:
			b.WriteByte(s[i])
		}
	}
	return "", 0, errors.New("redis: a quote is left open")
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

// nothing is a reply of nothing at all.
func nothing(start time.Time) *source.Result {
	return &source.Result{
		Rows:     &listing{cols: []model.ColumnDef{{Name: "reply", Type: model.DataType{Class: model.TypeString, Nullable: true}}}},
		Duration: time.Since(start),
		Messages: []source.Message{{Level: source.MessageInfo, Text: "(nil)"}},
	}
}

// replied lays a reply out as rows. What a command answers with is its own
// shape — a word, a number, a list of them, a list of lists, or the pairs of
// a map — and each is drawn as what it is rather than as one line of text.
func replied(start time.Time, reply any) *source.Result {
	cols, rows := replyRows(reply)
	res := &source.Result{Rows: &listing{cols: cols, rows: rows}, Duration: time.Since(start)}
	if n, ok := reply.(int64); ok {
		// A number is how many a write touched, as often as not, and the
		// footer says so where it is.
		res.Affected = n
	} else {
		res.Affected = int64(len(rows))
	}
	if s, ok := reply.(string); ok && (s == "OK" || s == "PONG") {
		res.Messages = []source.Message{{Level: source.MessageInfo, Text: s}}
	}
	return res
}

// replyRows is a reply as columns and rows.
func replyRows(reply any) ([]model.ColumnDef, []model.Row) {
	switch v := reply.(type) {
	case map[any]any:
		// RESP3 answers some commands with a map, CONFIG GET among them.
		cols := []model.ColumnDef{
			{Name: "name", Type: model.DataType{Class: model.TypeString}},
			{Name: "value", Type: model.DataType{Class: model.TypeString}},
		}
		rows := make([]model.Row, 0, len(v))
		for name, value := range v {
			rows = append(rows, model.Row{replyValue(name), replyValue(value)})
		}
		sort.Slice(rows, func(i, j int) bool { return fmt.Sprint(rows[i][0]) < fmt.Sprint(rows[j][0]) })
		return cols, rows
	case []any:
		rows := make([]model.Row, 0, len(v))
		for _, e := range v {
			rows = append(rows, model.Row{replyValue(e)})
		}
		return []model.ColumnDef{{Name: "reply", Type: replyType(v)}}, rows
	}
	return []model.ColumnDef{{Name: "reply", Type: replyType([]any{reply})}}, []model.Row{{replyValue(reply)}}
}

// replyType is what a column of these values holds: the one type they all
// are, or text where they are not all one.
func replyType(values []any) model.DataType {
	class := model.TypeUnknown
	for _, v := range values {
		var this model.TypeClass
		switch x := replyValue(v).(type) {
		case nil:
			continue
		case int64:
			this = model.TypeInteger
		case float64:
			this = model.TypeFloat
		case bool:
			this = model.TypeBool
		case model.JSON:
			this = model.TypeJSON
		case []byte:
			this = model.TypeBytes
		default:
			_ = x
			this = model.TypeString
		}
		if class == model.TypeUnknown {
			class = this
		} else if class != this {
			return model.DataType{Class: model.TypeString, Nullable: true}
		}
	}
	if class == model.TypeUnknown {
		class = model.TypeString
	}
	return model.DataType{Class: class, Nullable: true}
}

// replyValue is one value of a reply, as the grid takes it. What is nested —
// a list within a list, a map within one — is the structure it is, and shown
// as such.
func replyValue(v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case string:
		if !utf8.ValidString(x) {
			return []byte(x)
		}
		return x
	case []any, map[any]any:
		if b, err := json.Marshal(jsonable(x)); err == nil {
			return model.JSON(b)
		}
		return fmt.Sprint(x)
	case error:
		return x.Error()
	}
	return v
}

// jsonable turns a reply into something JSON can be written from: a map with
// keys of any type is not, and Redis's are names.
func jsonable(v any) any {
	switch x := v.(type) {
	case map[any]any:
		out := make(map[string]any, len(x))
		for name, value := range x {
			out[fmt.Sprint(name)] = jsonable(value)
		}
		return out
	case []any:
		out := make([]any, 0, len(x))
		for _, e := range x {
			out = append(out, jsonable(e))
		}
		return out
	}
	return v
}

// listing is rows already read, which is what a command answers with.
type listing struct {
	cols []model.ColumnDef
	rows []model.Row
	at   int
}

func (l *listing) Columns() []model.ColumnDef { return l.cols }

func (l *listing) Next(ctx context.Context) (model.Row, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if l.at >= len(l.rows) {
		return nil, io.EOF
	}
	l.at++
	return l.rows[l.at-1], nil
}

func (l *listing) Close() error { return nil }
