//go:build conformance

package redis

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

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
		if n.Ref.Kind != model.KindDatabase || !n.HasChildren {
			t.Errorf("%s is %s, children %v", n.Label, n.Ref.Kind, n.HasChildren)
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
	if _, err := src.Children(ctx, db); !errors.Is(err, errNotYet) {
		t.Errorf("listing keys: %v", err)
	}
	if _, err := src.Browse(ctx, db, source.BrowseOptions{}); !errors.Is(err, errNotYet) {
		t.Errorf("reading keys: %v", err)
	}
	if _, err := src.Describe(ctx, db); !errors.Is(err, errNotYet) {
		t.Errorf("describing: %v", err)
	}
	// A badge that is not there yet is not an error: the tree asks for one on
	// every node it draws, and a failure would be shown as one.
	if _, ok, err := src.Badge(ctx, db); ok || err != nil {
		t.Errorf("badge: %v %v", ok, err)
	}
	// The message names the task, so a person reading it knows it is coming.
	if _, err := src.Browse(ctx, db, source.BrowseOptions{}); !strings.Contains(err.Error(), "keys") {
		t.Errorf("the message says nothing about what is missing: %v", err)
	}
}
