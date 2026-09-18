package cassandra

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/gocql/gocql"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

func TestTheFormAsksForWhatACassandraNeeds(t *testing.T) {
	d := Driver{}.Describe()
	if d.ID != driverID || d.Paradigm != model.ParadigmRelational || d.DefaultPort != defaultPort {
		t.Errorf("the driver describes itself as %+v", d)
	}
	fields := map[string]source.Field{}
	for _, f := range d.Fields {
		fields[f.Key] = f
	}
	for _, key := range []string{"host", "port", "keyspace", "user", "password", "nodes", "datacenter"} {
		if _, ok := fields[key]; !ok {
			t.Errorf("the form has no %s", key)
		}
	}
	// A password is the one field that belongs in the keychain rather than
	// in the settings file (NFR-S1).
	for key, f := range fields {
		if want := key == "password"; f.Secret != want {
			t.Errorf("%s is secret %v", key, f.Secret)
		}
	}
	if !fields["host"].Required || fields["host"].Default != "localhost" {
		t.Errorf("the host is %+v", fields["host"])
	}
	if fields["port"].Default != "9042" {
		t.Errorf("the port is %+v", fields["port"])
	}
}

func TestTheNodesDialledAreTheHostAndWhateverElseIsGiven(t *testing.T) {
	config := func(host string, port int, nodes string) source.ConnectionConfig {
		return source.ConnectionConfig{Host: host, Port: port, Params: map[string]string{"nodes": nodes}}
	}
	for what, c := range map[string]struct {
		cfg  source.ConnectionConfig
		want []string
	}{
		"a host and its port":     {config("db", 9042, ""), []string{"db:9042"}},
		"the usual port":          {config("db", 0, ""), []string{"db:9042"}},
		"localhost where none is": {config("  ", 9042, ""), []string{"localhost:9042"}},
		"further nodes":           {config("a", 9042, "b:9043, c:9044"), []string{"a:9042", "b:9043", "c:9044"}},
		"a node without a port":   {config("a", 9042, "b"), []string{"a:9042", "b:9042"}},
		"nothing between commas":  {config("a", 9042, " , "), []string{"a:9042"}},
		"an address of its own":   {config("::1", 7000, ""), []string{"[::1]:7000"}},
	} {
		got, err := contactPoints(c.cfg)
		if err != nil || strings.Join(got, " ") != strings.Join(c.want, " ") {
			t.Errorf("%s: %v, %v; want %v", what, got, err, c.want)
		}
	}
	for what, cfg := range map[string]source.ConnectionConfig{
		"a port past the end":   config("a", 70000, ""),
		"a negative port":       config("a", -1, ""),
		"a node's port in text": config("a", 9042, "b:some"),
		"a node's port too big": config("a", 9042, "b:70000"),
	} {
		if got, err := contactPoints(cfg); err == nil {
			t.Errorf("%s was dialled as %v", what, got)
		} else if kind(err) != source.ConnectConfig {
			t.Errorf("%s: %v", what, err)
		}
	}
}

func TestTheSettingsAsGocqlTakesThem(t *testing.T) {
	cfg := source.ConnectionConfig{Host: "db", Port: 9042, Database: "shop", User: "ada",
		Secret: func(string) (string, error) { return "opensesame", nil },
		Params: map[string]string{"datacenter": "dc1"}}
	cluster, err := clusterOf(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if cluster.Keyspace != "shop" || cluster.NumConns != 1 || cluster.ConnectTimeout != timeout {
		t.Errorf("the cluster is %+v", cluster)
	}
	auth, ok := cluster.Authenticator.(gocql.PasswordAuthenticator)
	if !ok || auth.Username != "ada" || auth.Password != "opensesame" {
		t.Errorf("the credentials are %+v", cluster.Authenticator)
	}
	if cluster.PoolConfig.HostSelectionPolicy == nil {
		t.Error("a local data centre was given, and no policy prefers it")
	}
	// Nothing is encrypted unless the settings say so, and nothing downgrades
	// what they do say (ADR-0008).
	if cluster.SslOpts != nil {
		t.Errorf("a connection in the clear speaks TLS: %+v", cluster.SslOpts)
	}
	for _, mode := range []string{"require", "verify-ca", "verify-full"} {
		cfg.TLS = source.TLSConfig{Mode: mode}
		cluster, err := clusterOf(cfg)
		if err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		if cluster.SslOpts == nil {
			t.Fatalf("%s does not speak TLS", mode)
		}
		if want := mode != "require"; cluster.SslOpts.EnableHostVerification != want {
			t.Errorf("%s verifies the host %v", mode, cluster.SslOpts.EnableHostVerification)
		}
	}
	// A password with no user is still a credential: a cluster whose role
	// names are implicit still asks for one.
	only := source.ConnectionConfig{Host: "db", Secret: func(string) (string, error) { return "opensesame", nil }}
	if cluster, err := clusterOf(only); err != nil || cluster.Authenticator == nil {
		t.Errorf("a password with no user: %+v, %v", cluster.Authenticator, err)
	}

	// Without a user or a password, nothing authenticates: a cluster that
	// asks for neither is not sent an empty name.
	plain, err := clusterOf(source.ConnectionConfig{Host: "db"})
	if err != nil || plain.Authenticator != nil {
		t.Errorf("a connection with no credentials: %+v, %v", plain.Authenticator, err)
	}
}

func TestASecretThatCannotBeReadIsSaidToBe(t *testing.T) {
	cfg := source.ConnectionConfig{Host: "db", User: "ada",
		Secret: func(string) (string, error) { return "", errors.New("the keychain is locked") }}
	if _, err := clusterOf(cfg); err == nil || kind(err) != source.ConnectConfig {
		t.Errorf("a keychain that will not answer: %v", err)
	}
}

func TestWhatIsWrongWithAConnectionIsSaidInItsOwnWords(t *testing.T) {
	for what, c := range map[string]struct {
		err  error
		want source.ConnectKind
	}{
		"a password the cluster refused":  {errors.New("gocql: unable to create session: authentication failed"), source.ConnectAuth},
		"a user it does not know":         {errors.New("Provided username ada and/or password are incorrect"), source.ConnectAuth},
		"a keyspace that is not there":    {errors.New("Keyspace 'shop' does not exist"), source.ConnectNoDatabase},
		"a certificate it would not show": {errors.New("x509: certificate signed by unknown authority"), source.ConnectTLS},
		"a name that is not a host":       {&net.DNSError{Err: "no such host", Name: "nowhere"}, source.ConnectUnreachable},
		"a name gocql could not resolve":  {errors.New("gocql: unable to create session: failed to resolve any of the provided hostnames"), source.ConnectUnreachable},
		"nothing listening":               {errors.New("dial tcp 127.0.0.1:9042: connect: connection refused"), source.ConnectRefused},
		"a cluster that did not answer":   {errors.New("dial tcp 10.0.0.1:9042: i/o timeout"), source.ConnectUnreachable},
		"a connection given up":           {context.Canceled, source.ConnectUnreachable},
		"a deadline that passed":          {context.DeadlineExceeded, source.ConnectUnreachable},
		"no node that answered":           {gocql.ErrNoConnectionsStarted, source.ConnectUnreachable},
		"no node at all":                  {gocql.ErrNoHosts, source.ConnectConfig},
		"something else entirely":         {errors.New("the cluster fell over"), source.ConnectUnknown},
	} {
		if got := kind(classifyConnectError(c.err)); got != c.want {
			t.Errorf("%s: kind %d, want %d", what, got, c.want)
		}
	}
	// A failure already said in these terms is not said again in others.
	said := &source.ConnectError{Kind: source.ConnectConfig, Hint: "The port must be between 1 and 65535."}
	if got := classifyConnectError(said); got != error(said) {
		t.Errorf("a failure classified twice: %v", got)
	}
}

func TestOnlyAnEncryptedModeSpeaksTLS(t *testing.T) {
	for mode, want := range map[string]bool{
		"": false, "disable": false, "require": true, "verify-ca": true, "verify-full": true,
	} {
		if got := encrypted(source.TLSConfig{Mode: mode}); got != want {
			t.Errorf("%q speaks TLS %v", mode, got)
		}
	}
}

// kind is the kind of connection failure an error says it is.
func kind(err error) source.ConnectKind {
	var ce *source.ConnectError
	if errors.As(err, &ce) {
		return ce.Kind
	}
	return source.ConnectUnknown
}
