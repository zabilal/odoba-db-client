//go:build conformance

package redis

import (
	"context"
	"os"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/conformance"
)

// The shared driver suite (REQ-DRV-1), run against each shape a Redis
// connection takes: a single server, a server with the JSON module, and a
// cluster. The suite's writes have a key-value paradigm of their own, which
// changes and deletes the keys a keyspace holds and opens each key onto what
// it holds, so every kind of value is written through the same contract.

func TestConformance(t *testing.T) {
	src := live(t, liveConfig("1"))
	seed(t, src, 1)
	seed(t, src, 2)
	conformance.Run(t, suiteTarget("redis", liveConfig("1"),
		model.NewRef(model.KindDatabase, "db1"), model.NewRef(model.KindDatabase, "db2")))
}

// TestConformanceWithTheJSONModule adds a document to the kinds of value
// written, which only a server with the module holds.
func TestConformanceWithTheJSONModule(t *testing.T) {
	cfg := source.ConnectionConfig{DriverID: driverID, Host: "127.0.0.1", Port: jsonPort(), Database: "0",
		TLS: source.TLSConfig{Mode: "disable"}}
	src := required(t, cfg, "IKIGAI_REQUIRE_REDIS_JSON", "ikigai-redis-json")
	c := src.(*redisSource).client.(*goredis.Client)
	emptied(t, c)
	fill(t, c)
	if err := c.JSONSet(context.Background(), "profile", "$", `{"name":"Ada","tags":["maths","engines"]}`).Err(); err != nil {
		t.Fatal(err)
	}
	db0 := model.NewRef(model.KindDatabase, "db0")
	conformance.Run(t, suiteTarget("redis-json", cfg, db0, db0))
}

// TestConformanceOnACluster writes keys the cluster shares out between its
// shards, so that the suite's reads and writes reach every one of them.
func TestConformanceOnACluster(t *testing.T) {
	cfg := source.ConnectionConfig{DriverID: driverID, Host: "127.0.0.1", Port: clusterPort(),
		TLS: source.TLSConfig{Mode: "disable"}, Params: map[string]string{"mode": modeCluster}}
	src := required(t, cfg, "IKIGAI_REQUIRE_REDIS_CLUSTER", "ikigai-redis-cluster")
	c := src.(*redisSource).client.(*goredis.ClusterClient)
	emptied(t, c)
	fill(t, c)
	db0 := model.NewRef(model.KindDatabase, "db0")
	conformance.Run(t, suiteTarget("redis-cluster", cfg, db0, db0))
}

// suiteTarget is a connection the suite opens afresh for every check.
func suiteTarget(name string, cfg source.ConnectionConfig, browsable, writable model.ObjectRef) conformance.Target {
	open := func(ctx context.Context, t *testing.T, g source.Guard) source.Source {
		c := cfg
		c.Guard = g
		s, err := Driver{}.Open(ctx, c)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		return s
	}
	return conformance.Target{
		Name:        name,
		Open:        func(ctx context.Context, t *testing.T) source.Source { return open(ctx, t, source.Guard{}) },
		Browsable:   browsable,
		Writable:    writable,
		OpenGuarded: open,
		// A condition here is a pattern the keys' names are matched by. Any
		// text is one, so there is nothing to refuse, and a pattern has no
		// comments to end at the end of a line.
		Conditions: &conformance.Conditions{True: "*", False: "no key is called this"},
	}
}

// required connects, or skips where the server is not running; the named
// variable makes that a failure, as in CI.
func required(t *testing.T, cfg source.ConnectionConfig, variable, container string) source.Source {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	src, err := Driver{}.Open(ctx, cfg)
	if err != nil {
		if os.Getenv(variable) != "" {
			t.Fatalf("%s is required but unavailable: %v", container, err)
		}
		t.Skipf("no %s on port %d (docker start %s): %v", container, cfg.Port, container, err)
	}
	t.Cleanup(func() { src.Close() })
	return src
}

// emptied empties the keyspace a test is about to fill, and again when the
// test is over: the suite's checks read every key there, and these tests are
// the only thing in it. A cluster's keyspace is emptied on every shard.
func emptied(t *testing.T, c goredis.UniversalClient) {
	t.Helper()
	flush := func() error {
		ctx := context.Background()
		if cluster, ok := c.(*goredis.ClusterClient); ok {
			return cluster.ForEachMaster(ctx, func(ctx context.Context, m *goredis.Client) error {
				return m.FlushDB(ctx).Err()
			})
		}
		return c.FlushDB(ctx).Err()
	}
	if err := flush(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { flush() })
}
