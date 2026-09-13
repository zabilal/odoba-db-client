// Package redis is the Redis driver (T2.39), over github.com/redis/go-redis.
//
// Redis is the first source of the key-value paradigm, and the third answer
// to REQ-DB-4: a flat keyspace of typed values, with no schema, no rows and
// no query language, has to reach the same explorer and the same tabs as
// PostgreSQL does, through the same contract.
//
// A Redis connection is three connections wearing one form: a single server,
// a set watched by sentinels, and a cluster of shards. They differ in how the
// address is found, not in what is said afterwards, so the difference is a
// setting rather than three drivers.
package redis

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/capability"
	"github.com/ikigai-db/ikigai-db/internal/source/tlsconf"
)

const (
	driverID    = "redis"
	defaultPort = 6379

	modeStandalone = "standalone"
	modeSentinel   = "sentinel"
	modeCluster    = "cluster"

	// databasesWhenRefused is how many logical databases a server has when it
	// will not say: Redis has had sixteen by default since it had any.
	databasesWhenRefused = 16
)

func init() { source.Register(Driver{}) }

// Driver connects to Redis servers, sentinel-watched sets and clusters.
type Driver struct{}

func (Driver) Describe() source.Descriptor {
	return source.Descriptor{
		ID: driverID, Name: "Redis", Paradigm: model.ParadigmKeyValue, DefaultPort: defaultPort,
		URLSchemes: []string{"redis", "rediss"},
		// rediss is redis over TLS, and says so in the scheme rather than in
		// a parameter, so a pasted address keeps its encryption (FR-1.3).
		TLSSchemes: []string{"rediss"},
		Fields: []source.Field{
			{Key: "host", Label: "Host", Kind: source.FieldText, Required: true, Default: "localhost"},
			{Key: "port", Label: "Port", Kind: source.FieldNumber, Default: strconv.Itoa(defaultPort)},
			{Key: "database", Label: "Database", Kind: source.FieldNumber, Default: "0",
				Help: "The numbered database to start in. A cluster has only one."},
			{Key: "user", Label: "User", Kind: source.FieldText,
				Help: "Optional: an ACL user. Redis 6 and later."},
			{Key: "password", Label: "Password", Kind: source.FieldPassword, Secret: true},
			{Key: "mode", Label: "Mode", Kind: source.FieldSelect, Default: modeStandalone,
				Options: []string{modeStandalone, modeSentinel, modeCluster},
				Help:    "A single server, a set watched by sentinels, or a cluster of shards."},
			{Key: "nodes", Label: "Further addresses", Kind: source.FieldText,
				Help: "host:port, comma separated. The host and port above is the first."},
			{Key: "masterName", Label: "Master name", Kind: source.FieldText,
				Help: "Sentinel only: the name the sentinels know the set by."},
			{Key: "sentinelPassword", Label: "Sentinel password", Kind: source.FieldPassword, Secret: true,
				Help: "Sentinel only, where the sentinels themselves need one."},
		},
	}
}

// Open connects and proves the server answers, and nothing more (FR-1.4: a
// test connection must be quick and say what is wrong).
func (Driver) Open(ctx context.Context, cfg source.ConnectionConfig) (source.Source, error) {
	client, mode, err := dial(cfg)
	if err != nil {
		return nil, err
	}
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, classifyConnectError(err)
	}
	return &redisSource{client: client, mode: mode, cfg: cfg}, nil
}

// dial builds the client the mode calls for. Each mode is built by name
// rather than through the universal client, so a setting that mode does not
// have is refused here instead of being quietly dropped on the way through.
func dial(cfg source.ConnectionConfig) (goredis.UniversalClient, string, error) {
	mode, err := modeOf(cfg)
	if err != nil {
		return nil, "", err
	}
	addrs, err := addresses(cfg)
	if err != nil {
		return nil, "", err
	}
	db, err := database(cfg)
	if err != nil {
		return nil, "", err
	}
	user, pw, err := credentials(cfg, "password")
	if err != nil {
		return nil, "", err
	}
	tlsCfg, err := tlsconf.Config(cfg.TLS, hostOf(cfg))
	if err != nil {
		return nil, "", &source.ConnectError{Kind: source.ConnectConfig,
			Hint: "The TLS settings are not usable.", Err: err}
	}
	if !encrypted(cfg.TLS) {
		// An empty mode means verify-full to tlsconf, because a driver that
		// speaks TLS by default must verify it. Redis speaks its own
		// protocol in the clear unless the address says rediss, so here an
		// unset mode is a connection in the clear (ADR-0008: the choice is
		// explicit either way, and nothing downgrades silently).
		tlsCfg = nil
	}
	const timeout = 10 * time.Second
	switch mode {
	case modeCluster:
		if db != 0 {
			return nil, "", &source.ConnectError{Kind: source.ConnectConfig,
				Hint: "A cluster has one keyspace and no numbered databases. Leave the database at 0."}
		}
		return goredis.NewClusterClient(&goredis.ClusterOptions{
			Addrs: addrs, Username: user, Password: pw, TLSConfig: tlsCfg,
			ClientName: appName, DialTimeout: timeout,
		}), mode, nil
	case modeSentinel:
		master := strings.TrimSpace(cfg.Params["masterName"])
		if master == "" {
			return nil, "", &source.ConnectError{Kind: source.ConnectConfig,
				Hint: "Sentinel needs the name the sentinels know the set by."}
		}
		_, sentinelPw, err := credentials(cfg, "sentinelPassword")
		if err != nil {
			return nil, "", err
		}
		return goredis.NewFailoverClient(&goredis.FailoverOptions{
			MasterName: master, SentinelAddrs: addrs, SentinelPassword: sentinelPw,
			Username: user, Password: pw, DB: db, TLSConfig: tlsCfg,
			ClientName: appName, DialTimeout: timeout,
		}), mode, nil
	default:
		if len(addrs) > 1 {
			return nil, "", &source.ConnectError{Kind: source.ConnectConfig,
				Hint: "A single server has one address. Choose sentinel or cluster for several."}
		}
		return goredis.NewClient(&goredis.Options{
			Addr: addrs[0], Username: user, Password: pw, DB: db, TLSConfig: tlsCfg,
			ClientName: appName, DialTimeout: timeout,
		}), mode, nil
	}
}

// appName is what CLIENT LIST shows, so that a connection of this
// application's can be told from any other on the server.
const appName = "ikigai-db"

func modeOf(cfg source.ConnectionConfig) (string, error) {
	switch m := strings.ToLower(strings.TrimSpace(cfg.Params["mode"])); m {
	case "", modeStandalone:
		return modeStandalone, nil
	case modeSentinel, modeCluster:
		return m, nil
	default:
		return "", &source.ConnectError{Kind: source.ConnectConfig,
			Hint: "The mode must be standalone, sentinel or cluster."}
	}
}

// addresses is the host and port, then whatever further addresses were
// given. A sentinel set and a cluster are reached through several, and the
// first is no more important than the rest: any of them can name the others.
func addresses(cfg source.ConnectionConfig) ([]string, error) {
	port := cfg.Port
	if port == 0 {
		port = defaultPort
	}
	if port < 1 || port > 65535 {
		return nil, &source.ConnectError{Kind: source.ConnectConfig,
			Hint: "The port must be between 1 and 65535."}
	}
	out := []string{net.JoinHostPort(hostOf(cfg), strconv.Itoa(port))}
	for _, node := range strings.Split(cfg.Params["nodes"], ",") {
		node = strings.TrimSpace(node)
		if node == "" {
			continue
		}
		host, p, err := net.SplitHostPort(node)
		if err != nil {
			// A bare host means the usual port, which is what a person
			// writing a list of servers means by one.
			host, p = node, strconv.Itoa(defaultPort)
		}
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return nil, &source.ConnectError{Kind: source.ConnectConfig,
				Hint: fmt.Sprintf("%q is not a host and port between 1 and 65535.", node)}
		}
		out = append(out, net.JoinHostPort(host, strconv.Itoa(n)))
	}
	return out, nil
}

// database is the numbered database the connection starts in. Redis names
// its databases with numbers, so the field every other driver fills with a
// name holds one here.
func database(cfg source.ConnectionConfig) (int, error) {
	s := strings.TrimSpace(cfg.Database)
	if s == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(strings.TrimPrefix(strings.ToLower(s), "db"))
	if err != nil || n < 0 {
		return 0, &source.ConnectError{Kind: source.ConnectConfig,
			Hint: "A Redis database is a number, such as 0."}
	}
	return n, nil
}

// credentials reads a named secret. Nothing is written into an address: the
// client takes the user and password as settings of their own, so no error
// can quote a URL with a password in it (NFR-S2).
func credentials(cfg source.ConnectionConfig, key string) (user, password string, err error) {
	if cfg.Secret != nil {
		password, err = cfg.Secret(key)
		if err != nil {
			return "", "", &source.ConnectError{Kind: source.ConnectConfig,
				Hint: "The password could not be read from the keychain.", Err: err}
		}
	}
	return strings.TrimSpace(cfg.User), password, nil
}

// encrypted reports whether this connection speaks TLS at all.
func encrypted(t source.TLSConfig) bool { return t.Verifies() || t.Mode == "require" }

func hostOf(cfg source.ConnectionConfig) string {
	if host := strings.TrimSpace(cfg.Host); host != "" {
		return host
	}
	return "localhost"
}

// classifyConnectError says what went wrong in terms a person can act on
// (FR-1.4). Redis answers most refusals as an error string beginning with the
// word the server uses for it, so those are read first.
func classifyConnectError(err error) error {
	var ce *source.ConnectError
	if errors.As(err, &ce) {
		return err
	}
	var dns *net.DNSError
	var ne net.Error
	text := strings.ToLower(err.Error())
	switch {
	case strings.Contains(text, "wrongpass") || strings.Contains(text, "noauth") ||
		strings.Contains(text, "invalid username-password") || strings.Contains(text, "no password is set"):
		return &source.ConnectError{Kind: source.ConnectAuth,
			Hint: "The user name or password was not accepted.", Err: err}
	case strings.Contains(text, "noperm"):
		return &source.ConnectError{Kind: source.ConnectAuth,
			Hint: "That user may not run the commands this needs.", Err: err}
	case strings.Contains(text, "x509") || strings.Contains(text, "certificate") ||
		strings.Contains(text, "tls handshake") || strings.Contains(text, "first record does not look like a tls handshake"):
		return &source.ConnectError{Kind: source.ConnectTLS,
			Hint: "The server's certificate could not be verified.", Err: err}
	case strings.Contains(text, "cluster support disabled"):
		return &source.ConnectError{Kind: source.ConnectConfig,
			Hint: "That server is not a cluster. Choose standalone.", Err: err}
	case strings.Contains(text, "sentinels") || strings.Contains(text, "master") && strings.Contains(text, "unknown"):
		return &source.ConnectError{Kind: source.ConnectConfig,
			Hint: "No sentinel answered for that master name.", Err: err}
	case errors.As(err, &dns) || strings.Contains(text, "no such host"):
		return &source.ConnectError{Kind: source.ConnectUnreachable,
			Hint: "That host name could not be found.", Err: err}
	case strings.Contains(text, "connection refused"):
		return &source.ConnectError{Kind: source.ConnectRefused,
			Hint: "Nothing is listening on that host and port.", Err: err}
	case errors.As(err, &ne) && ne.Timeout() || strings.Contains(text, "timeout") ||
		strings.Contains(text, "context deadline exceeded") || strings.Contains(text, "i/o timeout"):
		return &source.ConnectError{Kind: source.ConnectUnreachable,
			Hint: "The server did not answer in time.", Err: err}
	}
	return &source.ConnectError{Kind: source.ConnectUnknown, Hint: "The connection failed.", Err: err}
}

// redisSource is one live connection.
type redisSource struct {
	client goredis.UniversalClient
	mode   string
	cfg    source.ConnectionConfig

	mu     sync.Mutex
	closed bool
}

var (
	_ source.Source    = (*redisSource)(nil)
	_ source.Countable = (*redisSource)(nil)
	_ source.Writer    = (*redisSource)(nil)
	_ source.RowObject = (*redisSource)(nil)
)

func (s *redisSource) Capabilities() capability.Capabilities {
	return capability.Capabilities{
		Paradigm: model.ParadigmKeyValue,
		// A numbered database is not created or dropped: there are as many as
		// the server was configured with, and a cluster has one.
		Structure: capability.Structure{MultipleDatabases: s.mode != modeCluster},
		// The server matches the names and the kind while it walks the
		// keyspace, and holds the number of keys a database has. There is no
		// order to sort by: SCAN walks a hash table. What a key holds is
		// edited as rows, one command at a time, with no transaction around
		// them (T2.41).
		Data: capability.Data{ServerFilter: true, ExactCount: true, ApproximateCount: true,
			Insert: true, Update: true, Delete: true, RowObjects: true},
		Objects: map[model.ObjectKind]bool{
			model.KindDatabase: true, model.KindKey: true,
		},
	}
}

// Info reads the server's own name and version, and times the round trip.
func (s *redisSource) Info(ctx context.Context) (_ source.ServerInfo, err error) {
	defer panics.Recover(&err, "reading the server's version")
	start := time.Now()
	text, err := s.client.Info(ctx, "server").Result()
	if err != nil {
		return source.ServerInfo{}, err
	}
	info := serverInfo(text, s.mode)
	info.Latency = time.Since(start)
	return info, nil
}

// serverInfo reads what INFO server says the server is.
func serverInfo(text, mode string) source.ServerInfo {
	f := infoFields(text)
	info := source.ServerInfo{Product: "Redis", Version: f["redis_version"],
		Attrs: map[string]string{"mode": mode}}
	// Valkey and KeyDB answer a Redis client and say what they are. A person
	// told "Redis 7.2" about a Valkey server has been told the wrong thing.
	if name := f["server_name"]; name != "" {
		info.Product = strings.ToUpper(name[:1]) + name[1:]
		if v := f[name+"_version"]; v != "" {
			info.Version = v
		}
	}
	if m := f["redis_mode"]; m != "" && m != mode {
		// The connection's mode is how it was reached; the server's is what
		// it is. A cluster reached as a single server is worth saying.
		info.Attrs["server_mode"] = m
	}
	return info
}

// infoFields reads INFO's "name:value" lines. A section heading — "# Server"
// — carries no colon and so is no field, and the lines end with CRLF.
func infoFields(text string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		if name, value, ok := strings.Cut(strings.TrimSpace(line), ":"); ok {
			out[name] = strings.TrimSpace(value)
		}
	}
	return out
}

func (s *redisSource) Ping(ctx context.Context) (err error) {
	defer panics.Recover(&err, "pinging the server")
	return s.client.Ping(ctx).Err()
}

func (s *redisSource) Close() (err error) {
	defer panics.Recover(&err, "closing the connection")
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.client.Close()
}

// errNotYet is what the parts of the contract this task does not reach
// return. A key's structure is T2.43's, where its time to live is shown, and
// the console is T2.44; a stub that says so is better than one that answers
// with nothing, which would read as an empty server.
var errNotYet = errors.New("redis: not implemented yet")

func (s *redisSource) Root(ctx context.Context) ([]model.Node, error) {
	return s.databases(ctx)
}

// Children is nothing: a database holds keys, and keys are rows rather than
// a tree. A keyspace of millions would be a tree nobody could read, and the
// grid is where a pattern narrows it down (T2.40).
func (s *redisSource) Children(context.Context, model.ObjectRef) ([]model.Node, error) {
	return nil, nil
}

func (s *redisSource) Describe(context.Context, model.ObjectRef) (any, error) {
	return nil, fmt.Errorf("%w: describing a key", errNotYet)
}

// databases lists the numbered databases the server holds.
//
// A cluster has one keyspace and no numbers to select between, so it shows
// the one. Everywhere else the count comes from the server's own
// configuration, and where the server will not answer — managed Redis often
// refuses CONFIG — the databases that hold keys are shown instead, with the
// one this connection is on among them.
func (s *redisSource) databases(ctx context.Context) (_ []model.Node, err error) {
	defer panics.Recover(&err, "listing the databases")
	current, err := database(s.cfg)
	if err != nil {
		return nil, err
	}
	if s.mode == modeCluster {
		return []model.Node{databaseNode(0, true)}, nil
	}
	if n, ok := s.databaseCount(ctx); ok {
		return numberedDatabases(n, current), nil
	}
	keyspace, err := s.client.Info(ctx, "keyspace").Result()
	if err != nil {
		return nil, err
	}
	return usedDatabases(keyspace, current), nil
}

// numberedDatabases is every database a server was configured with.
func numberedDatabases(n, current int) []model.Node {
	out := make([]model.Node, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, databaseNode(i, i == current))
	}
	return out
}

// usedDatabases reads INFO keyspace, whose lines are "db0:keys=1,expires=0"
// and which names only the databases holding something. The one this
// connection is on is there whether it holds anything or not: a person who
// chose db3 must see db3, empty or not.
func usedDatabases(keyspace string, current int) []model.Node {
	seen := map[int]bool{current: true}
	for name := range infoFields(keyspace) {
		if !strings.HasPrefix(name, "db") {
			continue
		}
		if n, err := strconv.Atoi(strings.TrimPrefix(name, "db")); err == nil {
			seen[n] = true
		}
	}
	numbers := make([]int, 0, len(seen))
	for n := range seen {
		numbers = append(numbers, n)
	}
	// db2 comes before db10, which sorting their names would not give.
	sort.Ints(numbers)
	out := make([]model.Node, 0, len(numbers))
	for _, n := range numbers {
		out = append(out, databaseNode(n, n == current))
	}
	return out
}

// databaseCount asks the server how many databases it was given. A server
// that will not say is not an error: CONFIG is disabled on many hosted
// services, and the tree has another way to find them.
func (s *redisSource) databaseCount(ctx context.Context) (int, bool) {
	res, err := s.client.ConfigGet(ctx, "databases").Result()
	if err != nil {
		return 0, false
	}
	n, err := strconv.Atoi(res["databases"])
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}

// databaseNode is one numbered database. Its keys are rows rather than
// children: opening it is how they are read (T2.40).
func databaseNode(n int, current bool) model.Node {
	name := "db" + strconv.Itoa(n)
	node := model.Node{Ref: model.NewRef(model.KindDatabase, name), Label: name, Browsable: true}
	if current {
		node.Attrs = map[string]string{"current": "true"}
	}
	return node
}
