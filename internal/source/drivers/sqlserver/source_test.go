package sqlserver

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/url"
	"strings"
	"syscall"
	"testing"

	mssql "github.com/microsoft/go-mssqldb"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Reaching a server, and saying what happened when it could not be reached.

func settings(pw string, tlsCfg source.TLSConfig) source.ConnectionConfig {
	return source.ConnectionConfig{DriverID: driverID, Host: "db.example", Port: 1433, User: "sa",
		Secret: func(string) (string, error) { return pw, nil }, TLS: tlsCfg}
}

func query(t *testing.T, s string) url.Values {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatalf("the connection string is not a URL: %v", err)
	}
	return u.Query()
}

// A password is carried where its punctuation cannot be read as the
// connection string's own (NFR-S6).
func TestAPasswordCannotEndTheConnectionString(t *testing.T) {
	s, err := dsn(settings("pa;ss=wo rd", source.TLSConfig{}), "shop")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(s)
	if err != nil {
		t.Fatalf("the connection string is not a URL: %v", err)
	}
	pw, _ := u.User.Password()
	if pw != "pa;ss=wo rd" {
		t.Errorf("the password came back as %q", pw)
	}
	if strings.Contains(u.RawQuery, "pa;ss") {
		t.Errorf("the password is loose in the connection string: %s", u.RawQuery)
	}
	if got := u.Host; got != "db.example:1433" {
		t.Errorf("host %q", got)
	}
	if got := query(t, s).Get("database"); got != "shop" {
		t.Errorf("database %q", got)
	}
}

// Without a host or a port, the ones a person would have typed.
func TestADefaultHostAndPort(t *testing.T) {
	cfg := source.ConnectionConfig{DriverID: driverID}
	s, err := dsn(cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(s)
	if u.Host != "localhost:1433" {
		t.Errorf("host %q, want localhost:1433", u.Host)
	}
	if q := query(t, s); q.Has("database") {
		t.Errorf("a database was named when none was asked for: %q", q.Get("database"))
	}
}

// A session is recognisable on the server as this application's.
func TestTheServerIsToldWhoIsCalling(t *testing.T) {
	s, err := dsn(settings("x", source.TLSConfig{}), "shop")
	if err != nil {
		t.Fatal(err)
	}
	if got := query(t, s).Get("app name"); got != "ikigai-db" {
		t.Errorf("app name %q", got)
	}
}

// Encryption is on and verified unless somebody chose otherwise (NFR-S3).
func TestEncryptionIsOnAndVerifiedByDefault(t *testing.T) {
	cases := []struct {
		mode            string
		encrypt, trusts string
	}{
		{"", "true", "false"},
		{"verify-full", "true", "false"},
		{"verify-ca", "true", "false"},
		{"require", "true", "true"},
		{"disable", "disable", ""},
	}
	for _, c := range cases {
		q := url.Values{}
		if err := encryption(source.TLSConfig{Mode: c.mode}, q); err != nil {
			t.Fatalf("mode %q: %v", c.mode, err)
		}
		if got := q.Get("encrypt"); got != c.encrypt {
			t.Errorf("mode %q encrypts %q, want %q", c.mode, got, c.encrypt)
		}
		if got := q.Get("TrustServerCertificate"); got != c.trusts {
			t.Errorf("mode %q trusts %q, want %q", c.mode, got, c.trusts)
		}
	}
	if err := encryption(source.TLSConfig{Mode: "sort-of"}, url.Values{}); err == nil {
		t.Error("a way of encrypting nobody has heard of was accepted")
	}
	q := url.Values{}
	if err := encryption(source.TLSConfig{ServerName: "other.example", CAFile: "/ca.pem"}, q); err != nil {
		t.Fatal(err)
	}
	if q.Get("hostNameInCertificate") != "other.example" || q.Get("certificate") != "/ca.pem" {
		t.Errorf("the certificate settings are %v", q)
	}
}

// Settings nobody can connect with are refused where they are read, rather
// than handed to the driver to complain about.
func TestSettingsThatCannotBeUsedAreRefused(t *testing.T) {
	if _, err := dsn(settings("x", source.TLSConfig{Mode: "sort-of"}), "shop"); err == nil {
		t.Error("a connection string was built from settings nobody can use")
	}
}

// What answered is named as itself: Azure's services are not a SQL Server
// somebody runs, and saying so would tell a person the wrong thing about
// what they may do to it.
func TestWhatAnsweredIsNamedAsItself(t *testing.T) {
	cases := map[int]string{
		1: "Microsoft SQL Server", 2: "Microsoft SQL Server", 3: "Microsoft SQL Server",
		4: "Microsoft SQL Server", 5: "Azure SQL Database", 6: "Azure Synapse Analytics",
		8: "Azure SQL Managed Instance", 9: "Azure SQL Edge", 11: "Azure Synapse Analytics",
		0: "Microsoft SQL Server",
	}
	for engine, want := range cases {
		if got := productOf(engine); got != want {
			t.Errorf("productOf(%d) = %q, want %q", engine, got, want)
		}
	}
}

// What the health display is told is the build, and the edition beside it
// (FR-1.16).
func TestTheServerSaysWhichBuildItIs(t *testing.T) {
	s := &sqlServerSource{version: "16.0.4115.5", edition: "Developer Edition (64-bit)", engine: 3}
	info, err := s.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Product != "Microsoft SQL Server" || info.Version != "16.0.4115.5" {
		t.Errorf("info is %+v", info)
	}
	if info.Attrs["edition"] != "Developer Edition (64-bit)" {
		t.Errorf("the edition is %q", info.Attrs["edition"])
	}
}

// What went wrong is said in terms somebody can act on (FR-1.4).
func TestAFailedConnectionSaysWhatToDo(t *testing.T) {
	cases := []struct {
		err  error
		want source.ConnectKind
	}{
		{mssql.Error{Number: 18456, Message: "Login failed"}, source.ConnectAuth},
		{mssql.Error{Number: 18452, Message: "Login failed"}, source.ConnectAuth},
		{mssql.Error{Number: 4060, Message: "Cannot open database"}, source.ConnectNoDatabase},
		{mssql.Error{Number: 911, Message: "Database does not exist"}, source.ConnectNoDatabase},
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
	// One already classified is passed through as it is.
	given := &source.ConnectError{Kind: source.ConnectConfig, Hint: "x"}
	if got := classifyConnectError(given); got != error(given) {
		t.Errorf("a classified failure was classified again: %v", got)
	}
}

// A server error says where it was, so the editor can point at it
// (FR-5.10).
func TestAServerErrorSaysWhereItWas(t *testing.T) {
	stmt := "SELECT 1,\n  frm x,\n  2"
	err := statementError(mssql.Error{Number: 102, Message: "Incorrect syntax near 'x'", LineNo: 2},
		context.Background(), stmt)
	var se *source.StatementError
	if !errors.As(err, &se) {
		t.Fatalf("%v is not a statement error", err)
	}
	if se.Message.Code != "102" || se.Message.Text != "Incorrect syntax near 'x'" {
		t.Errorf("the message is %+v", se.Message)
	}
	if got := se.Message.Position; got != 11 {
		t.Errorf("the position is %d, want where line 2 starts", got)
	}
}

// A statement stopped on purpose reports the stopping, not whatever the
// server said about being interrupted.
func TestAStoppedStatementReportsTheStopping(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, err := range []error{mssql.Error{Number: 596, Message: "Cannot continue"}, errors.New("read: connection reset")} {
		if got := statementError(err, ctx, ""); !errors.Is(got, context.Canceled) {
			t.Errorf("%v became %v, want the cancellation", err, got)
		}
	}
	if statementError(nil, ctx, "") != nil {
		t.Error("nothing went wrong, and something was reported")
	}
	if got := statementError(context.Canceled, nil, ""); !errors.Is(got, context.Canceled) {
		t.Errorf("a cancellation became %v", got)
	}
}

// Counting to the start of a line, in characters, so that the offset means
// what the editor means by one.
func TestWhereALineStarts(t *testing.T) {
	stmt := "é1\nab\ncd"
	cases := []struct {
		line, want int
	}{{0, 0}, {1, 0}, {2, 4}, {3, 7}, {4, 0}}
	for _, c := range cases {
		if got := startOfLine(stmt, c.line); got != c.want {
			t.Errorf("startOfLine(line %d) = %d, want %d", c.line, got, c.want)
		}
	}
	if got := startOfLine("", 2); got != 0 {
		t.Errorf("startOfLine of nothing = %d", got)
	}
}
