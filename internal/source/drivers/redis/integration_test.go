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
	"github.com/ikigai-db/ikigai-db/internal/source/sqlscript"
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
	walked := read(t, src, nodes[0].Ref, source.BrowseOptions{Limit: 100})
	for _, name := range written {
		if walked[name] == nil {
			t.Errorf("%s is not among %v", name, names(walked))
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

	// What a key holds is read from the shard that holds the key, whichever
	// shard the connection began with.
	if err := c.HSet(ctx, "queue:fields", "city", "London").Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Del(context.Background(), "queue:fields") })
	held, cols := rows(t, src, model.NewRef(model.KindKey, "db0", "queue:fields"), source.BrowseOptions{})
	if len(cols) != 2 || len(held) != 1 || held[0][0] != "city" || held[0][1] != "London" {
		t.Errorf("the hash on its shard holds %v as %+v", held, cols)
	}
	if n, err := src.(source.Countable).Count(ctx, model.NewRef(model.KindKey, "db0", "queue:fields"),
		source.BrowseOptions{}); err != nil || n != 1 {
		t.Errorf("the hash holds %d rows: %v", n, err)
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

// valueRef is a key of the seeded database, as a browse addresses it.
func valueRef(name string) model.ObjectRef { return model.NewRef(model.KindKey, "db1", name) }

// rows is every row of a browse, in the order it gave them.
func rows(t *testing.T, src source.Source, ref model.ObjectRef, opt source.BrowseOptions) ([]model.Row, []model.ColumnDef) {
	t.Helper()
	ctx := context.Background()
	rs, err := src.Browse(ctx, ref, opt)
	if err != nil {
		t.Fatalf("browse %s: %v", ref, err)
	}
	defer rs.Close()
	var out []model.Row
	for {
		row, err := rs.Next(ctx)
		if errors.Is(err, io.EOF) {
			return out, rs.Columns()
		}
		if err != nil {
			t.Fatalf("next: %v", err)
		}
		out = append(out, row)
	}
}

func TestLiveReadsWhatEachKindOfKeyHolds(t *testing.T) {
	src := live(t, liveConfig("1"))
	seed(t, src, 1)
	ctx := context.Background()

	str, cols := rows(t, src, valueRef("user:1"), source.BrowseOptions{})
	if len(cols) != 2 || cols[0].Name != "key" || cols[1].Name != "value" || !cols[0].ReadOnly {
		t.Errorf("a string shows %+v", cols)
	}
	if len(str) != 1 || str[0][0] != "user:1" || str[0][1] != "Ada" {
		t.Errorf("a string holds %v", str)
	}

	hash, cols := rows(t, src, valueRef("user:1:profile"), source.BrowseOptions{})
	if len(cols) != 2 || cols[0].Name != "field" || len(hash) != 1 || hash[0][0] != "city" || hash[0][1] != "London" {
		t.Errorf("a hash holds %v as %+v", hash, cols)
	}

	list, cols := rows(t, src, valueRef("queue"), source.BrowseOptions{})
	if len(cols) != 2 || cols[0].Name != "index" || len(list) != 3 {
		t.Fatalf("a list holds %v as %+v", list, cols)
	}
	for i, want := range []string{"a", "b", "c"} {
		if list[i][0] != int64(i) || list[i][1] != want {
			t.Errorf("the element at %d is %v", i, list[i])
		}
	}

	set, cols := rows(t, src, valueRef("tags"), source.BrowseOptions{})
	if len(cols) != 1 || cols[0].Name != "member" || len(set) != 2 {
		t.Errorf("a set holds %v as %+v", set, cols)
	}

	zset, cols := rows(t, src, valueRef("scores"), source.BrowseOptions{})
	if len(cols) != 2 || cols[1].Name != "score" || len(zset) != 1 {
		t.Fatalf("a sorted set holds %v as %+v", zset, cols)
	}
	if zset[0][0] != "Grace" || zset[0][1] != 7.0 {
		t.Errorf("the member is %v", zset[0])
	}

	// A row is addressed by the part of the value the server addresses it by.
	rs, err := src.Browse(ctx, valueRef("user:1:profile"), source.BrowseOptions{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	if id := model.IdentityOf(rs); id.Kind != model.IdentityKeyName || len(id.Columns) != 1 || id.Columns[0] != "field" {
		t.Errorf("identity %+v", id)
	}

	// What is not text is not shown as mangled letters.
	if err := conn(t, src, 1).Set(ctx, "raw", string([]byte{0xff, 0xfe, 0x00}), 0).Err(); err != nil {
		t.Fatal(err)
	}
	raw, _ := rows(t, src, valueRef("raw"), source.BrowseOptions{})
	if len(raw) != 1 {
		t.Fatalf("a binary string holds %v", raw)
	}
	if b, ok := raw[0][1].([]byte); !ok || len(b) != 3 {
		t.Errorf("bytes came back as %T %v", raw[0][1], raw[0][1])
	}
}

func TestLiveCountsAndPagesWhatAKeyHolds(t *testing.T) {
	src := live(t, liveConfig("1"))
	seed(t, src, 1)
	ctx := context.Background()
	counts := map[string]int64{"user:1": 1, "user:1:profile": 1, "queue": 3, "tags": 2, "scores": 1}
	for name, want := range counts {
		n, err := src.(source.Countable).Count(ctx, valueRef(name), source.BrowseOptions{})
		if err != nil || n != want {
			t.Errorf("%s holds %d rows: %v", name, n, err)
		}
	}
	// A list is read by position: the page is asked for outright.
	page, _ := rows(t, src, valueRef("queue"), source.BrowseOptions{Offset: 1, Limit: 1})
	if len(page) != 1 || page[0][0] != int64(1) || page[0][1] != "b" {
		t.Errorf("the second element is %v", page)
	}
	// A hash's fields are matched by the server while it walks them.
	if err := conn(t, src, 1).HSet(ctx, "user:1:profile", "country", "England", "county", "Middlesex").Err(); err != nil {
		t.Fatal(err)
	}
	matched, _ := rows(t, src, valueRef("user:1:profile"), source.BrowseOptions{
		Filters: []source.Filter{{Column: "field", Op: source.OpLike, Values: []any{"c%"}}}})
	if len(matched) != 3 {
		t.Errorf("the fields beginning with c are %v", matched)
	}
	// A hash is walked a piece at a time, and more of it than the server
	// hands back at once, so a later page has to walk over the pieces before
	// it and stop where it was told to.
	fields := make([]any, 0, 4000)
	for i := 0; i < 2000; i++ {
		fields = append(fields, fmt.Sprintf("f%04d", i), i)
	}
	if err := conn(t, src, 1).HSet(ctx, "big", fields...).Err(); err != nil {
		t.Fatal(err)
	}
	// The walk of an unchanging hash is the same walk every time, so a page
	// is the part of it that begins where the page does.
	all, _ := rows(t, src, valueRef("big"), source.BrowseOptions{Limit: 2000})
	if len(all) != 2000 {
		t.Fatalf("the hash walked to %d fields", len(all))
	}
	for _, offset := range []int64{0, 537, 1507} {
		page, _ := rows(t, src, valueRef("big"), source.BrowseOptions{Offset: offset, Limit: 50})
		if len(page) != 50 {
			t.Fatalf("the page at %d holds %d fields", offset, len(page))
		}
		for i, row := range page {
			if want := all[offset+int64(i)][0]; row[0] != want {
				t.Fatalf("the page at %d holds %v where the walk holds %v", offset, row[0], want)
			}
		}
	}

	// What a pattern picks out is only known by walking, so the count says
	// it does not know rather than guessing.
	n, err := src.(source.Countable).Count(ctx, valueRef("user:1:profile"), source.BrowseOptions{
		Filters: []source.Filter{{Column: "field", Op: source.OpLike, Values: []any{"c%"}}}})
	if err != nil || n != -1 {
		t.Errorf("a narrowed count is %d: %v", n, err)
	}
}

func TestLiveReadingAValueStopsWhenItIsCancelled(t *testing.T) {
	src := live(t, liveConfig("1"))
	seed(t, src, 1)
	ctx, cancel := context.WithCancel(context.Background())
	rs, err := src.Browse(ctx, valueRef("queue"), source.BrowseOptions{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	if _, err := rs.Next(ctx); err != nil {
		t.Fatalf("the first element: %v", err)
	}
	cancel()
	if _, err := rs.Next(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled read answered %v", err)
	}
}

func TestLiveRefusesWhatAValueCannotBeAsked(t *testing.T) {
	src := live(t, liveConfig("1"))
	seed(t, src, 1)
	ctx := context.Background()
	refused := map[string]struct {
		ref model.ObjectRef
		opt source.BrowseOptions
	}{
		"an order the server does not hold": {valueRef("user:1:profile"), source.BrowseOptions{
			Sorts: []source.Sort{{Column: "field"}}}},
		"a condition with no language": {valueRef("user:1:profile"), source.BrowseOptions{Where: "field = 'city'"}},
		"part of a list": {valueRef("queue"), source.BrowseOptions{
			Filters: []source.Filter{{Column: "value", Op: source.OpEqual, Values: []any{"a"}}}}},
		"part of a string": {valueRef("user:1"), source.BrowseOptions{
			Filters: []source.Filter{{Column: "value", Op: source.OpEqual, Values: []any{"Ada"}}}}},
		"anything but the part a row is known by": {valueRef("scores"), source.BrowseOptions{
			Filters: []source.Filter{{Column: "score", Op: source.OpGreater, Values: []any{1}}}}},
		"a key that is not there":   {valueRef("nobody"), source.BrowseOptions{}},
		"a key with no name at all": {model.NewRef(model.KindKey, "db1", ""), source.BrowseOptions{}},
		"a kind nothing here reads": {valueRef("events"), source.BrowseOptions{}},
	}
	for what, c := range refused {
		rs, err := src.Browse(ctx, c.ref, c.opt)
		if err == nil {
			rs.Close()
			t.Errorf("%s: accepted", what)
		}
	}
	// A key that is not there is said to be that, by name, rather than read
	// as a value of some kind nothing knows.
	if _, err := src.Browse(ctx, valueRef("nobody"), source.BrowseOptions{}); err == nil ||
		!strings.Contains(err.Error(), "nobody") {
		t.Errorf("a key that is not there: %v", err)
	}

	// The kind that is not read yet says which task brings it.
	if _, err := src.Browse(ctx, valueRef("events"), source.BrowseOptions{}); !errors.Is(err, errNotYet) {
		t.Errorf("a stream: %v", err)
	}
}

// change plans and applies one change to a key, and says what came of it.
func change(t *testing.T, src source.Source, ref model.ObjectRef, id []string, changes ...source.RowChange) *source.WriteOutcome {
	t.Helper()
	ctx := context.Background()
	w := src.(source.Writer)
	plan, err := w.Plan(ctx, source.Changeset{Target: ref,
		Identity: model.RowIdentity{Kind: model.IdentityKeyName, Columns: id, Target: ref}, Changes: changes})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.Statements) != len(changes) || len(plan.Descriptions) != len(changes) {
		t.Fatalf("a plan of %d statements for %d changes", len(plan.Statements), len(changes))
	}
	if plan.Atomic {
		t.Error("a plan promises a transaction Redis has not got here")
	}
	out, err := w.Apply(ctx, plan)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	return out
}

func TestLiveWritesWhatEachKindOfKeyHolds(t *testing.T) {
	src := live(t, liveConfig("1"))
	seed(t, src, 1)
	ctx := context.Background()
	c := conn(t, src, 1)

	// A string is set, and its expiry is left alone.
	if out := change(t, src, valueRef("user:2"), []string{"key"}, source.RowChange{
		Kind: source.ChangeUpdate, Key: []any{"user:2"}, Values: map[string]any{"value": "Grace Hopper"}}); out.Err != nil {
		t.Fatalf("setting a string: %+v", out)
	}
	if v, err := c.Get(ctx, "user:2").Result(); err != nil || v != "Grace Hopper" {
		t.Errorf("the string holds %q: %v", v, err)
	}
	if ttl, err := c.TTL(ctx, "user:2").Result(); err != nil || ttl <= 0 {
		t.Errorf("setting a value took its expiry away: %v %v", ttl, err)
	}

	// A hash's field is added, changed, renamed and deleted.
	hash := valueRef("user:1:profile")
	if out := change(t, src, hash, []string{"field"}, source.RowChange{
		Kind: source.ChangeInsert, Values: map[string]any{"field": "country", "value": "England"}}); out.Err != nil {
		t.Fatalf("adding a field: %+v", out)
	}
	if out := change(t, src, hash, []string{"field"}, source.RowChange{
		Kind: source.ChangeUpdate, Key: []any{"city"}, Values: map[string]any{"value": "Paris"}}); out.Err != nil {
		t.Fatalf("changing a field: %+v", out)
	}
	if out := change(t, src, hash, []string{"field"}, source.RowChange{
		Kind: source.ChangeUpdate, Key: []any{"city"}, Values: map[string]any{"field": "town"}}); out.Err != nil {
		t.Fatalf("renaming a field: %+v", out)
	}
	held, err := c.HGetAll(ctx, "user:1:profile").Result()
	if err != nil || held["town"] != "Paris" || held["country"] != "England" || len(held) != 2 {
		t.Errorf("the hash holds %v: %v", held, err)
	}
	if out := change(t, src, hash, []string{"field"}, source.RowChange{
		Kind: source.ChangeDelete, Key: []any{"country"}}); out.Err != nil {
		t.Fatalf("deleting a field: %+v", out)
	}

	// A field changed and then moved in the same plan carries what it holds
	// by then, and the plan says what it will write.
	moved, err := src.(source.Writer).Plan(ctx, source.Changeset{Target: hash,
		Identity: model.RowIdentity{Kind: model.IdentityKeyName, Columns: []string{"field"}, Target: hash},
		Changes: []source.RowChange{
			{Kind: source.ChangeUpdate, Key: []any{"town"}, Values: map[string]any{"value": "Lyon"}},
			{Kind: source.ChangeUpdate, Key: []any{"town"}, Values: map[string]any{"field": "city"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(moved.Statements[1].SQL, "Paris") {
		t.Errorf("the plan says %q, and the field holds Paris", moved.Statements[1].SQL)
	}
	if out, err := src.(source.Writer).Apply(ctx, moved); err != nil || out.Err != nil {
		t.Fatalf("changing a field and moving it: %v %+v", err, out)
	}
	if v, err := c.HGet(ctx, "user:1:profile", "city").Result(); err != nil || v != "Lyon" {
		t.Errorf("the field moved holds %q: %v", v, err)
	}

	// A set's member is added, renamed and removed.
	tags := valueRef("tags")
	if out := change(t, src, tags, []string{"member"},
		source.RowChange{Kind: source.ChangeInsert, Values: map[string]any{"member": "cache"}},
		source.RowChange{Kind: source.ChangeUpdate, Key: []any{"go"}, Values: map[string]any{"member": "golang"}},
		source.RowChange{Kind: source.ChangeDelete, Key: []any{"redis"}}); out.Err != nil || out.Applied != 3 {
		t.Fatalf("writing a set: %+v", out)
	}
	members, err := c.SMembers(ctx, "tags").Result()
	if err != nil || len(members) != 2 {
		t.Errorf("the set holds %v: %v", members, err)
	}

	// A sorted set's member is added, rescored, renamed and removed.
	scores := valueRef("scores")
	if out := change(t, src, scores, []string{"member"},
		source.RowChange{Kind: source.ChangeInsert, Values: map[string]any{"member": "Ada", "score": 42.0}},
		source.RowChange{Kind: source.ChangeUpdate, Key: []any{"Grace"}, Values: map[string]any{"score": "9"}},
		source.RowChange{Kind: source.ChangeUpdate, Key: []any{"Grace"}, Values: map[string]any{"member": "Hopper"}}); out.Err != nil {
		t.Fatalf("writing a sorted set: %+v", out)
	}
	if score, err := c.ZScore(ctx, "scores", "Hopper").Result(); err != nil || score != 9 {
		t.Errorf("the renamed member scores %v: %v", score, err)
	}

	// A list's element is changed, and one is added at the end.
	queue := valueRef("queue")
	if out := change(t, src, queue, []string{"index"},
		source.RowChange{Kind: source.ChangeUpdate, Key: []any{int64(1)}, Values: map[string]any{"value": "B"}},
		source.RowChange{Kind: source.ChangeInsert, Values: map[string]any{"value": "d"}}); out.Err != nil {
		t.Fatalf("writing a list: %+v", out)
	}
	if held, err := c.LRange(ctx, "queue", 0, -1).Result(); err != nil || strings.Join(held, "") != "aBcd" {
		t.Errorf("the list holds %v: %v", held, err)
	}
}

func TestLiveSaysWhenWhatAChangeWasAboutHasGone(t *testing.T) {
	src := live(t, liveConfig("1"))
	seed(t, src, 1)
	ctx := context.Background()
	c := conn(t, src, 1)
	hash := valueRef("user:1:profile")
	id := model.RowIdentity{Kind: model.IdentityKeyName, Columns: []string{"field"}, Target: hash}
	w := src.(source.Writer)

	// A second field, so that taking the first away leaves the hash there: a
	// hash with no fields is a key that is gone, and a tab on one is stale.
	if err := c.HSet(ctx, "user:1:profile", "name", "Ada").Err(); err != nil {
		t.Fatal(err)
	}

	// Planned while it was there, applied after it went: the change fails,
	// and does not put the field back.
	plan, err := w.Plan(ctx, source.Changeset{Target: hash, Identity: id, Changes: []source.RowChange{
		{Kind: source.ChangeUpdate, Key: []any{"city"}, Values: map[string]any{"value": "Paris"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.HDel(ctx, "user:1:profile", "city").Err(); err != nil {
		t.Fatal(err)
	}
	out, err := w.Apply(ctx, plan)
	if err != nil || !errors.Is(out.Err, sqlscript.ErrNoRow) || out.FailedAt != 0 {
		t.Fatalf("a change to a field that has gone: %v %+v", err, out)
	}
	if n, err := c.HExists(ctx, "user:1:profile", "city").Result(); err != nil || n {
		t.Error("the field was written back")
	}
	// Nothing was undone, and the outcome must not say it was.
	if out.RolledBack {
		t.Error("a plan that says it was undone, where there is no transaction")
	}

	// A position a list no longer has, and a member that has gone: each is a
	// row changed since it was read, and neither is put back.
	short := change(t, src, valueRef("queue"), []string{"index"}, source.RowChange{
		Kind: source.ChangeUpdate, Key: []any{int64(99)}, Values: map[string]any{"value": "z"}})
	if !errors.Is(short.Err, sqlscript.ErrNoRow) || short.FailedAt != 0 {
		t.Errorf("an element past the end of a list: %+v", short)
	}
	goneMember := change(t, src, valueRef("tags"), []string{"member"}, source.RowChange{
		Kind: source.ChangeUpdate, Key: []any{"nobody"}, Values: map[string]any{"member": "somebody"}})
	if !errors.Is(goneMember.Err, sqlscript.ErrNoRow) || goneMember.FailedAt != 0 {
		t.Errorf("a member that is not in the set: %+v", goneMember)
	}
	if there, err := c.SIsMember(ctx, "tags", "somebody").Result(); err != nil || there {
		t.Error("the member was added by a change to one that had gone")
	}
	goneScore := change(t, src, valueRef("scores"), []string{"member"}, source.RowChange{
		Kind: source.ChangeUpdate, Key: []any{"nobody"}, Values: map[string]any{"score": 1.0}})
	if !errors.Is(goneScore.Err, sqlscript.ErrNoRow) || goneScore.FailedAt != 0 {
		t.Errorf("a member that is not in the sorted set: %+v", goneScore)
	}

	// A part renamed onto one that is there already is refused rather than
	// written over.
	if err := c.HSet(ctx, "user:1:profile", "keep", "1").Err(); err != nil {
		t.Fatal(err)
	}
	onto := change(t, src, hash, []string{"field"}, source.RowChange{
		Kind: source.ChangeUpdate, Key: []any{"name"}, Values: map[string]any{"field": "keep"}})
	if onto.Err == nil || !strings.Contains(onto.Err.Error(), "already") {
		t.Errorf("a field renamed onto one that is there: %+v", onto)
	}
	if v, err := c.HGet(ctx, "user:1:profile", "keep").Result(); err != nil || v != "1" {
		t.Errorf("the field renamed onto holds %q: %v", v, err)
	}
	if err := c.HDel(ctx, "user:1:profile", "keep").Err(); err != nil {
		t.Fatal(err)
	}
	tagged := change(t, src, valueRef("tags"), []string{"member"}, source.RowChange{
		Kind: source.ChangeUpdate, Key: []any{"go"}, Values: map[string]any{"member": "redis"}})
	if tagged.Err == nil || !strings.Contains(tagged.Err.Error(), "already") {
		t.Errorf("a member renamed onto one that is there: %+v", tagged)
	}
	if err := c.ZAdd(ctx, "scores", goredis.Z{Score: 1, Member: "Ada"}).Err(); err != nil {
		t.Fatal(err)
	}
	scored := change(t, src, valueRef("scores"), []string{"member"}, source.RowChange{
		Kind: source.ChangeUpdate, Key: []any{"Grace"}, Values: map[string]any{"member": "Ada"}})
	if scored.Err == nil || !strings.Contains(scored.Err.Error(), "already") {
		t.Errorf("a member renamed onto one that is there: %+v", scored)
	}

	// A part that is there already is not written over silently.
	added := change(t, src, valueRef("tags"), []string{"member"},
		source.RowChange{Kind: source.ChangeInsert, Values: map[string]any{"member": "go"}})
	if added.Err == nil || !strings.Contains(added.Err.Error(), "already") {
		t.Errorf("adding a member twice: %+v", added)
	}
	dup := change(t, src, hash, []string{"field"},
		source.RowChange{Kind: source.ChangeInsert, Values: map[string]any{"field": "town", "value": "Paris"}},
		source.RowChange{Kind: source.ChangeInsert, Values: map[string]any{"field": "town", "value": "Lyon"}})
	if dup.Err == nil || dup.FailedAt != 1 || dup.Applied != 1 {
		t.Errorf("a field added twice: %+v", dup)
	}
	// The commands before the failure stand: there is no transaction here.
	if v, err := c.HGet(ctx, "user:1:profile", "town").Result(); err != nil || v != "Paris" {
		t.Errorf("the field written before the failure is %q: %v", v, err)
	}
}

func TestLiveRefusesToWriteAValueWhereItMayNot(t *testing.T) {
	src := live(t, liveConfig("1"))
	seed(t, src, 1)
	ctx := context.Background()
	hash := valueRef("user:1:profile")
	id := model.RowIdentity{Kind: model.IdentityKeyName, Columns: []string{"field"}, Target: hash}
	cs := source.Changeset{Target: hash, Identity: id, Changes: []source.RowChange{
		{Kind: source.ChangeInsert, Values: map[string]any{"field": "nobody", "value": "x"}}}}

	ro := liveConfig("1")
	ro.Guard = source.Guard{ReadOnly: true}
	rw := live(t, ro).(source.Writer)
	plan, err := rw.Plan(ctx, cs)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if _, err := rw.Apply(ctx, plan); !errors.Is(err, source.ErrReadOnly) {
		t.Errorf("a read-only connection wrote: %v", err)
	}

	prod := liveConfig("1")
	prod.Guard = source.Guard{Environment: source.EnvProduction}
	pw := live(t, prod).(source.Writer)
	plan, err = pw.Plan(ctx, cs)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if !plan.Guarded {
		t.Error("a production plan is not guarded")
	}
	if _, err := pw.Apply(ctx, plan); !errors.Is(err, source.ErrConfirmationRequired) {
		t.Errorf("production without consent: %v", err)
	}
	consented := cs
	consented.Confirmed = true
	plan, err = pw.Plan(ctx, consented)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := pw.Apply(ctx, plan); err != nil || out.Err != nil {
		t.Errorf("production with consent: %v %+v", err, out)
	}
	if v, err := conn(t, src, 1).HGet(ctx, "user:1:profile", "nobody").Result(); err != nil || v != "x" {
		t.Errorf("the field consented to is %q: %v", v, err)
	}
}

func TestLiveSaysWhatARowOfTheKeyspaceNames(t *testing.T) {
	src := live(t, liveConfig("1"))
	seed(t, src, 1)
	db := model.NewRef(model.KindDatabase, "db1")
	ro, ok := src.(source.RowObject)
	if !ok {
		t.Fatal("a key-value store's rows name their keys")
	}
	_, cols := rows(t, src, db, source.BrowseOptions{Limit: 1})
	ref, named := ro.ObjectOf(db, cols, model.Row{"user:1", "string", nil})
	if !named || !ref.Equal(valueRef("user:1")) {
		t.Errorf("a row names %v, %v", ref, named)
	}
	// A row of something else names nothing, and neither does one without a
	// name in it.
	if _, named := ro.ObjectOf(valueRef("user:1"), cols, model.Row{"user:1", "string", nil}); named {
		t.Error("a row of a value named a key")
	}
	if _, named := ro.ObjectOf(db, cols, model.Row{nil, "string", nil}); named {
		t.Error("a row with no name named a key")
	}
}
