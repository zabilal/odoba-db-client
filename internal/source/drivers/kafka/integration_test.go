//go:build conformance

package kafka

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

// Integration tests against a real broker (REQ-DRV-1, T2.54). They expect the
// ikigai-kafka container on port 59092 and skip if it is not running;
// IKIGAI_REQUIRE_KAFKA=1 makes that a failure, as in CI.

func port() int {
	if v := os.Getenv("IKIGAI_KAFKA_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			return p
		}
	}
	return 59092
}

func liveConfig() source.ConnectionConfig {
	return source.ConnectionConfig{DriverID: driverID, Host: "127.0.0.1", Port: port(),
		TLS: source.TLSConfig{Mode: "disable"}}
}

// live connects, or skips where no broker is running.
func live(t *testing.T, cfg source.ConnectionConfig) source.Source {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	src, err := Driver{}.Open(ctx, cfg)
	if err != nil {
		if os.Getenv("IKIGAI_REQUIRE_KAFKA") != "" {
			t.Fatalf("kafka required but unavailable: %v", err)
		}
		t.Skipf("no kafka on port %d (docker start ikigai-kafka): %v", port(), err)
	}
	t.Cleanup(func() { src.Close() })
	return src
}

func TestLiveConnects(t *testing.T) {
	src := live(t, liveConfig())
	if err := src.Ping(context.Background()); err != nil {
		t.Errorf("ping: %v", err)
	}
	// Close is idempotent: the teardown path can reach it twice.
	if err := src.Close(); err != nil {
		t.Errorf("first close: %v", err)
	}
	if err := src.Close(); err != nil {
		t.Errorf("second close: %v", err)
	}
}

func TestLiveSaysWhatTheClusterIs(t *testing.T) {
	src := live(t, liveConfig())
	info, err := src.Info(context.Background())
	if err != nil {
		t.Fatalf("info: %v", err)
	}
	if info.Product != "Kafka" {
		t.Errorf("the cluster says it is %q", info.Product)
	}
	// A version nobody was told: it is read back from which API versions the
	// broker speaks, so it is a guess, and it still has to look like one.
	if !strings.Contains(info.Version, "v") {
		t.Errorf("the version reads %q", info.Version)
	}
	if info.Latency <= 0 {
		t.Errorf("a round trip of %v", info.Latency)
	}
	// One broker, which is the cluster; its id, which is what tells one
	// cluster from another; and the broker answering for the whole.
	if info.Attrs["brokers"] != "1" {
		t.Errorf("the cluster holds %q brokers", info.Attrs["brokers"])
	}
	if info.Attrs["cluster id"] == "" {
		t.Error("the cluster has no id")
	}
	if info.Attrs["controller"] == "" {
		t.Error("no broker answers for the cluster")
	}
}

func TestLiveSaysWhyItCouldNotConnect(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	d := Driver{}

	// A port nothing listens on: the fix is the port, and it is said quickly
	// rather than retried until a timeout.
	cfg := liveConfig()
	cfg.Port = 59099
	start := time.Now()
	if src, err := d.Open(ctx, cfg); err == nil {
		src.Close()
		t.Error("a connection was opened to a port nothing listens on")
	} else if k := kind(err); k != source.ConnectRefused && k != source.ConnectUnreachable {
		t.Errorf("nothing listening reads as %v", err)
	}
	if waited := time.Since(start); waited > 15*time.Second {
		t.Errorf("a refused connection took %v to say so", waited)
	}

	// A host that does not resolve.
	cfg = liveConfig()
	cfg.Host = "no-such-broker.invalid"
	if src, err := d.Open(ctx, cfg); err == nil {
		src.Close()
		t.Error("a connection was opened to a host that does not exist")
	} else if kind(err) != source.ConnectUnreachable {
		t.Errorf("an unknown host reads as %v", err)
	}
}

func TestLiveFindsTheClusterFromAnyOneBroker(t *testing.T) {
	// A seed nothing listens on, beside the one that answers: a cluster is
	// reached if any of its bootstrap servers is, which is what naming more
	// than one is for.
	cfg := liveConfig()
	cfg.Host = "127.0.0.1"
	cfg.Port = 59099
	cfg.Params = map[string]string{"servers": "127.0.0.1:" + strconv.Itoa(port())}
	src := live(t, cfg)
	if err := src.Ping(context.Background()); err != nil {
		t.Errorf("ping over the second seed: %v", err)
	}
}

func TestLiveTheTreeHoldsTheClusterAndRecordsWait(t *testing.T) {
	src := live(t, liveConfig())
	ctx := context.Background()
	// The tree is the cluster and nothing else yet (T2.59).
	if nodes, err := src.Root(ctx); err != nil || len(nodes) != 1 {
		t.Errorf("the root holds %v: %v", nodes, err)
	}
	if _, err := src.Browse(ctx, model.NewRef(model.KindTopic, "anything"), source.BrowseOptions{}); err == nil {
		t.Error("records were read from a driver that does not read them yet")
	}
	// And nothing is claimed that is not written. Which kinds those are is
	// the unit test's business, by name; here it is enough that the paradigm
	// is the log's and that something is claimed at all.
	if caps := src.Capabilities(); caps.Paradigm != model.ParadigmStream || len(caps.Objects) == 0 {
		t.Errorf("the driver claims %+v", caps)
	}
}
