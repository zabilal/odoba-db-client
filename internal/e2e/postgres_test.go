//go:build conformance

package e2e

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/store"

	_ "github.com/ikigai-db/ikigai-db/internal/source/drivers/postgres"
)

const pgSchema = "e2e_j1"

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// postgresJourney is the fixture table on the PostgreSQL the driver's
// integration tests use. With no server it skips, or fails under
// IKIGAI_REQUIRE_PG, as in CI.
func postgresJourney(t *testing.T) journey {
	t.Helper()
	port, _ := strconv.Atoi(env("IKIGAI_PG_PORT", "55432"))
	host, db := env("IKIGAI_PG_HOST", "localhost"), env("IKIGAI_PG_DB", "ikigai_test")
	user, pass := env("IKIGAI_PG_USER", "postgres"), env("IKIGAI_PG_PASSWORD", "ikigai")
	dsn := fmt.Sprintf("host=%s port=%d dbname=%s user=%s password=%s sslmode=disable", host, port, db, user, pass)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		if os.Getenv("IKIGAI_REQUIRE_PG") != "" {
			t.Fatalf("PostgreSQL required but unavailable: %v", err)
		}
		t.Skipf("no PostgreSQL at port %d (docker start ikigai-pg): %v", port, err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, fmt.Sprintf(`DROP SCHEMA IF EXISTS %[1]s CASCADE;
		CREATE SCHEMA %[1]s;
		CREATE TABLE %[1]s.people (id int PRIMARY KEY, name text NOT NULL);
		INSERT INTO %[1]s.people SELECT g, 'person ' || g FROM generate_series(1, %[2]d) g;`,
		pgSchema, fixtureRows)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if c, err := pgx.Connect(ctx, dsn); err == nil {
			c.Exec(ctx, "DROP SCHEMA IF EXISTS "+pgSchema+" CASCADE")
			c.Close(ctx)
		}
	})
	return journey{
		conn: store.SavedConnection{Name: "it", Driver: "postgres", Host: host, Port: port,
			Database: db, User: user, TLS: store.TLS{Mode: "disable"}},
		secrets: map[string]string{"password": pass},
		path: []model.ObjectRef{
			model.NewRef(model.KindDatabase, db),
			model.NewRef(model.KindSchema, db, pgSchema),
			model.ClassRef(model.NewRef(model.KindSchema, db, pgSchema), model.KindTable),
			model.NewRef(model.KindTable, db, pgSchema, "people"),
		},
		table: pgSchema + ".people",
	}
}

// PostgreSQL reports no exact count, so J1's footer total can only come from
// the grid fetching to the end of the data.
func TestJ1PostgreSQL(t *testing.T) { runJ1(t, postgresJourney(t)) }
func TestJ3PostgreSQL(t *testing.T) { runJ3(t, postgresJourney(t)) }
