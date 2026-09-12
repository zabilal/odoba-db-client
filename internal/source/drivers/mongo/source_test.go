package mongo

import (
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

func config(host string, port int, params map[string]string) source.ConnectionConfig {
	return source.ConnectionConfig{DriverID: driverID, Host: host, Port: port, Params: params,
		TLS: source.TLSConfig{Mode: "disable"}}
}

// uri is the address the driver would resolve.
func uri(t *testing.T, cfg source.ConnectionConfig) string {
	t.Helper()
	got, err := connectionURI(cfg)
	if err != nil {
		t.Fatalf("uri: %v", err)
	}
	return got
}

func TestDescriptorClaimsBothSchemes(t *testing.T) {
	d := Driver{}.Describe()
	if d.ID != driverID || d.Paradigm != model.ParadigmDocument {
		t.Errorf("descriptor %+v", d)
	}
	want := map[string]bool{"mongodb": true, "mongodb+srv": true}
	for _, s := range d.URLSchemes {
		delete(want, s)
	}
	if len(want) != 0 {
		t.Errorf("schemes %v, want both mongodb and mongodb+srv", d.URLSchemes)
	}
	// The driver is registered under its ID, and both schemes find it.
	for _, scheme := range []string{"mongodb", "mongodb+srv"} {
		if got, ok := source.LookupScheme(scheme); !ok || got.ID != driverID {
			t.Errorf("%s resolves to %+v", scheme, got)
		}
	}
	var keys []string
	for _, f := range d.Fields {
		keys = append(keys, f.Key)
	}
	for _, want := range []string{"host", "port", "user", "password", "srv", "authSource", "replicaSet"} {
		if !contains(keys, want) {
			t.Errorf("fields %v, want %s among them", keys, want)
		}
	}
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

func TestClientOptionsAddressesTheServer(t *testing.T) {
	if got := uri(t, config("db.example", 27018, nil)); !strings.Contains(got, "db.example:27018") {
		t.Errorf("uri %q, want the host and port", got)
	}
	// The default port, and the default host.
	if got := uri(t, config("", 0, nil)); !strings.Contains(got, net.JoinHostPort("localhost", "27017")) {
		t.Errorf("uri %q, want localhost and 27017", got)
	}
	// A seed list carries its own hosts and ports, so none is written.
	got := uri(t, config("cluster0.abc.mongodb.net", 27018, map[string]string{"srv": "true"}))
	if !strings.HasPrefix(got, "mongodb+srv://cluster0.abc.mongodb.net") {
		t.Errorf("uri %q, want a seed list", got)
	}
	if strings.Contains(got, "27018") {
		t.Errorf("uri %q, want no port in a seed list", got)
	}
	// A replica set is checked by name where one is named.
	if got := uri(t, config("h", 0, map[string]string{"replicaSet": "rs0"})); !strings.Contains(got, "replicaSet=rs0") {
		t.Errorf("uri %q, want the replica set", got)
	}
	if got := uri(t, config("h", 0, nil)); strings.Contains(got, "replicaSet") {
		t.Errorf("uri %q, want no replica set where none is named", got)
	}
	if _, err := connectionURI(config("h", 99999, nil)); err == nil {
		t.Error("a port outside 1–65535 was accepted")
	}
	// The driver's own parser insists on the slash before the query.
	if got := uri(t, config("h", 0, nil)); !strings.Contains(got, "/?") {
		t.Errorf("uri %q, want a slash before its options", got)
	}
}

func TestClientOptionsKeepsThePasswordOutOfTheURI(t *testing.T) {
	cfg := config("h", 0, nil)
	cfg.User = "reader"
	cfg.Secret = func(string) (string, error) { return "s3cr3t", nil }
	opt, err := clientOptions(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if u := uri(t, cfg); strings.Contains(u, "s3cr3t") || strings.Contains(u, "reader") {
		t.Errorf("uri %q holds the credentials; a driver error would quote them", u)
	}
	_ = opt
	if opt.Auth == nil || opt.Auth.Username != "reader" || opt.Auth.Password != "s3cr3t" || !opt.Auth.PasswordSet {
		t.Errorf("credentials %+v", opt.Auth)
	}
	// With no user there is no authentication at all.
	if opt, _ := clientOptions(config("h", 0, nil)); opt.Auth != nil {
		t.Errorf("credentials %+v with no user", opt.Auth)
	}
	// A keychain that will not answer is a configuration error, not a
	// connection with no password.
	cfg.Secret = func(string) (string, error) { return "", errors.New("locked") }
	if _, err := clientOptions(cfg); err == nil {
		t.Error("a keychain failure was taken as an empty password")
	}
}

func TestClientOptionsAuthenticatesWhereTheUserIs(t *testing.T) {
	cfg := config("h", 0, map[string]string{"authSource": "admin"})
	cfg.User, cfg.Database = "u", "shop"
	cfg.Secret = func(string) (string, error) { return "p", nil }
	opt, _ := clientOptions(cfg)
	if opt.Auth.AuthSource != "admin" {
		t.Errorf("auth source %q, want the one named", opt.Auth.AuthSource)
	}
	// Unnamed, the database the connection is in is where the user is
	// looked for, as the driver's own URI rule says.
	cfg.Params = nil
	opt, _ = clientOptions(cfg)
	if opt.Auth.AuthSource != "shop" {
		t.Errorf("auth source %q, want the connection's database", opt.Auth.AuthSource)
	}
}

func TestClientOptionsCarriesTLS(t *testing.T) {
	cfg := config("db.example", 0, nil)
	cfg.TLS = source.TLSConfig{Mode: "verify-full"}
	opt, err := clientOptions(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if opt.TLSConfig == nil {
		t.Fatal("a verifying connection has no TLS settings")
	}
	if opt.TLSConfig.InsecureSkipVerify {
		t.Error("a verifying connection skips verification")
	}
	if got := uri(t, cfg); strings.Contains(got, "tls=false") {
		t.Errorf("uri %q turns TLS off", got)
	}
	// Refusing encryption has to be said: a seed list turns it on by itself.
	if got := uri(t, config("h", 0, map[string]string{"srv": "true"})); !strings.Contains(got, "tls=false") {
		t.Errorf("uri %q, want encryption refused outright", got)
	}
}

func TestClassifyConnectErrorSaysWhatToFix(t *testing.T) {
	cases := []struct {
		err  error
		want source.ConnectKind
	}{
		{errors.New(`connection() error occurred during connection handshake: auth error: sasl conversation error`), source.ConnectAuth},
		{errors.New(`server selection error: ... Authentication failed.`), source.ConnectAuth},
		{errors.New(`x509: certificate signed by unknown authority`), source.ConnectTLS},
		{errors.New(`dial tcp: lookup nowhere.example: no such host`), source.ConnectUnreachable},
		{errors.New(`dial tcp 127.0.0.1:27017: connect: connection refused`), source.ConnectRefused},
		{errors.New(`server selection error: context deadline exceeded`), source.ConnectUnreachable},
		{errors.New(`error parsing uri: scheme must be "mongodb"`), source.ConnectConfig},
		{errors.New(`something else entirely`), source.ConnectUnknown},
	}
	for _, tc := range cases {
		var ce *source.ConnectError
		if !errors.As(classifyConnectError(tc.err), &ce) {
			t.Fatalf("%v: not a connect error", tc.err)
		}
		if ce.Kind != tc.want {
			t.Errorf("%v: kind %d, want %d", tc.err, ce.Kind, tc.want)
		}
		if ce.Hint == "" {
			t.Errorf("%v: no hint", tc.err)
		}
	}
	// One already classified is left as it is.
	in := &source.ConnectError{Kind: source.ConnectConfig, Hint: "no"}
	if got := classifyConnectError(in); got != error(in) {
		t.Errorf("a classified error was classified again as %v", got)
	}
}

func TestCapabilitiesAreTheDocumentParadigms(t *testing.T) {
	c := (&mongoSource{}).Capabilities()
	if c.Paradigm != model.ParadigmDocument {
		t.Errorf("paradigm %q", c.Paradigm)
	}
	if !c.Structure.MultipleDatabases || c.Structure.Schemas {
		t.Errorf("structure %+v, want databases and no schemas", c.Structure)
	}
	if !c.Supports(model.KindCollection) || !c.Supports(model.KindIndex) || c.Supports(model.KindTable) {
		t.Errorf("objects %v, want collections and no tables", c.Objects)
	}
	if !c.Data.ServerSort || !c.Data.ServerFilter || !c.Data.ExactCount {
		t.Errorf("data %+v, want the server doing the work", c.Data)
	}
	if !c.Data.Insert || !c.Data.Update || !c.Data.Delete {
		t.Errorf("data %+v, want documents written", c.Data)
	}
	if !c.Data.Pipeline {
		t.Errorf("data %+v, want a pipeline read", c.Data)
	}
	if !c.Schema.Indexes || c.Schema.DDL {
		t.Errorf("schema %+v, want indexes managed and no DDL", c.Schema)
	}
	// A standalone server has no transaction, and the UI must not say there
	// is one: a write that fails leaves the writes before it.
	if c.Data.TransactionalWrite {
		t.Errorf("data %+v claims a transaction this server has not got", c.Data)
	}
	// Nothing is claimed that is not written yet: a capability claimed
	// without its interface is what the conformance suite fails a driver for.
	if c.Query.Supported || c.Data.BulkLoad || c.Data.DistinctValues {
		t.Errorf("capabilities %+v claim what is not written yet", c)
	}
}

func TestIsTrue(t *testing.T) {
	for _, s := range []string{"true", "TRUE", "1", "yes", " on "} {
		if !isTrue(s) {
			t.Errorf("%q read as false", s)
		}
	}
	for _, s := range []string{"", "false", "0", "no", "maybe"} {
		if isTrue(s) {
			t.Errorf("%q read as true", s)
		}
	}
}

func TestDatabaseNodes(t *testing.T) {
	nodes, err := databaseNodes([]string{"admin", "shop"}, nil, "shop")
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 || nodes[1].Ref.Name() != "shop" || !nodes[1].HasChildren {
		t.Fatalf("nodes %+v", nodes)
	}
	if nodes[1].Attrs["current"] != "true" || nodes[0].Attrs["current"] == "true" {
		t.Errorf("nodes %+v, want the connection's own database marked", nodes)
	}
	if nodes[0].Ref.Kind != model.KindDatabase {
		t.Errorf("kind %q, want a database", nodes[0].Ref.Kind)
	}
	// A user allowed one database and not the list of them still sees it.
	refused := errors.New("not authorized on admin to execute command listDatabases")
	nodes, err = databaseNodes(nil, refused, "shop")
	if err != nil {
		t.Fatalf("a refused listing became an error: %v", err)
	}
	if len(nodes) != 1 || nodes[0].Ref.Name() != "shop" {
		t.Errorf("nodes %+v, want the connection's own database alone", nodes)
	}
	// With no database named there is nothing to fall back on, and the
	// refusal is the answer.
	if _, err := databaseNodes(nil, refused, ""); !errors.Is(err, refused) {
		t.Errorf("error %v, want the server's own", err)
	}
}
