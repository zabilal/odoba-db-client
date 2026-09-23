package postgres

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
)

const (
	driverID = "postgres"

	// maxPools bounds how many databases one connection may have open. The
	// explorer opens a pool per expanded database; this stops a click-through
	// of a large cluster from holding a connection to every one of them.
	maxPools = 8

	// cancelDeadline is how long a cancelled query is given to stop after the
	// server has been asked to cancel it, before the socket is cut anyway.
	cancelDeadline = 3 * time.Second
)

var errClosed = errors.New("postgres: connection is closed")

// Driver is the PostgreSQL driver. Importing the package registers it; nothing
// else in the application needs to know it exists (REQ-DB-1).
type Driver struct{}

func init() { source.Register(Driver{}) }

// Describe returns the driver's static metadata and connection form.
func (Driver) Describe() source.Descriptor {
	return source.Descriptor{
		ID:          driverID,
		Name:        "PostgreSQL",
		Paradigm:    model.ParadigmRelational,
		DefaultPort: 5432,
		URLSchemes:  []string{"postgres", "postgresql"},
		Fields: []source.Field{
			{Key: "host", Label: "Host", Kind: source.FieldText, Required: true, Default: "localhost"},
			{Key: "port", Label: "Port", Kind: source.FieldNumber, Default: "5432"},
			{Key: "database", Label: "Database", Kind: source.FieldText, Default: "postgres"},
			{Key: "user", Label: "User", Kind: source.FieldText},
			{Key: "password", Label: "Password", Kind: source.FieldPassword, Secret: true},
		},
	}
}

// pgSource is a live PostgreSQL connection.
//
// A PostgreSQL session is bound to one database, but the explorer shows every
// database on the server. So a source holds one pool per database, opened
// lazily when that database is first expanded or browsed.
type pgSource struct {
	dialect

	cfg     source.ConnectionConfig
	primary string
	base    *pgxpool.Config

	mu     sync.Mutex
	pools  map[string]*pgxpool.Pool
	closed bool

	// notices routes server notices to whichever query is collecting them,
	// keyed by the connection they arrived on (FR-5.6).
	notices sync.Map // *pgconn.PgConn -> *noticeSink

	// pkCache memoises primary-key columns for the paging tiebreaker.
	pkCache sync.Map // "db\x00schema\x00table" -> []string

	// typeNames memoises names for result types outside the built-in OID
	// table. User-type OIDs are per database, so the key includes it.
	typeNames sync.Map // "db\x00oid" -> typeInfo
}

// Open connects and proves the server is reachable. It does no
// introspection: FR-1.4 requires "test connection" to be quick and to fail
// with a precise reason.
func (Driver) Open(ctx context.Context, cfg source.ConnectionConfig) (source.Source, error) {
	s := &pgSource{cfg: cfg, pools: map[string]*pgxpool.Pool{}, primary: cfg.Database}
	if s.primary == "" {
		s.primary = "postgres"
	}

	base, err := s.baseConfig()
	if err != nil {
		return nil, &source.ConnectError{Kind: source.ConnectConfig, Hint: "invalid connection settings", Err: err}
	}
	s.base = base

	p, err := s.pool(ctx, s.primary)
	if err != nil {
		return nil, classifyConnectError(err)
	}
	if err := p.Ping(ctx); err != nil {
		s.Close()
		return nil, classifyConnectError(err)
	}
	return s, nil
}

func (s *pgSource) baseConfig() (*pgxpool.Config, error) {
	// Parse a fixed, fully specified string, never an empty one. Given "", pgx
	// fills settings from PGHOST, PGSSLMODE, PGPASSWORD and ~/.pgpass, so a
	// stray variable in the user's shell would silently change which server a
	// saved connection reaches, or whether it uses TLS. Every field that
	// matters is then set explicitly below.
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
		port = 5432
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

	tlsCfg, err := tlsConfig(s.cfg.TLS, cc.Host)
	if err != nil {
		return nil, err
	}
	cc.TLSConfig = tlsCfg
	cc.Fallbacks = nil       // no silent downgrade to plaintext
	cc.ValidateConnect = nil // no environment-supplied target_session_attrs

	cc.RuntimeParams = map[string]string{"application_name": "Ikigai DB"}
	if s.cfg.Guard.ReadOnly {
		// The last of three read-only defences; see query.go.
		cc.RuntimeParams["default_transaction_read_only"] = "on"
	}

	// pgx's default response to a cancelled context is to put a deadline on
	// the socket. The failed read then abandons the connection: pgconn sends
	// a CancelRequest and a Terminate and closes it. The query does stop, but
	// the whole backend goes with it — and with it every temp table, SET and
	// open transaction in the user's session. For a pinned editor Session
	// (query.go) that would make every press of cancel silently throw the
	// session away. This handler sends the CancelRequest and keeps the
	// connection: the statement fails with query_canceled and the session
	// carries on.
	cc.BuildContextWatcherHandler = func(c *pgconn.PgConn) ctxwatch.Handler {
		return &pgconn.CancelRequestContextWatcherHandler{Conn: c, DeadlineDelay: cancelDeadline}
	}
	cc.OnNotice = s.onNotice

	pc.MaxConns = 4
	pc.MaxConnIdleTime = 5 * time.Minute
	return pc, nil
}

// pool returns the pool for a database, opening it on first use.
func (s *pgSource) pool(ctx context.Context, db string) (*pgxpool.Pool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errClosed
	}
	if p, ok := s.pools[db]; ok {
		return p, nil
	}
	if len(s.pools) >= maxPools {
		return nil, fmt.Errorf("postgres: %d databases already open on this connection", maxPools)
	}
	c := s.base.Copy()
	c.ConnConfig.Database = db
	p, err := pgxpool.NewWithConfig(ctx, c)
	if err != nil {
		return nil, err
	}
	s.pools[db] = p
	return p, nil
}

// poolFor resolves the pool that owns an object, from the first path element.
func (s *pgSource) poolFor(ctx context.Context, ref model.ObjectRef) (*pgxpool.Pool, error) {
	db := s.primary
	if len(ref.Path) > 0 && ref.Path[0] != "" {
		db = ref.Path[0]
	}
	return s.pool(ctx, db)
}

// Capabilities declares exactly what this driver implements and nothing more.
// The conformance suite fails a driver that claims a capability without the
// interface behind it, so every true here is backed by code in this package.
func (s *pgSource) Capabilities() capability.Capabilities {
	return capability.Capabilities{
		Paradigm:  model.ParadigmRelational,
		Structure: capability.Structure{MultipleDatabases: true, Schemas: true},
		Query: capability.Query{
			Supported:       true,
			Language:        "postgresql",
			MultiStatement:  true,
			Cancel:          true,
			Parameters:      true,
			Explain:         true,
			ExplainAnalyze:  true,
			Transactions:    true,
			EditableResults: true,
		},
		Data: capability.Data{
			ServerSort:       true,
			ServerFilter:     true,
			DistinctValues:   true,
			ColumnStats:      true,
			ApproximateCount: true,
			Insert:           true, Update: true, Delete: true, TransactionalWrite: true, BulkLoad: true,
		},
		// DDL because this driver renders a table's structure as statements
		// (ddl.go). What it does not do is run them: the preview does, once
		// somebody has read it (FR-6.4). Diff because it reads a whole
		// database in one pass (snapshot.go), which is what comparing two of
		// them needs (FR-7.1).
		Schema: capability.Schema{ForeignKeys: true, DDL: true, Diff: true},
		Objects: map[model.ObjectKind]bool{
			model.KindDatabase:         true,
			model.KindSchema:           true,
			model.KindFolder:           true,
			model.KindTable:            true,
			model.KindView:             true,
			model.KindMaterializedView: true,
			model.KindColumn:           true,
			model.KindRoutine:          true,
			model.KindSequence:         true,
			model.KindIndex:            true,
			model.KindTrigger:          true,
			model.KindUserType:         true,
		},
	}
}

// Info identifies the server, measuring round-trip latency as it does.
func (s *pgSource) Info(ctx context.Context) (source.ServerInfo, error) {
	p, err := s.pool(ctx, s.primary)
	if err != nil {
		return source.ServerInfo{}, err
	}
	start := time.Now()
	var full, ver string
	if err := p.QueryRow(ctx, "SELECT version(), current_setting('server_version')").Scan(&full, &ver); err != nil {
		return source.ServerInfo{}, err
	}
	product := "PostgreSQL"
	switch {
	case strings.Contains(full, "CockroachDB"):
		product = "CockroachDB"
	case strings.Contains(full, "Redshift"):
		product = "Amazon Redshift"
	}
	return source.ServerInfo{
		Product: product,
		Version: ver,
		Latency: time.Since(start),
		Attrs:   map[string]string{"version": full},
	}, nil
}

// Ping verifies the primary database is still reachable.
func (s *pgSource) Ping(ctx context.Context) error {
	p, err := s.pool(ctx, s.primary)
	if err != nil {
		return err
	}
	return p.Ping(ctx)
}

// Close releases every pool. Safe to call more than once.
func (s *pgSource) Close() error {
	s.mu.Lock()
	pools := s.pools
	s.pools = map[string]*pgxpool.Pool{}
	s.closed = true
	s.mu.Unlock()
	for _, p := range pools {
		p.Close()
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

func (s *pgSource) onNotice(c *pgconn.PgConn, n *pgconn.Notice) {
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
	kind, hint := source.ConnectUnknown, "could not connect"

	var pgErr *pgconn.PgError
	var dnsErr *net.DNSError
	var netErr net.Error
	switch {
	case errors.As(err, &pgErr):
		switch pgErr.Code {
		case "28P01", "28000":
			kind, hint = source.ConnectAuth, "the server rejected these credentials"
		case "3D000":
			kind, hint = source.ConnectNoDatabase, "that database does not exist"
		case "53300", "57P03":
			kind, hint = source.ConnectRefused, "the server is not accepting connections"
		}
	case isTLSFailure(err):
		kind, hint = source.ConnectTLS, "TLS negotiation or certificate verification failed"
	case errors.As(err, &dnsErr):
		kind, hint = source.ConnectUnreachable, "the host name could not be resolved"
	case errors.As(err, &netErr):
		kind, hint = source.ConnectUnreachable, "the server could not be reached"
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
	// A server with TLS off answers the TLS request with a refusal that pgconn
	// reports only as text.
	return strings.Contains(err.Error(), "server refused TLS connection")
}
