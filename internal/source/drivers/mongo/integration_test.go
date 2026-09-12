//go:build conformance

package mongo

// Integration tests against a real MongoDB server (REQ-DRV-1, T2.30). They
// expect the ikigai-mongo container on port 57017 and skip if it is not
// running; IKIGAI_REQUIRE_MONGO=1 makes that a failure, as in CI.

import (
	"context"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

func port() int {
	if v := os.Getenv("IKIGAI_MONGO_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			return p
		}
	}
	return 57017
}

func liveConfig(db string) source.ConnectionConfig {
	return source.ConnectionConfig{DriverID: driverID, Host: "127.0.0.1", Port: port(), Database: db,
		TLS: source.TLSConfig{Mode: "disable"}}
}

// open connects, or skips where no server is running.
func open(t *testing.T, cfg source.ConnectionConfig) source.Source {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	src, err := Driver{}.Open(ctx, cfg)
	if err != nil {
		if os.Getenv("IKIGAI_REQUIRE_MONGO") != "" {
			t.Fatalf("mongodb required but unavailable: %v", err)
		}
		t.Skipf("no mongodb on port %d (docker start ikigai-mongo): %v", port(), err)
	}
	t.Cleanup(func() { src.Close() })
	return src
}

// seed writes documents into a database, so there is something to list.
func seed(t *testing.T, src source.Source, db string) {
	t.Helper()
	s := src.(*mongoSource)
	ctx := context.Background()
	coll := s.client.Database(db).Collection("people")
	if err := coll.Drop(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := coll.InsertOne(ctx, map[string]any{"name": "Ada", "score": 42}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.client.Database(db).Drop(context.Background()) })
}

func TestLiveConnects(t *testing.T) {
	src := open(t, liveConfig("ikigai_it"))
	if err := src.Ping(context.Background()); err != nil {
		t.Errorf("ping: %v", err)
	}
	info, err := src.Info(context.Background())
	if err != nil {
		t.Fatalf("info: %v", err)
	}
	if info.Product != "MongoDB" || info.Version == "" {
		t.Errorf("server %+v, want MongoDB and a version", info)
	}
	if info.Latency <= 0 {
		t.Errorf("latency %v, want the round trip measured", info.Latency)
	}
	// Closing twice is not an error, and a closed connection stops answering.
	if err := src.Close(); err != nil {
		t.Errorf("close: %v", err)
	}
	if err := src.Close(); err != nil {
		t.Errorf("close again: %v", err)
	}
	if err := src.Ping(context.Background()); err == nil {
		t.Error("a closed connection still answers")
	}
}

func TestLiveListsDatabases(t *testing.T) {
	src := open(t, liveConfig("ikigai_it"))
	seed(t, src, "ikigai_it")
	nodes, err := src.Root(context.Background())
	if err != nil {
		t.Fatalf("root: %v", err)
	}
	var found, current bool
	for _, n := range nodes {
		if n.Ref.Kind != model.KindDatabase {
			t.Errorf("node %+v, want a database", n)
		}
		if n.Ref.Name() == "ikigai_it" {
			found = true
			current = n.Attrs["current"] == "true"
		}
	}
	if !found {
		t.Errorf("databases %v, want the one written to", nodes)
	}
	if !current {
		t.Error("the database the connection is in is not marked as such")
	}
}

func TestLiveRefusesWhatIsNotWrittenYet(t *testing.T) {
	src := open(t, liveConfig("ikigai_it"))
	ctx := context.Background()
	ref := model.NewRef(model.KindDatabase, "ikigai_it")
	if _, err := src.Children(ctx, ref); !errors.Is(err, errNotYet) {
		t.Errorf("children: %v, want it said plainly that it is not written yet", err)
	}
	if _, err := src.Browse(ctx, ref, source.BrowseOptions{}); !errors.Is(err, errNotYet) {
		t.Errorf("browse: %v", err)
	}
}

func TestLiveSaysWhatIsWrongWithAConnection(t *testing.T) {
	open(t, liveConfig("ikigai_it")) // skip early where no server is running
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	bad := liveConfig("ikigai_it")
	bad.Port = 1
	_, err := Driver{}.Open(ctx, bad)
	var ce *source.ConnectError
	if !errors.As(err, &ce) || ce.Kind != source.ConnectRefused && ce.Kind != source.ConnectUnreachable {
		t.Errorf("nothing listening: %v", err)
	}

	auth := liveConfig("ikigai_it")
	auth.User = "nobody"
	auth.Secret = func(string) (string, error) { return "wrong", nil }
	_, err = Driver{}.Open(ctx, auth)
	if !errors.As(err, &ce) || ce.Kind != source.ConnectAuth {
		t.Errorf("bad credentials: %v", err)
	}
}
