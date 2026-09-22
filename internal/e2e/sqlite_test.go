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
		INSERT INTO people SELECT i, 'person ' || i FROM n`, fixtureRows)); err != nil {
		t.Fatal(err)
	}
	return journey{
		conn: store.SavedConnection{Name: "local", Driver: "sqlite", Database: path},
		path: []model.ObjectRef{
			model.ClassRef(model.NewRef(model.KindDatabase, "main"), model.KindTable),
			model.NewRef(model.KindTable, "main", "people"),
		},
		table: "people",
	}
}

func TestJ1SQLite(t *testing.T) { runJ1(t, sqliteJourney(t)) }
func TestJ2SQLite(t *testing.T) { runJ2(t, sqliteJourney(t)) }
func TestJ5SQLite(t *testing.T) { runJ5(t, sqliteJourney(t)) }
func TestJ7SQLite(t *testing.T) { runJ7(t, sqliteJourney(t)) }
func TestJ3SQLite(t *testing.T) { runJ3(t, sqliteJourney(t)) }
