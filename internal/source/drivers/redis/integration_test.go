//go:build conformance

package redis

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Integration tests against a real Redis server (REQ-DRV-1, T2.39). They
// expect the ikigai-redis container on port 56379 and skip if it is not
// running; IKIGAI_REQUIRE_REDIS=1 makes that a failure, as in CI.

func port() int {
	if v := os.Getenv("IKIGAI_REDIS_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			return p
		}
	}
	return 56379
}

// clusterPort is the first of a cluster's shards, which is a container of its
// own: a cluster is not something a single server can be asked to be, and its
// shards must be reachable at the addresses they announce to each other.
func clusterPort() int {
	if v := os.Getenv("IKIGAI_REDIS_CLUSTER_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			return p
		}
	}
	return 7001
}

func liveConfig(db string) source.ConnectionConfig {
	return source.ConnectionConfig{DriverID: driverID, Host: "127.0.0.1", Port: port(), Database: db,
		TLS: source.TLSConfig{Mode: "disable"}}
}

// live connects, or skips where no server is running.
func live(t *testing.T, cfg source.ConnectionConfig) source.Source {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	src, err := Driver{}.Open(ctx, cfg)
	if err != nil {
		if os.Getenv("IKIGAI_REQUIRE_REDIS") != "" {
			t.Fatalf("redis required but unavailable: %v", err)
		}
		t.Skipf("no redis on port %d (docker start ikigai-redis): %v", port(), err)
	}
	t.Cleanup(func() { src.Close() })
	return src
}

// seed fills a database with a key of each kind, some of them expiring. The
// database is emptied first and again afterwards: these tests are the only
// thing in it.
func seed(t *testing.T, src source.Source, db int) {
	t.Helper()
	ctx := context.Background()
	c := conn(t, src, db)
	if err := c.FlushDB(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn(t, src, db).FlushDB(context.Background()) })
	if err := c.Set(ctx, "user:1", "Ada", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.Set(ctx, "user:2", "Grace", time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []*goredis.IntCmd{
		c.HSet(ctx, "user:1:profile", "city", "London"),
		c.RPush(ctx, "queue", "a", "b", "c"),
		c.SAdd(ctx, "tags", "go", "redis"),
		c.ZAdd(ctx, "scores", goredis.Z{Score: 7, Member: "Grace"}),
	} {
		if err := cmd.Err(); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.XAdd(ctx, &goredis.XAddArgs{Stream: "events", Values: map[string]any{"what": "started"}}).Err(); err != nil {
		t.Fatal(err)
	}
}

// conn is a connection of the test's own on one database, for writing what
// the browse then reads.
func conn(t *testing.T, src source.Source, db int) *goredis.Conn {
	t.Helper()
	c := src.(*redisSource).client.(*goredis.Client).Conn()
	t.Cleanup(func() { c.Close() })
	if err := c.Select(context.Background(), db).Err(); err != nil {
		t.Fatal(err)
	}
	return c
}

// read is every row of a browse, by the key's name.
func read(t *testing.T, src source.Source, ref model.ObjectRef, opt source.BrowseOptions) map[string]model.Row {
	t.Helper()
	ctx := context.Background()
	rs, err := src.Browse(ctx, ref, opt)
	if err != nil {
		t.Fatalf("browse: %v", err)
	}
	defer rs.Close()
	out := map[string]model.Row{}
	for {
		row, err := rs.Next(ctx)
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatalf("next: %v", err)
		}
		out[fmt.Sprint(row[0])] = row
	}
}

func TestLiveConnects(t *testing.T) {
	src := live(t, liveConfig(""))
	if err := src.Ping(context.Background()); err != nil {
		t.Errorf("ping: %v", err)
	}
	if err := src.Close(); err != nil {
		t.Errorf("close: %v", err)
	}
	// Closing twice is what a cancelled tab and its owner both do.
	if err := src.Close(); err != nil {
		t.Errorf("closing twice: %v", err)
	}
	if err := src.Ping(context.Background()); err == nil {
		t.Error("a closed connection answered a ping")
	}
}

func TestLiveSaysWhatTheServerIs(t *testing.T) {
	src := live(t, liveConfig(""))
	info, err := src.Info(context.Background())
	if err != nil {
		t.Fatalf("info: %v", err)
	}
	if info.Product == "" || info.Version == "" {
		t.Errorf("the server did not say what it is: %+v", info)
	}
	if info.Latency <= 0 {
		t.Errorf("a round trip took %v", info.Latency)
	}
	if info.Attrs["mode"] != modeStandalone {
		t.Errorf("reached as %q", info.Attrs["mode"])
	}
}

func TestLiveListsTheDatabases(t *testing.T) {
	src := live(t, liveConfig("3"))
	nodes, err := src.Root(context.Background())
	if err != nil {
		t.Fatalf("root: %v", err)
	}
	if len(nodes) < 4 {
		t.Fatalf("databases %v", labels(nodes))
	}
	current := ""
	for _, n := range nodes {
		if n.Ref.Kind != model.KindDatabase || !n.Browsable {
			t.Errorf("%s is %s, opens as %v", n.Label, n.Ref.Kind, n.Browsable)
		}
		if n.Attrs["current"] == "true" {
			current = n.Label
		}
	}
	if current != "db3" {
		t.Errorf("the connection is on %q, and db3 was asked for", current)
	}
}

func TestLiveSaysWhatIsWrongWithAConnection(t *testing.T) {
	live(t, liveConfig("")) // skips where no server runs
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// A password where the server wants none is refused by the server, and
	// read as what it is rather than as an unknown failure.
	withPassword := liveConfig("")
	withPassword.Secret = func(string) (string, error) { return "hunter2", nil }
	withPassword.User = "ada"
	if _, err := (Driver{}).Open(ctx, withPassword); kind(err) != source.ConnectAuth {
		t.Errorf("a credential the server does not want: %v", err)
	}

	shut := liveConfig("")
	shut.Port = 1 // nothing listens there
	if _, err := (Driver{}).Open(ctx, shut); kind(err) != source.ConnectRefused &&
		kind(err) != source.ConnectUnreachable {
		t.Errorf("a port nothing listens on: %v", err)
	}

	nowhere := liveConfig("")
	nowhere.Host = "no-such-host.invalid"
	if _, err := (Driver{}).Open(ctx, nowhere); kind(err) != source.ConnectUnreachable {
		t.Errorf("a host that does not resolve: %v", err)
	}

	notACluster := liveConfig("")
	notACluster.Params = map[string]string{"mode": modeCluster}
	if _, err := (Driver{}).Open(ctx, notACluster); err == nil {
		t.Error("a single server answered as a cluster")
	}
}

func TestLiveSaysWhatIsNotWrittenYet(t *testing.T) {
	src := live(t, liveConfig(""))
	ctx := context.Background()
	db := model.NewRef(model.KindDatabase, "db0")
	// A database holds keys, and keys are rows: there is nothing under it.
	if nodes, err := src.Children(ctx, db); err != nil || len(nodes) != 0 {
		t.Errorf("under a database: %v %v", labels(nodes), err)
	}
	if _, err := src.Describe(ctx, db); !errors.Is(err, errNotYet) {
		t.Errorf("describing: %v", err)
	}
}

func TestLiveWalksAKeyspace(t *testing.T) {
	src := live(t, liveConfig("1"))
	seed(t, src, 1)
	db := model.NewRef(model.KindDatabase, "db1")
	rows := read(t, src, db, source.BrowseOptions{})
	if len(rows) != 7 {
		t.Fatalf("keys %v", names(rows))
	}
	// A key is told from another by its name, which is the only thing that
	// addresses a value in Redis.
	rs, err := src.Browse(context.Background(), db, source.BrowseOptions{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	if id := model.IdentityOf(rs); id.Kind != model.IdentityKeyName ||
		len(id.Columns) != 1 || id.Columns[0] != "key" || !id.Target.Equal(db) {
		t.Errorf("identity %+v", id)
	}
	kinds := map[string]string{"user:1": "string", "user:2": "string", "user:1:profile": "hash",
		"queue": "list", "tags": "set", "scores": "zset", "events": "stream"}
	for name, kind := range kinds {
		row := rows[name]
		if row == nil {
			t.Errorf("no %s among %v", name, names(rows))
			continue
		}
		if row[1] != kind {
			t.Errorf("%s holds a %v, want a %s", name, row[1], kind)
		}
	}
	// A key that will not expire has no time to live, and one that will says
	// how much of it is left.
	if rows["user:1"][2] != nil {
		t.Errorf("a key that never expires has %v left", rows["user:1"][2])
	}
	left, ok := rows["user:2"][2].(time.Duration)
	if !ok || left <= 0 || left > time.Hour {
		t.Errorf("an hour from now is %v", rows["user:2"][2])
	}
}

func TestLiveMatchesNamesAndKindsOnTheServer(t *testing.T) {
	src := live(t, liveConfig("1"))
	seed(t, src, 1)
	db := model.NewRef(model.KindDatabase, "db1")
	cases := []struct {
		what string
		opt  source.BrowseOptions
		want []string
	}{
		{"a pattern", source.BrowseOptions{Filters: []source.Filter{
			{Column: "key", Op: source.OpLike, Values: []any{"user:%"}}}},
			[]string{"user:1", "user:2", "user:1:profile"}},
		{"a name outright", source.BrowseOptions{Filters: []source.Filter{
			{Column: "key", Op: source.OpEqual, Values: []any{"user:1"}}}}, []string{"user:1"}},
		{"a name within", source.BrowseOptions{Filters: []source.Filter{
			{Column: "key", Op: source.OpContains, Values: []any{"ue"}}}}, []string{"queue"}},
		{"a kind", source.BrowseOptions{Filters: []source.Filter{
			{Column: "type", Op: source.OpEqual, Values: []any{"string"}}}}, []string{"user:1", "user:2"}},
		{"both at once", source.BrowseOptions{Filters: []source.Filter{
			{Column: "key", Op: source.OpLike, Values: []any{"user:%"}},
			{Column: "type", Op: source.OpEqual, Values: []any{"hash"}}}}, []string{"user:1:profile"}},
		{"a pattern matching nothing", source.BrowseOptions{Filters: []source.Filter{
			{Column: "key", Op: source.OpLike, Values: []any{"nobody:%"}}}}, nil},
	}
	for _, c := range cases {
		rows := read(t, src, db, c.opt)
		if len(rows) != len(c.want) {
			t.Errorf("%s: %v, want %v", c.what, names(rows), c.want)
			continue
		}
		for _, name := range c.want {
			if rows[name] == nil {
				t.Errorf("%s: %v, want %v", c.what, names(rows), c.want)
			}
		}
		// What the server matched, it counted the same way.
		n, err := src.(source.Countable).Count(context.Background(), db, c.opt)
		if err != nil || n != int64(len(c.want)) {
			t.Errorf("%s counted %d: %v", c.what, n, err)
		}
	}
}

func TestLiveReadsAPageAtATime(t *testing.T) {
	src := live(t, liveConfig("1"))
	seed(t, src, 1)
	db := model.NewRef(model.KindDatabase, "db1")
	seen := map[string]bool{}
	for offset := int64(0); offset < 8; offset += 3 {
		rows := read(t, src, db, source.BrowseOptions{Offset: offset, Limit: 3})
		if offset < 6 && len(rows) != 3 || offset == 6 && len(rows) != 1 {
			t.Errorf("the page at %d holds %v", offset, names(rows))
		}
		for name := range rows {
			if seen[name] {
				t.Errorf("%s was on two pages", name)
			}
			seen[name] = true
		}
	}
	if len(seen) != 7 {
		t.Errorf("the pages together held %d keys", len(seen))
	}
	// Asked for one column, a browse reads one: nothing is asked of the
	// server about a key that is not being shown.
	rows := read(t, src, db, source.BrowseOptions{Columns: []string{"key"}, Limit: 100})
	for name, row := range rows {
		if len(row) != 1 {
			t.Errorf("%s came back as %v", name, row)
		}
	}
}

func TestLiveCountsAndBadgesADatabase(t *testing.T) {
	src := live(t, liveConfig("1"))
	seed(t, src, 1)
	db := model.NewRef(model.KindDatabase, "db1")
	ctx := context.Background()
	n, err := src.(source.Countable).Count(ctx, db, source.BrowseOptions{})
	if err != nil || n != 7 {
		t.Errorf("count = %d, %v", n, err)
	}
	badge, ok, err := src.Badge(ctx, db)
	if err != nil || !ok || badge.Text != "7" || !badge.Exact {
		t.Errorf("badge = %+v, %v, %v", badge, ok, err)
	}
	// A database with nothing in it has a badge all the same, and it says so.
	empty, ok, err := src.Badge(ctx, model.NewRef(model.KindDatabase, "db9"))
	if err != nil || !ok || empty.Text != "0" {
		t.Errorf("an empty database: %+v, %v, %v", empty, ok, err)
	}
	// Nothing but a database has one.
	if _, ok, err := src.Badge(ctx, model.NewRef(model.KindKey, "db1", "user:1")); ok || err != nil {
		t.Errorf("a key's badge: %v %v", ok, err)
	}
}

func TestLiveReadsTheDatabaseItIsAskedFor(t *testing.T) {
	// The connection is on db1; db2 is another keyspace, and reading it must
	// not move the connection or the pool's other connections.
	src := live(t, liveConfig("1"))
	seed(t, src, 1)
	ctx := context.Background()
	other := conn(t, src, 2)
	if err := other.FlushDB(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn(t, src, 2).FlushDB(context.Background()) })
	if err := other.Set(ctx, "elsewhere", "x", 0).Err(); err != nil {
		t.Fatal(err)
	}
	rows := read(t, src, model.NewRef(model.KindDatabase, "db2"), source.BrowseOptions{})
	if len(rows) != 1 || rows["elsewhere"] == nil {
		t.Errorf("db2 holds %v", names(rows))
	}
	if rows := read(t, src, model.NewRef(model.KindDatabase, "db1"), source.BrowseOptions{}); len(rows) != 7 {
		t.Errorf("db1 holds %v after reading db2", names(rows))
	}
}

func TestLiveRefusesWhatTheServerCannotDo(t *testing.T) {
	src := live(t, liveConfig("1"))
	db := model.NewRef(model.KindDatabase, "db1")
	ctx := context.Background()
	refused := map[string]source.BrowseOptions{
		"an order the keyspace has not": {Sorts: []source.Sort{{Column: "key"}}},
		"a condition with no language":  {Where: "key = 'user:1'"},
		"a column a key has not got":    {Filters: []source.Filter{{Column: "value", Op: source.OpEqual, Values: []any{"x"}}}},
		"a column that is not shown":    {Columns: []string{"value"}},
	}
	for what, opt := range refused {
		rs, err := src.Browse(ctx, db, opt)
		if err == nil {
			rs.Close()
			t.Errorf("%s: accepted", what)
		}
	}
	if _, err := src.Browse(ctx, model.NewRef(model.KindTable, "db1"), source.BrowseOptions{}); err == nil {
		t.Error("a table was browsed on a server that has none")
	}
}

func TestLiveReadingStopsWhenItIsCancelled(t *testing.T) {
	src := live(t, liveConfig("1"))
	seed(t, src, 1)
	ctx, cancel := context.WithCancel(context.Background())
	rs, err := src.Browse(ctx, model.NewRef(model.KindDatabase, "db1"), source.BrowseOptions{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	if _, err := rs.Next(ctx); err != nil {
		t.Fatalf("the first key: %v", err)
	}
	cancel()
	if _, err := rs.Next(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled read answered %v", err)
	}
}

func names(rows map[string]model.Row) []string {
	out := make([]string, 0, len(rows))
	for name := range rows {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func TestLiveWalksAClustersKeyspace(t *testing.T) {
	cfg := source.ConnectionConfig{DriverID: driverID, Host: "127.0.0.1", Port: clusterPort(),
		TLS: source.TLSConfig{Mode: "disable"}, Params: map[string]string{"mode": modeCluster}}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	src, err := Driver{}.Open(ctx, cfg)
	if err != nil {
		if os.Getenv("IKIGAI_REQUIRE_REDIS_CLUSTER") != "" {
			t.Fatalf("a redis cluster is required but unavailable: %v", err)
		}
		t.Skipf("no redis cluster on port %d (docker start ikigai-redis-cluster): %v", clusterPort(), err)
	}
	defer src.Close()

	// A cluster shares one keyspace out between its shards, and has no
	// numbered databases to choose between.
	if src.Capabilities().Structure.MultipleDatabases {
		t.Error("a cluster claims databases it has not got")
	}
	nodes, err := src.Root(ctx)
	if err != nil || len(nodes) != 1 || nodes[0].Label != "db0" {
		t.Fatalf("root: %v %v", labels(nodes), err)
	}
	c := src.(*redisSource).client.(*goredis.ClusterClient)
	// Names that hash into every shard's slots, so the walk has to reach all
	// three rather than answering from whichever it began with. alpha is in
	// the first shard's slots, user:1 and user:2 the second's, the rest the
	// third's.
	written := []string{"alpha", "user:1", "user:2", "queue", "tags", "scores"}
	for _, name := range written {
		if err := c.Set(ctx, name, "x", 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, name := range written {
			c.Del(context.Background(), name)
		}
	})
	rows := read(t, src, nodes[0].Ref, source.BrowseOptions{Limit: 100})
	for _, name := range written {
		if rows[name] == nil {
			t.Errorf("%s is not among %v", name, names(rows))
		}
	}
	if n, err := src.(source.Countable).Count(ctx, nodes[0].Ref, source.BrowseOptions{}); err != nil || n < int64(len(written)) {
		t.Errorf("the cluster holds %d keys: %v", n, err)
	}
	// Everything the shards hold is walked, and each is asked about its own
	// keys: a pattern narrows it the same way.
	pattern := read(t, src, nodes[0].Ref, source.BrowseOptions{Limit: 100, Filters: []source.Filter{
		{Column: "key", Op: source.OpLike, Values: []any{"user:%"}}}})
	if len(pattern) != 2 || pattern["user:1"] == nil || pattern["user:2"] == nil {
		t.Errorf("the keys matching user:%% are %v", names(pattern))
	}
	if _, err := src.Browse(ctx, model.NewRef(model.KindDatabase, "db1"), source.BrowseOptions{}); err == nil {
		t.Error("a cluster answered about a numbered database")
	}
}

func TestLiveWalksPastTheKeysBeforeThePage(t *testing.T) {
	src := live(t, liveConfig("1"))
	ctx := context.Background()
	c := conn(t, src, 3)
	if err := c.FlushDB(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn(t, src, 3).FlushDB(context.Background()) })
	// More keys than the server walks past in one answer, so a page that
	// begins after the first piece has to walk over the whole of it.
	p := c.Pipeline()
	for i := 0; i < 250; i++ {
		p.Set(ctx, fmt.Sprintf("k:%03d", i), i, 0)
	}
	if _, err := p.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	db := model.NewRef(model.KindDatabase, "db3")
	seen := map[string]bool{}
	for offset := int64(0); offset < 250; offset += 50 {
		rows := read(t, src, db, source.BrowseOptions{Offset: offset, Limit: 50})
		if len(rows) != 50 {
			t.Fatalf("the page at %d holds %d keys", offset, len(rows))
		}
		for name := range rows {
			seen[name] = true
		}
	}
	if len(seen) != 250 {
		t.Errorf("the pages together held %d keys of 250", len(seen))
	}
}

func TestLiveLeavesTheConnectionOnItsOwnDatabase(t *testing.T) {
	src := live(t, liveConfig("1"))
	ctx := context.Background()
	// A connection let go of goes back to the pool with the database it was
	// last told to use, and the next question asked on it would be about
	// whichever keyspace that was.
	read(t, src, model.NewRef(model.KindDatabase, "db4"), source.BrowseOptions{})
	c := src.(*redisSource).client.(*goredis.Client).Conn()
	defer c.Close()
	info, err := c.Do(ctx, "client", "info").Text()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(info, "db=1") {
		t.Errorf("the connection came back on another database: %s", strings.TrimSpace(info))
	}
}
