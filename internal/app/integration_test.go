//go:build conformance

package app

// End to end against a real PostgreSQL server: paste a URL, save it, test it,
// open it, browse it. Run with `go test -tags conformance ./internal/app/`.

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/app/connstr"
	"github.com/ikigai-db/ikigai-db/internal/source"
	_ "github.com/ikigai-db/ikigai-db/internal/source/drivers/postgres"
	"github.com/ikigai-db/ikigai-db/internal/store"
)

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func pgConn() store.SavedConnection {
	port, _ := strconv.Atoi(envOr("IKIGAI_PG_PORT", "55432"))
	return store.SavedConnection{Name: "it", Driver: "postgres",
		Host: envOr("IKIGAI_PG_HOST", "localhost"), Port: port,
		Database: envOr("IKIGAI_PG_DB", "ikigai_test"), User: envOr("IKIGAI_PG_USER", "postgres"),
		TLS: store.TLS{Mode: "disable"}, Environment: "local"}
}

func TestEndToEndAgainstPostgres(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	pw := envOr("IKIGAI_PG_PASSWORD", "ikigai")

	probe := f.c.Test(ctx, pgConn(), map[string]string{"password": pw})
	if !probe.OK {
		if os.Getenv("IKIGAI_REQUIRE_PG") != "" {
			t.Fatalf("PostgreSQL required but unavailable: %s (%s)", probe.Hint, probe.Detail)
		}
		t.Skipf("no PostgreSQL (docker start ikigai-pg): %s", probe.Hint)
	}
	if probe.Server.Product != "PostgreSQL" || probe.Elapsed <= 0 {
		t.Errorf("test result: %+v", probe)
	}

	// Paste a URL, exactly as a user would.
	c := pgConn()
	url := "postgres://" + c.User + ":" + pw + "@" + c.Host + ":" + strconv.Itoa(c.Port) +
		"/" + c.Database + "?sslmode=disable&application_name=ikigai-it"
	parsed, err := connstr.Parse(url)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := f.c.Create(parsed.Conn, parsed.Secrets)
	if err != nil {
		t.Fatal(err)
	}

	// A saved connection tests without the password being typed again.
	if r := f.c.Test(ctx, saved, nil); !r.OK {
		t.Fatalf("saved connection did not test: %s (%s)", r.Hint, r.Detail)
	}

	live, err := f.c.Open(ctx, saved.ID, MonitorConfig{Interval: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	roots, err := live.Source.Root(ctx)
	if err != nil || len(roots) == 0 {
		t.Fatalf("Root: %v (%d nodes)", err, len(roots))
	}
	time.Sleep(120 * time.Millisecond) // a few health checks against the real server
	if st := live.Status(); st.State != StateConnected {
		t.Errorf("status %s: %s", st.State, st.Message())
	}

	// The two failures a user actually meets, each saying what to fix.
	if r := f.c.Test(ctx, saved, map[string]string{"password": "definitely-wrong"}); r.Kind != source.ConnectAuth ||
		contains(r.Detail, "definitely-wrong") {
		t.Errorf("wrong password: %+v", r)
	}
	// TLS is verified by default (NFR-S3), and this container speaks plaintext:
	// the first error a new user sees against a local database. It must say TLS.
	tlsDefault := pgConn()
	tlsDefault.TLS.Mode = ""
	if r := f.c.Test(ctx, tlsDefault, map[string]string{"password": pw}); r.Kind != source.ConnectTLS {
		t.Errorf("default TLS against a plaintext server: kind %d, hint %q", r.Kind, r.Hint)
	}
}
