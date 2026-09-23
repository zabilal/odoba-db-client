// Package oracle is the Oracle Database driver (T3.32), over sijms/go-ora
// in its thin mode: pure Go, speaking the wire protocol directly, so there
// is no Instant Client to install beside the application and no C toolchain
// to build it with.
//
// Oracle's shape is its own. A connection is to one service, and what it
// holds is schemas — which are its users, there being no separate idea of
// one. So the explorer's top level is schemas rather than databases. And a
// row has an address: ROWID says where it is, and every table has one,
// which is what lets a grid edit a table with no key of its own
// (ADR-0144).
package oracle

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	ora "github.com/sijms/go-ora/v2"
	"github.com/sijms/go-ora/v2/network"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/capability"
)

const driverID = "oracle"

func init() { source.Register(Driver{}) }

// Driver connects to Oracle Database.
type Driver struct{}

func (Driver) Describe() source.Descriptor {
	return source.Descriptor{
		ID: driverID, Name: "Oracle", Paradigm: model.ParadigmRelational, DefaultPort: 1521,
		URLSchemes: []string{"oracle"},
		Fields: []source.Field{
			{Key: "host", Label: "Host", Kind: source.FieldText, Required: true, Default: "localhost"},
			{Key: "port", Label: "Port", Kind: source.FieldNumber, Default: "1521"},
			{Key: "database", Label: "Service", Kind: source.FieldText, Required: true,
				Help: "The service name, not a database: FREEPDB1, ORCLPDB1, XEPDB1."},
			{Key: "user", Label: "User", Kind: source.FieldText, Required: true},
			{Key: "password", Label: "Password", Kind: source.FieldPassword, Secret: true},
		},
	}
}

// Open connects and learns which server answered, and nothing more
// (source.Driver: a test connection must be fast and precise).
func (Driver) Open(ctx context.Context, cfg source.ConnectionConfig) (source.Source, error) {
	conn, err := dsn(cfg)
	if err != nil {
		return nil, err
	}
	db := sql.OpenDB(&utcConnector{Connector: ora.NewConnector(conn)})
	db.SetConnMaxIdleTime(5 * time.Minute)
	s := &oracleSource{cfg: cfg, db: db, identities: map[string]identity{}}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, classifyConnectError(err)
	}
	// One round trip for what a connection has to know about itself: which
	// server answered, and which schema an unqualified name means.
	if err := db.QueryRowContext(ctx, `SELECT banner_full, sys_context('userenv', 'current_schema')
		FROM v$version WHERE rownum = 1`).Scan(&s.version, &s.primary); err != nil {
		// A login without rights over v$version still has a schema.
		if err := db.QueryRowContext(ctx,
			`SELECT sys_context('userenv', 'current_schema') FROM dual`).Scan(&s.primary); err != nil {
			db.Close()
			return nil, classifyConnectError(err)
		}
	}
	return s, nil
}

// dsn builds a connection string.
//
// A URL rather than the easy-connect form, because a password with a slash
// or an at-sign in it ends an easy-connect string early and nothing says so
// (NFR-S6).
func dsn(cfg source.ConnectionConfig) (string, error) {
	host, port := cfg.Host, cfg.Port
	if host == "" {
		host = "localhost"
	}
	if port == 0 {
		port = 1521
	}
	service := strings.TrimSpace(cfg.Database)
	if service == "" {
		return "", &source.ConnectError{Kind: source.ConnectConfig,
			Hint: "Name the service to connect to.",
			Err:  errors.New("oracle: a connection needs a service name")}
	}
	pw := ""
	if cfg.Secret != nil {
		if v, err := cfg.Secret("password"); err == nil {
			pw = v
		}
	}
	q := url.Values{}
	// The application's own name, so that a session is recognisable in
	// v$session and in anybody's audit.
	q.Set("program", "ikigai-db")
	q.Set("timeout", "10")
	if err := encryption(cfg.TLS, q); err != nil {
		return "", &source.ConnectError{Kind: source.ConnectConfig, Hint: "Invalid connection settings.", Err: err}
	}
	u := url.URL{
		Scheme:   "oracle",
		User:     url.UserPassword(cfg.User, pw),
		Host:     net.JoinHostPort(host, strconv.Itoa(port)),
		Path:     "/" + service,
		RawQuery: q.Encode(),
	}
	return u.String(), nil
}

// encryption says how the connection is protected (NFR-S3).
//
// Oracle's own transport encryption is a server-side setting, and what this
// driver can ask for is TLS. On and verified unless somebody chose
// otherwise.
func encryption(t source.TLSConfig, q url.Values) error {
	switch t.Mode {
	case "", "verify-full", "verify-ca":
		q.Set("SSL", "true")
		q.Set("SSL Verify", "true")
	case "require":
		// Encrypted, and the certificate not checked: that is what the mode
		// means everywhere else, and it is a choice somebody has to make.
		q.Set("SSL", "true")
		q.Set("SSL Verify", "false")
	case "disable":
		q.Set("SSL", "false")
	default:
		return fmt.Errorf("oracle: %q is not a way to encrypt a connection", t.Mode)
	}
	if t.CAFile != "" {
		q.Set("wallet", t.CAFile)
	}
	return nil
}

// utcConnector puts every connection it opens on UTC.
//
// A time here has to mean the same on the way out and on the way back.
// Oracle compares a DATE or a TIMESTAMP with a value that carries a zone by
// reading the zone-less one in the session's zone, so a row read at one
// zone and filtered at another misses itself. The session is therefore
// fixed at UTC, as the MySQL driver fixes its own, and every time this
// driver hands over or binds is a time in UTC.
type utcConnector struct{ driver.Connector }

func (c *utcConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	ex, ok := conn.(driver.ExecerContext)
	if !ok {
		conn.Close()
		return nil, errors.New("oracle: this connection cannot be put on UTC")
	}
	if _, err := ex.ExecContext(ctx, `ALTER SESSION SET TIME_ZONE = '+00:00'`, nil); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

// oracleSource is one connection pool.
type oracleSource struct {
	dialect
	cfg     source.ConnectionConfig
	db      *sql.DB
	version string
	// primary is the schema an unqualified name means, which is where the
	// explorer says somebody is.
	primary string

	// identities remembers how each table's rows are addressed, so that
	// paging and editing do not ask again on every page.
	mu         sync.Mutex
	identities map[string]identity
}

var (
	_ source.Source         = (*oracleSource)(nil)
	_ source.Dialect        = (*oracleSource)(nil)
	_ source.Queryer        = (*oracleSource)(nil)
	_ source.Sessioner      = (*oracleSource)(nil)
	_ source.DistinctLister = (*oracleSource)(nil)
	_ source.Writer         = (*oracleSource)(nil)
)

func (s *oracleSource) Capabilities() capability.Capabilities {
	return capability.Capabilities{
		Paradigm: model.ParadigmRelational,
		// Schemas rather than databases: a connection is to one service,
		// and what it holds is its users' schemas.
		Structure: capability.Structure{Schemas: true},
		Query: capability.Query{Supported: true, Language: driverID, MultiStatement: true,
			Parameters: true},
		Data: capability.Data{ServerSort: true, ServerFilter: true, DistinctValues: true,
			ApproximateCount: true, Insert: true, Update: true, Delete: true,
			TransactionalWrite: true},
		Objects: map[model.ObjectKind]bool{
			model.KindSchema: true, model.KindFolder: true,
			model.KindTable: true, model.KindView: true, model.KindMaterializedView: true,
			model.KindColumn: true, model.KindIndex: true, model.KindRoutine: true,
			model.KindTrigger: true, model.KindSequence: true, model.KindUserType: true,
		},
	}
}

func (s *oracleSource) Info(ctx context.Context) (source.ServerInfo, error) {
	return source.ServerInfo{Product: "Oracle Database", Version: versionOf(s.version)}, nil
}

// versionOf is the release out of the banner, which is a sentence about
// what the edition is for as much as a version.
func versionOf(banner string) string {
	banner = strings.TrimSpace(banner)
	if i := strings.Index(banner, " - "); i > 0 {
		banner = banner[:i]
	}
	return strings.TrimSpace(banner)
}

func (s *oracleSource) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

func (s *oracleSource) Close() error { return s.db.Close() }

// classifyConnectError says what went wrong in terms a person can act on
// (FR-1.4).
func classifyConnectError(err error) error {
	var ce *source.ConnectError
	if errors.As(err, &ce) {
		return err
	}
	var oe *network.OracleError
	var dns *net.DNSError
	var ne net.Error
	switch {
	case errors.As(err, &oe) && (oe.ErrCode == 1017 || oe.ErrCode == 1005 || oe.ErrCode == 28000):
		return &source.ConnectError{Kind: source.ConnectAuth, Hint: "The user name or password was not accepted.", Err: err}
	case errors.As(err, &oe) && (oe.ErrCode == 12514 || oe.ErrCode == 12505):
		return &source.ConnectError{Kind: source.ConnectNoDatabase, Hint: "The listener does not know that service.", Err: err}
	case strings.Contains(err.Error(), "x509:"):
		return &source.ConnectError{Kind: source.ConnectTLS, Hint: "The server's certificate could not be verified.", Err: err}
	case errors.As(err, &dns):
		return &source.ConnectError{Kind: source.ConnectUnreachable, Hint: "That host name could not be found.", Err: err}
	case errors.Is(err, syscall.ECONNREFUSED):
		return &source.ConnectError{Kind: source.ConnectRefused, Hint: "Nothing is listening on that host and port.", Err: err}
	case errors.As(err, &ne) && ne.Timeout():
		return &source.ConnectError{Kind: source.ConnectUnreachable, Hint: "The server did not answer in time.", Err: err}
	}
	return &source.ConnectError{Kind: source.ConnectUnknown, Hint: "The connection failed.", Err: err}
}

// statementError carries a server failure as the contract's StatementError.
//
// Oracle knows where in a statement an error was and this driver does not
// say: go-ora reads the offset off the wire and keeps it to itself, so
// what is reported is the code and the message (FR-5.10).
func statementError(err error, ctx context.Context) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var oe *network.OracleError
	if errors.As(err, &oe) {
		if ctx != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		// ORA-01013 is the server's word for a statement somebody stopped.
		if oe.ErrCode == 1013 {
			return context.Canceled
		}
		return &source.StatementError{Err: err, Message: source.Message{
			Level: source.MessageError, Text: firstLine(oe.ErrMsg),
			Code: fmt.Sprintf("ORA-%05d", oe.ErrCode)}}
	}
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return &source.StatementError{Err: err, Message: source.Message{
		Level: source.MessageError, Text: err.Error()}}
}

// firstLine is the message without the code Oracle repeats in front of it
// and without the lines of context under it, which are longer than the
// window and say what the code already said.
func firstLine(msg string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(msg), "\n")
	if i := strings.Index(line, ": "); i > 0 && strings.HasPrefix(line, "ORA-") {
		line = line[i+2:]
	}
	return strings.TrimSpace(line)
}
