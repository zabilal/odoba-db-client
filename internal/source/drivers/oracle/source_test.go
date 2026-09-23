package oracle

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strings"
	"syscall"
	"testing"

	"github.com/sijms/go-ora/v2/network"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Reaching a server, and saying what happened when it could not be reached.

func settings(pw string, tlsCfg source.TLSConfig) source.ConnectionConfig {
	return source.ConnectionConfig{DriverID: driverID, Host: "db.example", Port: 1521,
		Database: "FREEPDB1", User: "reader",
		Secret: func(string) (string, error) { return pw, nil }, TLS: tlsCfg}
}

func parsed(t *testing.T, cfg source.ConnectionConfig) *url.URL {
	t.Helper()
	s, err := dsn(cfg)
	if err != nil {
		t.Fatalf("dsn: %v", err)
	}
	u, err := url.Parse(s)
	if err != nil {
		t.Fatalf("the connection string is not a URL: %v", err)
	}
	return u
}

// A password is carried where its punctuation cannot be read as the
// connection string's own (NFR-S6).
func TestAPasswordCannotEndTheConnectionString(t *testing.T) {
	u := parsed(t, settings("pa/ss@wo rd", source.TLSConfig{Mode: "disable"}))
	pw, _ := u.User.Password()
	if pw != "pa/ss@wo rd" {
		t.Errorf("the password came back as %q", pw)
	}
	if strings.Contains(u.RawQuery, "pa/ss") {
		t.Errorf("the password is loose in the connection string: %s", u.RawQuery)
	}
	if u.Host != "db.example:1521" || u.Path != "/FREEPDB1" {
		t.Errorf("it dials %q%q", u.Host, u.Path)
	}
}

// Without a host or a port, the ones a person would have typed.
func TestTheDefaultsAPersonWouldHaveTyped(t *testing.T) {
	u := parsed(t, source.ConnectionConfig{DriverID: driverID, Database: "FREEPDB1",
		TLS: source.TLSConfig{Mode: "disable"}})
	if u.Host != "localhost:1521" {
		t.Errorf("it dials %q", u.Host)
	}
}

// A connection needs a service to connect to, and says so rather than
// failing at the listener.
func TestAConnectionNeedsAService(t *testing.T) {
	for _, db := range []string{"", "   "} {
		cfg := settings("x", source.TLSConfig{Mode: "disable"})
		cfg.Database = db
		var ce *source.ConnectError
		_, err := dsn(cfg)
		if !errors.As(err, &ce) || ce.Kind != source.ConnectConfig {
			t.Errorf("a connection with no service: %v", err)
		}
	}
}

// The session is recognisable on the server as this application's.
func TestTheServerIsToldWhoIsCalling(t *testing.T) {
	u := parsed(t, settings("x", source.TLSConfig{Mode: "disable"}))
	if got := u.Query().Get("program"); got != "ikigai-db" {
		t.Errorf("the program is %q", got)
	}
	if u.Query().Get("timeout") == "" {
		t.Error("a connection that cannot be made would be waited for forever")
	}
}

// Encryption is on and verified unless somebody chose otherwise (NFR-S3).
func TestEncryptionIsOnAndVerifiedByDefault(t *testing.T) {
	cases := []struct{ mode, ssl, verify string }{
		{"", "true", "true"},
		{"verify-ca", "true", "true"},
		{"verify-full", "true", "true"},
		{"require", "true", "false"},
		{"disable", "false", ""},
	}
	for _, c := range cases {
		q := url.Values{}
		if err := encryption(source.TLSConfig{Mode: c.mode}, q); err != nil {
			t.Fatalf("mode %q: %v", c.mode, err)
		}
		if got := q.Get("SSL"); got != c.ssl {
			t.Errorf("mode %q encrypts %q, want %q", c.mode, got, c.ssl)
		}
		if got := q.Get("SSL Verify"); got != c.verify {
			t.Errorf("mode %q verifies %q, want %q", c.mode, got, c.verify)
		}
	}
	if err := encryption(source.TLSConfig{Mode: "sort-of"}, url.Values{}); err == nil {
		t.Error("a way of encrypting nobody has heard of was accepted")
	}
	q := url.Values{}
	if err := encryption(source.TLSConfig{CAFile: "/wallet"}, q); err != nil || q.Get("wallet") != "/wallet" {
		t.Errorf("the wallet is %q, %v", q.Get("wallet"), err)
	}
	if _, err := dsn(settings("x", source.TLSConfig{Mode: "sort-of"})); err == nil {
		t.Error("settings nobody can connect with were used")
	}
}

// The release out of the banner, which is a sentence about what the edition
// is for as much as a version.
func TestTheVersionIsTheVersion(t *testing.T) {
	cases := map[string]string{
		"Oracle AI Database 26ai Free Release 23.26.3.0.0 - Develop, Learn, and Run for Free": "Oracle AI Database 26ai Free Release 23.26.3.0.0",
		"Oracle Database 19c Enterprise Edition Release 19.0.0.0.0 - Production":              "Oracle Database 19c Enterprise Edition Release 19.0.0.0.0",
		"  something else  ": "something else",
		"":                   "",
	}
	for banner, want := range cases {
		if got := versionOf(banner); got != want {
			t.Errorf("versionOf(%.40q) = %q, want %q", banner, got, want)
		}
	}
}

// What went wrong is said in terms somebody can act on (FR-1.4).
func TestAFailedConnectionSaysWhatToDo(t *testing.T) {
	ora := func(code int) error { return &network.OracleError{ErrCode: code, ErrMsg: "x"} }
	cases := []struct {
		err  error
		want source.ConnectKind
	}{
		{ora(1017), source.ConnectAuth},
		{ora(1005), source.ConnectAuth},
		{ora(28000), source.ConnectAuth},
		{ora(12514), source.ConnectNoDatabase},
		{ora(12505), source.ConnectNoDatabase},
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

// A server failure carries the server's own code and message.
func TestAServerFailureSaysWhatTheServerSaid(t *testing.T) {
	err := statementError(&network.OracleError{ErrCode: 942,
		ErrMsg: "ORA-00942: table or view does not exist\nHelp: https://example"}, context.Background())
	var se *source.StatementError
	if !errors.As(err, &se) {
		t.Fatalf("%v is not a statement error", err)
	}
	if se.Message.Code != "ORA-00942" {
		t.Errorf("the code is %q", se.Message.Code)
	}
	if se.Message.Text != "table or view does not exist" {
		t.Errorf("the message is %q", se.Message.Text)
	}
}

func TestAMessageIsCutToWhatItSays(t *testing.T) {
	cases := map[string]string{
		"ORA-00942: table or view does not exist\nHelp: x": "table or view does not exist",
		"ORA-00942: table or view does not exist":          "table or view does not exist",
		"something else": "something else",
		"  plain  ":      "plain",
		"":               "",
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
	for _, err := range []error{&network.OracleError{ErrCode: 1013}, errors.New("read: connection reset")} {
		if got := statementError(err, ctx); !errors.Is(got, context.Canceled) {
			t.Errorf("%v became %v, want the cancellation", err, got)
		}
	}
	// ORA-01013 is the server's word for it, whoever asked.
	if got := statementError(&network.OracleError{ErrCode: 1013}, context.Background()); !errors.Is(got, context.Canceled) {
		t.Errorf("a statement somebody else stopped became %v", got)
	}
	// A failure that arrives after the statement was stopped is the
	// stopping too.
	if got := statementError(&network.OracleError{ErrCode: 942}, ctx); !errors.Is(got, context.Canceled) {
		t.Errorf("a failure after a stop became %v", got)
	}
	if statementError(nil, ctx) != nil {
		t.Error("nothing went wrong, and something was reported")
	}
	if got := statementError(context.Canceled, nil); !errors.Is(got, context.Canceled) {
		t.Errorf("a cancellation became %v", got)
	}
}

// The window is told what this source is and what it holds.
func TestWhatTheWindowIsTold(t *testing.T) {
	s := &oracleSource{}
	caps := s.Capabilities()
	if !caps.Structure.Schemas || caps.Structure.MultipleDatabases {
		t.Errorf("a connection is to one service and holds schemas: %+v", caps.Structure)
	}
	if !caps.Data.Insert || !caps.Data.Update || !caps.Data.Delete || !caps.Data.TransactionalWrite {
		t.Errorf("rows are written here, in a transaction: %+v", caps.Data)
	}
	if !caps.Supports(model.KindSchema) || !caps.Supports(model.KindTable) {
		t.Errorf("the explorer is not told what it will be shown: %+v", caps.Objects)
	}
}
