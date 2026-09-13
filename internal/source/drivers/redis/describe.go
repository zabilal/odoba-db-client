package redis

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// What a keyspace and a key are made of (FR-2.4, FR-12.2, T2.45).
//
// Redis describes itself in one place: INFO. So the structure of a database
// is what INFO says about the server holding it, under the headings it uses
// and by the names it uses, beside the keys the database itself holds. A key
// is described by asking after it: what it is, how long it has left, how much
// of the server it is using, and how that is being held.

// Describe reads a database's keyspace, or one key.
func (s *redisSource) Describe(ctx context.Context, ref model.ObjectRef) (_ any, err error) {
	defer panics.Recover(&err, "reading the structure")
	switch ref.Kind {
	case model.KindDatabase:
		return s.describeKeyspace(ctx, ref)
	case model.KindKey:
		return s.describeKey(ctx, ref)
	}
	return nil, fmt.Errorf("redis: %s is not something this source describes", ref)
}

// describeKeyspace is a database: how many keys it holds, how many of them
// are going to expire, and what the server is spending.
func (s *redisSource) describeKeyspace(ctx context.Context, ref model.ObjectRef) (*model.Keyspace, error) {
	db, err := databaseOf(ref)
	if err != nil {
		return nil, err
	}
	out := &model.Keyspace{Name: ref.Path[0], Keys: -1, Expiring: -1}
	if n, err := s.Count(ctx, ref, source.BrowseOptions{}); err == nil {
		out.Keys = n
	}
	text, err := s.client.Info(ctx, "everything").Result()
	if err != nil {
		// A server that will not say is not a failure: the keys it holds are
		// still worth showing, and a managed service may allow no more.
		return out, nil
	}
	fields := infoFields(text)
	out.Expiring = expiringIn(fields, db)
	out.Figures = figures(fields)
	if shards, ok := s.shardFigures(ctx); ok {
		out.Figures = append(out.Figures, shards)
	}
	return out, nil
}

// expiringIn is how many of a database's keys have an expiry set, which INFO
// reports as "db0:keys=3,expires=1,avg_ttl=0".
func expiringIn(fields map[string]string, db int) int64 {
	line := fields["db"+strconv.Itoa(db)]
	for _, part := range strings.Split(line, ",") {
		if name, value, ok := strings.Cut(part, "="); ok && strings.TrimSpace(name) == "expires" {
			if n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil {
				return n
			}
		}
	}
	return -1
}

// grouped are the figures a person looks at, under the headings they belong
// to. Every one of them is INFO's own name for it.
var grouped = []struct {
	title string
	names []string
}{
	{"Server", []string{"redis_version", "server_name", "redis_mode", "os", "arch_bits",
		"process_id", "tcp_port", "uptime_in_days", "executable", "config_file"}},
	{"Memory", []string{"used_memory_human", "used_memory_rss_human", "used_memory_peak_human",
		"used_memory_lua_human", "maxmemory_human", "maxmemory_policy", "mem_fragmentation_ratio",
		"mem_allocator", "number_of_cached_scripts"}},
	{"Clients", []string{"connected_clients", "blocked_clients", "cluster_connections", "maxclients"}},
	{"Statistics", []string{"total_connections_received", "total_commands_processed",
		"instantaneous_ops_per_sec", "keyspace_hits", "keyspace_misses", "expired_keys",
		"evicted_keys", "rejected_connections", "total_net_input_bytes", "total_net_output_bytes"}},
	{"Persistence", []string{"loading", "rdb_changes_since_last_save", "rdb_last_save_time",
		"rdb_last_bgsave_status", "aof_enabled", "aof_last_write_status"}},
	{"Replication", []string{"role", "connected_slaves", "master_host", "master_link_status",
		"master_repl_offset"}},
}

// figures picks the numbers worth showing out of INFO, in the order they are
// listed here rather than the order the server happened to print them. A
// figure the server did not report is left out rather than shown as empty.
func figures(fields map[string]string) []model.FigureGroup {
	var out []model.FigureGroup
	for _, g := range grouped {
		group := model.FigureGroup{Title: g.title}
		for _, name := range g.names {
			if value, ok := fields[name]; ok {
				group.Values = append(group.Values, model.Figure{Name: name, Value: value})
			}
		}
		if len(group.Values) > 0 {
			out = append(out, group)
		}
	}
	return out
}

// shardFigures is a cluster's shards and what each holds, which is not in any
// one server's INFO. ok is false for a connection that is not a cluster.
func (s *redisSource) shardFigures(ctx context.Context) (model.FigureGroup, bool) {
	c, cluster := s.client.(*goredis.ClusterClient)
	if !cluster {
		return model.FigureGroup{}, false
	}
	var group model.FigureGroup
	group.Title = "Shards"
	var found []model.Figure
	if err := c.ForEachMaster(ctx, func(ctx context.Context, m *goredis.Client) error {
		n, err := m.DBSize(ctx).Result()
		if err != nil {
			return err
		}
		used := ""
		if text, err := m.Info(ctx, "memory").Result(); err == nil {
			used = infoFields(text)["used_memory_human"]
		}
		found = append(found, model.Figure{Name: m.Options().Addr,
			Value: strings.TrimSpace(fmt.Sprintf("%d keys %s", n, used))})
		return nil
	}); err != nil {
		return model.FigureGroup{}, false
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Name < found[j].Name })
	group.Values = found
	return group, len(found) > 0
}

// describeKey is one key: what it holds, how long it has left, what it costs
// and how the server is holding it.
func (s *redisSource) describeKey(ctx context.Context, ref model.ObjectRef) (*model.StoredKey, error) {
	db, name, err := keyOf(ref)
	if err != nil {
		return nil, err
	}
	nodes, done, err := s.nodes(ctx, db)
	if err != nil {
		return nil, err
	}
	defer done()
	node, ok := nodes[0].(writer)
	if !ok {
		return nil, fmt.Errorf("redis: %T does not read a key", nodes[0])
	}
	if len(nodes) > 1 {
		if node, err = s.shardFor(ctx, name); err != nil {
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
	out := &model.StoredKey{Name: name, Kind: kind, Bytes: -1, Length: -1}
	if ttl, ok := ttlOf(node.TTL(ctx, name)); ok {
		if left, is := ttl.(time.Duration); is {
			out.TTL = left
		}
	}
	// A managed server may refuse MEMORY and OBJECT, and neither is worth
	// failing a panel over: what is not answered is left unsaid.
	if used, err := node.MemoryUsage(ctx, name).Result(); err == nil {
		out.Bytes = used
	}
	if encoding, err := node.ObjectEncoding(ctx, name).Result(); err == nil {
		out.Encoding = encoding
	}
	out.Length = lengthOf(ctx, node, kind, name)
	return out, nil
}

// lengthOf is how much a key holds, in whatever its kind is counted in: a
// string's characters, a hash's fields, a list's elements, a set's or a
// sorted set's members, a stream's entries. -1 where nothing counts it.
func lengthOf(ctx context.Context, node writer, kind, name string) int64 {
	var n int64
	var err error
	switch kind {
	case "string":
		n, err = node.StrLen(ctx, name).Result()
	case "hash":
		n, err = node.HLen(ctx, name).Result()
	case "list":
		n, err = node.LLen(ctx, name).Result()
	case "set":
		n, err = node.SCard(ctx, name).Result()
	case "zset":
		n, err = node.ZCard(ctx, name).Result()
	case "stream":
		n, err = node.XLen(ctx, name).Result()
	default:
		return -1
	}
	if err != nil {
		return -1
	}
	return n
}
