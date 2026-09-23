package clickhouse

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	ch "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/proto"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Reaching a server, and saying what happened when it could not be reached.

func settings(pw string, tlsCfg source.TLSConfig) source.ConnectionConfig {
	return source.ConnectionConfig{DriverID: driverID, Host: "db.example", Port: 9000, User: "reader",
		Database: "shop", Secret: func(string) (string, error) { return pw, nil }, TLS: tlsCfg}
}

// What a connection is opened with is what was asked for, and the password
// travels in the settings rather than in a string somebody could log.
func TestTheConnectionIsOpenedWithWhatWasAskedFor(t *testing.T) {
	opt, err := options(settings("pa;ss word", source.TLSConfig{Mode: "disable"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(opt.Addr) != 1 || opt.Addr[0] != "db.example:9000" {
		t.Errorf("it dials %q", opt.Addr)
	}
	if opt.Auth.Username != "reader" || opt.Auth.Password != "pa;ss word" || opt.Auth.Database != "shop" {
		t.Errorf("the login is %+v", opt.Auth)
	}
	if opt.Protocol != ch.Native {
		t.Errorf("it speaks %v, want the native protocol", opt.Protocol)
	}
	if len(opt.ClientInfo.Products) == 0 || opt.ClientInfo.Products[0].Name != "ikigai-db" {
		t.Errorf("the server is told it is talking to %+v", opt.ClientInfo.Products)
	}
	if opt.DialTimeout == 0 {
		t.Error("a connection that cannot be made would be waited for forever")
	}
}

// Without a host, a port or a user, the ones a person would have typed.
func TestTheDefaultsAPersonWouldHaveTyped(t *testing.T) {
	opt, err := options(source.ConnectionConfig{DriverID: driverID, TLS: source.TLSConfig{Mode: "disable"}})
	if err != nil {
		t.Fatal(err)
	}
	if opt.Addr[0] != "localhost:9000" {
		t.Errorf("it dials %q", opt.Addr)
	}
	if opt.Auth.Username != "default" {
		t.Errorf("it logs in as %q", opt.Auth.Username)
	}
}

// A password nobody can fetch is no password, and not a failure to connect.
func TestAPasswordThatCannotBeFetched(t *testing.T) {
	cfg := settings("", source.TLSConfig{Mode: "disable"})
	cfg.Secret = func(string) (string, error) { return "unused", errors.New("the keychain is locked") }
	opt, err := options(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if opt.Auth.Password != "" {
		t.Errorf("the password is %q, want none at all", opt.Auth.Password)
	}
}

// Encryption is on and verified unless somebody chose otherwise (NFR-S3).
func TestEncryptionIsOnAndVerifiedByDefault(t *testing.T) {
	for _, mode := range []string{"", "verify-ca", "verify-full"} {
		cfg, err := transport(source.TLSConfig{Mode: mode})
		if err != nil {
			t.Fatalf("mode %q: %v", mode, err)
		}
		if cfg == nil || cfg.InsecureSkipVerify {
			t.Errorf("mode %q gives %+v, want a verified connection", mode, cfg)
		}
	}
	cfg, err := transport(source.TLSConfig{Mode: "require"})
	if err != nil || cfg == nil || !cfg.InsecureSkipVerify {
		t.Errorf("require gives %+v, %v", cfg, err)
	}
	cfg, err = transport(source.TLSConfig{Mode: "disable"})
	if err != nil || cfg != nil {
		t.Errorf("disable gives %+v, %v, want no encryption at all", cfg, err)
	}
	if _, err := transport(source.TLSConfig{Mode: "sort-of"}); err == nil {
		t.Error("a way of encrypting nobody has heard of was accepted")
	}
	cfg, err = transport(source.TLSConfig{ServerName: "other.example"})
	if err != nil || cfg.ServerName != "other.example" {
		t.Errorf("the name checked against the certificate is %+v, %v", cfg, err)
	}
	// A file that is not there, or holds no certificate, is a settings
	// problem and is said as one rather than failing at dial time — and
	// they are two different problems, said differently.
	_, err = transport(source.TLSConfig{CAFile: "/no/such/file"})
	if err == nil || !strings.Contains(err.Error(), "cannot be read") {
		t.Errorf("a certificate file that is not there: %v", err)
	}
	empty := filepath.Join(t.TempDir(), "empty.pem")
	if err := os.WriteFile(empty, []byte("not a certificate\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = transport(source.TLSConfig{CAFile: empty})
	if err == nil || !strings.Contains(err.Error(), "holds no certificate") {
		t.Errorf("a file holding no certificate: %v", err)
	}
	if _, err := transport(source.TLSConfig{CertFile: "/no/such/file", KeyFile: "/no/such/key"}); err == nil {
		t.Error("a key pair that is not there was accepted")
	}
	if _, err := options(settings("x", source.TLSConfig{Mode: "sort-of"})); err == nil {
		t.Error("settings nobody can connect with were used")
	}
}

// What went wrong is said in terms somebody can act on (FR-1.4).
func TestAFailedConnectionSaysWhatToDo(t *testing.T) {
	cases := []struct {
		err  error
		want source.ConnectKind
	}{
		{&proto.Exception{Code: 516, Message: "Authentication failed"}, source.ConnectAuth},
		{&proto.Exception{Code: 192, Message: "Unknown user"}, source.ConnectAuth},
		{&proto.Exception{Code: 193, Message: "Wrong password"}, source.ConnectAuth},
		{&proto.Exception{Code: 81, Message: "Database does not exist"}, source.ConnectNoDatabase},
		{&tls.CertificateVerificationError{}, source.ConnectTLS},
		{errors.New("x509: certificate signed by unknown authority"), source.ConnectTLS},
		{&net.DNSError{Err: "no such host"}, source.ConnectUnreachable},
		{syscall.ECONNREFUSED, source.ConnectRefused},
		{&net.DNSError{Err: "timeout", IsTimeout: true}, source.ConnectUnreachable},
		{errors.New("something else"), source.ConnectUnknown},
	}
	for _, c := range cases {
		var ce *source.ConnectError
		if !errors.As(classifyConnectError(c.err), &ce) {
			t.Fatalf("%v was not a connection failure", c.err)
		}
		if ce.Kind != c.want {
			t.Errorf("%v is %v, want %v", c.err, ce.Kind, c.want)
		}
		if ce.Hint == "" {
			t.Errorf("%v says nothing to do about it", c.err)
		}
	}
	given := &source.ConnectError{Kind: source.ConnectConfig, Hint: "x"}
	if got := classifyConnectError(given); got != error(given) {
		t.Errorf("a classified failure was classified again: %v", got)
	}
}

// A server failure carries the server's own code and its message, and not
// the list of every token the parser would have accepted.
func TestAServerFailureSaysWhatTheServerSaid(t *testing.T) {
	err := statementError(&proto.Exception{Code: 60,
		Message: "Table shop.nope does not exist. Maybe you meant shop.nope2?\nStack trace:\n  0x1"},
		context.Background())
	var se *source.StatementError
	if !errors.As(err, &se) {
		t.Fatalf("%v is not a statement error", err)
	}
	if se.Message.Code != "60" {
		t.Errorf("the code is %q", se.Message.Code)
	}
	if se.Message.Text != "Table shop.nope does not exist. Maybe you meant shop.nope2?" {
		t.Errorf("the message is %q", se.Message.Text)
	}
}

func TestAMessageIsCutToWhatItSays(t *testing.T) {
	cases := map[string]string{
		"Syntax error: failed at position 1. Expected one of: Query, SELECT, WITH, FROM": "Syntax error: failed at position 1.",
		"Table t does not exist\nStack trace:\n  0x1":                                    "Table t does not exist",
		"  plain  ": "plain",
		"":          "",
	}
	for msg, want := range cases {
		if got := firstLine(msg); got != want {
			t.Errorf("firstLine(%q) = %q, want %q", msg, got, want)
		}
	}
}

// A statement stopped on purpose reports the stopping, not whatever the
// server said about being cancelled.
func TestAStoppedStatementReportsTheStopping(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, err := range []error{&proto.Exception{Code: 394, Message: "Query was cancelled"},
		errors.New("read: connection reset")} {
		if got := statementError(err, ctx); !errors.Is(got, context.Canceled) {
			t.Errorf("%v became %v, want the cancellation", err, got)
		}
	}
	// Even where nobody here stopped it: somebody else did, and what came
	// back is not a result.
	if got := statementError(&proto.Exception{Code: 394}, context.Background()); !errors.Is(got, context.Canceled) {
		t.Errorf("a statement somebody else stopped became %v", got)
	}
	// A server failure that arrives after the statement was stopped is the
	// stopping too: what the server was complaining about is that it was
	// asked to stop.
	if got := statementError(&proto.Exception{Code: 60, Message: "Unknown table"}, ctx); !errors.Is(got, context.Canceled) {
		t.Errorf("a failure after a stop became %v", got)
	}
	if statementError(nil, ctx) != nil {
		t.Error("nothing went wrong, and something was reported")
	}
	if got := statementError(context.Canceled, nil); !errors.Is(got, context.Canceled) {
		t.Errorf("a cancellation became %v", got)
	}
}

// A statement is stopped by the name its session gave it, and a session
// with no name stops nothing.
func TestAStatementIsStoppedByItsName(t *testing.T) {
	s := &clickhouseSource{}
	if err := s.KillQuery(context.Background(), ""); err == nil {
		t.Error("a statement with no name was stopped")
	}
}
