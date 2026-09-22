//go:build conformance

package e2e

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/store"

	_ "github.com/ikigai-db/ikigai-db/internal/source/drivers/mysql"
)

const myDB = "e2e_j1"

// mysqlJourney is the fixture table on one of the MySQL-family test servers
// (ikigai-mysql on 53306, ikigai-mariadb on 53307). With no server it skips,
// or fails under IKIGAI_REQUIRE_MYSQL.
func mysqlJourney(t *testing.T, name, portEnv string, port int) journey {
	t.Helper()
	if v := os.Getenv(portEnv); v != "" {
		port, _ = strconv.Atoi(v)
	}
	pass := env("IKIGAI_MYSQL_PASSWORD", "ikigai")
	dsn := fmt.Sprintf("root:%s@tcp(127.0.0.1:%d)/?multiStatements=true", pass, port)
	// Bounded, as the app's own connect is: a server that accepts and never
	// answers fails the suite in seconds, not at go test's ten minutes.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := sql.Open("mysql", dsn)
	if err == nil {
		err = db.PingContext(ctx)
	}
	if err != nil {
		if os.Getenv("IKIGAI_REQUIRE_MYSQL") != "" {
			t.Fatalf("%s required but unavailable: %v", name, err)
		}
		t.Skipf("no %s on port %d (docker start ikigai-%s): %v", name, port, name, err)
	}
	defer db.Close()
	if _, err := db.Exec(fmt.Sprintf(`DROP DATABASE IF EXISTS %[1]s;
		CREATE DATABASE %[1]s;
		CREATE TABLE %[1]s.people (id INT PRIMARY KEY, name VARCHAR(50) NOT NULL);
		INSERT INTO %[1]s.people
		  WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < %[2]d)
		  SELECT i, CONCAT('person ', i) FROM n;`, myDB, fixtureRows)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if c, err := sql.Open("mysql", dsn); err == nil {
			c.ExecContext(ctx, "DROP DATABASE IF EXISTS "+myDB)
			c.Close()
		}
	})
	return journey{
		conn: store.SavedConnection{Name: name, Driver: "mysql", Host: "127.0.0.1", Port: port,
			User: "root", TLS: store.TLS{Mode: "disable"}},
		secrets: map[string]string{"password": pass},
		path: []model.ObjectRef{
			model.NewRef(model.KindDatabase, myDB),
			model.ClassRef(model.NewRef(model.KindDatabase, myDB), model.KindTable),
			model.NewRef(model.KindTable, myDB, "people"),
		},
		table: myDB + ".people",
	}
}

func TestJ1MySQL(t *testing.T)   { runJ1(t, mysqlJourney(t, "mysql", "IKIGAI_MYSQL_PORT", 53306)) }
func TestJ3MySQL(t *testing.T)   { runJ3(t, mysqlJourney(t, "mysql", "IKIGAI_MYSQL_PORT", 53306)) }
func TestJ1MariaDB(t *testing.T) { runJ1(t, mysqlJourney(t, "mariadb", "IKIGAI_MARIADB_PORT", 53307)) }
func TestJ3MariaDB(t *testing.T) { runJ3(t, mysqlJourney(t, "mariadb", "IKIGAI_MARIADB_PORT", 53307)) }
func TestJ2MySQL(t *testing.T)   { runJ2(t, mysqlJourney(t, "mysql", "IKIGAI_MYSQL_PORT", 53306)) }
func TestJ2MariaDB(t *testing.T) { runJ2(t, mysqlJourney(t, "mariadb", "IKIGAI_MARIADB_PORT", 53307)) }
func TestJ5MySQL(t *testing.T)   { runJ5(t, mysqlJourney(t, "mysql", "IKIGAI_MYSQL_PORT", 53306)) }
func TestJ5MariaDB(t *testing.T) { runJ5(t, mysqlJourney(t, "mariadb", "IKIGAI_MARIADB_PORT", 53307)) }
