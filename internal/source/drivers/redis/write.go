package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/sqlscript"
)

// Editing what a key holds (FR-4.4, FR-4.5, FR-12.2, T2.41).
//
// A plan begins by asking what the key is, and so does applying one: a key
// that has gone since the tab was opened is refused there, before a command
// is sent, and what each command then looks for is the part of the value it
// was to change.
//
// A change is planned as the commands it would send, written as a person
// would type them, and carried beside them in the form the client takes. The
// kind of value decides what a change even means, so the plan asks the server
// what the key is before it writes a word.
//
// Redis has no statement that changes a part of a value only if it is still
// there, so a change is a look and then a write: the look is what makes a
// change to something that has gone fail rather than put it back.

var _ source.Writer = (*redisSource)(nil)

// Plan renders a changeset as the commands it would send.
func (s *redisSource) Plan(ctx context.Context, cs source.Changeset) (_ *source.WritePlan, err error) {
	defer panics.Recover(&err, "planning a write")
	_, name, err := keyOf(cs.Target)
	if err != nil {
		return nil, err
	}
	kind, node, done, err := s.keyKind(ctx, cs.Target)
	if err != nil {
		return nil, err
	}
	defer done()
	cols, id, err := valueColumns(kind)
	if err != nil {
		return nil, err
	}
	if !cs.Identity.Editable() && len(cs.Changes) > 0 && !insertsOnly(cs.Changes) {
		return nil, errors.New("these rows have nothing to tell them apart, so they cannot be written")
	}
	plan := &source.WritePlan{
		Target: cs.Target,
		// Each command stands on its own: a plan that fails partway leaves
		// the commands before it, and the person is told so (FR-4.5).
		Atomic:  false,
		Guarded: s.cfg.Guard.RequiresConfirmation(source.AccessWrite),
	}
	now := holding(ctx, kind, node, name)
	for i, c := range cs.Changes {
		w, desc, err := writeOf(kind, name, cols, id, c, now)
		if err != nil {
			return nil, fmt.Errorf("change %d: %w", i+1, err)
		}
		plan.Statements = append(plan.Statements, source.Statement{SQL: w.command, Op: w, Confirmed: cs.Confirmed})
		plan.Descriptions = append(plan.Descriptions, desc)
	}
	return plan, nil
}

// Apply sends a plan's commands in order.
func (s *redisSource) Apply(ctx context.Context, plan *source.WritePlan) (_ *source.WriteOutcome, err error) {
	defer panics.Recover(&err, "writing a value")
	if err := sqlscript.AllowWrites(s.cfg.Guard, plan); err != nil {
		return nil, err
	}
	_, name, err := keyOf(plan.Target)
	if err != nil {
		return nil, err
	}
	var node writer
	var done func()
	return sqlscript.ApplyWith(plan, func(st source.Statement) (int64, error) {
		w, ok := st.Op.(*valueWrite)
		if !ok {
			return 0, errors.New("redis: this plan was not made here")
		}
		if node == nil {
			// The connection is taken when the first command needs it, so a
			// plan that was made somewhere else holds nothing open.
			var err error
			if _, node, done, err = s.keyKind(ctx, plan.Target); err != nil {
				return 0, err
			}
		}
		return w.run(ctx, node, name)
	}, func() error {
		if done != nil {
			done()
		}
		return nil
	}, func() error {
		if done != nil {
			done()
		}
		// Nothing was undone, and the outcome must not say it was.
		return errNoRollback
	}), nil
}

var errNoRollback = errors.New("redis: the commands are sent one at a time, so those before a failure stand")

func insertsOnly(changes []source.RowChange) bool {
	for _, c := range changes {
		if c.Kind != source.ChangeInsert {
			return false
		}
	}
	return true
}

// keyKind is what a key holds, and the server to ask about it. The caller
// lets the connection go with done.
func (s *redisSource) keyKind(ctx context.Context, ref model.ObjectRef) (string, writer, func(), error) {
	db, name, err := keyOf(ref)
	if err != nil {
		return "", nil, nil, err
	}
	nodes, done, err := s.nodes(ctx, db)
	if err != nil {
		return "", nil, nil, err
	}
	node, ok := nodes[0].(writer)
	if !ok {
		done()
		return "", nil, nil, fmt.Errorf("redis: %T does not write a key", nodes[0])
	}
	if len(nodes) > 1 {
		if node, err = s.shardFor(ctx, name); err != nil {
			done()
			return "", nil, nil, err
		}
	}
	kind, err := node.Type(ctx, name).Result()
	if err != nil {
		done()
		return "", nil, nil, err
	}
	if kind == "none" {
		done()
		return "", nil, nil, fmt.Errorf("redis: there is no key called %q", name)
	}
	return kind, node, done, nil
}

// valueWrite is one change: what a person reads, and what is sent.
type valueWrite struct {
	command string
	send    func(ctx context.Context, node writer, name string) (int64, error)
}

// run makes the change and says how many rows it was about: none where what
// it was to change is no longer there, which is a row changed since it was
// read (sqlscript.ApplyWith).
func (w *valueWrite) run(ctx context.Context, node writer, name string) (int64, error) {
	return w.send(ctx, node, name)
}

// held reads what a part of a value holds now — a hash's field, a sorted
// set's member — so that a change which keeps it can be rendered as the
// command it will really send rather than as one with a blank in it. false
// where there is no such part any more.
type held func(part string) (string, bool)

// holding reads a part of a value off the server, for a plan to render with.
// A part that cannot be read is rendered as empty, and the write itself
// fails when it goes to look again.
func holding(ctx context.Context, kind string, node writer, name string) held {
	return func(part string) (string, bool) {
		switch kind {
		case "hash":
			v, err := node.HGet(ctx, name, part).Result()
			return v, err == nil
		case "zset":
			score, err := node.ZScore(ctx, name, part).Result()
			return number(score), err == nil
		}
		return "", false
	}
}

// writeOf renders one change against a kind of value, and says in a line
// what it does.
func writeOf(kind, name string, cols []model.ColumnDef, id []string, c source.RowChange, now held) (*valueWrite, string, error) {
	for _, col := range cols {
		if v, ok := c.Values[col.Name]; ok {
			if _, gone := v.(model.Removed); gone {
				return nil, "", fmt.Errorf("a %s has no %s to take away; it can only be changed or removed whole",
					kind, col.Name)
			}
		}
	}
	switch kind {
	case "string":
		return stringWrite(name, c)
	case "hash":
		return hashWrite(name, c, now)
	case "list":
		return listWrite(name, c)
	case "set":
		return setWrite(name, c)
	case "zset":
		return zsetWrite(name, c, now)
	case "stream":
		return streamWrite(name, c)
	case "ReJSON-RL":
		return documentWrite(name, c)
	}
	_ = id
	return nil, "", fmt.Errorf("redis: nothing here writes a %s", kind)
}

func stringWrite(name string, c source.RowChange) (*valueWrite, string, error) {
	switch c.Kind {
	case source.ChangeUpdate:
		if _, renamed := c.Values["key"]; renamed {
			return nil, "", errors.New("a key is renamed on its own, not by editing the value it holds")
		}
		v, ok := c.Values["value"]
		if !ok {
			return nil, "", errors.New("an update that changes nothing")
		}
		text := str(v)
		w := &valueWrite{command: fmt.Sprintf("SET %s %s", quote(name), quote(text))}
		w.send = func(ctx context.Context, node writer, name string) (int64, error) {
			// KeepTTL: setting a value is not a reason for a key to stop
			// expiring, and a plain SET would clear the expiry it has.
			return 1, node.Set(ctx, name, text, goredis.KeepTTL).Err()
		}
		return w, "Set " + name, nil
	case source.ChangeInsert, source.ChangeDelete:
		return nil, "", errors.New("a string holds one value: it is set, and the key itself is added or deleted")
	}
	return nil, "", fmt.Errorf("a change of unknown kind %d", c.Kind)
}

func hashWrite(name string, c source.RowChange, now held) (*valueWrite, string, error) {
	switch c.Kind {
	case source.ChangeInsert:
		field, value := str(c.Values["field"]), str(c.Values["value"])
		if field == "" {
			return nil, "", errors.New("a field with no name")
		}
		w := &valueWrite{command: fmt.Sprintf("HSET %s %s %s", quote(name), quote(field), quote(value))}
		w.send = func(ctx context.Context, node writer, name string) (int64, error) {
			added, err := node.HSetNX(ctx, name, field, value).Result()
			if err != nil {
				return 0, err
			}
			if !added {
				return 0, fmt.Errorf("the hash has a field called %q already", field)
			}
			return 1, nil
		}
		return w, "Add the field " + field, nil
	case source.ChangeUpdate:
		field, err := one(c.Key, "field")
		if err != nil {
			return nil, "", err
		}
		renamed, isRenamed := c.Values["field"]
		value, changed := c.Values["value"]
		if !isRenamed && !changed {
			return nil, "", errors.New("an update that changes nothing")
		}
		to := field
		if isRenamed {
			to = str(renamed)
		}
		text := str(value)
		if !changed && now != nil {
			// A field moved and not otherwise changed carries what it holds,
			// and the plan shows the value it will really write.
			text, _ = now(field)
		}
		w := &valueWrite{command: fmt.Sprintf("HSET %s %s %s", quote(name), quote(to), quote(text))}
		if to != field {
			w.command = fmt.Sprintf("HDEL %s %s ; %s", quote(name), quote(field), w.command)
		}
		w.send = func(ctx context.Context, node writer, name string) (int64, error) {
			was, err := node.HGet(ctx, name, field).Result()
			if errors.Is(err, goredis.Nil) {
				return 0, nil // the field has gone since it was read
			}
			if err != nil {
				return 0, err
			}
			if !changed {
				text = was
			}
			if to == field {
				return 1, node.HSet(ctx, name, field, text).Err()
			}
			taken, err := node.HExists(ctx, name, to).Result()
			if err != nil {
				return 0, err
			}
			if taken {
				return 0, fmt.Errorf("the hash has a field called %q already", to)
			}
			p := node.TxPipeline()
			p.HDel(ctx, name, field)
			p.HSet(ctx, name, to, text)
			_, err = p.Exec(ctx)
			return 1, err
		}
		return w, "Change the field " + field, nil
	case source.ChangeDelete:
		field, err := one(c.Key, "field")
		if err != nil {
			return nil, "", err
		}
		w := &valueWrite{command: fmt.Sprintf("HDEL %s %s", quote(name), quote(field))}
		w.send = func(ctx context.Context, node writer, name string) (int64, error) {
			return node.HDel(ctx, name, field).Result()
		}
		return w, "Delete the field " + field, nil
	}
	return nil, "", fmt.Errorf("a change of unknown kind %d", c.Kind)
}

func listWrite(name string, c source.RowChange) (*valueWrite, string, error) {
	switch c.Kind {
	case source.ChangeInsert:
		if at, ok := c.Values["index"]; ok && !isDefault(at) {
			return nil, "", errors.New("an element is added at the end of a list, so it is given no position")
		}
		value := str(c.Values["value"])
		w := &valueWrite{command: fmt.Sprintf("RPUSH %s %s", quote(name), quote(value))}
		w.send = func(ctx context.Context, node writer, name string) (int64, error) {
			if err := node.RPush(ctx, name, value).Err(); err != nil {
				return 0, err
			}
			return 1, nil
		}
		return w, "Add an element at the end", nil
	case source.ChangeUpdate:
		at, err := index(c.Key)
		if err != nil {
			return nil, "", err
		}
		if _, moved := c.Values["index"]; moved {
			return nil, "", errors.New("an element is where it is: moving one is a rewrite of the list, not an edit")
		}
		value, ok := c.Values["value"]
		if !ok {
			return nil, "", errors.New("an update that changes nothing")
		}
		text := str(value)
		w := &valueWrite{command: fmt.Sprintf("LSET %s %d %s", quote(name), at, quote(text))}
		w.send = func(ctx context.Context, node writer, name string) (int64, error) {
			err := node.LSet(ctx, name, at, text).Err()
			if err != nil && strings.Contains(strings.ToLower(err.Error()), "index out of range") {
				// The list is shorter than it was when it was read.
				return 0, nil
			}
			return 1, err
		}
		return w, fmt.Sprintf("Set the element at %d", at), nil
	case source.ChangeDelete:
		return nil, "", errors.New("Redis removes a list's elements by value rather than by position, which is LREM in the console")
	}
	return nil, "", fmt.Errorf("a change of unknown kind %d", c.Kind)
}

func setWrite(name string, c source.RowChange) (*valueWrite, string, error) {
	switch c.Kind {
	case source.ChangeInsert:
		member := str(c.Values["member"])
		w := &valueWrite{command: fmt.Sprintf("SADD %s %s", quote(name), quote(member))}
		w.send = func(ctx context.Context, node writer, name string) (int64, error) {
			added, err := node.SAdd(ctx, name, member).Result()
			if err != nil {
				return 0, err
			}
			if added == 0 {
				return 0, fmt.Errorf("%q is in the set already", member)
			}
			return added, nil
		}
		return w, "Add " + member, nil
	case source.ChangeUpdate:
		member, err := one(c.Key, "member")
		if err != nil {
			return nil, "", err
		}
		to, ok := c.Values["member"]
		if !ok {
			return nil, "", errors.New("an update that changes nothing")
		}
		next := str(to)
		w := &valueWrite{command: fmt.Sprintf("SREM %s %s ; SADD %s %s",
			quote(name), quote(member), quote(name), quote(next))}
		w.send = func(ctx context.Context, node writer, name string) (int64, error) {
			there, err := node.SIsMember(ctx, name, member).Result()
			if err != nil || !there {
				return 0, err
			}
			taken, err := node.SIsMember(ctx, name, next).Result()
			if err != nil {
				return 0, err
			}
			if taken && next != member {
				return 0, fmt.Errorf("%q is in the set already", next)
			}
			p := node.TxPipeline()
			p.SRem(ctx, name, member)
			p.SAdd(ctx, name, next)
			_, err = p.Exec(ctx)
			return 1, err
		}
		return w, "Change " + member, nil
	case source.ChangeDelete:
		member, err := one(c.Key, "member")
		if err != nil {
			return nil, "", err
		}
		w := &valueWrite{command: fmt.Sprintf("SREM %s %s", quote(name), quote(member))}
		w.send = func(ctx context.Context, node writer, name string) (int64, error) {
			return node.SRem(ctx, name, member).Result()
		}
		return w, "Remove " + member, nil
	}
	return nil, "", fmt.Errorf("a change of unknown kind %d", c.Kind)
}

func zsetWrite(name string, c source.RowChange, now held) (*valueWrite, string, error) {
	switch c.Kind {
	case source.ChangeInsert:
		member := str(c.Values["member"])
		score, err := scoreOf(c.Values["score"])
		if err != nil {
			return nil, "", err
		}
		w := &valueWrite{command: fmt.Sprintf("ZADD %s NX %s %s", quote(name), number(score), quote(member))}
		w.send = func(ctx context.Context, node writer, name string) (int64, error) {
			added, err := node.ZAddNX(ctx, name, goredis.Z{Score: score, Member: member}).Result()
			if err != nil {
				return 0, err
			}
			if added == 0 {
				return 0, fmt.Errorf("%q is in the sorted set already", member)
			}
			return added, nil
		}
		return w, "Add " + member, nil
	case source.ChangeUpdate:
		member, err := one(c.Key, "member")
		if err != nil {
			return nil, "", err
		}
		to, renamed := c.Values["member"]
		given, rescored := c.Values["score"]
		if !renamed && !rescored {
			return nil, "", errors.New("an update that changes nothing")
		}
		next := member
		if renamed {
			next = str(to)
		}
		var score float64
		if rescored {
			if score, err = scoreOf(given); err != nil {
				return nil, "", err
			}
		} else if now != nil {
			// A member renamed keeps the score it has, and the plan shows it.
			if was, there := now(member); there {
				score, _ = scoreOf(was)
			}
		}
		w := &valueWrite{command: fmt.Sprintf("ZADD %s %s %s", quote(name), number(score), quote(next))}
		if next != member {
			w.command = fmt.Sprintf("ZREM %s %s ; %s", quote(name), quote(member), w.command)
		}
		w.send = func(ctx context.Context, node writer, name string) (int64, error) {
			was, err := node.ZScore(ctx, name, member).Result()
			if errors.Is(err, goredis.Nil) {
				return 0, nil // the member has gone since it was read
			}
			if err != nil {
				return 0, err
			}
			if !rescored {
				score = was
			}
			if next == member {
				return 1, node.ZAddXX(ctx, name, goredis.Z{Score: score, Member: member}).Err()
			}
			if _, err := node.ZScore(ctx, name, next).Result(); err == nil {
				return 0, fmt.Errorf("%q is in the sorted set already", next)
			} else if !errors.Is(err, goredis.Nil) {
				return 0, err
			}
			p := node.TxPipeline()
			p.ZRem(ctx, name, member)
			p.ZAdd(ctx, name, goredis.Z{Score: score, Member: next})
			_, err = p.Exec(ctx)
			return 1, err
		}
		return w, "Change " + member, nil
	case source.ChangeDelete:
		member, err := one(c.Key, "member")
		if err != nil {
			return nil, "", err
		}
		w := &valueWrite{command: fmt.Sprintf("ZREM %s %s", quote(name), quote(member))}
		w.send = func(ctx context.Context, node writer, name string) (int64, error) {
			return node.ZRem(ctx, name, member).Result()
		}
		return w, "Remove " + member, nil
	}
	return nil, "", fmt.Errorf("a change of unknown kind %d", c.Kind)
}

// streamWrite adds an entry to a stream or takes one out. An entry is
// written once: a stream is a log, and a log that can be rewritten is not one.
func streamWrite(name string, c source.RowChange) (*valueWrite, string, error) {
	switch c.Kind {
	case source.ChangeInsert:
		fields, err := entryFields(c.Values["fields"])
		if err != nil {
			return nil, "", err
		}
		w := &valueWrite{command: fmt.Sprintf("XADD %s * %s", quote(name), pairs(fields))}
		w.send = func(ctx context.Context, node writer, name string) (int64, error) {
			// The server gives the entry its id, which is when it was added
			// and what orders it among the rest.
			if err := node.XAdd(ctx, &goredis.XAddArgs{Stream: name, Values: fields}).Err(); err != nil {
				return 0, err
			}
			return 1, nil
		}
		return w, "Add an entry", nil
	case source.ChangeDelete:
		id, err := one(c.Key, "id")
		if err != nil {
			return nil, "", err
		}
		w := &valueWrite{command: fmt.Sprintf("XDEL %s %s", quote(name), quote(id))}
		w.send = func(ctx context.Context, node writer, name string) (int64, error) {
			return node.XDel(ctx, name, id).Result()
		}
		return w, "Delete the entry " + id, nil
	case source.ChangeUpdate:
		return nil, "", errors.New("a stream's entries are written once: an entry is added or deleted, never changed")
	}
	return nil, "", fmt.Errorf("a change of unknown kind %d", c.Kind)
}

// entryFields reads the fields an entry is to be written with, which are
// given as the one value they are read as.
func entryFields(v any) (map[string]any, error) {
	text := strings.TrimSpace(str(v))
	if text == "" {
		return nil, errors.New("an entry with no fields in it")
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(text), &fields); err != nil {
		return nil, fmt.Errorf("an entry's fields are a JSON object of them: %w", err)
	}
	if len(fields) == 0 {
		return nil, errors.New("an entry with no fields in it")
	}
	return fields, nil
}

// pairs writes an entry's fields as they would be typed, in a settled order
// so that the same change reads the same way twice.
func pairs(fields map[string]any) string {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]string, 0, len(names)*2)
	for _, name := range names {
		out = append(out, quote(name), quote(str(fields[name])))
	}
	return strings.Join(out, " ")
}

// documentWrite sets what a JSON key holds. A document is one value, as a
// string is, so it is set rather than added to or deleted from a row at a
// time.
func documentWrite(name string, c source.RowChange) (*valueWrite, string, error) {
	switch c.Kind {
	case source.ChangeUpdate:
		if _, renamed := c.Values["key"]; renamed {
			return nil, "", errors.New("a key is renamed on its own, not by editing the value it holds")
		}
		v, ok := c.Values["value"]
		if !ok {
			return nil, "", errors.New("an update that changes nothing")
		}
		text := strings.TrimSpace(str(v))
		if !json.Valid([]byte(text)) {
			return nil, "", errors.New("a JSON key holds JSON, and this is not")
		}
		w := &valueWrite{command: fmt.Sprintf("JSON.SET %s $ %s", quote(name), text)}
		w.send = func(ctx context.Context, node writer, name string) (int64, error) {
			return 1, node.JSONSet(ctx, name, "$", text).Err()
		}
		return w, "Set the document " + name, nil
	case source.ChangeInsert, source.ChangeDelete:
		return nil, "", errors.New("a JSON key holds one document: it is set, and the key itself is added or deleted")
	}
	return nil, "", fmt.Errorf("a change of unknown kind %d", c.Kind)
}

// writer is what the commands that change a value run on.
type writer interface {
	keyReader
	HGet(ctx context.Context, key, field string) *goredis.StringCmd
	Set(ctx context.Context, key string, value any, expiration time.Duration) *goredis.StatusCmd
	HSet(ctx context.Context, key string, values ...any) *goredis.IntCmd
	HSetNX(ctx context.Context, key, field string, value any) *goredis.BoolCmd
	HExists(ctx context.Context, key, field string) *goredis.BoolCmd
	HDel(ctx context.Context, key string, fields ...string) *goredis.IntCmd
	RPush(ctx context.Context, key string, values ...any) *goredis.IntCmd
	LSet(ctx context.Context, key string, index int64, value any) *goredis.StatusCmd
	SAdd(ctx context.Context, key string, members ...any) *goredis.IntCmd
	SRem(ctx context.Context, key string, members ...any) *goredis.IntCmd
	SIsMember(ctx context.Context, key string, member any) *goredis.BoolCmd
	ZAddNX(ctx context.Context, key string, members ...goredis.Z) *goredis.IntCmd
	ZAddXX(ctx context.Context, key string, members ...goredis.Z) *goredis.IntCmd
	ZAdd(ctx context.Context, key string, members ...goredis.Z) *goredis.IntCmd
	ZRem(ctx context.Context, key string, members ...any) *goredis.IntCmd
	ZScore(ctx context.Context, key, member string) *goredis.FloatCmd
	XAdd(ctx context.Context, a *goredis.XAddArgs) *goredis.StringCmd
	XDel(ctx context.Context, stream string, ids ...string) *goredis.IntCmd
	JSONSet(ctx context.Context, key, path string, value any) *goredis.StatusCmd
	TxPipeline() goredis.Pipeliner
}

// one is the single value a row is addressed by.
func one(key []any, what string) (string, error) {
	if len(key) != 1 {
		return "", fmt.Errorf("a row is addressed by one %s, and %d values were given", what, len(key))
	}
	if key[0] == nil {
		return "", fmt.Errorf("the %s is empty, which addresses nothing", what)
	}
	return str(key[0]), nil
}

// index is the position a list's element is addressed by.
func index(key []any) (int64, error) {
	s, err := one(key, "position")
	if err != nil {
		return 0, err
	}
	at, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not a position in a list", s)
	}
	return at, nil
}

// str is a value as the server takes it: Redis holds bytes, and everything
// the grid carries is written out as the text of it.
func str(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case []byte:
		return string(x)
	case model.Default, model.Removed:
		return ""
	case float64:
		return number(x)
	case model.Decimal:
		return string(x)
	}
	return fmt.Sprint(v)
}

func isDefault(v any) bool {
	if v == nil {
		return true
	}
	_, ok := v.(model.Default)
	return ok
}

// scoreOf is the number a sorted set orders a member by.
func scoreOf(v any) (float64, error) {
	switch x := v.(type) {
	case nil:
		return 0, nil
	case float64:
		return x, nil
	case int64:
		return float64(x), nil
	case model.Default:
		return 0, nil
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(str(v)), 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not a score", str(v))
	}
	return f, nil
}

// number writes a score as the server does: a whole number without a point.
func number(f float64) string { return strconv.FormatFloat(f, 'g', -1, 64) }

// quote writes a value as it would be typed at redis-cli, where a word with
// nothing surprising in it needs no quotes at all.
func quote(s string) string {
	plain := s != ""
	for _, r := range s {
		if r <= ' ' || r > '~' || strings.ContainsRune(`"'\`, r) {
			plain = false
			break
		}
	}
	if plain {
		return s
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
