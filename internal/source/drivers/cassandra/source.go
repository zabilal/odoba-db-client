// Package cassandra is the Cassandra driver (T2.47), over github.com/gocql/gocql.
//
// Cassandra is a wide-column store: keyspaces hold tables, tables hold rows
// with declared columns, and CQL is a SQL-family language. So it belongs to
// the relational paradigm here, even though what it does under the covers —
// partitions spread over a ring, no joins, no transactions — is not what
// PostgreSQL does. Where that difference shows, it shows in the capabilities
// this driver claims rather than in a paradigm of its own.
//
// This is the connection alone (FR-1.2, FR-1.4): what a person fills in, what
// is dialled, and what is said when it fails. The tree comes with T2.48, and
// rows with T2.51.
package cassandra

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gocql/gocql"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/capability"
	"github.com/ikigai-db/ikigai-db/internal/source/tlsconf"
)

const (
	driverID    = "cassandra"
	defaultPort = 9042

	// timeout bounds both dialling and the queries this driver sends. FR-1.4
	// wants a test connection that fails quickly and says why.
	timeout = 10 * time.Second
)

// consistencies are the levels a person may choose, offered from the fewest
// replicas to the most, with each data-centre-local level beside the
// cluster-wide one it answers to.
//
// ANY is not among them. It is a write's level alone — a write that reached
// any node at all, even as a hint nobody has replayed — and a read at ANY is
// refused by the cluster. A selector that offered it would be offering a
// setting that stops reading working.
var consistencies = []string{"ONE", "LOCAL_ONE", "TWO", "THREE", "QUORUM", "LOCAL_QUORUM", "EACH_QUORUM", "ALL"}

// defaultConsistency is what a person who has not chosen gets, and what gocql
// itself would have used: a majority of the replicas.
const defaultConsistency = "QUORUM"

func init() { source.Register(Driver{}) }

// Driver connects to Cassandra clusters.
type Driver struct{}

func (Driver) Describe() source.Descriptor {
	return source.Descriptor{
		ID: driverID, Name: "Cassandra", Paradigm: model.ParadigmRelational, DefaultPort: defaultPort,
		URLSchemes: []string{"cassandra"},
		Fields: []source.Field{
			{Key: "host", Label: "Host", Kind: source.FieldText, Required: true, Default: "localhost",
				Help: "Any node of the cluster. The rest are found from it."},
			{Key: "port", Label: "Port", Kind: source.FieldNumber, Default: strconv.Itoa(defaultPort)},
			{Key: "keyspace", Label: "Keyspace", Kind: source.FieldText,
				Help: "Optional: the keyspace to open in."},
			{Key: "user", Label: "User", Kind: source.FieldText,
				Help: "Optional: a role, where the cluster asks for one."},
			{Key: "password", Label: "Password", Kind: source.FieldPassword, Secret: true},
			{Key: "nodes", Label: "Further nodes", Kind: source.FieldText,
				Help: "host:port, comma separated. The host above is the first."},
			{Key: "datacenter", Label: "Local data centre", Kind: source.FieldText,
				Help: "Optional: the data centre whose nodes are asked first."},
			{Key: "consistency", Label: "Consistency", Kind: source.FieldSelect, Default: defaultConsistency,
				Options: consistencies,
				Help:    "How many replicas must answer before a read or a write is."},
		},
	}
}

// Open dials the cluster and proves it answers, and nothing more.
func (Driver) Open(ctx context.Context, cfg source.ConnectionConfig) (source.Source, error) {
	cluster, err := clusterOf(cfg)
	if err != nil {
		return nil, err
	}
	session, err := connect(ctx, cluster)
	if err != nil {
		return nil, whyNot(ctx, cfg, err)
	}
	return &cassandraSource{session: session, cfg: cfg}, nil
}

// whyNot says why a connection failed.
//
// gocql folds every node's refusal into one sentence — "no connections were
// made when creating the session" — which hides the commonest reason of all:
// a keyspace that is not there. Where one was named, the cluster is dialled
// again without it and asked whether it has that keyspace, so that the person
// is told to fix the keyspace rather than the network (FR-1.4).
func whyNot(ctx context.Context, cfg source.ConnectionConfig, failed error) error {
	keyspace := strings.TrimSpace(cfg.Database)
	if keyspace == "" {
		return classifyConnectError(failed)
	}
	plain := cfg
	plain.Database = ""
	cluster, err := clusterOf(plain)
	if err != nil {
		return classifyConnectError(failed)
	}
	session, err := connect(ctx, cluster)
	if err != nil {
		// The cluster is not answering at all, which is what failed says.
		return classifyConnectError(failed)
	}
	defer session.Close()
	var name string
	err = session.Query("SELECT keyspace_name FROM system_schema.keyspaces WHERE keyspace_name = ?", keyspace).
		WithContext(ctx).Scan(&name)
	if errors.Is(err, gocql.ErrNotFound) {
		return &source.ConnectError{Kind: source.ConnectNoDatabase,
			Hint: fmt.Sprintf("The cluster has no keyspace called %q.", keyspace), Err: failed}
	}
	return classifyConnectError(failed)
}

// connect makes the session, which is where gocql dials, authenticates and
// reads the cluster's own view of itself. CreateSession takes no context, so
// a session that arrives after the caller has given up is closed rather than
// leaked.
func connect(ctx context.Context, cluster *gocql.ClusterConfig) (*gocql.Session, error) {
	type opened struct {
		session *gocql.Session
		err     error
	}
	done := make(chan opened, 1)
	go func() {
		s, err := cluster.CreateSession()
		done <- opened{s, err}
	}()
	select {
	case o := <-done:
		return o.session, o.err
	case <-ctx.Done():
		go func() {
			if o := <-done; o.session != nil {
				o.session.Close()
			}
		}()
		return nil, ctx.Err()
	}
}

// clusterOf is the settings as gocql takes them.
func clusterOf(cfg source.ConnectionConfig) (*gocql.ClusterConfig, error) {
	hosts, err := contactPoints(cfg)
	if err != nil {
		return nil, err
	}
	cluster := gocql.NewCluster(hosts...)
	cluster.Keyspace = strings.TrimSpace(cfg.Database)
	cluster.ConnectTimeout, cluster.Timeout = timeout, timeout
	// One connection to each node is enough to ask a question; the pool is
	// not what this application is short of.
	cluster.NumConns = 1

	user := strings.TrimSpace(cfg.User)
	password, err := secret(cfg, "password")
	if err != nil {
		return nil, err
	}
	if user != "" || password != "" {
		// The credentials are settings of their own and never part of an
		// address, so no error can quote a URL with a password in it (NFR-S2).
		cluster.Authenticator = gocql.PasswordAuthenticator{Username: user, Password: password}
	}

	level, err := consistencyOf(cfg)
	if err != nil {
		return nil, err
	}
	// Set on the cluster rather than on each statement: gocql gives a query
	// the session's level, so every read, every write and the schema the tree
	// is read from are all answered at the level a person asked for. Somebody
	// who chooses ALL means it about all of it.
	cluster.Consistency = level

	if dc := strings.TrimSpace(cfg.Params["datacenter"]); dc != "" {
		// Asking the local data centre first is what keeps a query off a
		// link between regions. Token awareness above it sends a query to a
		// node that holds the partition, which is T2.51's business too.
		cluster.PoolConfig.HostSelectionPolicy = gocql.TokenAwareHostPolicy(gocql.DCAwareRoundRobinPolicy(dc))
	}

	tlsCfg, err := tlsconf.Config(cfg.TLS, hostOf(cfg))
	if err != nil {
		return nil, &source.ConnectError{Kind: source.ConnectConfig,
			Hint: "The TLS settings are not usable.", Err: err}
	}
	if encrypted(cfg.TLS) {
		// gocql reads EnableHostVerification rather than the tls.Config's own
		// InsecureSkipVerify, so what the mode says is said again here.
		cluster.SslOpts = &gocql.SslOptions{Config: tlsCfg, EnableHostVerification: cfg.TLS.Verifies()}
	}
	return cluster, nil
}

// contactPoints is the host and port, then whatever further nodes were given.
// Any node can name the rest, so the first is no more important than the others.
func contactPoints(cfg source.ConnectionConfig) ([]string, error) {
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
			// writing a list of nodes means by one.
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

// secret reads a named secret from the keychain.
func secret(cfg source.ConnectionConfig, key string) (string, error) {
	if cfg.Secret == nil {
		return "", nil
	}
	v, err := cfg.Secret(key)
	if err != nil {
		return "", &source.ConnectError{Kind: source.ConnectConfig,
			Hint: "The password could not be read from the keychain.", Err: err}
	}
	return v, nil
}

// consistencyOf is the level this connection reads and writes at.
//
// A name nobody offered is refused rather than quietly replaced by a default:
// a person who typed EACH_QUORM meant something, and reading at QUORUM
// instead would answer a question they did not ask.
func consistencyOf(cfg source.ConnectionConfig) (gocql.Consistency, error) {
	name := strings.ToUpper(strings.TrimSpace(cfg.Params["consistency"]))
	if name == "" {
		name = defaultConsistency
	}
	if !slices.Contains(consistencies, name) {
		return 0, &source.ConnectError{Kind: source.ConnectConfig,
			Hint: fmt.Sprintf("%q is not a level to read at; choose one of %s.",
				name, strings.Join(consistencies, ", "))}
	}
	// Every name offered is one gocql knows, which a test says rather than a
	// branch here that nothing could reach.
	level, _ := gocql.ParseConsistencyWrapper(name)
	return level, nil
}

// encrypted reports whether this connection speaks TLS at all. Cassandra
// speaks its own protocol in the clear unless it is told otherwise, so an
// unset mode is a connection in the clear and nothing downgrades silently
// (ADR-0008).
func encrypted(t source.TLSConfig) bool { return t.Verifies() || t.Mode == "require" }

func hostOf(cfg source.ConnectionConfig) string {
	if host := strings.TrimSpace(cfg.Host); host != "" {
		return host
	}
	return "localhost"
}

// classifyConnectError says what went wrong in terms a person can act on
// (FR-1.4). gocql wraps every node's failure into one string, so the words
// the cluster used are what there is to read.
func classifyConnectError(err error) error {
	var ce *source.ConnectError
	if errors.As(err, &ce) {
		return err
	}
	var dns *net.DNSError
	var ne net.Error
	text := strings.ToLower(err.Error())
	switch {
	case strings.Contains(text, "authentication") || strings.Contains(text, "username") ||
		strings.Contains(text, "password") || strings.Contains(text, "unauthorized"):
		return &source.ConnectError{Kind: source.ConnectAuth,
			Hint: "The user name or password was not accepted.", Err: err}
	case strings.Contains(text, "keyspace") && (strings.Contains(text, "does not exist") ||
		strings.Contains(text, "not exist") || strings.Contains(text, "unknown")):
		return &source.ConnectError{Kind: source.ConnectNoDatabase,
			Hint: "There is no keyspace of that name on this cluster.", Err: err}
	case strings.Contains(text, "x509") || strings.Contains(text, "certificate") ||
		strings.Contains(text, "tls handshake") || strings.Contains(text, "first record does not look like a tls handshake"):
		return &source.ConnectError{Kind: source.ConnectTLS,
			Hint: "The server's certificate could not be verified.", Err: err}
	case errors.As(err, &dns) || strings.Contains(text, "no such host") ||
		strings.Contains(text, "failed to resolve"):
		return &source.ConnectError{Kind: source.ConnectUnreachable,
			Hint: "That host name could not be found.", Err: err}
	case strings.Contains(text, "connection refused"):
		return &source.ConnectError{Kind: source.ConnectRefused,
			Hint: "Nothing is listening on that host and port.", Err: err}
	case errors.Is(err, context.Canceled):
		return &source.ConnectError{Kind: source.ConnectUnreachable,
			Hint: "The connection was given up before the cluster answered.", Err: err}
	case errors.As(err, &ne) && ne.Timeout() || errors.Is(err, context.DeadlineExceeded) ||
		strings.Contains(text, "timeout") || strings.Contains(text, "context deadline exceeded") ||
		strings.Contains(text, "i/o timeout"):
		return &source.ConnectError{Kind: source.ConnectUnreachable,
			Hint: "The cluster did not answer in time.", Err: err}
	case errors.Is(err, gocql.ErrNoConnectionsStarted) || strings.Contains(text, "no connections were made"):
		// Every node was tried and none answered, and gocql has already said
		// what each one said.
		return &source.ConnectError{Kind: source.ConnectUnreachable,
			Hint: "No node of the cluster could be reached.", Err: err}
	case errors.Is(err, gocql.ErrNoHosts) || strings.Contains(text, "no hosts provided"):
		return &source.ConnectError{Kind: source.ConnectConfig,
			Hint: "No node was given to connect to.", Err: err}
	}
	return &source.ConnectError{Kind: source.ConnectUnknown, Hint: "The connection failed.", Err: err}
}

// cassandraSource is one live session, which is a connection to every node
// the cluster named rather than to the one that was dialled.
type cassandraSource struct {
	// The dialect is the connection's own: the rest of the application asks
	// a source how its language is written (T2.49).
	dialect

	session *gocql.Session
	cfg     source.ConnectionConfig

	// states remembers where a browse's pages ended, since a Cassandra page
	// is where the last one stopped rather than an offset (T2.51).
	states pageStates
}

var (
	_ source.Source  = (*cassandraSource)(nil)
	_ source.Dialect = (*cassandraSource)(nil)
)

func (s *cassandraSource) Capabilities() capability.Capabilities {
	return capability.Capabilities{
		Paradigm: model.ParadigmRelational,
		// A keyspace is this paradigm's database: a cluster holds several,
		// and a connection can be opened on any of them.
		Structure: capability.Structure{MultipleDatabases: true},
		// The cluster filters and orders what it can, and refuses the rest in
		// its own words. Nothing is counted: counting a table is a read of
		// every partition on every node, so neither count is claimed and the
		// grid pages without a scrollbar it cannot draw honestly (FR-2.5).
		Data: capability.Data{ServerSort: true, ServerFilter: true},
		// CQL is the language, and a script of it runs a statement at a
		// time. Every claim here is a promise the conformance suite holds
		// this driver to, against a real cluster (REQ-DRV-1).
		Query: capability.Query{Supported: true, Language: "cql", MultiStatement: true},
		Objects: map[model.ObjectKind]bool{
			model.KindDatabase: true, model.KindFolder: true,
			model.KindTable: true, model.KindMaterializedView: true,
			model.KindColumn: true, model.KindIndex: true, model.KindUserType: true,
		},
	}
}

// Info reads what the node this connection is talking to says it is, and
// times the round trip.
func (s *cassandraSource) Info(ctx context.Context) (_ source.ServerInfo, err error) {
	defer panics.Recover(&err, "reading the cluster's version")
	start := time.Now()
	var version, cluster, partitioner string
	if err := s.session.Query("SELECT release_version, cluster_name, partitioner FROM system.local").
		WithContext(ctx).Scan(&version, &cluster, &partitioner); err != nil {
		return source.ServerInfo{}, err
	}
	info := source.ServerInfo{Product: "Cassandra", Version: version, Latency: time.Since(start),
		Attrs: map[string]string{"cluster": cluster, "partitioner": partitioner}}
	return info, nil
}

func (s *cassandraSource) Ping(ctx context.Context) (err error) {
	defer panics.Recover(&err, "pinging the cluster")
	return s.session.Query("SELECT release_version FROM system.local").WithContext(ctx).Exec()
}

func (s *cassandraSource) Close() (err error) {
	defer panics.Recover(&err, "closing the connection")
	// gocql's own Close is idempotent, and the session teardown path can
	// reach this twice, which the live tests hold it to. A flag of this
	// driver's own would say the same thing twice.
	s.session.Close()
	return nil
}
