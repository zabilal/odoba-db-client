//go:build conformance

package e2e

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/store"
	"github.com/ikigai-db/ikigai-db/internal/ui/editor/view"

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
		INSERT INTO %[1]s.people SELECT g, 'person ' || g FROM generate_series(1, %[2]d) g;
		CREATE TABLE %[1]s.orders (id int PRIMARY KEY,
		  person_id int NOT NULL REFERENCES %[1]s.people (id), total int NOT NULL);
		INSERT INTO %[1]s.orders VALUES (1, 7, 10), (2, 7, 20), (3, 9, 30);`,
		pgSchema, fixtureRows)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if c, err := pgx.Connect(ctx, dsn); err == nil {
			_, _ = c.Exec(ctx, "DROP SCHEMA IF EXISTS "+pgSchema+" CASCADE")
			_ = c.Close(ctx)
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
		child: []model.ObjectRef{
			model.NewRef(model.KindDatabase, db),
			model.NewRef(model.KindSchema, db, pgSchema),
			model.ClassRef(model.NewRef(model.KindSchema, db, pgSchema), model.KindTable),
			model.NewRef(model.KindTable, db, pgSchema, "orders"),
		},
		childFK: "person_id",
		ddl: func(t *testing.T, stmt string) {
			t.Helper()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			c, err := pgx.Connect(ctx, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close(ctx)
			if _, err := c.Exec(ctx, "SET search_path = "+pgSchema+"; "+stmt); err != nil {
				t.Fatalf("%.60q: %v", stmt, err)
			}
		},
	}
}

// PostgreSQL reports no exact count, so J1's footer total can only come from
// the grid fetching to the end of the data.
func TestJ1PostgreSQL(t *testing.T) { runJ1(t, postgresJourney(t)) }
func TestJ3PostgreSQL(t *testing.T) { runJ3(t, postgresJourney(t)) }
func TestJ2PostgreSQL(t *testing.T) { runJ2(t, postgresJourney(t)) }
func TestJ5PostgreSQL(t *testing.T) { runJ5(t, postgresJourney(t)) }
func TestJ7PostgreSQL(t *testing.T) { runJ7(t, postgresJourney(t)) }
func TestJ4PostgreSQL(t *testing.T) { runJ4(t, postgresJourney(t)) }
func TestJ6PostgreSQL(t *testing.T) { runJ6(t, postgresJourney(t)) }

// One script, two engines (FR-5.17): a function only SQLite has, then, after
// a line naming the other connection, one only PostgreSQL has. Each answers
// only if it was asked of the right server.
func TestAScriptAcrossSQLiteAndPostgreSQL(t *testing.T) {
	lite, pg := sqliteJourney(t), postgresJourney(t)
	h := start(t, lite)
	other := pg.conn
	other.Name = "The Postgres"
	if _, err := h.conns.Create(other, pg.secrets); err != nil {
		t.Fatal(err)
	}
	h.s.OpenQuery(h.conn.ID)
	ed := find[*view.Editor](h.tabs.Selected().Content)[0]
	ed.Document().SetText("SELECT count(*) AS n, sqlite_version() AS v FROM " + lite.table + ";\n" +
		"@\"The Postgres\"\n" +
		"SELECT count(*) AS n, current_setting('server_version') AS v FROM " + pg.table + ";\n")
	runWhenReady(t, h.q, h.s, "query.runAll")
	var said string
	waitFor(t, h.q, "both servers to answer", func() bool {
		h.w.Canvas().Capture()
		said = strings.Join(labelsIn(h.tabs.Selected().Content), "\n")
		return strings.Contains(said, "Ran 2 statements") || strings.Contains(said, "failed") || strings.Contains(said, "Could not")
	})
	for _, want := range []string{"On " + h.conn.Name + ":", "On The Postgres:", "Ran 2 statements"} {
		if !strings.Contains(said, want) {
			t.Errorf("the tab does not say %q:\n%s", want, said)
		}
	}
	if strings.Contains(said, "failed") {
		t.Errorf("a statement was asked of the wrong server:\n%s", said)
	}
}
