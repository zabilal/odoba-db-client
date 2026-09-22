package importer

import "testing"

func TestWhatAJDBCURLSays(t *testing.T) {
	for _, c := range []struct {
		url      string
		driver   string
		host     string
		port     int
		database string
		user     string
	}{
		{"jdbc:postgresql://db.example.com:5432/orders", "postgres", "db.example.com", 5432, "orders", ""},
		{"jdbc:postgresql://db.example.com/orders", "postgres", "db.example.com", 0, "orders", ""},
		{"jdbc:mysql://127.0.0.1:3306/shop", "mysql", "127.0.0.1", 3306, "shop", ""},
		{"jdbc:mariadb://127.0.0.1:3306/shop", "mysql", "127.0.0.1", 3306, "shop", ""},
		{"jdbc:mongodb://mongo.example.com:27017/app", "mongodb", "mongo.example.com", 27017, "app", ""},
		{"jdbc:cassandra://ring.example.com:9042/space", "cassandra", "ring.example.com", 9042, "space", ""},
		{"jdbc:redis://cache.example.com:6379", "redis", "cache.example.com", 6379, "", ""},
		// No host at all: everything after the scheme is a path.
		{"jdbc:sqlite:/var/data/app.db", "sqlite", "", 0, "/var/data/app.db", ""},
		// The account can be in the URL or in its query.
		{"jdbc:postgresql://jane@db.example.com:5432/orders", "postgres", "db.example.com", 5432, "orders", "jane"},
		{"jdbc:postgresql://db.example.com:5432/orders?user=jane", "postgres", "db.example.com", 5432, "orders", "jane"},
		// A bracketed address, which splitting on a colon by hand gets wrong.
		{"jdbc:postgresql://[2001:db8::1]:5432/orders", "postgres", "2001:db8::1", 5432, "orders", ""},
	} {
		got, ok := readJDBC(c.url)
		if !ok {
			t.Errorf("%s could not be read", c.url)
			continue
		}
		if got.Driver != c.driver || got.Host != c.host || got.Port != c.port ||
			got.Database != c.database || got.User != c.user {
			t.Errorf("%s reads as %+v", c.url, got)
		}
	}
}

func TestAURLForSomethingWithNoDriverStillSaysWhatItWas(t *testing.T) {
	got, _ := readJDBC("jdbc:sqlserver://db.example.com:1433;databaseName=orders")
	if got.Driver != "" {
		t.Errorf("it claimed a driver: %q", got.Driver)
	}
	if got.Scheme != "sqlserver" {
		t.Errorf("it could not even say what it was: %q", got.Scheme)
	}
}

func TestAPasswordInAURLIsNoticedAndNotKept(t *testing.T) {
	for _, url := range []string{
		"jdbc:postgresql://jane:s3cret@db.example.com:5432/orders",
		"jdbc:postgresql://db.example.com:5432/orders?user=jane&password=s3cret",
	} {
		got, ok := readJDBC(url)
		if !ok {
			t.Fatalf("%s could not be read", url)
		}
		if !got.HadPassword {
			t.Errorf("%s carried a password and it went unnoticed", url)
		}
		// There is nowhere on the struct to put it, which is the point: a
		// password kept here would be written to the settings file.
		if got.User != "jane" {
			t.Errorf("the user is %q", got.User)
		}
	}
}

// And the other half of that: a URL with no password in it must not be said
// to have one, or the note on every imported connection becomes a lie.
func TestAURLWithNoPasswordIsNotSaidToHaveOne(t *testing.T) {
	for _, url := range []string{
		"jdbc:postgresql://db.example.com:5432/orders",
		"jdbc:postgresql://jane@db.example.com:5432/orders",
		"jdbc:postgresql://db.example.com:5432/orders?user=jane&ssl=true",
		"jdbc:sqlite:/var/data/app.db",
	} {
		got, ok := readJDBC(url)
		if !ok {
			t.Fatalf("%s could not be read", url)
		}
		if got.HadPassword {
			t.Errorf("%s was said to carry a password", url)
		}
	}
}

func TestWhatIsNotAJDBCURLAtAll(t *testing.T) {
	for _, s := range []string{"", "postgresql://db/x", "jdbc:", "jdbc:postgresql"} {
		if _, ok := readJDBC(s); ok {
			t.Errorf("%q was read as a JDBC URL", s)
		}
	}
}
