package redis

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	goredis "github.com/redis/go-redis/v9"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// What a key holds, read as rows (FR-12.2, T2.41).
//
// Every kind of value is rows of its own shape: a hash is its fields and
// theirs, a list its elements in order, a set its members, a sorted set its
// members and their scores, and a string the one value it is. The grid draws
// all five without knowing which it has, because each says its columns and
// what tells one row from another.

// valueColumns is the shape a kind of value takes as rows. The column a row
// is known by comes first, and it is the name the server addresses that part
// of the value with.
func valueColumns(kind string) ([]model.ColumnDef, []string, error) {
	text := model.DataType{Class: model.TypeString}
	switch kind {
	case "string":
		// A string is one value, and the key is the whole of its address.
		return []model.ColumnDef{
			{Name: "key", Type: text, ReadOnly: true},
			{Name: "value", Type: text},
		}, []string{"key"}, nil
	case "hash":
		return []model.ColumnDef{{Name: "field", Type: text}, {Name: "value", Type: text}}, []string{"field"}, nil
	case "list":
		// An element is where it is: Redis addresses one by its position,
		// and moving it is not an edit but a rewrite of the list.
		return []model.ColumnDef{
			{Name: "index", Type: model.DataType{Class: model.TypeInteger}, ReadOnly: true},
			{Name: "value", Type: text},
		}, []string{"index"}, nil
	case "set":
		return []model.ColumnDef{{Name: "member", Type: text}}, []string{"member"}, nil
	case "zset":
		return []model.ColumnDef{
			{Name: "member", Type: text},
			{Name: "score", Type: model.DataType{Class: model.TypeFloat}},
		}, []string{"member"}, nil
	case "stream", "ReJSON-RL":
		return nil, nil, fmt.Errorf("%w: reading a %s", errNotYet, kind)
	}
	return nil, nil, fmt.Errorf("redis: nothing here reads a %s", kind)
}

// browseValue reads what one key holds.
func (s *redisSource) browseValue(ctx context.Context, ref model.ObjectRef, opt source.BrowseOptions) (model.RowStream, error) {
	db, name, err := keyOf(ref)
	if err != nil {
		return nil, err
	}
	nodes, done, err := s.nodes(ctx, db)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			done()
		}
	}()
	node, isKey := nodes[0].(keyReader)
	if !isKey {
		return nil, fmt.Errorf("redis: %T does not read a key", nodes[0])
	}
	if len(nodes) > 1 {
		// A cluster holds a key on the shard its name hashes to; asking the
		// wrong one would be answered with a redirection, not a value.
		node, err = s.shardFor(ctx, name)
		if err != nil {
			return nil, err
		}
	}
	kind, err := node.Type(ctx, name).Result()
	if err != nil {
		return nil, err
	}
	if kind == "none" {
		return nil, fmt.Errorf("redis: there is no key called %q", name)
	}
	cols, id, err := valueColumns(kind)
	if err != nil {
		return nil, err
	}
	match, err := memberMatch(kind, cols, opt)
	if err != nil {
		return nil, err
	}
	limit := opt.Limit
	if limit <= 0 {
		limit = defaultPage
	}
	v := &valueStream{done: done, ref: ref, cols: cols, id: id, skip: opt.Offset, left: limit}
	switch kind {
	case "string":
		v.read = stringRows(node, name, cols)
	case "hash":
		v.read = hashRows(node, name, match, batchOf(limit))
	case "set":
		v.read = setRows(node, name, match, batchOf(limit))
	case "zset":
		v.read = zsetRows(node, name, match, batchOf(limit))
	case "list":
		// A list is read by position, so the page is asked for outright
		// rather than walked to.
		v.read = listRows(node, name, opt.Offset, limit)
		v.skip = 0
	}
	ok = true
	return v, nil
}

// countValue is how many rows a key's value has: the server holds the length
// of every kind, and a string is one value.
func (s *redisSource) countValue(ctx context.Context, ref model.ObjectRef, opt source.BrowseOptions) (int64, error) {
	db, name, err := keyOf(ref)
	if err != nil {
		return 0, err
	}
	nodes, done, err := s.nodes(ctx, db)
	if err != nil {
		return 0, err
	}
	defer done()
	node, isKey := nodes[0].(keyReader)
	if !isKey {
		return 0, fmt.Errorf("redis: %T does not read a key", nodes[0])
	}
	if len(nodes) > 1 {
		if node, err = s.shardFor(ctx, name); err != nil {
			return 0, err
		}
	}
	kind, err := node.Type(ctx, name).Result()
	if err != nil {
		return 0, err
	}
	cols, _, err := valueColumns(kind)
	if err != nil {
		return 0, err
	}
	match, err := memberMatch(kind, cols, opt)
	if err != nil {
		return 0, err
	}
	if match != "" {
		// What a pattern picks out is only known by walking, and the walk is
		// the browse itself.
		return -1, nil
	}
	switch kind {
	case "string":
		return 1, nil
	case "hash":
		return node.HLen(ctx, name).Result()
	case "list":
		return node.LLen(ctx, name).Result()
	case "set":
		return node.SCard(ctx, name).Result()
	case "zset":
		return node.ZCard(ctx, name).Result()
	}
	return -1, nil
}

// keyOf reads the database and the name a ref addresses a key by.
func keyOf(ref model.ObjectRef) (int, string, error) {
	if ref.Kind != model.KindKey || len(ref.Path) != 2 {
		return 0, "", fmt.Errorf("redis: %s is not a key", ref)
	}
	db, err := databaseOf(model.NewRef(model.KindDatabase, ref.Path[0]))
	if err != nil {
		return 0, "", err
	}
	if ref.Path[1] == "" {
		return 0, "", errors.New("redis: a key with no name addresses nothing")
	}
	return db, ref.Path[1], nil
}

// memberMatch is the pattern a filter puts on the part of the value a row is
// known by. Only the kinds the server walks take one: a list is read by
// position and a string is one value, so a filter on either is refused
// rather than applied to the rows already read (REQ-DRV-3).
func memberMatch(kind string, cols []model.ColumnDef, opt source.BrowseOptions) (string, error) {
	if len(opt.Sorts) > 0 {
		return "", errors.New("redis: a value is read in the order the server holds it, and cannot be sorted")
	}
	if strings.TrimSpace(opt.Where) != "" {
		return "", errors.New("redis: there is no query language here, so there is no condition to write")
	}
	if len(opt.Filters) == 0 {
		return "", nil
	}
	walked := kind == "hash" || kind == "set" || kind == "zset"
	var match string
	for _, f := range opt.Filters {
		if !walked {
			return "", fmt.Errorf("redis: the server cannot pick out part of a %s", kind)
		}
		if f.Negate || f.Column != cols[0].Name {
			return "", fmt.Errorf("redis: the server matches a %s's %s by pattern, and nothing else about it",
				kind, cols[0].Name)
		}
		if match != "" {
			return "", errors.New("redis: a walk takes one pattern, and two were given")
		}
		m, err := pattern(f)
		if err != nil {
			return "", err
		}
		match = m
	}
	return match, nil
}

// keyReader is what the commands about one key run on: a connection of its
// own on the database, or the shard of a cluster that holds the key.
type keyReader interface {
	scanner
	Type(ctx context.Context, key string) *goredis.StatusCmd
	Get(ctx context.Context, key string) *goredis.StringCmd
	HLen(ctx context.Context, key string) *goredis.IntCmd
	HScan(ctx context.Context, key string, cursor uint64, match string, count int64) *goredis.ScanCmd
	LLen(ctx context.Context, key string) *goredis.IntCmd
	LRange(ctx context.Context, key string, start, stop int64) *goredis.StringSliceCmd
	SCard(ctx context.Context, key string) *goredis.IntCmd
	SScan(ctx context.Context, key string, cursor uint64, match string, count int64) *goredis.ScanCmd
	ZCard(ctx context.Context, key string) *goredis.IntCmd
	ZScan(ctx context.Context, key string, cursor uint64, match string, count int64) *goredis.ScanCmd
}

// shardFor is the master of a cluster that holds a key, which is the one to
// ask about it.
func (s *redisSource) shardFor(ctx context.Context, name string) (writer, error) {
	c, ok := s.client.(*goredis.ClusterClient)
	if !ok {
		return nil, errors.New("redis: this connection is not a cluster")
	}
	m, err := c.MasterForKey(ctx, name)
	if err != nil {
		return nil, err
	}
	return m, nil
}

// piece is a part of a value, and where to carry on from. A cursor of zero
// back means there is no more of it.
type piece func(ctx context.Context, cursor uint64) (rows []model.Row, next uint64, err error)

// valueStream reads a key's value out, a piece at a time.
type valueStream struct {
	read piece
	done func()
	ref  model.ObjectRef
	cols []model.ColumnDef
	id   []string

	skip int64
	left int64

	cursor uint64
	over   bool
	rows   []model.Row
	at     int
}

var _ model.Identified = (*valueStream)(nil)

func (v *valueStream) Columns() []model.ColumnDef { return v.cols }

// Identity is the part of the value the server addresses a row by: a hash's
// field, a set's member, a list's position (FR-4.7).
func (v *valueStream) Identity() model.RowIdentity {
	return model.RowIdentity{Kind: model.IdentityKeyName, Columns: v.id, Target: v.ref}
}

func (v *valueStream) Next(ctx context.Context) (_ model.Row, err error) {
	defer panics.Recover(&err, "reading a value")
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if v.at < len(v.rows) {
			row := v.rows[v.at]
			v.at++
			return row, nil
		}
		if v.over || v.left <= 0 {
			return nil, io.EOF
		}
		if err := v.fill(ctx); err != nil {
			return nil, err
		}
	}
}

func (v *valueStream) fill(ctx context.Context) error {
	v.rows, v.at = nil, 0
	for len(v.rows) == 0 && !v.over && v.left > 0 {
		rows, next, err := v.read(ctx, v.cursor)
		if err != nil {
			return err
		}
		v.cursor = next
		v.over = next == 0
		if int64(len(rows)) <= v.skip {
			v.skip -= int64(len(rows))
			continue
		}
		rows = rows[v.skip:]
		v.skip = 0
		if int64(len(rows)) > v.left {
			rows = rows[:v.left]
		}
		v.left -= int64(len(rows))
		v.rows = rows
	}
	return nil
}

func (v *valueStream) Close() error {
	v.done()
	return nil
}

// stringRows is the one value a string key holds, beside the key that is its
// whole address.
func stringRows(node keyReader, name string, cols []model.ColumnDef) piece {
	return func(ctx context.Context, _ uint64) ([]model.Row, uint64, error) {
		s, err := node.Get(ctx, name).Result()
		if errors.Is(err, goredis.Nil) {
			return nil, 0, nil
		}
		if err != nil {
			return nil, 0, err
		}
		return []model.Row{{name, value(s)}}, 0, nil
	}
}

// hashRows walks a hash's fields, which the server hands back in pairs.
func hashRows(node keyReader, name, match string, batch int64) piece {
	return func(ctx context.Context, cursor uint64) ([]model.Row, uint64, error) {
		pairs, next, err := node.HScan(ctx, name, cursor, match, batch).Result()
		if err != nil {
			return nil, 0, err
		}
		rows := make([]model.Row, 0, len(pairs)/2)
		for i := 0; i+1 < len(pairs); i += 2 {
			rows = append(rows, model.Row{pairs[i], value(pairs[i+1])})
		}
		return rows, next, nil
	}
}

// setRows walks a set's members.
func setRows(node keyReader, name, match string, batch int64) piece {
	return func(ctx context.Context, cursor uint64) ([]model.Row, uint64, error) {
		members, next, err := node.SScan(ctx, name, cursor, match, batch).Result()
		if err != nil {
			return nil, 0, err
		}
		rows := make([]model.Row, 0, len(members))
		for _, m := range members {
			rows = append(rows, model.Row{value(m)})
		}
		return rows, next, nil
	}
}

// zsetRows walks a sorted set, whose members come back with their scores.
func zsetRows(node keyReader, name, match string, batch int64) piece {
	return func(ctx context.Context, cursor uint64) ([]model.Row, uint64, error) {
		pairs, next, err := node.ZScan(ctx, name, cursor, match, batch).Result()
		if err != nil {
			return nil, 0, err
		}
		rows := make([]model.Row, 0, len(pairs)/2)
		for i := 0; i+1 < len(pairs); i += 2 {
			score, err := strconv.ParseFloat(pairs[i+1], 64)
			if err != nil {
				return nil, 0, fmt.Errorf("redis: %q is not a score: %w", pairs[i+1], err)
			}
			rows = append(rows, model.Row{value(pairs[i]), score})
		}
		return rows, next, nil
	}
}

// listRows reads a list by position: the page is asked for outright, because
// a list is held in order and the server can be told where to begin.
func listRows(node keyReader, name string, offset, limit int64) piece {
	return func(ctx context.Context, _ uint64) ([]model.Row, uint64, error) {
		elements, err := node.LRange(ctx, name, offset, offset+limit-1).Result()
		if err != nil {
			return nil, 0, err
		}
		rows := make([]model.Row, 0, len(elements))
		for i, e := range elements {
			rows = append(rows, model.Row{offset + int64(i), value(e)})
		}
		return rows, 0, nil
	}
}

// value is what the server held, as the grid takes it. Redis strings are
// bytes: text where they are text, and bytes where they are not, so that
// what is not text is shown as what it is rather than as mangled letters.
func value(s string) any {
	if utf8.ValidString(s) {
		return s
	}
	return []byte(s)
}
