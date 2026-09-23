// Package sqlserver is the Microsoft SQL Server driver (T3.30), over
// microsoft/go-mssqldb: pure Go, like every other driver here, so the
// application stays a single binary with no C toolchain.
//
// SQL Server puts a level between the server and the schema that the other
// relational engines do not: a server holds databases, a database holds
// schemas, and a schema holds tables. A connection is to one database, and
// reaching another means another connection — so the explorer shows the
// databases a login can see and opens a pool per database, as the PostgreSQL
// driver does for the same reason.
package sqlserver

import (
	"context"
	"crypto/tls"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	mssql "github.com/microsoft/go-mssqldb"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/capability"
)

const driverID = "sqlserver"

func init() { source.Register(Driver{}) }

// Driver connects to Microsoft SQL Server.
type Driver struct{}

func (Driver) Describe() source.Descriptor {
	return source.Descriptor{
		ID: driverID, Name: "SQL Server", Paradigm: model.ParadigmRelational, DefaultPort: 1433,
		URLSchemes: []string{"sqlserver", "mssql"},
		Fields: []source.Field{
			{Key: "host", Label: "Host", Kind: source.FieldText, Required: true, Default: "localhost"},
			{Key: "port", Label: "Port", Kind: source.FieldNumber, Default: "1433"},
			{Key: "database", Label: "Database", Kind: source.FieldText,
				Help: "Optional: the database to start in. Without one, the login's default."},
			{Key: "user", Label: "User", Kind: source.FieldText, Default: "sa"},
			{Key: "password", Label: "Password", Kind: source.FieldPassword, Secret: true},
		},
	}
}

// Open connects and learns which server answered, and nothing more
// (source.Driver: a test connection must be fast and precise).
func (Driver) Open(ctx context.Context, cfg source.ConnectionConfig) (source.Source, error) {
	s := &sqlServerSource{cfg: cfg, pools: map[string]*sql.DB{}, identities: map[string]identity{}}
	db, err := s.open(ctx, cfg.Database)
	if err != nil {
		return nil, err
	}
	// One round trip for what a connection has to know about itself. The
	// server properties rather than the @@VERSION banner: the banner is four
	// lines of prose about the build and the operating system, and these are
	// the same four facts on every edition, Azure's included.
	if err := db.QueryRowContext(ctx, `SELECT CAST(SERVERPROPERTY('ProductVersion') AS nvarchar(128)),
		  CAST(SERVERPROPERTY('Edition') AS nvarchar(128)),
		  CAST(SERVERPROPERTY('EngineEdition') AS int), DB_NAME()`).
		Scan(&s.version, &s.edition, &s.engine, &s.primary); err != nil {
		s.Close()
		return nil, classifyConnectError(err)
	}
	return s, nil
}

// dsn builds a connection string for one database.
//
// A URL rather than the keyword form, because a password with a semicolon in
// it ends a keyword string early and nothing says so: the URL form escapes
// it (NFR-S6).
func dsn(cfg source.ConnectionConfig, database string) (string, error) {
	host, port := cfg.Host, cfg.Port
	if host == "" {
		host = "localhost"
	}
	if port == 0 {
		port = 1433
	}
	pw := ""
	if cfg.Secret != nil {
		if v, err := cfg.Secret("password"); err == nil {
			pw = v
		}
	}
	q := url.Values{}
	if database != "" {
		q.Set("database", database)
	}
	q.Set("connection timeout", "10")
	// The driver's own application name, so that a session on the server is
	// recognisable in sys.dm_exec_sessions and in anybody's audit.
	q.Set("app name", "ikigai-db")
	if err := encryption(cfg.TLS, q); err != nil {
		return "", err
	}
	u := url.URL{
		Scheme:   "sqlserver",
		User:     url.UserPassword(cfg.User, pw),
		Host:     net.JoinHostPort(host, strconv.Itoa(port)),
		RawQuery: q.Encode(),
	}
	return u.String(), nil
}

// encryption says how the connection is protected.
//
// On by default and verified by default, as everywhere else (NFR-S3). SQL
// Server's driver spells the two halves of that in two settings, and the
// second only means anything while the first is on.
func encryption(t source.TLSConfig, q url.Values) error {
	switch t.Mode {
	case "", "verify-full", "verify-ca":
		q.Set("encrypt", "true")
		q.Set("TrustServerCertificate", "false")
	case "require":
		// Encrypted, and the certificate not checked: that is what the mode
		// means everywhere else, and it is a choice somebody has to make.
		q.Set("encrypt", "true")
		q.Set("TrustServerCertificate", "true")
	case "disable":
		q.Set("encrypt", "disable")
	default:
		return fmt.Errorf("sqlserver: %q is not a way to encrypt a connection", t.Mode)
	}
	if t.ServerName != "" {
		q.Set("hostNameInCertificate", t.ServerName)
	}
	if t.CAFile != "" {
		q.Set("certificate", t.CAFile)
	}
	return nil
}

// sqlServerSource is one login, with a pool per database it has been asked
// about.
type sqlServerSource struct {
	dialect
	cfg     source.ConnectionConfig
	version string
	edition string
	// engine is SERVERPROPERTY('EngineEdition'), which says whether this is
	// a server somebody runs or one of Azure's services.
	engine int
	// primary is the database the connection was opened on, which is where
	// everything happens unless a reference names another.
	primary string

	mu    sync.Mutex
	pools map[string]*sql.DB

	// identities remembers each table's browse tiebreak, so that paging is
	// stable on a table with no key of its own.
	idMu       sync.Mutex
	identities map[string]identity
}

var (
	_ source.Source         = (*sqlServerSource)(nil)
	_ source.Dialect        = (*sqlServerSource)(nil)
	_ source.Queryer        = (*sqlServerSource)(nil)
	_ source.Sessioner      = (*sqlServerSource)(nil)
	_ source.DistinctLister = (*sqlServerSource)(nil)
)

// open is the pool for a database, opened the first time it is asked for.
func (s *sqlServerSource) open(ctx context.Context, database string) (*sql.DB, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if db, ok := s.pools[database]; ok {
		return db, nil
	}
	conn, err := dsn(s.cfg, database)
	if err != nil {
		return nil, &source.ConnectError{Kind: source.ConnectConfig, Hint: "Invalid connection settings.", Err: err}
	}
	c, err := mssql.NewConnector(conn)
	if err != nil {
		return nil, &source.ConnectError{Kind: source.ConnectConfig, Hint: "Invalid connection settings.", Err: err}
	}
	db := sql.OpenDB(c)
	db.SetConnMaxIdleTime(5 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, classifyConnectError(err)
	}
	s.pools[database] = db
	return db, nil
}

// pool is the connection for whatever database a reference names, or the
// one the connection was opened on.
func (s *sqlServerSource) pool(ctx context.Context, ref model.ObjectRef) (*sql.DB, error) {
	name := s.primary
	if len(ref.Path) > 0 && ref.Path[0] != "" {
		name = ref.Path[0]
	}
	return s.open(ctx, name)
}

func (s *sqlServerSource) Capabilities() capability.Capabilities {
	return capability.Capabilities{
		Paradigm:  model.ParadigmRelational,
		Structure: capability.Structure{MultipleDatabases: true},
		Query: capability.Query{Supported: true, Language: "sqlserver", MultiStatement: true,
			Parameters: true},
		Data: capability.Data{ServerSort: true, ServerFilter: true, DistinctValues: true,
			ApproximateCount: true, Insert: true, Update: true, Delete: true,
			TransactionalWrite: true},
		Objects: map[model.ObjectKind]bool{
			model.KindDatabase: true, model.KindSchema: true, model.KindFolder: true,
			model.KindTable: true, model.KindView: true, model.KindColumn: true,
			model.KindIndex: true, model.KindRoutine: true, model.KindTrigger: true,
			model.KindSequence: true, model.KindUserType: true,
		},
	}
}

func (s *sqlServerSource) Info(ctx context.Context) (source.ServerInfo, error) {
	return source.ServerInfo{Product: productOf(s.engine), Version: s.version,
		Attrs: map[string]string{"edition": s.edition}}, nil
}

// productOf names what answered. A connection to Azure's SQL Database is not
// a connection to a SQL Server somebody runs, and a health display that
// called it one would be telling a person the wrong thing about what they
// may do to it.
func productOf(engine int) string {
	switch engine {
	case 5:
		return "Azure SQL Database"
	case 6, 11:
		return "Azure Synapse Analytics"
	case 8:
		return "Azure SQL Managed Instance"
	case 9:
		return "Azure SQL Edge"
	}
	return "Microsoft SQL Server"
}

func (s *sqlServerSource) Ping(ctx context.Context) error {
	db, err := s.open(ctx, s.primary)
	if err != nil {
		return err
	}
	return db.PingContext(ctx)
}

func (s *sqlServerSource) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var err error
	for name, db := range s.pools {
		if e := db.Close(); e != nil && err == nil {
			err = e
		}
		delete(s.pools, name)
	}
	return err
}

// classifyConnectError says what went wrong in terms a person can act on
// (FR-1.4).
func classifyConnectError(err error) error {
	var ce *source.ConnectError
	if errors.As(err, &ce) {
		return err
	}
	var se mssql.Error
	var dns *net.DNSError
	var ne net.Error
	var cv *tls.CertificateVerificationError
	switch {
	case errors.As(err, &se) && (se.Number == 18456 || se.Number == 18452):
		return &source.ConnectError{Kind: source.ConnectAuth, Hint: "The user name or password was not accepted.", Err: err}
	case errors.As(err, &se) && (se.Number == 4060 || se.Number == 911):
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
// A statement stopped by its context reports the cancellation, not whatever
// the server said about being interrupted.
//
// SQL Server reports where an error was as a line number, and the contract
// asks for a character offset, so the statement is counted through to the
// start of that line: an editor underlines the line it names (FR-5.10).
func statementError(err error, ctx context.Context, stmt string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var se mssql.Error
	if errors.As(err, &se) {
		if ctx != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		return &source.StatementError{Err: err, Message: source.Message{
			Level: source.MessageError, Text: se.Message,
			Code: strconv.Itoa(int(se.Number)), Position: startOfLine(stmt, int(se.LineNo))}}
	}
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return &source.StatementError{Err: err, Message: source.Message{
		Level: source.MessageError, Text: err.Error()}}
}

// startOfLine is the 1-based character offset the given line begins at, or 0
// where there is no such line to point at.
func startOfLine(stmt string, line int) int {
	at := 1
	for _, r := range stmt {
		at++
		if r == '\n' {
			line--
			if line == 1 {
				return at
			}
		}
	}
	return 0
}
