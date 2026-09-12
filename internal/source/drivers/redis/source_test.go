package redis

import (
	"errors"
	"net"
	"strings"
	"testing"

	goredis "github.com/redis/go-redis/v9"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// cfg is a connection as the settings would hold one.
func cfg(params map[string]string) source.ConnectionConfig {
	return source.ConnectionConfig{DriverID: driverID, Host: "cache.example.com", Port: 6380, Params: params}
}

func TestTheDriverDescribesItselfAsAKeyValueSource(t *testing.T) {
	d := Driver{}.Describe()
	if d.ID != "redis" || d.Paradigm != model.ParadigmKeyValue || d.DefaultPort != 6379 {
		t.Errorf("descriptor %+v", d)
	}
	fields := map[string]source.Field{}
	for _, f := range d.Fields {
		fields[f.Key] = f
	}
	for _, key := range []string{"host", "port", "database", "user", "password", "mode", "nodes", "masterName", "sentinelPassword"} {
		if _, ok := fields[key]; !ok {
			t.Errorf("the form has no %s field", key)
		}
	}
	if !fields["password"].Secret || !fields["sentinelPassword"].Secret {
		t.Error("a password belongs in the keychain, not the settings file")
	}
	if fields["mode"].Kind != source.FieldSelect || len(fields["mode"].Options) != 3 {
		t.Errorf("the mode is chosen from its three: %+v", fields["mode"])
	}
}

func TestBothSchemesReachTheDriverAndOnlyOneOfThemMeansEncryption(t *testing.T) {
	for _, scheme := range []string{"redis", "rediss"} {
		desc, ok := source.LookupScheme(scheme)
		if !ok || desc.ID != driverID {
			t.Errorf("%s:// is not this driver's: %v %+v", scheme, ok, desc)
		}
	}
	d := Driver{}.Describe()
	if len(d.TLSSchemes) != 1 || d.TLSSchemes[0] != "rediss" {
		t.Errorf("the encrypted scheme is rediss, not %v", d.TLSSchemes)
	}
}

func TestTheAddressIsTheHostAndPortAndWhateverElseWasGiven(t *testing.T) {
	one, err := addresses(cfg(nil))
	if err != nil || len(one) != 1 || one[0] != "cache.example.com:6380" {
		t.Fatalf("one server: %v %v", one, err)
	}
	// A bare host means the usual port, which is what a person writing a
	// list of servers means by one.
	many, err := addresses(cfg(map[string]string{"nodes": "a:7000, b , [::1]:7001 ,"}))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"cache.example.com:6380", "a:7000", "b:6379", "[::1]:7001"}
	if strings.Join(many, " ") != strings.Join(want, " ") {
		t.Errorf("addresses %v, want %v", many, want)
	}
}

func TestTheHostAndPortHaveDefaultsAndThePortMustBeOne(t *testing.T) {
	bare, err := addresses(source.ConnectionConfig{})
	if err != nil || len(bare) != 1 || bare[0] != "localhost:6379" {
		t.Errorf("a connection naming nothing: %v %v", bare, err)
	}
	for _, c := range []source.ConnectionConfig{
		{Port: 70000},
		{Params: map[string]string{"nodes": "a:0"}},
		{Params: map[string]string{"nodes": "a:http"}},
	} {
		if _, err := addresses(c); err == nil {
			t.Errorf("a port that is not one: %+v", c)
		} else if kind(err) != source.ConnectConfig {
			t.Errorf("a bad port is a settings error, not %v", err)
		}
	}
}

func TestADatabaseIsANumberHere(t *testing.T) {
	for in, want := range map[string]int{"": 0, "0": 0, "3": 3, " 11 ": 11, "db3": 3, "DB3": 3} {
		got, err := database(source.ConnectionConfig{Database: in})
		if err != nil || got != want {
			t.Errorf("database(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"sales", "-1", "db"} {
		if _, err := database(source.ConnectionConfig{Database: in}); err == nil {
			t.Errorf("database(%q) was accepted", in)
		}
	}
}

func TestTheModeIsOneOfThreeAndStandaloneWhenUnsaid(t *testing.T) {
	for in, want := range map[string]string{"": modeStandalone, "standalone": modeStandalone,
		"sentinel": modeSentinel, "cluster": modeCluster, " Cluster ": modeCluster} {
		got, err := modeOf(cfg(map[string]string{"mode": in}))
		if err != nil || got != want {
			t.Errorf("mode %q = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := modeOf(cfg(map[string]string{"mode": "replica"})); err == nil {
		t.Error("an unknown mode was accepted")
	}
}

func TestEachModeIsBuiltAsWhatItIs(t *testing.T) {
	single, mode, err := dial(cfg(nil))
	if err != nil || mode != modeStandalone {
		t.Fatalf("standalone: %v %v", mode, err)
	}
	if o := single.(*goredis.Client).Options(); o.Addr != "cache.example.com:6380" || o.ClientName != appName {
		t.Errorf("a single server at %+v", o)
	}
	cluster, mode, err := dial(cfg(map[string]string{"mode": modeCluster, "nodes": "b:6379"}))
	if err != nil || mode != modeCluster {
		t.Fatalf("cluster: %v %v", mode, err)
	}
	if o := cluster.(*goredis.ClusterClient).Options(); len(o.Addrs) != 2 {
		t.Errorf("a cluster of %v", o.Addrs)
	}
	failover, mode, err := dial(cfg(map[string]string{"mode": modeSentinel, "masterName": "mymaster"}))
	if err != nil || mode != modeSentinel {
		t.Fatalf("sentinel: %v %v", mode, err)
	}
	if _, ok := failover.(*goredis.Client); !ok {
		t.Errorf("a sentinel connection is to whichever server it names: %T", failover)
	}
}

func TestASettingTheModeHasNotIsRefusedRatherThanDropped(t *testing.T) {
	cases := map[string]source.ConnectionConfig{
		"a cluster has one keyspace": {Params: map[string]string{"mode": modeCluster}, Database: "3"},
		"a sentinel set has a name":  {Params: map[string]string{"mode": modeSentinel}},
		"one server has one address": {Params: map[string]string{"nodes": "b:6379"}},
	}
	for what, c := range cases {
		if _, _, err := dial(c); err == nil {
			t.Errorf("%s: accepted", what)
		} else if kind(err) != source.ConnectConfig {
			t.Errorf("%s: %v", what, err)
		}
	}
}

func TestTheDatabaseGoesToTheServerThatHasOne(t *testing.T) {
	c := cfg(nil)
	c.Database = "3"
	single, _, err := dial(c)
	if err != nil {
		t.Fatal(err)
	}
	if db := single.(*goredis.Client).Options().DB; db != 3 {
		t.Errorf("a single server on db%d", db)
	}
	c.Params = map[string]string{"mode": modeSentinel, "masterName": "mymaster"}
	failover, _, err := dial(c)
	if err != nil {
		t.Fatal(err)
	}
	if db := failover.(*goredis.Client).Options().DB; db != 3 {
		t.Errorf("a sentinel-watched server on db%d", db)
	}
}

func TestTheCredentialsAreSettingsAndNotPartOfAnyAddress(t *testing.T) {
	c := cfg(nil)
	c.User = "ada"
	asked := ""
	c.Secret = func(key string) (string, error) { asked = key; return "hunter2", nil }
	client, _, err := dial(c)
	if err != nil {
		t.Fatal(err)
	}
	o := client.(*goredis.Client).Options()
	if o.Username != "ada" || o.Password != "hunter2" || asked != "password" {
		t.Errorf("credentials %q/%q, asked for %q", o.Username, redacted(o.Password), asked)
	}
	if strings.Contains(o.Addr, "hunter2") {
		t.Error("the password is in the address, where an error would quote it")
	}
	// The sentinels have a password of their own, read under its own name.
	c.Params = map[string]string{"mode": modeSentinel, "masterName": "mymaster"}
	if _, _, err := dial(c); err != nil {
		t.Fatal(err)
	}
	if asked != "sentinelPassword" {
		t.Errorf("the sentinels were asked for %q", asked)
	}
}

func TestAKeychainThatWillNotAnswerIsAnError(t *testing.T) {
	c := cfg(nil)
	c.User = "ada"
	c.Secret = func(string) (string, error) { return "", errors.New("the keychain is locked") }
	if _, _, err := dial(c); err == nil {
		t.Fatal("a locked keychain was passed over")
	} else if kind(err) != source.ConnectConfig {
		t.Errorf("%v", err)
	}
}

func TestEncryptionIsUsedWhereItIsAskedForAndNotOtherwise(t *testing.T) {
	for mode, want := range map[string]bool{"": false, "disable": false, "require": true, "verify-full": true} {
		c := cfg(nil)
		c.TLS = source.TLSConfig{Mode: mode}
		client, _, err := dial(c)
		if err != nil {
			t.Fatalf("TLS %q: %v", mode, err)
		}
		got := client.(*goredis.Client).Options().TLSConfig != nil
		if got != want {
			t.Errorf("TLS %q: encrypted %v, want %v", mode, got, want)
		}
	}
	// A verifying connection carries its own settings, and a bad one is said
	// to be a settings error rather than dialled.
	c := cfg(nil)
	c.TLS = source.TLSConfig{Mode: "verify-full", CAFile: "/no/such/ca.pem"}
	if _, _, err := dial(c); err == nil || kind(err) != source.ConnectConfig {
		t.Errorf("a CA file that is not there: %v", err)
	}
	c.TLS = source.TLSConfig{Mode: "verify-full", ServerName: "other.example.com"}
	client, _, err := dial(c)
	if err != nil {
		t.Fatal(err)
	}
	if name := client.(*goredis.Client).Options().TLSConfig.ServerName; name != "other.example.com" {
		t.Errorf("the certificate is checked against %q", name)
	}
}

func TestAFailureSaysWhatToFix(t *testing.T) {
	cases := []struct {
		err  error
		kind source.ConnectKind
	}{
		{errors.New("WRONGPASS Invalid password"), source.ConnectAuth},         // the code alone
		{errors.New("ERR invalid username-password pair"), source.ConnectAuth}, // and what a proxy says instead
		{errors.New("NOAUTH Authentication required"), source.ConnectAuth},
		{errors.New("NOPERM this user has no permissions to run the 'info' command"), source.ConnectAuth},
		{errors.New("ERR Client sent AUTH, but no password is set. Did you mean AUTH <username> <password>?"), source.ConnectAuth},
		{errors.New("x509: certificate signed by unknown authority"), source.ConnectTLS},
		{errors.New("ERR This instance has cluster support disabled"), source.ConnectConfig},
		{errors.New("redis: all sentinels specified in configuration are unreachable"), source.ConnectConfig},
		{&net.DNSError{Err: "no such host", Name: "cache.example.com"}, source.ConnectUnreachable},
		{errors.New("dial tcp 127.0.0.1:6379: connect: connection refused"), source.ConnectRefused},
		{errors.New("context deadline exceeded"), source.ConnectUnreachable},
		{errors.New("MISCONF Redis is configured to save RDB snapshots"), source.ConnectUnknown},
	}
	for _, c := range cases {
		got := classifyConnectError(c.err)
		if kind(got) != c.kind {
			t.Errorf("%v classified %v, want %v", c.err, kind(got), c.kind)
		}
		var ce *source.ConnectError
		if errors.As(got, &ce) && ce.Hint == "" {
			t.Errorf("%v was classified with nothing to act on", c.err)
		}
	}
	// An error already classified is left as it was said.
	already := &source.ConnectError{Kind: source.ConnectConfig, Hint: "The TLS settings are not usable."}
	if got := classifyConnectError(already); got != error(already) {
		t.Errorf("a classified error was classified again: %v", got)
	}
}

func TestWhatTheConnectionClaims(t *testing.T) {
	single := (&redisSource{mode: modeStandalone}).Capabilities()
	if single.Paradigm != model.ParadigmKeyValue {
		t.Errorf("paradigm %q", single.Paradigm)
	}
	if !single.Objects[model.KindKey] || !single.Objects[model.KindDatabase] {
		t.Errorf("objects %v, want keys in databases", single.Objects)
	}
	if !single.Structure.MultipleDatabases {
		t.Error("a server has sixteen databases by default")
	}
	if (&redisSource{mode: modeCluster}).Capabilities().Structure.MultipleDatabases {
		t.Error("a cluster has one keyspace, and claiming more would offer a database that is not there")
	}
	// The server matches names and kinds while it walks, and holds the
	// number of keys a database has.
	if !single.Data.ServerFilter || !single.Data.ExactCount || !single.Data.ApproximateCount {
		t.Errorf("data %+v", single.Data)
	}
	// There is no order to sort by: SCAN walks a hash table.
	if single.Data.ServerSort {
		t.Error("claims an order the keyspace has not got")
	}
	// Nothing is claimed that is not written: the editors are T2.41, the
	// console T2.44.
	if single.Query.Supported || single.Data.Insert || single.Schema.Indexes {
		t.Errorf("claims more than it does: %+v", single)
	}
}

func TestInfoIsReadLineByLineAndSectionsSkipped(t *testing.T) {
	f := infoFields("# Server\r\nredis_version:7.4.1\r\nexecutable:/usr/local/bin/redis-server\r\n\r\nconfig_file:\r\n")
	if f["redis_version"] != "7.4.1" {
		t.Errorf("version %q", f["redis_version"])
	}
	if f["executable"] != "/usr/local/bin/redis-server" {
		t.Errorf("a value with a colon in it: %q", f["executable"])
	}
	if _, ok := f["config_file"]; !ok {
		t.Error("a name with an empty value is still a name")
	}
	if _, ok := f["# Server"]; ok {
		t.Error("a section heading is not a field")
	}
}

func TestAServerThatIsNotRedisIsCalledWhatItIs(t *testing.T) {
	plain := serverInfo("# Server\r\nredis_version:7.4.1\r\nredis_mode:standalone\r\n", modeStandalone)
	if plain.Product != "Redis" || plain.Version != "7.4.1" {
		t.Errorf("server %+v", plain)
	}
	if plain.Attrs["mode"] != modeStandalone || plain.Attrs["server_mode"] != "" {
		t.Errorf("attrs %v, want nothing worth saying about the mode", plain.Attrs)
	}
	// Valkey answers a Redis client, and says both versions: the one it is
	// and the one it is compatible with.
	valkey := serverInfo("# Server\r\nredis_version:7.2.4\r\nserver_name:valkey\r\nvalkey_version:8.0.1\r\n", modeStandalone)
	if valkey.Product != "Valkey" || valkey.Version != "8.0.1" {
		t.Errorf("a Valkey server called %s %s", valkey.Product, valkey.Version)
	}
	// A cluster reached as a single server is worth saying.
	shard := serverInfo("redis_version:7.4.1\r\nredis_mode:cluster\r\n", modeStandalone)
	if shard.Attrs["server_mode"] != "cluster" {
		t.Errorf("attrs %v, want the server's own mode said", shard.Attrs)
	}
}

func TestTheDatabasesAreTheServersOwnAndTheCurrentOneIsMarked(t *testing.T) {
	nodes := numberedDatabases(16, 3)
	if len(nodes) != 16 || nodes[0].Label != "db0" || nodes[15].Label != "db15" {
		t.Fatalf("databases %v", labels(nodes))
	}
	for i, n := range nodes {
		// A database's keys are rows, not children: a keyspace of millions
		// would be a tree nobody could read.
		if !n.Browsable || n.HasChildren {
			t.Errorf("%s opens as %v, expands as %v", n.Label, n.Browsable, n.HasChildren)
		}
		if got := n.Attrs["current"] == "true"; got != (i == 3) {
			t.Errorf("%s marked current %v", n.Label, got)
		}
		if n.Ref.Kind != model.KindDatabase {
			t.Errorf("%s is a %s", n.Label, n.Ref.Kind)
		}
	}
}

func TestWhereTheServerWillNotSayTheDatabasesHoldingKeysAreShown(t *testing.T) {
	// A managed Redis that refuses CONFIG still answers INFO keyspace.
	nodes := usedDatabases("# Keyspace\r\ndb0:keys=3,expires=0,avg_ttl=0\r\ndb10:keys=1,expires=0\r\ndb2:keys=7,expires=0\r\n", 5)
	if got := strings.Join(labels(nodes), " "); got != "db0 db2 db5 db10" {
		t.Errorf("databases %q; want those holding keys and the one connected to, in order", got)
	}
	for _, n := range nodes {
		if got := n.Attrs["current"] == "true"; got != (n.Label == "db5") {
			t.Errorf("%s marked current %v", n.Label, got)
		}
	}
}

func labels(nodes []model.Node) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.Label)
	}
	return out
}

func kind(err error) source.ConnectKind {
	var ce *source.ConnectError
	if errors.As(err, &ce) {
		return ce.Kind
	}
	return source.ConnectUnknown
}

// redacted keeps a failing test from printing a password.
func redacted(s string) string {
	if s == "" {
		return "[empty]"
	}
	return "[set]"
}
