// Package cockroach is the CockroachDB driver (T3.33), over pgx.
//
// CockroachDB speaks PostgreSQL's wire protocol, and behind it answers a
// catalogue of its own. Three things about it are not PostgreSQL, and this
// driver is arranged around them (ADR-0145):
//
// A connection reads the whole cluster. Every database is reachable from
// any of them by naming it, catalogue and all, so there is one pool here
// where the PostgreSQL driver keeps one per database.
//
// Every row has a key, because the engine makes one where nobody declared
// one. It is called rowid, it is hidden, and it is what lets a grid edit a
// table that has no key its owner would recognise.
//
// crdb_internal is not read at all. The server refuses it now unless a
// session asks to be allowed into what it calls its unsupported internals,
// and a client that turned that on would be lowering a guard the server
// raised on purpose.
package cockroach

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgconn/ctxwatch"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/capability"
	"github.com/ikigai-db/ikigai-db/internal/source/tlsconf"
)

const (
	driverID = "cockroach"

	// language is what the editor highlights and completes by. The lexer
	// already knows the name, and it resolves to PostgreSQL's rules, which
	// are CockroachDB's.
	language = "cockroachdb"

	// cancelDeadline is how long a cancelled statement is given to stop at
	// the server before the socket is cut anyway.
	cancelDeadline = 3 * time.Second
)

var errClosed = errors.New("cockroach: connection is closed")

// Driver connects to CockroachDB. Importing the package registers it.
type Driver struct{}

func init() { source.Register(Driver{}) }

func (Driver) Describe() source.Descriptor {
	return source.Descriptor{
		ID: driverID, Name: "CockroachDB", Paradigm: model.ParadigmRelational,
		DefaultPort: 26257,
		URLSchemes:  []string{"cockroach", "cockroachdb"},
		Fields: []source.Field{
			{Key: "host", Label: "Host", Kind: source.FieldText, Required: true, Default: "localhost"},
			{Key: "port", Label: "Port", Kind: source.FieldNumber, Default: "26257"},
			{Key: "database", Label: "Database", Kind: source.FieldText, Default: "defaultdb",
				Help: "Where an unqualified name is looked for. Every database in the cluster is readable from any of them."},
			{Key: "user", Label: "User", Kind: source.FieldText, Default: "root"},
			{Key: "password", Label: "Password", Kind: source.FieldPassword, Secret: true},
		},
	}
}

// crdbSource is one connection pool over a whole cluster.
//
// One pool rather than one per database: a CockroachDB connection reads any
// database by naming it, so a second connection would be for nothing. That
// is the first thing here that is not PostgreSQL.
type crdbSource struct {
	dialect

	cfg     source.ConnectionConfig
	primary string
	pool    *pgxpool.Pool

	mu     sync.Mutex
	closed bool

	version string

	// notices routes server notices to whichever query is collecting them,
	// keyed by the connection they arrived on (FR-5.6).
	notices sync.Map // *pgconn.PgConn -> *noticeSink

	// keys memoises how each table's rows are addressed, so that paging and
	// editing do not ask the catalogue again on every page.
	keys sync.Map // "db\x00schema\x00table" -> tableKey
}

var (
	_ source.Source         = (*crdbSource)(nil)
	_ source.Dialect        = (*crdbSource)(nil)
	_ source.Queryer        = (*crdbSource)(nil)
	_ source.Sessioner      = (*crdbSource)(nil)
	_ source.DistinctLister = (*crdbSource)(nil)
	_ source.Writer         = (*crdbSource)(nil)
)

// Open connects and proves the server is reachable, and learns which one
// answered. It does no introspection: FR-1.4 requires a test connection to
// be quick and to fail with a precise reason.
func (Driver) Open(ctx context.Context, cfg source.ConnectionConfig) (source.Source, error) {
	s := &crdbSource{cfg: cfg, primary: cfg.Database}
	if s.primary == "" {
		s.primary = "defaultdb"
	}
	pc, err := s.config()
	if err != nil {
		return nil, &source.ConnectError{Kind: source.ConnectConfig, Hint: "Invalid connection settings.", Err: err}
	}
	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return nil, classifyConnectError(err)
	}
	s.pool = pool
	if err := pool.QueryRow(ctx, "SELECT version()").Scan(&s.version); err != nil {
		s.Close()
		return nil, classifyConnectError(err)
	}
	return s, nil
}

func (s *crdbSource) config() (*pgxpool.Config, error) {
	// Parse a fixed, fully specified string, never an empty one. Given "",
	// pgx fills settings from PGHOST, PGSSLMODE, PGPASSWORD and ~/.pgpass,
	// so a stray variable in the user's shell would silently change which
	// cluster a saved connection reaches, or whether it uses TLS.
	pc, err := pgxpool.ParseConfig("host=localhost sslmode=disable")
	if err != nil {
		return nil, err
	}
	cc := pc.ConnConfig

	cc.Host = s.cfg.Host
	if cc.Host == "" {
		cc.Host = "localhost"
	}
	port := s.cfg.Port
	if port == 0 {
		port = 26257
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("port %d out of range", port)
	}
	cc.Port = uint16(port)
	cc.Database = s.primary
	cc.User = s.cfg.User
	cc.Password = ""
	if s.cfg.Secret != nil {
		pw, err := s.cfg.Secret("password")
		if err != nil {
			return nil, fmt.Errorf("reading password from the keychain: %w", err)
		}
		cc.Password = pw
	}
	cc.ConnectTimeout = 10 * time.Second

	tlsCfg, err := tlsconf.Config(s.cfg.TLS, cc.Host)
	if err != nil {
		return nil, err
	}
	cc.TLSConfig = tlsCfg
	cc.Fallbacks = nil       // no silent downgrade to plaintext
	cc.ValidateConnect = nil // no environment-supplied target_session_attrs

	cc.RuntimeParams = map[string]string{"application_name": "Ikigai DB"}
	if s.cfg.Guard.ReadOnly {
		// The last of three read-only defences; see query.go. CockroachDB
		// honours the setting and refuses a write with 25006.
		cc.RuntimeParams["default_transaction_read_only"] = "on"
	}

	// pgx's default answer to a cancelled context is a deadline on the
	// socket, and the failed read then abandons the connection — taking the
	// session's temporary state with it. This handler sends the wire
	// protocol's cancel request and keeps the connection, so a pinned
	// editor session survives every press of cancel.
	cc.BuildContextWatcherHandler = func(c *pgconn.PgConn) ctxwatch.Handler {
		return &pgconn.CancelRequestContextWatcherHandler{Conn: c, DeadlineDelay: cancelDeadline}
	}
	cc.OnNotice = s.onNotice

	pc.MaxConns = 4
	pc.MaxConnIdleTime = 5 * time.Minute
	return pc, nil
}

// conn is the pool, or a refusal once the source is closed.
func (s *crdbSource) conn() (*pgxpool.Pool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errClosed
	}
	return s.pool, nil
}

func (s *crdbSource) Capabilities() capability.Capabilities {
	return capability.Capabilities{
		Paradigm: model.ParadigmRelational,
		// Databases and schemas both: a cluster holds databases, a database
		// holds schemas, and one connection reads all of them.
		Structure: capability.Structure{MultipleDatabases: true, Schemas: true},
		Query: capability.Query{Supported: true, Language: language, MultiStatement: true,
			Cancel: true, Parameters: true},
		Data: capability.Data{ServerSort: true, ServerFilter: true, DistinctValues: true,
			ApproximateCount: true, Insert: true, Update: true, Delete: true,
			TransactionalWrite: true},
		Schema: capability.Schema{ForeignKeys: true},
		Objects: map[model.ObjectKind]bool{
			model.KindDatabase: true, model.KindSchema: true, model.KindFolder: true,
			model.KindTable: true, model.KindView: true, model.KindMaterializedView: true,
			model.KindColumn: true, model.KindIndex: true, model.KindSequence: true,
			model.KindRoutine: true, model.KindUserType: true,
		},
	}
}

// Info identifies the server, measuring round-trip latency as it does.
func (s *crdbSource) Info(ctx context.Context) (source.ServerInfo, error) {
	p, err := s.conn()
	if err != nil {
		return source.ServerInfo{}, err
	}
	start := time.Now()
	var banner string
	if err := p.QueryRow(ctx, "SELECT version()").Scan(&banner); err != nil {
		return source.ServerInfo{}, err
	}
	return source.ServerInfo{
		Product: "CockroachDB", Version: versionOf(banner), Latency: time.Since(start),
		Attrs: map[string]string{"version": banner},
	}, nil
}

// versionOf is the release out of the banner, which names the build and the
// platform as well: "CockroachDB CCL v26.3.2 (aarch64-…, built …)".
func versionOf(banner string) string {
	for _, f := range strings.Fields(banner) {
		if len(f) > 1 && f[0] == 'v' && f[1] >= '0' && f[1] <= '9' {
			return f
		}
	}
	return strings.TrimSpace(banner)
}

func (s *crdbSource) Ping(ctx context.Context) error {
	p, err := s.conn()
	if err != nil {
		return err
	}
	return p.Ping(ctx)
}

// Close releases the pool. Safe to call more than once: pgx closes a pool
// once however often it is asked, so there is nothing here to remember.
func (s *crdbSource) Close() error {
	s.mu.Lock()
	pool := s.pool
	s.closed = true
	s.mu.Unlock()
	if pool != nil {
		pool.Close()
	}
	return nil
}

// noticeSink collects the notices raised while one query runs.
type noticeSink struct {
	mu   sync.Mutex
	msgs []source.Message
}

func (n *noticeSink) add(m source.Message) {
	n.mu.Lock()
	n.msgs = append(n.msgs, m)
	n.mu.Unlock()
}

func (n *noticeSink) drain() []source.Message {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := n.msgs
	n.msgs = nil
	return out
}

func (s *crdbSource) onNotice(c *pgconn.PgConn, n *pgconn.Notice) {
	v, ok := s.notices.Load(c)
	if !ok {
		return // no query is collecting on this connection
	}
	level := source.MessageInfo
	if n.Severity == "WARNING" {
		level = source.MessageWarning
	}
	v.(*noticeSink).add(source.Message{
		Level: level, Text: n.Message, Code: n.Code, Position: int(n.Position),
	})
}

// classifyConnectError turns a connection failure into one the connection
// form can act on (FR-1.4).
func classifyConnectError(err error) error {
	if errors.Is(err, errClosed) {
		return err
	}
	kind, hint := source.ConnectUnknown, "The connection failed."

	var pgErr *pgconn.PgError
	var dnsErr *net.DNSError
	var netErr net.Error
	switch {
	case errors.As(err, &pgErr):
		switch pgErr.Code {
		case "28P01", "28000":
			kind, hint = source.ConnectAuth, "The server rejected these credentials."
		case "3D000":
			kind, hint = source.ConnectNoDatabase, "That database does not exist."
		case "53300", "57P03":
			kind, hint = source.ConnectRefused, "The server is not accepting connections."
		}
	case isTLSFailure(err):
		kind, hint = source.ConnectTLS, "TLS negotiation or certificate verification failed."
	case errors.As(err, &dnsErr):
		kind, hint = source.ConnectUnreachable, "That host name could not be found."
	case errors.As(err, &netErr):
		kind, hint = source.ConnectUnreachable, "The server could not be reached."
	}
	return &source.ConnectError{Kind: kind, Hint: hint, Err: err}
}

func isTLSFailure(err error) bool {
	var unknownCA x509.UnknownAuthorityError
	var hostErr x509.HostnameError
	var invalid x509.CertificateInvalidError
	var verify *tls.CertificateVerificationError
	if errors.As(err, &unknownCA) || errors.As(err, &hostErr) ||
		errors.As(err, &invalid) || errors.As(err, &verify) {
		return true
	}
	// A node started with --insecure answers the TLS request with a refusal
	// that pgconn reports only as text.
	return strings.Contains(err.Error(), "server refused TLS connection")
}
