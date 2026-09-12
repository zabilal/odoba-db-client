package connstr

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/ikigai-db/ikigai-db/internal/source/drivers/mongo"
	_ "github.com/ikigai-db/ikigai-db/internal/source/drivers/postgres"
	"github.com/ikigai-db/ikigai-db/internal/store"
)

func TestParse(t *testing.T) {
	cases := []struct {
		in       string
		host     string
		port     int
		db, user string
		pw       string
		tls      string
		params   map[string]string
	}{
		{"postgres://ada:hunter2@db.example.com:6543/sales?sslmode=require&application_name=report",
			"db.example.com", 6543, "sales", "ada", "hunter2", "require", map[string]string{"application_name": "report"}},
		{"postgresql://ada@localhost/sales", "localhost", 0, "sales", "ada", "", "", nil},
		{"postgres://ada:p%40ss%3Aw0rd@h/d", "h", 0, "d", "ada", "p@ss:w0rd", "", nil}, // percent-encoded
		{"postgres://[::1]:5432/d", "::1", 5432, "d", "", "", "", nil},                 // IPv6
		{"postgres:///sales?host=/var/run/postgresql", "/var/run/postgresql", 0, "sales", "", "", "", nil},
		{"jdbc:postgresql://h:5432/d?user=ada&password=hunter2", "h", 5432, "d", "ada", "hunter2", "", nil},
		{"jdbc:postgresql://h/d?ssl=false", "h", 0, "d", "", "", "disable", nil},
		{"host=db port=5432 dbname='my db' user=ada password='it\\'s secret' sslmode=verify-ca",
			"db", 5432, "my db", "ada", "it's secret", "verify-ca", nil},
		{"  host = db   dbname=x  ", "db", 0, "x", "", "", "", nil},
	}
	for _, c := range cases {
		r, err := Parse(c.in)
		if err != nil {
			t.Errorf("Parse(%q): %v", c.in, err)
			continue
		}
		got := r.Conn
		if got.Driver != "postgres" || got.Host != c.host || got.Port != c.port ||
			got.Database != c.db || got.User != c.user || got.TLS.Mode != c.tls {
			t.Errorf("Parse(%q) = %+v", c.in, got)
		}
		if r.Secrets["password"] != c.pw {
			t.Errorf("Parse(%q) password = %q, want %q", c.in, r.Secrets["password"], c.pw)
		}
		for k, v := range c.params {
			if got.Params[k] != v {
				t.Errorf("Parse(%q) param %s = %q, want %q", c.in, k, got.Params[k], v)
			}
		}
	}
}

func TestSecretsNeverLandInTheConnection(t *testing.T) {
	// Every result must be storable, with no secret anywhere in what the
	// settings file would hold. Checked by actually saving it.
	inputs := []string{
		"postgres://ada:hunter2@h/d?sslpassword=keypass&api-key=k3y",
		"jdbc:postgresql://h/d?user=ada&password=hunter2",
		"host=h password=hunter2 sslpassword=keypass",
	}
	path := filepath.Join(t.TempDir(), "settings.json")
	sf, _, _ := store.OpenSettings(path)
	for _, in := range inputs {
		r, err := Parse(in)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		conn := r.Conn
		conn.ID = store.NewID()
		for k := range r.Secrets {
			conn.Secrets = append(conn.Secrets, k)
		}
		if err := sf.Update(func(s *store.Settings) error {
			s.Connections = append(s.Connections, conn)
			return nil
		}); err != nil {
			t.Errorf("%q: result is not storable: %v", in, err)
		}
		data, _ := json.Marshal(conn)
		for _, secret := range []string{"hunter2", "keypass", "k3y"} {
			if strings.Contains(string(data), secret) {
				t.Errorf("%q: secret %q is in the connection itself: %s", in, secret, data)
			}
		}
	}
}

func TestPreferSslmodeWarnsAndVerifiesInstead(t *testing.T) {
	r, err := Parse("postgres://h/d?sslmode=prefer")
	if err != nil {
		t.Fatal(err)
	}
	if r.Conn.TLS.Mode != "" || len(r.Warnings) == 0 || !strings.Contains(r.Warnings[0], "fall back") {
		t.Errorf("prefer should verify, and say why: mode %q, warnings %q", r.Conn.TLS.Mode, r.Warnings)
	}
}

func TestErrorsNeverRepeatTheInput(t *testing.T) {
	cases := []string{
		"postgres://ada:hunt%zzer2@h/d", // invalid escape: url.Parse would echo "%zz"
		"postgres://ada:hunter2@h:99999/d",
		"host=h password='hunter2",       // unterminated
		"postgres://ada:hunter2@h1,h2/d", // multiple hosts
		"mysql://ada:hunter2@h/d",        // no driver
	}
	for _, in := range cases {
		_, err := Parse(in)
		if err == nil {
			t.Errorf("%q: accepted", in)
			continue
		}
		for _, frag := range []string{"hunter2", "hunt", "zzer", "%zz"} {
			if strings.Contains(err.Error(), frag) {
				t.Errorf("%q: error leaks %q: %v", in, frag, err)
			}
		}
	}
}

func TestRejectsGarbage(t *testing.T) {
	for _, in := range []string{"", "   ", "just some words", "jdbc:sqlserver://h:1433;databaseName=x"} {
		if _, err := Parse(in); err == nil {
			t.Errorf("%q: accepted", in)
		}
	}
}

func TestParsePgpass(t *testing.T) {
	file := `# comment
db.example.com:5432:sales:ada:s3cr\:et\\x
*:*:*:postgres:anything
localhost:*:*:bob:pw
not a valid line
`
	rs, warns, err := ParsePgpass(strings.NewReader(file))
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 2 {
		t.Fatalf("got %d connections, want 2 (wildcard host and malformed line skipped)", len(rs))
	}
	if rs[0].Conn.Host != "db.example.com" || rs[0].Conn.Port != 5432 || rs[0].Secrets["password"] != `s3cr:et\x` {
		t.Errorf("escapes: %+v %q", rs[0].Conn, rs[0].Secrets["password"])
	}
	if rs[1].Conn.Port != 0 || rs[1].Conn.Database != "" {
		t.Errorf("wildcards should mean unset: %+v", rs[1].Conn)
	}
	if len(warns) != 2 {
		t.Errorf("want 2 warnings, got %q", warns)
	}
	for _, w := range warns {
		if strings.Contains(w, "anything") {
			t.Errorf("warning echoes a password: %q", w)
		}
	}
}

func TestParseMyCnf(t *testing.T) {
	file := `[mysqldump]
user=dumper
[client]
host = db.local
port=3307
user=ada
password="p#ss w0rd"
; comment
[mysql]
database=shop
user=override
`
	r, err := parseMyCnf(strings.NewReader(file), "mysql")
	if err != nil {
		t.Fatal(err)
	}
	c := r.Conn
	if c.Host != "db.local" || c.Port != 3307 || c.Database != "shop" || c.User != "override" {
		t.Errorf("got %+v", c)
	}
	if r.Secrets["password"] != "p#ss w0rd" {
		t.Errorf("password = %q", r.Secrets["password"])
	}
	if _, err := ParseMyCnf(strings.NewReader(file)); err == nil {
		t.Error("ParseMyCnf should refuse while no MySQL driver is installed")
	}
}

func TestParseSeedListScheme(t *testing.T) {
	r, err := Parse("mongodb+srv://reader:pw@cluster0.abc.mongodb.net/shop?authSource=admin&retryWrites=true")
	if err != nil {
		t.Fatal(err)
	}
	if r.Conn.Driver != "mongodb" {
		t.Errorf("driver %q, want the MongoDB driver", r.Conn.Driver)
	}
	if r.Conn.Params["srv"] != "true" {
		t.Errorf("params %v, want the seed list marked", r.Conn.Params)
	}
	if r.Conn.Host != "cluster0.abc.mongodb.net" || r.Conn.Database != "shop" || r.Conn.User != "reader" {
		t.Errorf("connection %+v", r.Conn)
	}
	if r.Secrets["password"] != "pw" {
		t.Errorf("the password did not reach the keychain: %v", r.Secrets)
	}
	if r.Conn.Params["authSource"] != "admin" || r.Conn.Params["retryWrites"] != "true" {
		t.Errorf("params %v, want the driver's own options kept", r.Conn.Params)
	}
	// A server named outright is not a seed list.
	plain, err := Parse("mongodb://localhost:27017/shop")
	if err != nil {
		t.Fatal(err)
	}
	if plain.Conn.Params["srv"] != "" {
		t.Errorf("params %v, want no seed list", plain.Conn.Params)
	}
	if plain.Conn.Port != 27017 {
		t.Errorf("port %d", plain.Conn.Port)
	}
}
