// Package clickhouse is the ClickHouse driver (T3.31), over
// ClickHouse/clickhouse-go: pure Go, like every other driver here, so the
// application stays a single binary with no C toolchain.
//
// ClickHouse is a column store, and the places it differs from the row
// stores are not only in its syntax. It has no schema between the database
// and the table. It has no key that addresses a row: a MergeTree's ORDER BY
// orders the parts and is not unique, and the virtual columns that say
// where a row sits on disk move when parts merge. So a grid opened on it
// reads and does not edit, and paging is made deterministic by ordering
// rather than by a key (ADR-0142).
package clickhouse

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/capability"
)

const driverID = "clickhouse"

func init() { source.Register(Driver{}) }

// Driver connects to ClickHouse over its native protocol.
type Driver struct{}

func (Driver) Describe() source.Descriptor {
	return source.Descriptor{
		ID: driverID, Name: "ClickHouse", Paradigm: model.ParadigmRelational, DefaultPort: 9000,
		URLSchemes: []string{"clickhouse"}, TLSSchemes: []string{"clickhouses"},
		Fields: []source.Field{
			{Key: "host", Label: "Host", Kind: source.FieldText, Required: true, Default: "localhost"},
			{Key: "port", Label: "Port", Kind: source.FieldNumber, Default: "9000",
				Help: "9000 for the native protocol, 9440 with TLS."},
			{Key: "database", Label: "Database", Kind: source.FieldText, Default: "default"},
			{Key: "user", Label: "User", Kind: source.FieldText, Default: "default"},
			{Key: "password", Label: "Password", Kind: source.FieldPassword, Secret: true},
		},
	}
}

// Open connects and learns which server answered, and nothing more
// (source.Driver: a test connection must be fast and precise).
func (Driver) Open(ctx context.Context, cfg source.ConnectionConfig) (source.Source, error) {
	opt, err := options(cfg)
	if err != nil {
		return nil, err
	}
	s := &clickhouseSource{cfg: cfg, db: ch.OpenDB(opt), keys: map[string][]string{}}
	s.db.SetConnMaxIdleTime(5 * time.Minute)
	if err := s.db.PingContext(ctx); err != nil {
		s.db.Close()
		return nil, classifyConnectError(err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT version(), currentDatabase()`).
		Scan(&s.version, &s.primary); err != nil {
		s.db.Close()
		return nil, classifyConnectError(err)
	}
	return s, nil
}

// options builds the connection settings.
//
// The native protocol rather than HTTP: it is what the driver is fastest
// over, and it carries the server's progress and its exceptions as
// themselves rather than as a status code.
func options(cfg source.ConnectionConfig) (*ch.Options, error) {
	host, port := cfg.Host, cfg.Port
	if host == "" {
		host = "localhost"
	}
	if port == 0 {
		port = 9000
	}
	pw := ""
	if cfg.Secret != nil {
		if v, err := cfg.Secret("password"); err == nil {
			pw = v
		}
	}
	user := cfg.User
	if user == "" {
		user = "default"
	}
	tlsCfg, err := transport(cfg.TLS)
	if err != nil {
		return nil, &source.ConnectError{Kind: source.ConnectConfig,
			Hint: "Invalid connection settings.", Err: err}
	}
	return &ch.Options{
		Protocol: ch.Native,
		Addr:     []string{net.JoinHostPort(host, strconv.Itoa(port))},
		Auth:     ch.Auth{Database: cfg.Database, Username: user, Password: pw},
		TLS:      tlsCfg,
		// The application's own name, so a query is recognisable in
		// system.query_log and in anybody's audit.
		ClientInfo:  ch.ClientInfo{Products: []struct{ Name, Version string }{{Name: "ikigai-db", Version: "1"}}},
		DialTimeout: 10 * time.Second,
	}, nil
}

// transport says how the connection is protected (NFR-S3).
//
// On and verified unless somebody chose otherwise. A nil answer means no
// encryption at all, which is what "disable" asks for.
func transport(t source.TLSConfig) (*tls.Config, error) {
	switch t.Mode {
	case "disable":
		return nil, nil
	case "require":
		// Encrypted, and the certificate not checked: that is what the mode
		// means everywhere else, and it is a choice somebody has to make.
		return &tls.Config{InsecureSkipVerify: true}, nil
	case "", "verify-ca", "verify-full":
	default:
		return nil, fmt.Errorf("clickhouse: %q is not a way to encrypt a connection", t.Mode)
	}
	cfg := &tls.Config{ServerName: t.ServerName}
	if t.CAFile != "" {
		pem, err := os.ReadFile(t.CAFile)
		if err != nil {
			return nil, fmt.Errorf("clickhouse: the certificate file cannot be read: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("clickhouse: %s holds no certificate", t.CAFile)
		}
		cfg.RootCAs = pool
	}
	if t.CertFile != "" || t.KeyFile != "" {
		pair, err := tls.LoadX509KeyPair(t.CertFile, t.KeyFile)
		if err != nil {
			return nil, err
		}
		cfg.Certificates = []tls.Certificate{pair}
	}
	return cfg, nil
}

// clickhouseSource is one connection pool.
//
// One pool and not one per database, unlike the SQL Server driver: a
// ClickHouse connection reads every database it has rights to by naming it,
// so there is nothing to open a second connection for.
type clickhouseSource struct {
	dialect
	cfg     source.ConnectionConfig
	db      *sql.DB
	version string
	// primary is the database the connection was opened on, which is the one
	// the explorer marks as where somebody is.
	primary string

	// keys remembers each table's sorting key, which is what paging orders
	// by when nothing else was asked for.
	mu   sync.Mutex
	keys map[string][]string
}

var (
	_ source.Source         = (*clickhouseSource)(nil)
	_ source.Dialect        = (*clickhouseSource)(nil)
	_ source.Queryer        = (*clickhouseSource)(nil)
	_ source.Sessioner      = (*clickhouseSource)(nil)
	_ source.DistinctLister = (*clickhouseSource)(nil)
	_ source.Killer         = (*clickhouseSource)(nil)
	_ source.Writer         = (*clickhouseSource)(nil)
)

func (s *clickhouseSource) Capabilities() capability.Capabilities {
	return capability.Capabilities{
		Paradigm:  model.ParadigmRelational,
		Structure: capability.Structure{MultipleDatabases: true},
		Query: capability.Query{Supported: true, Language: driverID, MultiStatement: true,
			Parameters: true, Cancel: true},
		// Rows are written back by the key somebody names for them: no row
		// here has an address of its own, and there is no transaction to
		// write a changeset in either (ADR-0142).
		Data: capability.Data{ServerSort: true, ServerFilter: true, DistinctValues: true,
			ApproximateCount: true, Insert: true, Update: true, Delete: true},
		Objects: map[model.ObjectKind]bool{
			model.KindDatabase: true, model.KindFolder: true,
			model.KindTable: true, model.KindView: true, model.KindMaterializedView: true,
			model.KindColumn: true, model.KindRoutine: true,
		},
	}
}

func (s *clickhouseSource) Info(ctx context.Context) (source.ServerInfo, error) {
	return source.ServerInfo{Product: "ClickHouse", Version: s.version}, nil
}

func (s *clickhouseSource) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

func (s *clickhouseSource) Close() error { return s.db.Close() }

// KillQuery stops a statement at the server (source.Killer, FR-5.5).
//
// A session names every statement it runs after itself, so what the handle
// matches is whatever that session has running — which is one statement,
// and perhaps the tail of one somebody has already stopped.
func (s *clickhouseSource) KillQuery(ctx context.Context, handle string) error {
	if handle == "" {
		return errors.New("clickhouse: a statement is stopped by the name it was given")
	}
	_, err := s.db.ExecContext(ctx, `KILL QUERY WHERE startsWith(query_id, ?)`, handle)
	return err
}

// classifyConnectError says what went wrong in terms a person can act on
// (FR-1.4).
func classifyConnectError(err error) error {
	var ce *source.ConnectError
	if errors.As(err, &ce) {
		return err
	}
	var ex *ch.Exception
	var dns *net.DNSError
	var ne net.Error
	var cv *tls.CertificateVerificationError
	switch {
	case errors.As(err, &ex) && (ex.Code == 516 || ex.Code == 192 || ex.Code == 193):
		return &source.ConnectError{Kind: source.ConnectAuth, Hint: "The user name or password was not accepted.", Err: err}
	case errors.As(err, &ex) && ex.Code == 81:
		return &source.ConnectError{Kind: source.ConnectNoDatabase, Hint: "That database does not exist on this server.", Err: err}
	case errors.As(err, &cv) || strings.Contains(err.Error(), "x509:"):
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
// A statement stopped by its context reports the cancellation: the server's
// own word for a statement somebody stopped is that it was cancelled, and
// reporting that as a failure would make a stop look like one.
func statementError(err error, ctx context.Context) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var ex *ch.Exception
	if errors.As(err, &ex) {
		if ctx != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		// QUERY_WAS_CANCELLED: somebody stopped it, whoever asked.
		if ex.Code == 394 {
			return context.Canceled
		}
		return &source.StatementError{Err: err, Message: source.Message{
			Level: source.MessageError, Text: firstLine(ex.Message), Code: strconv.Itoa(int(ex.Code))}}
	}
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return &source.StatementError{Err: err, Message: source.Message{
		Level: source.MessageError, Text: err.Error()}}
}

// firstLine is the message without the stack trace and the list of every
// token the parser would have accepted, which a ClickHouse syntax error
// carries and which is longer than the window.
func firstLine(msg string) string {
	if i := strings.Index(msg, ". Expected one of: "); i >= 0 {
		msg = msg[:i+1]
	}
	line, _, _ := strings.Cut(msg, "\n")
	return strings.TrimSpace(line)
}
