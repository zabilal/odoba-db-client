package e2e

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/store"

	_ "github.com/ikigai-db/ikigai-db/internal/source/drivers/sqlite"
)

// sqliteJourney is a database file holding the fixture table. It needs no
// server, so these journeys run in the ordinary test suite, and in CI.
func sqliteJourney(t *testing.T) journey {
	t.Helper()
	path := filepath.Join(t.TempDir(), "journey.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(fmt.Sprintf(`CREATE TABLE people (id INTEGER PRIMARY KEY, name TEXT NOT NULL);
		WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < %d)
		INSERT INTO people SELECT i, 'person ' || i FROM n;
		CREATE TABLE orders (id INTEGER PRIMARY KEY, person_id INTEGER NOT NULL REFERENCES people (id), total INTEGER NOT NULL);
		INSERT INTO orders VALUES (1, 7, 10), (2, 7, 20), (3, 9, 30)`, fixtureRows)); err != nil {
		t.Fatal(err)
	}
	return journey{
		conn: store.SavedConnection{Name: "local", Driver: "sqlite", Database: path},
		path: []model.ObjectRef{
			model.ClassRef(model.NewRef(model.KindDatabase, "main"), model.KindTable),
			model.NewRef(model.KindTable, "main", "people"),
		},
		table: "people",
		child: []model.ObjectRef{
			model.ClassRef(model.NewRef(model.KindDatabase, "main"), model.KindTable),
			model.NewRef(model.KindTable, "main", "orders"),
		},
		childFK: "person_id",
		// No schema: this engine's tree shows the class folders under
		// the connection itself, so there is no node a diagram could be
		// drawn of.
	}
}

func TestJ1SQLite(t *testing.T) { runJ1(t, sqliteJourney(t)) }
func TestJ2SQLite(t *testing.T) { runJ2(t, sqliteJourney(t)) }
func TestJ5SQLite(t *testing.T) { runJ5(t, sqliteJourney(t)) }
func TestJ7SQLite(t *testing.T) { runJ7(t, sqliteJourney(t)) }
func TestJ3SQLite(t *testing.T) { runJ3(t, sqliteJourney(t)) }
func TestJ4SQLite(t *testing.T) { runJ4(t, sqliteJourney(t)) }
func TestJ6SQLite(t *testing.T) { runJ6(t, sqliteJourney(t)) }
