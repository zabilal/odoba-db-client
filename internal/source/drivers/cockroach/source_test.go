package cockroach

import (
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// The driver describes itself well enough for the connection form to be
// built from it (FR-1.1).
func TestTheDriverDescribesItself(t *testing.T) {
	desc := Driver{}.Describe()
	if desc.ID != driverID || desc.Paradigm != model.ParadigmRelational {
		t.Fatalf("the driver describes itself as %+v", desc)
	}
	if desc.DefaultPort != 26257 {
		t.Errorf("the default port is %d", desc.DefaultPort)
	}
	// Not "postgres": that scheme belongs to the PostgreSQL driver, and a
	// URL has to resolve to one driver.
	for _, s := range desc.URLSchemes {
		if s == "postgres" || s == "postgresql" {
			t.Errorf("this driver claims %q, which is another driver's", s)
		}
	}
	want := map[string]bool{"host": true, "port": true, "database": true, "user": true, "password": true}
	for _, f := range desc.Fields {
		delete(want, f.Key)
		if f.Key == "password" && !f.Secret {
			t.Error("the password is not marked as a secret (FR-1.5)")
		}
	}
	if len(want) != 0 {
		t.Errorf("the form is missing %v", want)
	}
}

// The release is what somebody wants to read, out of a banner that names
// the build and the platform as well.
func TestTheVersionIsTheRelease(t *testing.T) {
	for in, want := range map[string]string{
		"CockroachDB CCL v26.3.2 (aarch64-unknown-linux-gnu, built 2026/09/16 12:26:13, go1.26.6)": "v26.3.2",
		"CockroachDB OSS v23.1.11 (x86_64-pc-linux-gnu)":                                           "v23.1.11",
		"something else entirely": "something else entirely",
		"":                        "",
		// A word that merely begins with v is not a release.
		"CockroachDB variant v1.2.3": "v1.2.3",
		"CockroachDB vanilla":        "CockroachDB vanilla",
	} {
		if got := versionOf(in); got != want {
			t.Errorf("versionOf(%q) = %q, want %q", in, got, want)
		}
	}
}

// A connection that fails says why in terms somebody can act on (FR-1.4).
func TestAFailedConnectionSaysWhy(t *testing.T) {
	for _, c := range []struct {
		name string
		err  error
		want source.ConnectKind
	}{
		{"a password", &pgconn.PgError{Code: "28P01"}, source.ConnectAuth},
		{"no such role", &pgconn.PgError{Code: "28000"}, source.ConnectAuth},
		{"no such database", &pgconn.PgError{Code: "3D000"}, source.ConnectNoDatabase},
		{"too many clients", &pgconn.PgError{Code: "53300"}, source.ConnectRefused},
		{"shutting down", &pgconn.PgError{Code: "57P03"}, source.ConnectRefused},
		{"no such host", &net.DNSError{Err: "no such host", IsNotFound: true}, source.ConnectUnreachable},
		{"no TLS there", errors.New("server refused TLS connection"), source.ConnectTLS},
		{"something else", errors.New("who knows"), source.ConnectUnknown},
	} {
		t.Run(c.name, func(t *testing.T) {
			var ce *source.ConnectError
			if !errors.As(classifyConnectError(c.err), &ce) {
				t.Fatalf("%v was not classified at all", c.err)
			}
			if ce.Kind != c.want {
				t.Errorf("%v classified as %v, want %v", c.err, ce.Kind, c.want)
			}
			if ce.Hint == "" {
				t.Error("nothing was said about what to do")
			}
		})
	}
	// A source that is closed says so as itself, not dressed up as a
	// connection that failed: nothing was dialled and nothing went wrong
	// with a server.
	got := classifyConnectError(errClosed)
	if got != errClosed {
		t.Errorf("a closed source reads as %#v", got)
	}
	var ce *source.ConnectError
	if errors.As(got, &ce) {
		t.Errorf("a closed source was classified as a connection failure: %v", ce.Kind)
	}
}

// Nothing about the connection comes from the environment: a stray
// variable in somebody's shell must not change which cluster a saved
// connection reaches, or whether it is encrypted.
func TestTheEnvironmentDoesNotReachTheConnection(t *testing.T) {
	t.Setenv("PGHOST", "elsewhere.example.com")
	t.Setenv("PGSSLMODE", "disable")
	t.Setenv("PGDATABASE", "somebody_elses")

	s := &crdbSource{primary: "shop", cfg: source.ConnectionConfig{Host: "127.0.0.1", Port: 26257}}
	pc, err := s.config()
	if err != nil {
		t.Fatal(err)
	}
	if pc.ConnConfig.Host != "127.0.0.1" {
		t.Errorf("the host is %q", pc.ConnConfig.Host)
	}
	if pc.ConnConfig.Database != "shop" {
		t.Errorf("the database is %q", pc.ConnConfig.Database)
	}
	if pc.ConnConfig.TLSConfig == nil {
		t.Error("TLS was turned off by the environment (NFR-S3)")
	}
	if pc.ConnConfig.Fallbacks != nil {
		t.Error("the connection may still fall back to plaintext")
	}
	if pc.ConnConfig.RuntimeParams["application_name"] != "Ikigai DB" {
		t.Errorf("the session is named %q", pc.ConnConfig.RuntimeParams["application_name"])
	}
}

// A read-only connection says so to the server as well, which is the last
// of three defences (NFR-S4).
func TestAReadOnlyConnectionTellsTheServer(t *testing.T) {
	s := &crdbSource{primary: "shop", cfg: source.ConnectionConfig{Guard: source.Guard{ReadOnly: true}}}
	pc, err := s.config()
	if err != nil {
		t.Fatal(err)
	}
	if pc.ConnConfig.RuntimeParams["default_transaction_read_only"] != "on" {
		t.Error("a read-only connection did not say so as it opened")
	}
	// And a writable one does not say it, rather than saying "off": the
	// server's own default is what a connection with no opinion gets.
	plain := &crdbSource{primary: "shop"}
	pc, err = plain.config()
	if err != nil {
		t.Fatal(err)
	}
	if _, set := pc.ConnConfig.RuntimeParams["default_transaction_read_only"]; set {
		t.Error("a writable connection has an opinion about read-only mode")
	}
}

// A port that is not a port is refused before anything is dialled.
func TestAPortOutOfRangeIsRefused(t *testing.T) {
	for _, port := range []int{-1, 65536, 100000} {
		s := &crdbSource{primary: "shop", cfg: source.ConnectionConfig{Port: port}}
		if _, err := s.config(); err == nil {
			t.Errorf("port %d was accepted", port)
		}
	}
	// Nothing at all means the engine's own port.
	s := &crdbSource{primary: "shop"}
	pc, err := s.config()
	if err != nil {
		t.Fatal(err)
	}
	if pc.ConnConfig.Port != 26257 {
		t.Errorf("no port given reached %d", pc.ConnConfig.Port)
	}
	if pc.ConnConfig.Host != "localhost" {
		t.Errorf("no host given reached %q", pc.ConnConfig.Host)
	}
}

// A password is read from the keychain, and a keychain that will not open
// fails the connection rather than opening one without it (FR-1.5).
func TestThePasswordComesFromTheKeychain(t *testing.T) {
	s := &crdbSource{primary: "shop", cfg: source.ConnectionConfig{
		Secret: func(string) (string, error) { return "opensesame", nil }}}
	pc, err := s.config()
	if err != nil {
		t.Fatal(err)
	}
	if pc.ConnConfig.Password != "opensesame" {
		t.Errorf("the password read %q", pc.ConnConfig.Password)
	}
	locked := &crdbSource{primary: "shop", cfg: source.ConnectionConfig{
		Secret: func(string) (string, error) { return "", errors.New("the keychain is locked") }}}
	if _, err := locked.config(); err == nil || !strings.Contains(err.Error(), "keychain") {
		t.Errorf("a locked keychain gave %v", err)
	}
}

// Every capability claimed has the interface behind it, and the language
// is one the editor knows how to read.
func TestNothingIsClaimedWithoutSomethingBehindIt(t *testing.T) {
	s := &crdbSource{}
	caps := s.Capabilities()
	if caps.Query.Cancel {
		if _, ok := any(s).(source.Killer); !ok {
			t.Error("Cancel is claimed without a Killer")
		}
	}
	if caps.Data.DistinctValues {
		if _, ok := any(s).(source.DistinctLister); !ok {
			t.Error("DistinctValues is claimed without a DistinctLister")
		}
	}
	if caps.Data.Insert || caps.Data.Update || caps.Data.Delete {
		if _, ok := any(s).(source.Writer); !ok {
			t.Error("writing is claimed without a Writer")
		}
	}
	// A capability with no interface behind it is worse than none: these
	// are the ones this driver does not claim.
	if caps.Query.Transactions {
		t.Error("Query.Transactions is claimed and there is no Transactor")
	}
	if caps.Query.Explain || caps.Query.ExplainAnalyze {
		t.Error("Explain is claimed and there is no Explainer")
	}
	if caps.Query.EditableResults {
		t.Error("EditableResults is claimed and nothing tells a result's columns where they came from")
	}
	if caps.Data.BulkLoad {
		t.Error("BulkLoad is claimed and there is no BulkLoader")
	}
	if caps.Data.ColumnStats {
		t.Error("ColumnStats is claimed and there is no Statistician")
	}
	if caps.Schema.DDL || caps.Schema.Diff {
		t.Error("DDL or Diff is claimed and there is nothing to render or snapshot")
	}
	// A cluster holds databases and each holds schemas: three parts.
	if !caps.Structure.MultipleDatabases || !caps.Structure.Schemas {
		t.Errorf("the structure reads %+v", caps.Structure)
	}
}

// Closing twice is closing once (source.Source).
func TestClosingTwiceIsClosingOnce(t *testing.T) {
	s := &crdbSource{}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("the second close said %v", err)
	}
	if _, err := s.conn(); !errors.Is(err, errClosed) {
		t.Errorf("a closed source handed out a connection: %v", err)
	}
}
