package importer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fileAt(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func byName(found []Found, name string) (Found, bool) {
	for _, f := range found {
		if f.Connection.Name == name {
			return f, true
		}
	}
	return Found{}, false
}

const dbeaverJSON = `{
  "folders": {"Work": {}},
  "connections": {
    "postgres-jdbc-1": {
      "provider": "postgresql",
      "name": "Orders",
      "folder": "Work",
      "read-only": true,
      "configuration": {
        "host": "db.example.com", "port": "5432", "database": "orders",
        "url": "jdbc:postgresql://db.example.com:5432/orders",
        "user": "jane", "type": "prod"
      }
    },
    "mysql-jdbc-2": {
      "provider": "mysql",
      "name": "Shop",
      "configuration": {
        "url": "jdbc:mysql://shop.example.com:3306/shop", "user": "bob", "type": "dev"
      }
    },
    "oracle-jdbc-3": {
      "provider": "oracle",
      "name": "Ledger",
      "configuration": {"host": "oracle.example.com", "port": 1521}
    }
  }
}`

func TestDBeaverConnectionsComeAcrossWithoutTheirPasswords(t *testing.T) {
	found, err := ReadDBeaver(fileAt(t, "data-sources.json", dbeaverJSON))
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 3 {
		t.Fatalf("it found %d of 3", len(found))
	}

	orders, ok := byName(found, "Orders")
	if !ok || !orders.OK() {
		t.Fatalf("Orders did not come across: %+v", found)
	}
	c := orders.Connection
	if c.Driver != "postgres" || c.Host != "db.example.com" || c.Port != 5432 ||
		c.Database != "orders" || c.User != "jane" || c.Folder != "Work" {
		t.Errorf("it read %+v", c)
	}
	// Both of these change what this program will let somebody do, so they
	// are worth more than their labels.
	if !c.ReadOnly {
		t.Error("a read-only connection came across writable")
	}
	if c.Environment != "production" {
		t.Errorf("a production connection came across as %q", c.Environment)
	}
	if orders.Password != "" {
		t.Error("a password was invented for a file that holds none")
	}
	if !strings.Contains(orders.Note, "encrypted store") {
		t.Errorf("it did not say where the password stayed: %q", orders.Note)
	}

	// Described only by a URL, which is all some connections have.
	shop, _ := byName(found, "Shop")
	if !shop.OK() || shop.Connection.Host != "shop.example.com" || shop.Connection.Port != 3306 ||
		shop.Connection.Database != "shop" || shop.Connection.Environment != "dev" {
		t.Errorf("Shop read as %+v", shop.Connection)
	}

	// And one there is no driver for, which is reported rather than dropped.
	var ledger Found
	for _, f := range found {
		if strings.Contains(f.Where, "oracle-jdbc-3") {
			ledger = f
		}
	}
	if ledger.OK() {
		t.Error("an Oracle connection was imported as something else")
	}
	if !strings.Contains(ledger.Note, "oracle") || !strings.Contains(ledger.Note, "Ledger") {
		t.Errorf("it said %q, which does not name what was left behind", ledger.Note)
	}
}

const dataGripXML = `<?xml version="1.0" encoding="UTF-8"?>
<component name="DataSourceManagerImpl" format="xml" multifile-model="true">
  <data-source source="LOCAL" name="Orders" uuid="1111">
    <driver-ref>postgresql</driver-ref>
    <jdbc-url>jdbc:postgresql://db.example.com:5432/orders</jdbc-url>
    <user-name>jane</user-name>
  </data-source>
  <data-source source="LOCAL" name="Cache" uuid="2222">
    <driver-ref>some-vendor-thing</driver-ref>
    <jdbc-url>jdbc:redis://cache.example.com:6379</jdbc-url>
  </data-source>
  <data-source source="LOCAL" name="Ledger" uuid="3333">
    <driver-ref>oracle</driver-ref>
    <jdbc-url>jdbc:oracle:thin:@ora.example.com:1521:XE</jdbc-url>
  </data-source>
</component>`

func TestDataGripDataSourcesComeAcross(t *testing.T) {
	found, err := ReadDataGrip(fileAt(t, "dataSources.xml", dataGripXML))
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 3 {
		t.Fatalf("it found %d of 3", len(found))
	}

	orders, _ := byName(found, "Orders")
	if !orders.OK() || orders.Connection.Driver != "postgres" ||
		orders.Connection.Host != "db.example.com" || orders.Connection.User != "jane" {
		t.Errorf("Orders read as %+v", orders.Connection)
	}
	if !strings.Contains(orders.Note, "password store") {
		t.Errorf("it did not say where the password stayed: %q", orders.Note)
	}

	// The driver name means nothing here, but the URL still does.
	cache, _ := byName(found, "Cache")
	if !cache.OK() || cache.Connection.Driver != "redis" || cache.Connection.Port != 6379 {
		t.Errorf("Cache read as %+v (%s)", cache.Connection, cache.Note)
	}

	var ledger Found
	for _, f := range found {
		if strings.Contains(f.Where, "Ledger") {
			ledger = f
		}
	}
	if ledger.OK() {
		t.Errorf("an Oracle data source was imported as %+v", ledger.Connection)
	}
}

// The one thing an import must never do. A JDBC URL can carry a password in
// plain text, and a connection saved with it would put that password in the
// settings file, which is exactly what FR-1.5 exists to prevent.
func TestAPasswordInAURLNeverReachesASavedConnection(t *testing.T) {
	const secret = "s3cretpassword"
	dbeaver := fileAt(t, "data-sources.json", `{"connections":{"c1":{"provider":"postgresql","name":"Leaky",
		"configuration":{"url":"jdbc:postgresql://jane:`+secret+`@db.example.com:5432/orders"}}}}`)
	grip := fileAt(t, "dataSources.xml", `<component><data-source name="Leaky">
		<driver-ref>postgresql</driver-ref>
		<jdbc-url>jdbc:postgresql://jane:`+secret+`@db.example.com:5432/orders</jdbc-url>
		</data-source></component>`)

	for _, c := range []struct {
		what string
		read func(string) ([]Found, error)
		path string
	}{
		{"DBeaver", ReadDBeaver, dbeaver},
		{"DataGrip", ReadDataGrip, grip},
	} {
		t.Run(c.what, func(t *testing.T) {
			found, err := c.read(c.path)
			if err != nil {
				t.Fatal(err)
			}
			f, ok := byName(found, "Leaky")
			if !ok || !f.OK() {
				t.Fatalf("it did not come across at all: %+v", found)
			}
			if f.Password != "" {
				t.Errorf("the password was brought: %q", f.Password)
			}
			// Nowhere in what would be written to settings.json.
			saved, err := json.Marshal(f.Connection)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(saved), secret) {
				t.Errorf("the password is in what would be saved:\n%s", saved)
			}
			if f.Connection.User != "jane" {
				t.Errorf("the user was lost with it: %q", f.Connection.User)
			}
			if !strings.Contains(f.Note, "keychain") {
				t.Errorf("it did not say the password was left behind: %q", f.Note)
			}
		})
	}
}

func TestAFileOfTheWrongKindIsSaidToBeOne(t *testing.T) {
	if _, err := ReadDBeaver(fileAt(t, "x.json", "not json")); err == nil {
		t.Error("anything was read as DBeaver's")
	}
	if _, err := ReadDataGrip(fileAt(t, "x.xml", "<not-xml")); err == nil {
		t.Error("anything was read as DataGrip's")
	}
	// An empty but valid file is not an error; it is an answer.
	found, err := ReadDBeaver(fileAt(t, "y.json", `{"connections":{}}`))
	if err != nil || len(found) != 1 || found[0].OK() {
		t.Errorf("an empty file gave %v, %+v", err, found)
	}
}

func TestScanReadsNothingAndOffersOnlyWhatExists(t *testing.T) {
	t.Setenv("PGPASSFILE", filepath.Join(t.TempDir(), "absent"))
	for _, c := range Scan() {
		if c.Tool == "PostgreSQL password file" {
			t.Errorf("it offered a file that is not there: %s", c.Path)
		}
	}
	// And every tool it knows about says whether opening it means opening
	// credentials, before anything is opened.
	for _, tool := range Tools() {
		if tool.Name == "" || tool.Read == nil {
			t.Errorf("a tool is not usable: %+v", tool.Name)
		}
	}
}
