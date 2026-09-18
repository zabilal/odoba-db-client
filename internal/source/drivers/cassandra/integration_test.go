//go:build conformance

package cassandra

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Integration tests against a real Cassandra (REQ-DRV-1, T2.47). They expect
// the ikigai-cassandra container on port 59042 and skip if it is not running;
// IKIGAI_REQUIRE_CASSANDRA=1 makes that a failure, as in CI.

func port() int {
	if v := os.Getenv("IKIGAI_CASSANDRA_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			return p
		}
	}
	return 59042
}

func liveConfig(keyspace string) source.ConnectionConfig {
	return source.ConnectionConfig{DriverID: driverID, Host: "127.0.0.1", Port: port(), Database: keyspace,
		TLS: source.TLSConfig{Mode: "disable"}}
}

// live connects, or skips where no cluster is running.
func live(t *testing.T, cfg source.ConnectionConfig) source.Source {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	src, err := Driver{}.Open(ctx, cfg)
	if err != nil {
		if os.Getenv("IKIGAI_REQUIRE_CASSANDRA") != "" {
			t.Fatalf("cassandra required but unavailable: %v", err)
		}
		t.Skipf("no cassandra on port %d (docker start ikigai-cassandra): %v", port(), err)
	}
	t.Cleanup(func() { src.Close() })
	return src
}

func TestLiveConnects(t *testing.T) {
	src := live(t, liveConfig(""))
	if err := src.Ping(context.Background()); err != nil {
		t.Errorf("ping: %v", err)
	}
	// Close is idempotent: the session teardown path can reach it twice.
	if err := src.Close(); err != nil {
		t.Errorf("first close: %v", err)
	}
	if err := src.Close(); err != nil {
		t.Errorf("second close: %v", err)
	}
}

func TestLiveSaysWhatTheClusterIs(t *testing.T) {
	src := live(t, liveConfig(""))
	info, err := src.Info(context.Background())
	if err != nil {
		t.Fatalf("info: %v", err)
	}
	// A version, and not the cluster's name: the columns are read in the
	// order they are asked for, and swapping them would read as both.
	if info.Product != "Cassandra" || !versionLike(info.Version) {
		t.Errorf("the cluster says it is %+v", info)
	}
	if info.Latency <= 0 {
		t.Errorf("a round trip of %v", info.Latency)
	}
	// The cluster's own name and how it spreads its partitions, which is what
	// tells one cluster from another.
	if info.Attrs["cluster"] == "" || info.Attrs["partitioner"] == "" {
		t.Errorf("the cluster is described as %v", info.Attrs)
	}
}

// versionLike reports whether a value reads as a version rather than a name.
func versionLike(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) < 2 {
		return false
	}
	for _, p := range parts {
		if _, err := strconv.Atoi(p); err != nil {
			return false
		}
	}
	return true
}

func TestLiveOpensOnAKeyspace(t *testing.T) {
	src := live(t, liveConfig("system"))
	if err := src.Ping(context.Background()); err != nil {
		t.Errorf("ping on a keyspace: %v", err)
	}
}

func TestLiveSaysWhatIsWrongWithAConnection(t *testing.T) {
	live(t, liveConfig("")) // skips where no cluster runs
	ctx := context.Background()
	d := Driver{}

	nowhere := liveConfig("")
	nowhere.Port = 1
	if _, err := d.Open(ctx, nowhere); kind(err) != source.ConnectRefused && kind(err) != source.ConnectUnreachable {
		t.Errorf("a port nothing listens on: %v", err)
	}
	unknown := liveConfig("")
	unknown.Host = "cassandra.invalid"
	if _, err := d.Open(ctx, unknown); kind(err) != source.ConnectUnreachable {
		t.Errorf("a host that is not there: %v", err)
	}
	// A keyspace the cluster has not got is its own kind of failure: the
	// cluster answered, and the fix is the keyspace rather than the network.
	missing := liveConfig("no_such_keyspace")
	if _, err := d.Open(ctx, missing); kind(err) != source.ConnectNoDatabase {
		t.Errorf("a keyspace that is not there: %v", err)
	}
}

func TestLiveConnectingStopsWhenItIsGivenUp(t *testing.T) {
	live(t, liveConfig("")) // skips where no cluster runs
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d := Driver{}
	if _, err := d.Open(ctx, liveConfig("")); kind(err) != source.ConnectUnreachable {
		t.Errorf("a connection given up before it was made: %v", err)
	}
}

// TestLiveClaimsNothingItHasNotWritten holds the driver to what it says it
// is: it runs CQL, and it does not yet read a table's rows or write any.
func TestLiveClaimsNothingItHasNotWritten(t *testing.T) {
	src := live(t, liveConfig(""))
	caps := src.Capabilities()
	if caps.Data.Insert || caps.Data.Update || caps.Data.Delete {
		t.Errorf("the driver claims writes it has not written: %+v", caps)
	}
	// A claimed language is a promise the editor holds it to: the lexer must
	// know it, and a Queryer must be behind it (REQ-DRV-1).
	if !caps.Query.Supported || caps.Query.Language != "cql" {
		t.Errorf("the language is %+v", caps.Query)
	}
	if _, ok := src.(source.Queryer); !ok {
		t.Error("a language is claimed with nothing to run it")
	}
	// The cluster filters and orders what it can; nothing is counted, because
	// counting a table reads every partition on every node (FR-2.5).
	if !caps.Data.ServerFilter || !caps.Data.ServerSort {
		t.Errorf("filtering and ordering are the cluster's: %+v", caps.Data)
	}
	if caps.Data.ExactCount || caps.Data.ApproximateCount {
		t.Errorf("a count is claimed that would read every partition: %+v", caps.Data)
	}
	if !caps.Paradigm.Valid() || len(caps.Objects) == 0 {
		t.Errorf("the driver claims no paradigm or no objects: %+v", caps)
	}
	// A keyspace holds tables rather than rows of its own, and says so.
	keyspace := model.NewRef(model.KindDatabase, "system")
	if _, err := src.Browse(context.Background(), keyspace, source.BrowseOptions{}); err == nil {
		t.Error("a keyspace was browsed as though it held rows")
	}
}
