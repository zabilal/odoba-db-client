// Package mysql is the MySQL and MariaDB driver (T1.35–T1.37), over
// go-sql-driver/mysql. One driver serves both: MariaDB began as MySQL and
// still speaks its protocol. Where they differ, the driver asks the server
// which it is rather than trusting what the connection was called (REQ-DB-2).
package mysql

import (
	"context"
	"crypto/tls"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/capability"
	"github.com/ikigai-db/ikigai-db/internal/source/tlsconf"
)

const driverID = "mysql"

func init() { source.Register(Driver{}) }

// Driver connects to MySQL and MariaDB servers.
type Driver struct{}

func (Driver) Describe() source.Descriptor {
	return source.Descriptor{
		ID: driverID, Name: "MySQL / MariaDB", Paradigm: model.ParadigmRelational, DefaultPort: 3306,
		URLSchemes: []string{"mysql", "mariadb"},
		Fields: []source.Field{
			{Key: "host", Label: "Host", Kind: source.FieldText, Required: true, Default: "localhost"},
			{Key: "port", Label: "Port", Kind: source.FieldNumber, Default: "3306"},
			{Key: "database", Label: "Database", Kind: source.FieldText, Help: "Optional: the database to start in."},
			{Key: "user", Label: "User", Kind: source.FieldText, Default: "root"},
			{Key: "password", Label: "Password", Kind: source.FieldPassword, Secret: true},
		},
	}
}

// flavor is which server answered, learnt from the server itself.
type flavor struct {
	mariadb bool
	version string
}

// readOnlyVar is the session variable that makes a connection's transactions
// read-only: where MariaDB and MySQL part ways by name.
func (f flavor) readOnlyVar() string {
	if f.mariadb {
		return "tx_read_only"
	}
	return "transaction_read_only"
}

// Open connects, learns which server it is, and nothing more.
func (Driver) Open(ctx context.Context, cfg source.ConnectionConfig) (source.Source, error) {
	mc, err := baseConfig(cfg)
	if err != nil {
		return nil, &source.ConnectError{Kind: source.ConnectConfig, Hint: "Invalid connection settings.", Err: err}
	}
	db, err := openDB(ctx, mc)
	if err != nil {
		return nil, err
	}
	var version string
	if err := db.QueryRowContext(ctx, `SELECT VERSION()`).Scan(&version); err != nil {
		db.Close()
		return nil, classifyConnectError(err)
	}
	fl := flavor{mariadb: strings.Contains(strings.ToLower(version), "mariadb"), version: version}
	if cfg.Guard.ReadOnly {
		// The second defence: every pooled connection starts read-only, so the
		// server itself refuses a write the classifier missed (NFR-S4).
		db.Close()
		mc.Params[fl.readOnlyVar()] = "1"
		if db, err = openDB(ctx, mc); err != nil {
			return nil, err
		}
	}
	return &mysqlSource{db: db, cfg: cfg, fl: fl, identities: map[string]identity{}}, nil
}

func baseConfig(cfg source.ConnectionConfig) (*mysql.Config, error) {
	host, port := cfg.Host, cfg.Port
	if host == "" {
		host = "localhost"
	}
	if port == 0 {
		port = 3306
	}
	mc := mysql.NewConfig()
	mc.Net, mc.Addr = "tcp", net.JoinHostPort(host, strconv.Itoa(port))
	mc.User, mc.DBName = cfg.User, cfg.Database
	if cfg.Secret != nil {
		if pw, err := cfg.Secret("password"); err == nil {
			mc.Passwd = pw
		}
	}
	mc.ParseTime, mc.Loc = true, time.UTC
	mc.Timeout = 10 * time.Second
	mc.AllowNativePasswords = true
	// TIMESTAMP values then arrive as UTC instants, whatever the server's zone.
	mc.Params = map[string]string{"time_zone": "'+00:00'"}
	t, err := tlsconf.Config(cfg.TLS, host)
	if err != nil {
		return nil, err
	}
	mc.TLS = t
	return mc, nil
}

func openDB(ctx context.Context, mc *mysql.Config) (*sql.DB, error) {
	conn, err := mysql.NewConnector(mc)
	if err != nil {
		return nil, &source.ConnectError{Kind: source.ConnectConfig, Hint: "Invalid connection settings.", Err: err}
	}
	db := sql.OpenDB(conn)
	db.SetConnMaxIdleTime(5 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, classifyConnectError(err)
	}
	return db, nil
}

// classifyConnectError says what went wrong in terms a person can act on
// (FR-1.4).
func classifyConnectError(err error) error {
	var ce *source.ConnectError
	if errors.As(err, &ce) {
		return err
	}
	var me *mysql.MySQLError
	var dns *net.DNSError
	var ne net.Error
	var cv *tls.CertificateVerificationError
	switch {
	case errors.As(err, &me) && (me.Number == 1045 || me.Number == 1044 || me.Number == 1698):
		return &source.ConnectError{Kind: source.ConnectAuth, Hint: "The user name or password was not accepted.", Err: err}
	case errors.As(err, &me) && me.Number == 1049:
		return &source.ConnectError{Kind: source.ConnectNoDatabase, Hint: "That database does not exist on this server.", Err: err}
	case errors.Is(err, mysql.ErrNoTLS):
		return &source.ConnectError{Kind: source.ConnectTLS,
			Hint: "The server does not offer encryption. Choose “Don't encrypt” if that is expected.", Err: err}
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

type mysqlSource struct {
	dialect
	db  *sql.DB
	cfg source.ConnectionConfig
	fl  flavor

	mu         sync.Mutex
	identities map[string]identity // browse tiebreak per table
}

var (
	_ source.Source    = (*mysqlSource)(nil)
	_ source.Queryer   = (*mysqlSource)(nil)
	_ source.Sessioner = (*mysqlSource)(nil)
	_ source.Dialect   = (*mysqlSource)(nil)
	_ source.Killer    = (*mysqlSource)(nil)
)

func (s *mysqlSource) Capabilities() capability.Capabilities {
	lang := "mysql"
	if s.fl.mariadb {
		lang = "mariadb"
	}
	return capability.Capabilities{
		Paradigm:  model.ParadigmRelational,
		Structure: capability.Structure{MultipleDatabases: true, CreateDatabase: true},
		Query: capability.Query{Supported: true, Language: lang, MultiStatement: true,
			Cancel: true, Parameters: true},
		// InnoDB counts by scanning; the table statistics' estimate is cheap.
		Data:   capability.Data{ServerSort: true, ServerFilter: true, DistinctValues: true, ApproximateCount: true},
		Schema: capability.Schema{ForeignKeys: true},
		Objects: map[model.ObjectKind]bool{
			model.KindDatabase: true, model.KindFolder: true, model.KindTable: true,
			model.KindView: true, model.KindColumn: true,
		},
	}
}

func (s *mysqlSource) Info(ctx context.Context) (source.ServerInfo, error) {
	start := time.Now()
	var v string
	if err := s.db.QueryRowContext(ctx, `SELECT VERSION()`).Scan(&v); err != nil {
		return source.ServerInfo{}, err
	}
	product := "MySQL"
	if s.fl.mariadb {
		product = "MariaDB"
		v = strings.SplitN(v, "-", 2)[0]
	}
	return source.ServerInfo{Product: product, Version: v, Latency: time.Since(start)}, nil
}

func (s *mysqlSource) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }
func (s *mysqlSource) Close() error                   { return s.db.Close() }

// KillQuery stops what a session is running, from another connection
// (source.Killer). The handle is the session's server connection id.
func (s *mysqlSource) KillQuery(ctx context.Context, handle string) error {
	id, err := strconv.ParseInt(handle, 10, 64)
	if err != nil {
		return fmt.Errorf("mysql: invalid session handle %q", handle)
	}
	return s.kill(id)
}

func (s *mysqlSource) kill(id int64) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := s.db.ExecContext(ctx, fmt.Sprintf("KILL QUERY %d", id))
	return err
}

func (s *mysqlSource) Session(ctx context.Context) (source.Session, error) {
	c, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	var id int64
	if err := c.QueryRowContext(ctx, `SELECT CONNECTION_ID()`).Scan(&id); err != nil {
		c.Close()
		return nil, err
	}
	return &session{src: s, conn: c, connID: id}, nil
}

// Query runs one statement on a session of its own, released when the
// result is closed.
func (s *mysqlSource) Query(ctx context.Context, stmt source.Statement) (*source.Result, error) {
	ss, err := s.Session(ctx)
	if err != nil {
		return nil, err
	}
	res, err := ss.Query(ctx, stmt)
	if err != nil || res.Rows == nil {
		ss.Close()
		return res, err
	}
	res.Rows = &ownedStream{RowStream: res.Rows, owner: ss}
	return res, nil
}

// QueryMulti runs a script on a session of its own, released when the
// script ends or, if its last result is still streaming, when that closes.
func (s *mysqlSource) QueryMulti(ctx context.Context, script string, confirmed bool) (<-chan source.ScriptResult, error) {
	ss, err := s.Session(ctx)
	if err != nil {
		return nil, err
	}
	in, err := ss.QueryMulti(ctx, script, confirmed)
	if err != nil {
		ss.Close()
		return nil, err
	}
	out := make(chan source.ScriptResult, cap(in))
	go func() {
		defer close(out)
		owned := false
		for r := range in {
			if r.Result != nil && r.Result.Rows != nil {
				if _, buffered := r.Result.Rows.(*sliceStream); !buffered {
					r.Result.Rows = &ownedStream{RowStream: r.Result.Rows, owner: ss}
					owned = true
				}
			}
			out <- r
		}
		if !owned {
			ss.Close()
		}
	}()
	return out, nil
}

// statementError carries a server failure as the contract's StatementError.
// A statement stopped by its context reports the cancellation, not the
// server's "query interrupted".
func statementError(err error, ctx context.Context) error {
	if err == nil {
		return nil
	}
	var me *mysql.MySQLError
	if errors.As(err, &me) {
		if (me.Number == 1317 || me.Number == 1927) && ctx != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		return &source.StatementError{Message: source.Message{Level: source.MessageError, Text: me.Message,
			Code: strconv.Itoa(int(me.Number))}, Err: err}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return &source.StatementError{Message: source.Message{Level: source.MessageError, Text: err.Error()}, Err: err}
}
