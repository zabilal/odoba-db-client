package redis

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Editing a database's keys (FR-12.2, T2.43).
//
// The keyspace browses as its keys, and those rows are editable in the two
// ways a key itself can be changed without touching what it holds: how long
// it has left, and what it is called. A key is deleted here too — it is the
// only place a whole key can be — and never added, because a key comes into
// being when something is written to it and not before.

// keyspaceWrite renders one change to a row of a database's keyspace, on a
// cluster where cluster is set.
func keyspaceWrite(c source.RowChange, cluster bool) (*valueWrite, string, error) {
	switch c.Kind {
	case source.ChangeInsert:
		return nil, "", errors.New("a key comes into being when something is written to it: add it by writing a value, not a row")
	case source.ChangeDelete:
		name, err := one(c.Key, "key")
		if err != nil {
			return nil, "", err
		}
		w := &valueWrite{command: fmt.Sprintf("DEL %s", quote(name))}
		w.send = func(ctx context.Context, node writer, _ string) (int64, error) {
			return node.Del(ctx, name).Result()
		}
		return w, "Delete the key " + name, nil
	case source.ChangeUpdate:
		name, err := one(c.Key, "key")
		if err != nil {
			return nil, "", err
		}
		if _, changed := c.Values["type"]; changed {
			return nil, "", errors.New("a key is of the kind of what it holds, so its kind is not something to type over")
		}
		renamed, isRenamed := c.Values["key"]
		ttl, expires := c.Values["ttl"]
		switch {
		case isRenamed && expires:
			// Two commands would be two changes, and a plan says what each
			// of its changes does.
			return nil, "", errors.New("a key is renamed or its time to live is set, one change at a time")
		case isRenamed:
			return renameWrite(name, str(renamed), cluster)
		case expires:
			return expiryWrite(name, ttl)
		}
		return nil, "", errors.New("an update that changes nothing")
	}
	return nil, "", fmt.Errorf("a change of unknown kind %d", c.Kind)
}

// renameWrite gives a key another name. RENAMENX rather than RENAME: RENAME
// would delete whatever was there under the new name, which nobody typing a
// name into a grid is asking for.
//
// A cluster renames a key only within its hash slot, and refuses two names in
// different slots with CROSSSLOT. That is refused here, before anything is
// sent, in words that say what would work: moving a key between shards is
// not a rename but a copy and a delete, which is not what was asked.
func renameWrite(name, to string, cluster bool) (*valueWrite, string, error) {
	if strings.TrimSpace(to) == "" {
		return nil, "", errors.New("a key with no name addresses nothing")
	}
	if to == name {
		return nil, "", errors.New("an update that changes nothing")
	}
	if cluster && slot(name) != slot(to) {
		return nil, "", fmt.Errorf("a cluster renames a key only within its hash slot, and %s and %s are in different ones; "+
			"names that share a {tag} share a slot", name, to)
	}
	w := &valueWrite{command: fmt.Sprintf("RENAMENX %s %s", quote(name), quote(to))}
	w.send = func(ctx context.Context, node writer, _ string) (int64, error) {
		free, err := node.RenameNX(ctx, name, to).Result()
		if isNoSuchKey(err) {
			return 0, nil // the key has gone since it was read
		}
		if err != nil {
			return 0, err
		}
		if !free {
			return 0, fmt.Errorf("there is a key called %q already", to)
		}
		return 1, nil
	}
	return w, fmt.Sprintf("Rename %s to %s", name, to), nil
}

// expiryWrite sets how long a key has left, or takes its expiry away.
func expiryWrite(name string, v any) (*valueWrite, string, error) {
	left, err := ttlValue(v)
	if err != nil {
		return nil, "", err
	}
	if left <= 0 {
		// A key is deleted by deleting its row, which says what it does. An
		// expiry of nothing would delete it and read as an edit.
		w := &valueWrite{command: fmt.Sprintf("PERSIST %s", quote(name))}
		w.send = func(ctx context.Context, node writer, _ string) (int64, error) {
			// PERSIST answers 0 both for a key that is not there and for one
			// with no expiry to take away, so the key is asked after first:
			// making a key that never expires never expire is no failure.
			there, err := node.Exists(ctx, name).Result()
			if err != nil || there == 0 {
				return 0, err
			}
			return 1, node.Persist(ctx, name).Err()
		}
		return w, name + " never expires", nil
	}
	w := &valueWrite{command: fmt.Sprintf("PEXPIRE %s %d", quote(name), left.Milliseconds())}
	w.send = func(ctx context.Context, node writer, _ string) (int64, error) {
		set, err := node.PExpire(ctx, name, left).Result()
		if err != nil || !set {
			return 0, err
		}
		return 1, nil
	}
	return w, fmt.Sprintf("%s expires in %s", name, left), nil
}

// ttlValue reads how long a key is to have left. Nothing at all — an empty
// cell, a value taken away — is a key that never expires; a bare number is
// seconds, as the server counts them, and anything else is a length of time
// as it is written and read ("90s", "36h", "1h30m").
func ttlValue(v any) (time.Duration, error) {
	switch x := v.(type) {
	case nil, model.Removed, model.Default:
		return 0, nil
	case time.Duration:
		return x, nil
	case int64:
		return time.Duration(x) * time.Second, nil
	case float64:
		return time.Duration(x * float64(time.Second)), nil
	}
	text := strings.TrimSpace(str(v))
	if text == "" {
		return 0, nil
	}
	if n, err := strconv.ParseFloat(text, 64); err == nil {
		return time.Duration(n * float64(time.Second)), nil
	}
	d, err := time.ParseDuration(text)
	if err != nil {
		return 0, fmt.Errorf("%q is not a length of time, such as 30m, 12h or 3600", text)
	}
	return d, nil
}

// slot is the hash slot a cluster keeps a key in: the CRC16 of its name, or
// of the part between its first { and the } after it where that part is not
// empty, modulo 16384 (the Redis cluster specification).
func slot(name string) uint16 {
	if open := strings.IndexByte(name, '{'); open >= 0 {
		if end := strings.IndexByte(name[open+1:], '}'); end > 0 {
			name = name[open+1 : open+1+end]
		}
	}
	var crc uint16
	for i := 0; i < len(name); i++ {
		crc ^= uint16(name[i]) << 8
		for bit := 0; bit < 8; bit++ {
			if crc&0x8000 != 0 {
				crc = crc<<1 ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return crc & (16384 - 1)
}

// isNoSuchKey reports the server refusing a command because the key it names
// is not there, which is a row changed since it was read rather than a
// failure of the connection.
func isNoSuchKey(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "no such key")
}

// keyspaceOf reads the database a keyspace changeset is about.
func keyspaceOf(ref model.ObjectRef) (int, error) { return databaseOf(ref) }

// keyspaceNode is what a keyspace's changes run on.
//
// A cluster is answered by the cluster itself: each of these commands names
// the key it is about, so the client sends it to the shard that holds it —
// where a walk of the keyspace has to visit every shard in turn, a change to
// one key has one place to go.
func (s *redisSource) keyspaceNode(ctx context.Context, ref model.ObjectRef) (writer, func(), error) {
	db, err := keyspaceOf(ref)
	if err != nil {
		return nil, nil, err
	}
	if c, cluster := s.client.(*goredis.ClusterClient); cluster {
		if db != 0 {
			return nil, nil, errors.New("redis: a cluster has one keyspace, and no numbered databases in it")
		}
		return c, func() {}, nil
	}
	nodes, done, err := s.nodes(ctx, db)
	if err != nil {
		return nil, nil, err
	}
	node, ok := nodes[0].(writer)
	if !ok {
		done()
		return nil, nil, fmt.Errorf("redis: %T does not write a key", nodes[0])
	}
	return node, done, nil
}
