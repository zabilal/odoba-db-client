package redis

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// The key browser (FR-12.2, T2.40).
//
// A database's keys are its rows: the name, what kind of value it holds, and
// how long it has left. There is no query language and no order — SCAN walks
// the keyspace in whatever order the table it lives in gives, a piece at a
// time, and the server matches the pattern and the type while it walks. What
// the server cannot do the browse refuses rather than doing quietly on part
// of the keyspace, which would be an answer about no keyspace at all.

// keyColumns are what a key shows. A value is not among them: reading every
// key's value to draw a page would be a command for each, and the kinds
// differ — that is what the editors are for (T2.41).
var keyColumns = []model.ColumnDef{
	{Name: "key", Type: model.DataType{Class: model.TypeString}},
	{Name: "type", Type: model.DataType{Class: model.TypeString}, ReadOnly: true},
	{Name: "ttl", Type: model.DataType{Class: model.TypeInterval, Nullable: true}},
}

// keyTypes are the kinds of value a key holds, as the server names them.
// ReJSON-RL is what the JSON module calls its own (T2.42).
var keyTypes = map[string]bool{
	"string": true, "list": true, "set": true, "zset": true,
	"hash": true, "stream": true, "ReJSON-RL": true,
}

// Browse reads a database's keys, or what one key holds (T2.41).
func (s *redisSource) Browse(ctx context.Context, ref model.ObjectRef, opt source.BrowseOptions) (_ model.RowStream, err error) {
	defer panics.Recover(&err, "reading the keys")
	if ref.Kind == model.KindKey {
		return s.browseValue(ctx, ref, opt)
	}
	db, err := databaseOf(ref)
	if err != nil {
		return nil, err
	}
	cols, err := projection(opt.Columns)
	if err != nil {
		return nil, err
	}
	scan, err := scanOf(opt)
	if err != nil {
		return nil, err
	}
	limit := opt.Limit
	if limit <= 0 {
		limit = defaultPage
	}
	nodes, done, err := s.nodes(ctx, db)
	if err != nil {
		return nil, err
	}
	return &keyStream{nodes: nodes, done: done, ref: ref, cols: cols, scan: scan,
		batch: batchOf(limit), skip: opt.Offset, left: limit}, nil
}

// batchOf is how much of the keyspace the server walks before it answers.
// SCAN's COUNT is that, not a number of keys: too small and a page is a
// round trip for every few keys, too large and the server is busy in one
// call for longer than anything should hold it (NFR-P9).
func batchOf(limit int64) int64 {
	switch {
	case limit < minBatch:
		return minBatch
	case limit > maxBatch:
		return maxBatch
	}
	return limit
}

const (
	defaultPage = 200
	minBatch    = 100
	maxBatch    = 1000
)

// Count is how many keys a browse would read. The whole of a database is a
// number the server holds; anything narrower is walked, which is what SCAN is
// for and what the cursor makes interruptible.
func (s *redisSource) Count(ctx context.Context, ref model.ObjectRef, opt source.BrowseOptions) (_ int64, err error) {
	defer panics.Recover(&err, "counting the keys")
	if ref.Kind == model.KindKey {
		return s.countValue(ctx, ref, opt)
	}
	db, err := databaseOf(ref)
	if err != nil {
		return 0, err
	}
	scan, err := scanOf(opt)
	if err != nil {
		return 0, err
	}
	nodes, done, err := s.nodes(ctx, db)
	if err != nil {
		return 0, err
	}
	defer done()
	if scan.match == "" && scan.kind == "" {
		var total int64
		for _, n := range nodes {
			size, err := n.DBSize(ctx).Result()
			if err != nil {
				return 0, err
			}
			total += size
		}
		return total, nil
	}
	var total int64
	for _, n := range nodes {
		for cursor := uint64(0); ; {
			names, next, err := scan.run(ctx, n, cursor, maxBatch)
			if err != nil {
				return 0, err
			}
			total += int64(len(names))
			if next == 0 {
				break
			}
			cursor = next
		}
	}
	return total, nil
}

// Badge is the number of keys in a database, which the server holds and
// answers at once (FR-2.5).
func (s *redisSource) Badge(ctx context.Context, ref model.ObjectRef) (_ model.Badge, _ bool, err error) {
	defer panics.Recover(&err, "counting a database's keys")
	if ref.Kind != model.KindDatabase {
		return model.Badge{}, false, nil
	}
	n, err := s.Count(ctx, ref, source.BrowseOptions{})
	if err != nil {
		return model.Badge{}, false, err
	}
	return model.Badge{Text: strconv.FormatInt(n, 10), Exact: true}, true, nil
}

// databaseOf reads the database a ref names. Redis names its databases with
// numbers, and the tree writes them as db0…db15.
func databaseOf(ref model.ObjectRef) (int, error) {
	if ref.Kind != model.KindDatabase || len(ref.Path) != 1 {
		return 0, fmt.Errorf("redis: %s is not a database of keys", ref)
	}
	n, err := strconv.Atoi(strings.TrimPrefix(ref.Path[0], "db"))
	if err != nil || n < 0 {
		return 0, fmt.Errorf("redis: %q is not a database's number", ref.Path[0])
	}
	return n, nil
}

// projection is the columns asked for, in the order they were asked for.
func projection(names []string) ([]model.ColumnDef, error) {
	if len(names) == 0 {
		return keyColumns, nil
	}
	out := make([]model.ColumnDef, 0, len(names))
	for _, name := range names {
		i := slicesIndex(keyColumns, name)
		if i < 0 {
			return nil, fmt.Errorf("redis: a key has no %q; it has key, type and ttl", name)
		}
		out = append(out, keyColumns[i])
	}
	return out, nil
}

func slicesIndex(cols []model.ColumnDef, name string) int {
	for i, c := range cols {
		if c.Name == name {
			return i
		}
	}
	return -1
}

// scanTerms are what the server itself can narrow a walk by: a pattern over
// the names, and the kind of value held.
type scanTerms struct {
	match string
	kind  string
}

// run makes one SCAN call, which answers a piece of the keyspace and where to
// carry on from. A cursor of zero back means the walk is over.
func (t scanTerms) run(ctx context.Context, n scanner, cursor uint64, batch int64) ([]string, uint64, error) {
	if t.kind != "" {
		return n.ScanType(ctx, cursor, t.match, batch, t.kind).Result()
	}
	return n.Scan(ctx, cursor, t.match, batch).Result()
}

// scanOf reads a browse's options as the two things SCAN takes. Anything else
// is refused: a filter this applied to the page it had already read would
// answer about part of the keyspace as though it were the whole (REQ-DRV-3).
func scanOf(opt source.BrowseOptions) (scanTerms, error) {
	var t scanTerms
	if len(opt.Sorts) > 0 {
		return t, errors.New("redis: SCAN walks the keyspace in no order, so keys cannot be sorted")
	}
	if strings.TrimSpace(opt.Where) != "" {
		return t, errors.New("redis: there is no query language here, so there is no condition to write")
	}
	for _, f := range opt.Filters {
		if f.Negate {
			return t, fmt.Errorf("redis: the server cannot look for keys that do not match %q", text(f))
		}
		switch f.Column {
		case "key":
			if t.match != "" {
				return t, errors.New("redis: a walk takes one pattern, and two were given")
			}
			m, err := pattern(f)
			if err != nil {
				return t, err
			}
			t.match = m
		case "type":
			if f.Op != source.OpEqual || len(f.Values) != 1 {
				return t, fmt.Errorf("redis: a key is of one kind, so %s is not something to ask of it", f.Op)
			}
			kind := text(f)
			if !keyTypes[kind] {
				return t, fmt.Errorf("redis: no key holds a %q", kind)
			}
			t.kind = kind
		case "ttl":
			return t, errors.New("redis: the server cannot pick keys by how long they have left")
		default:
			return t, fmt.Errorf("redis: a key has no %q; it has key, type and ttl", f.Column)
		}
	}
	return t, nil
}

// pattern turns a filter on the name into the glob SCAN matches with.
func pattern(f source.Filter) (string, error) {
	if len(f.Values) != 1 {
		return "", fmt.Errorf("redis: %s takes one pattern, and %d values were given", f.Op, len(f.Values))
	}
	v := text(f)
	switch f.Op {
	case source.OpEqual:
		return glob(v), nil
	case source.OpContains:
		return "*" + glob(v) + "*", nil
	case source.OpLike:
		return globOfLike(v), nil
	}
	return "", fmt.Errorf("redis: the server matches names by pattern, and %s is not one", f.Op)
}

// text is a filter's one value as the server reads it: a key's name is text,
// whatever the grid held it as.
func text(f source.Filter) string {
	if len(f.Values) == 0 {
		return ""
	}
	if s, ok := f.Values[0].(string); ok {
		return s
	}
	return fmt.Sprint(f.Values[0])
}

// glob is a literal name as a pattern that matches it and nothing else.
func glob(s string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(`*?[]\`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// globOfLike turns the grid's own pattern language into the server's: % is
// any run of characters and _ is one, where a glob writes them * and ?.
func globOfLike(s string) string {
	var b strings.Builder
	escaped := false
	for _, r := range s {
		switch {
		case escaped:
			b.WriteString(glob(string(r)))
			escaped = false
		case r == '\\':
			escaped = true
		case r == '%':
			b.WriteByte('*')
		case r == '_':
			b.WriteByte('?')
		default:
			b.WriteString(glob(string(r)))
		}
	}
	if escaped {
		// A pattern ending in a backslash escapes nothing, and the server
		// would read a trailing one as the start of an escape.
		b.WriteString(`\\`)
	}
	return b.String()
}

// scanner is one server a walk runs on: a connection of its own for a single
// server, and each master for a cluster, whose keyspace is shared out.
type scanner interface {
	Scan(ctx context.Context, cursor uint64, match string, count int64) *goredis.ScanCmd
	ScanType(ctx context.Context, cursor uint64, match string, count int64, keyType string) *goredis.ScanCmd
	DBSize(ctx context.Context) *goredis.IntCmd
	Pipeline() goredis.Pipeliner
}

// nodes are the servers a database's keys are walked on, and what to do when
// the walk is over.
//
// A single server's walk takes a connection of its own and selects the
// database on it: selecting on the pool would change whichever connection
// answered and leave the others where they were.
func (s *redisSource) nodes(ctx context.Context, db int) ([]scanner, func(), error) {
	switch c := s.client.(type) {
	case *goredis.ClusterClient:
		if db != 0 {
			return nil, nil, errors.New("redis: a cluster has one keyspace, and no numbered databases in it")
		}
		var mu sync.Mutex
		var masters []*goredis.Client
		if err := c.ForEachMaster(ctx, func(_ context.Context, m *goredis.Client) error {
			mu.Lock()
			defer mu.Unlock()
			masters = append(masters, m)
			return nil
		}); err != nil {
			return nil, nil, err
		}
		// The shards answer in whatever order they were reached; walked in
		// the order of their addresses, a second page follows the first.
		sort.Slice(masters, func(i, j int) bool { return masters[i].Options().Addr < masters[j].Options().Addr })
		out := make([]scanner, 0, len(masters))
		for _, m := range masters {
			out = append(out, m)
		}
		return out, func() {}, nil
	case *goredis.Client:
		// A connection let go of goes back to the pool with the database it
		// was last told to use, so it is told every time and told back
		// afterwards: a connection left on another database would answer the
		// next question about the wrong keyspace.
		conn := c.Conn()
		if err := conn.Select(ctx, db).Err(); err != nil {
			conn.Close()
			return nil, nil, err
		}
		home := c.Options().DB
		return []scanner{conn}, func() {
			conn.Select(context.WithoutCancel(ctx), home)
			conn.Close()
		}, nil
	}
	return nil, nil, fmt.Errorf("redis: %T is not a connection keys can be walked on", s.client)
}

// keyStream is a walk of the keyspace, a piece at a time.
type keyStream struct {
	nodes []scanner
	done  func()
	ref   model.ObjectRef
	cols  []model.ColumnDef
	scan  scanTerms
	batch int64

	skip int64 // keys still to be passed over before the page begins
	left int64 // keys still wanted

	node   int
	cursor uint64
	over   bool

	rows []model.Row
	at   int
}

var _ model.Identified = (*keyStream)(nil)

func (k *keyStream) Columns() []model.ColumnDef { return k.cols }

// Identity is the key's own name: it is what addresses a value in Redis, and
// there is nothing else to address one by (FR-4.7).
func (k *keyStream) Identity() model.RowIdentity {
	return model.RowIdentity{Kind: model.IdentityKeyName, Columns: []string{"key"}, Target: k.ref}
}

func (k *keyStream) Next(ctx context.Context) (_ model.Row, err error) {
	defer panics.Recover(&err, "reading a key")
	for {
		// A walk holds a piece of the keyspace and reads it out without
		// asking the context: a cancelled read would go on returning keys
		// until the piece ran out (NFR-P9).
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if k.at < len(k.rows) {
			row := k.rows[k.at]
			k.at++
			return row, nil
		}
		if k.over || k.left <= 0 {
			return nil, io.EOF
		}
		if err := k.fill(ctx); err != nil {
			return nil, err
		}
	}
}

// fill walks until it has keys to hand back, or until there is no more
// keyspace. A piece of it can hold nothing that matches, which is why this
// is a loop and not a single call.
func (k *keyStream) fill(ctx context.Context) error {
	k.rows, k.at = nil, 0
	for len(k.rows) == 0 && !k.over && k.left > 0 {
		// The server this piece of the keyspace came from is the one to ask
		// about its keys: on a cluster the others hold none of them.
		node := k.nodes[k.node]
		names, next, err := k.scan.run(ctx, node, k.cursor, k.batch)
		if err != nil {
			return err
		}
		k.cursor = next
		if next == 0 {
			// This server's keyspace is walked; the next one's begins.
			k.node++
			k.over = k.node >= len(k.nodes)
		}
		if int64(len(names)) <= k.skip {
			k.skip -= int64(len(names))
			continue
		}
		names = names[k.skip:]
		k.skip = 0
		if int64(len(names)) > k.left {
			names = names[:k.left]
		}
		rows, err := k.rowsOf(ctx, node, names)
		if err != nil {
			return err
		}
		k.left -= int64(len(rows))
		k.rows = rows
	}
	return nil
}

// rowsOf asks after the keys a walk found. What a key holds and how long it
// has left are each a command, so they are sent as one pipeline rather than a
// round trip for every key.
func (k *keyStream) rowsOf(ctx context.Context, node scanner, names []string) ([]model.Row, error) {
	wantType, wantTTL := slicesIndex(k.cols, "type") >= 0, slicesIndex(k.cols, "ttl") >= 0
	types := make([]*goredis.StatusCmd, len(names))
	ttls := make([]*goredis.DurationCmd, len(names))
	if wantType || wantTTL {
		p := node.Pipeline()
		for i, name := range names {
			if wantType {
				types[i] = p.Type(ctx, name)
			}
			if wantTTL {
				ttls[i] = p.TTL(ctx, name)
			}
		}
		// A pipeline whose commands failed is not itself a failure: a key
		// that expired between the walk and the question answers that it is
		// not there, which is read below.
		if _, err := p.Exec(ctx); err != nil && !errors.Is(err, goredis.Nil) {
			return nil, err
		}
	}
	out := make([]model.Row, 0, len(names))
	for i, name := range names {
		if row, ok := keyRow(k.cols, name, types[i], ttls[i]); ok {
			out = append(out, row)
		}
	}
	return out, nil
}

// keyRow lays one key out under the columns asked for. It is not a row at
// all where the key went between the walk and the question: it is not in the
// keyspace any more, and drawing it would be a lie about now rather than a
// fact about then.
func keyRow(cols []model.ColumnDef, name string, kind *goredis.StatusCmd, ttl *goredis.DurationCmd) (model.Row, bool) {
	row := make(model.Row, len(cols))
	for c, col := range cols {
		switch col.Name {
		case "key":
			row[c] = name
		case "type":
			held, err := kind.Result()
			if err != nil || held == "none" {
				return nil, false
			}
			row[c] = held
		case "ttl":
			left, there := ttlOf(ttl)
			if !there {
				return nil, false
			}
			row[c] = left
		}
	}
	return row, true
}

// ttlOf reads what TTL answered: how long the key has left, nothing where it
// will not expire, and not there at all where the key has gone. The server
// says the last two with -1 and -2, which the client carries as negative
// durations of its own.
func ttlOf(cmd *goredis.DurationCmd) (any, bool) {
	d, err := cmd.Result()
	if err != nil {
		return nil, false
	}
	switch {
	case d >= 0:
		return d, true
	case d == -2 || d == -2*time.Second:
		return nil, false
	}
	return nil, true
}

func (k *keyStream) Close() error {
	k.done()
	return nil
}

// ObjectOf is the key a row of a database's keyspace names: its value opens
// as rows of its own (FR-12.2, T2.41).
func (s *redisSource) ObjectOf(ref model.ObjectRef, cols []model.ColumnDef, row model.Row) (model.ObjectRef, bool) {
	if _, err := databaseOf(ref); err != nil {
		return model.ObjectRef{}, false
	}
	i := slicesIndex(cols, "key")
	if i < 0 || i >= len(row) {
		return model.ObjectRef{}, false
	}
	name, ok := row[i].(string)
	if !ok || name == "" {
		return model.ObjectRef{}, false
	}
	return model.NewRef(model.KindKey, ref.Path[0], name), true
}
