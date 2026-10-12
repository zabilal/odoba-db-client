// Package trino is the Trino driver (T5.15), over trinodb/trino-go-client:
// pure Go, like every other driver here.
//
// Trino is not a database. It is a query engine in front of other people's:
// a catalog is somebody's warehouse, lake or operational store, a schema is a
// namespace in it, and a table is whatever that store calls one. Trino keeps
// none of the data and few of the promises a database makes about it — no key
// that addresses a row, no order rows come back in, no count it has not
// worked out by reading. So a grid opened on it reads and does not edit, and
// one page follows another only because every page is ordered (ADR-0174).
package trino

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

	tr "github.com/trinodb/trino-go-client/trino"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/capability"
)

const driverID = "trino"

func init() { source.Register(Driver{}) }

// Driver connects to a Trino coordinator.
type Driver struct{}

func (Driver) Describe() source.Descriptor {
	return source.Descriptor{
		ID: driverID, Name: "Trino", Paradigm: model.ParadigmRelational, DefaultPort: 8080,
		URLSchemes: []string{"trino"},
		Fields: []source.Field{
			{Key: "host", Label: "Host", Kind: source.FieldText, Required: true, Default: "localhost"},
			{Key: "port", Label: "Port", Kind: source.FieldNumber, Default: "8080",
				Help: "8080 as it is installed; 443 or 8443 where it is encrypted."},
			{Key: "user", Label: "User", Kind: source.FieldText, Required: true,
				Help: "Trino wants a name for every query, whether or not it checks one."},
			{Key: "password", Label: "Password", Kind: source.FieldPassword, Secret: true,
				Help: "Only over an encrypted connection: a password is not sent in the clear."},
			{Key: "database", Label: "Catalog", Kind: source.FieldText,
				Help: "The catalog a bare name means in a query. Every catalog is shown either way."},
		},
	}
}

// Open connects and learns which server answered, and nothing more
// (source.Driver: a test connection must be fast and precise).
func (Driver) Open(ctx context.Context, cfg source.ConnectionConfig) (source.Source, error) {
	conf, err := config(cfg)
	if err != nil {
		return nil, err
	}
	connector, err := tr.NewConnector(conf)
	if err != nil {
		return nil, &source.ConnectError{Kind: source.ConnectConfig,
			Hint: "Those settings cannot become a connection.", Err: err}
	}
	version, err := hello(ctx, cfg)
	if err != nil {
		return nil, err
	}
	s := &trinoSource{cfg: cfg, db: sql.OpenDB(connector), catalog: conf.Catalog, version: version,
		cols: map[string][]model.Column{}}
	s.db.SetConnMaxIdleTime(5 * time.Minute)
	return s, nil
}

// hello asks the server what it is, once, and takes the first answer.
//
// On a connection of its own, because the client's patience is right for a
// query and wrong for this. It tries a request that could not be sent twenty
// times over two minutes, which is what somebody wants of a query that has
// been running for an hour when a coordinator restarts — and is two minutes of
// nothing for somebody who typed the wrong port and pressed Test. So the first
// question is asked once: a refusal is a refusal, and says so at once.
func hello(ctx context.Context, cfg source.ConnectionConfig) (string, error) {
	conf, err := config(cfg)
	if err != nil {
		return "", err
	}
	once := 1
	conf.RequestRetryMaxAttempts = &once
	connector, err := tr.NewConnector(conf)
	if err != nil {
		return "", &source.ConnectError{Kind: source.ConnectConfig,
			Hint: "Those settings cannot become a connection.", Err: err}
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	// The one question every Trino answers, whatever its catalogs: it needs
	// no catalog, reads no table and is refused to nobody who may connect.
	var version string
	if err := db.QueryRowContext(ctx, `SELECT version()`).Scan(&version); err != nil {
		return "", classifyConnectError(err)
	}
	return version, nil
}

// config is the client's settings for a connection, without speaking to
// anything: what cannot become a request is said before the network is
// touched.
func config(cfg source.ConnectionConfig) (*tr.Config, error) {
	bad := func(hint string, err error) (*tr.Config, error) {
		return nil, &source.ConnectError{Kind: source.ConnectConfig, Hint: hint, Err: err}
	}
	host := strings.TrimSpace(cfg.Host)
	if host == "" {
		host = "localhost"
	}
	port := cfg.Port
	if port == 0 {
		port = 8080
	}
	user := strings.TrimSpace(cfg.User)
	if user == "" {
		return bad("Trino wants a user name for every query.", errors.New("trino: no user"))
	}
	conf := &tr.Config{
		// The application's own name, so a query is recognisable in the
		// coordinator's list and in anybody's audit.
		Source:  "ikigai-db",
		Catalog: strings.TrimSpace(cfg.Database),
		Schema:  strings.TrimSpace(cfg.Params["schema"]),
		// Every time in UTC, for the session as well as for what comes back.
		// A timestamp with no zone is compared with one that has a zone by
		// reading it in the session's, so a session in somebody's own zone
		// would make a filter on a value this showed them select a different
		// hour from the one they picked (browse.go).
		TimeZone: "UTC",
	}

	// Encrypted and verified unless somebody chose otherwise (NFR-S3).
	scheme := "https"
	switch cfg.TLS.Mode {
	case "disable":
		scheme = "http"
	case "require":
		conf.SSLVerification = tr.SSLVerificationNone
	case "verify-ca":
		conf.SSLVerification = tr.SSLVerificationCA
	case "", "verify-full":
		conf.SSLVerification = tr.SSLVerificationFull
	default:
		return bad(fmt.Sprintf("%q is not a way to secure a connection.", cfg.TLS.Mode),
			fmt.Errorf("trino: unknown TLS mode %q", cfg.TLS.Mode))
	}
	if cfg.TLS.ServerName != "" {
		// The client checks a certificate against the host it dialled and
		// offers no other name to check against. Accepting one and ignoring
		// it would be verification that is not the one asked for.
		return bad("This driver checks a certificate against the host it connects to, and cannot be given another name.",
			errors.New("trino: a server name to verify against is not supported"))
	}
	if scheme == "https" {
		conf.SSLCertPath = cfg.TLS.CAFile
		conf.SSLClientCertPath = cfg.TLS.CertFile
		conf.SSLClientKeyPath = cfg.TLS.KeyFile
	}

	who := url.User(user)
	if cfg.Secret != nil {
		pw, err := cfg.Secret("password")
		if err != nil {
			return bad("The password for this connection could not be read.", err)
		}
		if pw != "" {
			if scheme != "https" {
				// Trino refuses a password over plain HTTP, and so does its
				// client; said here, it is said in terms of the setting.
				return bad("A password is sent only over an encrypted connection: turn encryption on, or leave the password out.",
					errors.New("trino: a password over an unencrypted connection"))
			}
			who = url.UserPassword(user, pw)
		}
	}
	conf.ServerURI = (&url.URL{Scheme: scheme, User: who,
		Host: net.JoinHostPort(host, strconv.Itoa(port))}).String()
	return conf, nil
}

// trinoSource is one connection pool to a coordinator.
type trinoSource struct {
	dialect
	cfg     source.ConnectionConfig
	db      *sql.DB
	version string
	// catalog is the one the connection names, which is the one the explorer
	// marks as where somebody is.
	catalog string

	// cols remembers each table's columns, which is what paging orders by:
	// the answer does not change while a connection is open, and paging asks
	// on every page.
	mu   sync.Mutex
	cols map[string][]model.Column
}

var (
	_ source.Source         = (*trinoSource)(nil)
	_ source.Dialect        = (*trinoSource)(nil)
	_ source.Queryer        = (*trinoSource)(nil)
	_ source.Sessioner      = (*trinoSource)(nil)
	_ source.DistinctLister = (*trinoSource)(nil)
)

func (s *trinoSource) Capabilities() capability.Capabilities {
	return capability.Capabilities{
		Paradigm:  model.ParadigmRelational,
		Structure: capability.Structure{MultipleDatabases: true, Schemas: true},
		// No Cancel: that claims a statement can be stopped from another
		// connection by its name, and this keeps no name to stop one by. A
		// statement somebody stops is still stopped at the server — the
		// client tells the coordinator when its context ends.
		Query: capability.Query{Supported: true, Language: driverID, MultiStatement: true, Parameters: true},
		// Read, filtered, sorted and grouped on the server, and not written:
		// no row here has an address. An estimate where the catalog's
		// connector keeps one; never an exact count, which is a read of the
		// whole table in an engine built for tables too large to read.
		Data: capability.Data{ServerSort: true, ServerFilter: true, DistinctValues: true, ApproximateCount: true},
		Objects: map[model.ObjectKind]bool{
			model.KindDatabase: true, model.KindSchema: true, model.KindFolder: true,
			model.KindTable: true, model.KindView: true, model.KindColumn: true,
		},
	}
}

func (s *trinoSource) Info(ctx context.Context) (source.ServerInfo, error) {
	start := time.Now()
	if err := s.Ping(ctx); err != nil {
		return source.ServerInfo{}, err
	}
	return source.ServerInfo{Product: "Trino", Version: s.version, Latency: time.Since(start)}, nil
}

// Ping asks the cheapest question there is. A pool's own ping proves nothing
// here: a connection to Trino is a set of headers, and nothing is sent until
// something is asked.
func (s *trinoSource) Ping(ctx context.Context) error {
	var one int64
	return statementError(s.db.QueryRowContext(ctx, `SELECT 1`).Scan(&one), ctx)
}

func (s *trinoSource) Close() error { return s.db.Close() }

// failure is what the server said went wrong, where it was the server that
// said it.
func failure(err error) (tr.ErrTrino, bool) {
	var byValue tr.ErrTrino
	if errors.As(err, &byValue) {
		return byValue, true
	}
	var byPointer *tr.ErrTrino
	if errors.As(err, &byPointer) && byPointer != nil {
		return *byPointer, true
	}
	return tr.ErrTrino{}, false
}

// classifyConnectError says what went wrong in terms a person can act on
// (FR-1.4).
func classifyConnectError(err error) error {
	var ce *source.ConnectError
	if errors.As(err, &ce) {
		return err
	}
	var (
		failed *tr.ErrQueryFailed
		dns    *net.DNSError
		ne     net.Error
		verify *tls.CertificateVerificationError
		record tls.RecordHeaderError
	)
	said, isServer := failure(err)
	switch {
	case isServer && (said.ErrorName == "CATALOG_NOT_FOUND" || said.ErrorName == "SCHEMA_NOT_FOUND"):
		return &source.ConnectError{Kind: source.ConnectNoDatabase,
			Hint: "That catalog or schema does not exist on this server.", Err: err}
	case errors.As(err, &failed) && (failed.StatusCode == 401 || failed.StatusCode == 403):
		return &source.ConnectError{Kind: source.ConnectAuth,
			Hint: "The server refused that user name or password.", Err: err}
	case errors.As(err, &failed) && failed.StatusCode == 503:
		return &source.ConnectError{Kind: source.ConnectRefused,
			Hint: "The server is there but not ready: it may still be starting.", Err: err}
	case errors.As(err, &verify) || errors.As(err, &record) ||
		strings.Contains(err.Error(), "server gave HTTP response to HTTPS client"):
		return &source.ConnectError{Kind: source.ConnectTLS,
			Hint: "The encrypted connection could not be agreed.", Err: err}
	case errors.As(err, &dns):
		return &source.ConnectError{Kind: source.ConnectUnreachable,
			Hint: "That host name could not be found.", Err: err}
	case errors.Is(err, syscall.ECONNREFUSED):
		return &source.ConnectError{Kind: source.ConnectUnreachable,
			Hint: "Nothing is listening on that host and port.", Err: err}
	case errors.As(err, &ne) && ne.Timeout():
		return &source.ConnectError{Kind: source.ConnectUnreachable,
			Hint: "The server did not answer in time.", Err: err}
	case isServer:
		return &source.ConnectError{Kind: source.ConnectUnknown, Hint: firstLine(said.Message), Err: err}
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
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	said, ok := failure(err)
	if !ok {
		return &source.StatementError{Err: err, Message: source.Message{
			Level: source.MessageError, Text: err.Error()}}
	}
	if said.ErrorName == "USER_CANCELED" {
		return context.Canceled // somebody stopped it, whoever asked
	}
	return &source.StatementError{Err: err, Message: source.Message{
		Level: source.MessageError, Text: firstLine(said.Message), Code: said.ErrorName}}
}

// positioned is statementError for a statement somebody typed, which can say
// where in it the server stopped reading (FR-5.10).
func positioned(err error, ctx context.Context, text string) error {
	out := statementError(err, ctx)
	var se *source.StatementError
	if said, ok := failure(err); ok && errors.As(out, &se) {
		se.Message.Position = offsetOf(text, said.ErrorLocation.LineNumber, said.ErrorLocation.ColumnNumber)
	}
	return out
}

// offsetOf is a line and a column as a character offset, both counted from
// one, or nought where the server named no place.
func offsetOf(text string, line, column int) int {
	if line < 1 || column < 1 {
		return 0
	}
	at := 0
	for l := 1; l < line; l++ {
		next := strings.IndexByte(text[at:], '\n')
		if next < 0 {
			return 0
		}
		at += next + 1
	}
	return len([]rune(text[:at])) + column
}

// firstLine is a message without the list of every word the parser would
// have taken instead, which a Trino syntax error carries and which is longer
// than the window.
func firstLine(msg string) string {
	if i := strings.Index(msg, ". Expecting: "); i >= 0 {
		msg = msg[:i]
	}
	line, _, _ := strings.Cut(msg, "\n")
	return strings.TrimSpace(line)
}
