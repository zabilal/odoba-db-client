// Package firebird is the Firebird driver (T4.2), over nakagami/firebirdsql:
// pure Go, like every other driver here, so the application stays a single
// binary with no C toolchain.
//
// Firebird is smaller than the engines around it in two ways that shape
// everything below. A connection is to one database file — there is no
// server-wide catalogue of databases to browse, and reaching another means
// another connection. And there are no schemas: a database holds tables
// directly, so the tree is database → class → object, as SQLite's is, and an
// object is never qualified by a namespace in a statement. Firebird 6 adds
// schemas; this driver is written against 3 to 5, which is what is deployed.
//
// The catalogue is a set of ordinary tables whose names begin with RDB$, and
// they are the whole of introspection here: there is no information_schema.
// Names in them are stored exactly as the object was created, which for an
// unquoted identifier means upper case, because Firebird folds an unquoted
// name up rather than down.
package firebird

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"syscall"

	fb "github.com/nakagami/firebirdsql"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/capability"
)

const (
	// driverID is this application's name for the source, saved in a
	// connection's settings.
	driverID = "firebird"
	// goDriver is what the library registered itself with database/sql as,
	// which is not the same word and is not ours to choose.
	goDriver = "firebirdsql"
)

func init() { source.Register(Driver{}) }

// Driver connects to Firebird.
type Driver struct{}

func (Driver) Describe() source.Descriptor {
	return source.Descriptor{
		ID: driverID, Name: "Firebird", Paradigm: model.ParadigmRelational, DefaultPort: 3050,
		URLSchemes: []string{"firebird", "firebirdsql", "fb"},
		Fields: []source.Field{
			{Key: "host", Label: "Host", Kind: source.FieldText, Required: true, Default: "localhost"},
			{Key: "port", Label: "Port", Kind: source.FieldNumber, Default: "3050"},
			{Key: "database", Label: "Database", Kind: source.FieldText, Required: true,
				Help: "The database's path on the server, or an alias the server knows it by."},
			{Key: "user", Label: "User", Kind: source.FieldText, Default: "SYSDBA"},
			{Key: "password", Label: "Password", Kind: source.FieldPassword, Secret: true},
			{Key: "role", Label: "Role", Kind: source.FieldText,
				Help: "Optional: a role to take on for this connection."},
		},
	}
}

// Open connects and learns what answered, and nothing more (source.Driver: a
// test connection must be fast and precise).
func (Driver) Open(ctx context.Context, cfg source.ConnectionConfig) (source.Source, error) {
	conn, err := dsn(cfg)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open(goDriver, conn)
	if err != nil {
		return nil, &source.ConnectError{Kind: source.ConnectConfig,
			Hint: "Those connection settings could not be used.", Err: err}
	}
	s := &firebirdSource{db: db, cfg: cfg, sessions: map[string]*session{},
		identities: map[string]model.RowIdentity{}}
	// One round trip for what a connection has to know about itself. The
	// engine version through rdb$get_context rather than a banner: it is the
	// version as the engine states it, and the monitoring table beside it
	// gives the database this turned out to be, which for an alias is not
	// what was typed.
	if err := db.QueryRowContext(ctx, `SELECT rdb$get_context('SYSTEM', 'ENGINE_VERSION'),
			TRIM(d.MON$DATABASE_NAME), d.MON$ODS_MAJOR, d.MON$ODS_MINOR, d.MON$PAGE_SIZE,
			TRIM(d.MON$OWNER), TRIM(r.RDB$CHARACTER_SET_NAME)
		FROM MON$DATABASE d CROSS JOIN RDB$DATABASE r`).
		Scan(&s.version, &s.path, &s.odsMajor, &s.odsMinor, &s.pageSize, &s.owner, &s.charset); err != nil {
		db.Close()
		return nil, classifyConnectError(err)
	}
	return s, nil
}

// database is the name the tree hangs everything under: the database's own
// file name without its directory, which is what somebody recognises it by.
// It is never written into a statement — Firebird has no way to qualify an
// object by the database it is in, there being only ever one.
func (s *firebirdSource) database() string {
	if i := strings.LastIndexAny(s.path, `/\`); i >= 0 {
		return s.path[i+1:]
	}
	if s.path != "" {
		return s.path
	}
	return "database"
}

// dsn builds a connection string.
//
// The password goes through url.UserPassword rather than into the text, so
// that one containing a slash or an at-sign cannot end the address early and
// send half of itself to the wrong place (NFR-S6).
func dsn(cfg source.ConnectionConfig) (string, error) {
	path := strings.TrimSpace(cfg.Database)
	if path == "" {
		return "", &source.ConnectError{Kind: source.ConnectConfig,
			Hint: "Name the database's path on the server, or an alias for it."}
	}
	host := cfg.Host
	if host == "" {
		host = "localhost"
	}
	port := cfg.Port
	if port == 0 {
		port = 3050
	}
	user := cfg.User
	if user == "" {
		user = "SYSDBA"
	}
	pw := ""
	if cfg.Secret != nil {
		if v, err := cfg.Secret("password"); err == nil {
			pw = v
		}
	}
	q := url.Values{}
	// UTF8 for everything, so that what is read back is what was written
	// whatever the database's own default character set is.
	q.Set("charset", "UTF8")
	crypt, err := wireCrypt(cfg.TLS)
	if err != nil {
		return "", err
	}
	q.Set("wire_crypt", crypt)
	if role := strings.TrimSpace(cfg.Params["role"]); role != "" {
		q.Set("role", role)
	}
	u := url.URL{
		Scheme:   "firebird",
		User:     url.UserPassword(user, pw),
		Host:     net.JoinHostPort(host, strconv.Itoa(port)),
		Path:     "/" + strings.TrimPrefix(path, "/"),
		RawQuery: q.Encode(),
	}
	return u.String(), nil
}

// wireCrypt says how the connection is protected.
//
// Firebird does not speak TLS. What it has instead is wire encryption of its
// own: a cipher negotiated during authentication, keyed from the credentials,
// with no certificate anywhere in it. So the two halves of NFR-S3 come apart
// here — encryption is available and is on by default, and there is nothing
// to verify at any setting.
//
// A mode that asks for a certificate to be checked is therefore refused
// rather than quietly treated as encryption alone. Somebody who chose
// verify-full asked for the server to prove who it is, and this protocol
// cannot; saying so is the only honest answer, and "require" is the strongest
// thing it does offer.
func wireCrypt(t source.TLSConfig) (string, error) {
	switch t.Mode {
	case "", "require":
		// Encrypted, and a plaintext fallback refused: the default is the
		// safe one, as everywhere else.
		return "required", nil
	case "disable":
		return "disabled", nil
	case "verify-ca", "verify-full":
		return "", &source.ConnectError{Kind: source.ConnectConfig,
			Hint: "Firebird's wire encryption presents no certificate, so there is nothing to " +
				"verify. Choose Require, which encrypts and refuses to fall back to plain text."}
	}
	return "", &source.ConnectError{Kind: source.ConnectConfig,
		Hint: fmt.Sprintf("%q is not a way to protect a Firebird connection.", t.Mode)}
}

// firebirdSource is one connection to one database.
type firebirdSource struct {
	dialect
	db  *sql.DB
	cfg source.ConnectionConfig

	version  string
	path     string
	owner    string
	charset  string
	odsMajor int
	odsMinor int
	pageSize int64

	mu         sync.Mutex
	sessions   map[string]*session // for KillQuery
	nextID     int
	identities map[string]model.RowIdentity // browse tiebreak per table
}

var (
	_ source.Source         = (*firebirdSource)(nil)
	_ source.Dialect        = (*firebirdSource)(nil)
	_ source.Queryer        = (*firebirdSource)(nil)
	_ source.Sessioner      = (*firebirdSource)(nil)
	_ source.Countable      = (*firebirdSource)(nil)
	_ source.DistinctLister = (*firebirdSource)(nil)
)

func (s *firebirdSource) Capabilities() capability.Capabilities {
	return capability.Capabilities{
		Paradigm: model.ParadigmRelational,
		Query: capability.Query{
			Supported: true, Language: "firebird", MultiStatement: true,
			Cancel: true, Parameters: true, Transactions: true,
			// A plan is available to a client that asks the API for it while
			// preparing a statement, and this library keeps it to itself, so
			// there is nothing to show (FR-5.13). Said as false rather than
			// offered and failing.
			Explain: false,
			// A query's result carries a column's name and its type and
			// nothing about the table it came from, so a result cannot be
			// known by a key (FR-4.8). Browsing a table and editing it is
			// unaffected: a browse knows its own table.
			EditableResults: false,
		},
		Data: capability.Data{
			ServerSort: true, ServerFilter: true, ColumnStats: true,
			Insert: true, Update: true, Delete: true, TransactionalWrite: true,
			BulkLoad: true, DistinctValues: true,
			// Firebird keeps no row estimate anybody can read: there is no
			// statistic in the catalogue and no cheap approximation. A count
			// is a scan, which is affordable on the databases Firebird is
			// deployed on and is what the grid's own count does.
			ApproximateCount: false, ExactCount: true,
		},
		Schema: capability.Schema{
			// Diff is not claimed: it means this source can hand over a whole
			// database in one pass (source.Snapshotter), and Firebird's
			// catalogue would be as many round trips as there are objects.
			// Comparison still works, walked object by object, as SQLite's is.
			ERDiagram: true, ForeignKeys: true,
		},
		Objects: map[model.ObjectKind]bool{
			model.KindDatabase: true, model.KindFolder: true,
			model.KindTable: true, model.KindView: true, model.KindColumn: true,
			model.KindIndex: true, model.KindRoutine: true, model.KindTrigger: true,
			model.KindSequence: true,
		},
	}
}

func (s *firebirdSource) Info(ctx context.Context) (source.ServerInfo, error) {
	return source.ServerInfo{Product: "Firebird", Version: s.version, Attrs: map[string]string{
		"database":       s.path,
		"owner":          s.owner,
		"character set":  s.charset,
		"on-disk format": fmt.Sprintf("%d.%d", s.odsMajor, s.odsMinor),
		"page size":      strconv.FormatInt(s.pageSize, 10),
	}}, nil
}

func (s *firebirdSource) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// major is the engine's major version, or 0 where it did not say. It decides
// nothing about SQL this driver writes except where a clause arrived in a
// known release.
func (s *firebirdSource) major() int {
	n, err := strconv.Atoi(strings.SplitN(s.version, ".", 2)[0])
	if err != nil {
		return 0
	}
	return n
}

func (s *firebirdSource) Close() error { return s.db.Close() }

// classifyConnectError says what went wrong in terms a person can act on
// (FR-1.4).
//
// Firebird reports everything through a status vector of GDS codes, which the
// library hands over whole. The codes are read rather than the message,
// because the message is localised on some builds and the codes never are.
func classifyConnectError(err error) error {
	var ce *source.ConnectError
	if errors.As(err, &ce) {
		return err
	}
	var fe *fb.FbError
	var dns *net.DNSError
	var ne net.Error
	switch {
	case errors.As(err, &fe) && hasGDS(fe, gdsLogin):
		return &source.ConnectError{Kind: source.ConnectAuth,
			Hint: "The user name or password was not accepted.", Err: err}
	case errors.As(err, &fe) && hasGDS(fe, gdsIOError, gdsFileNotFound):
		return &source.ConnectError{Kind: source.ConnectNoDatabase,
			Hint: "There is no database at that path on the server.", Err: err}
	case errors.As(err, &dns):
		return &source.ConnectError{Kind: source.ConnectUnreachable,
			Hint: "That host name could not be found.", Err: err}
	case errors.Is(err, syscall.ECONNREFUSED):
		return &source.ConnectError{Kind: source.ConnectRefused,
			Hint: "Nothing is listening on that host and port.", Err: err}
	case errors.As(err, &ne) && ne.Timeout():
		return &source.ConnectError{Kind: source.ConnectUnreachable,
			Hint: "The server did not answer in time.", Err: err}
	case strings.Contains(err.Error(), "wire_crypt"):
		// The library's own refusal: encryption was required and the server
		// would not do it. Its advice is written in its own settings, which
		// nobody using this application can act on, so the hint says what
		// there is to do here instead.
		return &source.ConnectError{Kind: source.ConnectTLS,
			Hint: "This server will not encrypt the connection. To connect anyway, set this " +
				"connection's encryption to Disable — which means everything, the password " +
				"included, crosses the network as plain text.", Err: err}
	}
	return &source.ConnectError{Kind: source.ConnectUnknown, Hint: "The connection failed.", Err: err}
}

// The GDS codes this driver acts on, each one observed coming back from a
// server rather than copied out of a header. They are written out here
// because the library does not export them, and named because a number in a
// switch says nothing to the next reader.
const (
	gdsIOError      = 335544344 // isc_io_error: the file could not be opened
	gdsFileNotFound = 335544734 // isc_io_open_err: and this is why
	gdsLogin        = 335544472 // isc_login: the credentials were refused
)

// hasGDS reports whether any of the codes is in the error's status vector.
// Any, not the first: Firebird puts the general failure first and the
// specific reason after it, so a login failure arrives behind a connection
// failure and looking only at the head of the vector finds the wrong one.
func hasGDS(e *fb.FbError, codes ...int) bool {
	for _, c := range codes {
		for _, got := range e.GDSCodes {
			if got == c {
				return true
			}
		}
	}
	return false
}

// statementError carries a server failure as the contract's StatementError.
// A statement stopped by its context reports the cancellation, not whatever
// the server said about being interrupted.
func statementError(err error, ctx context.Context, stmt string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	msg := source.Message{Level: source.MessageError, Text: err.Error()}
	var fe *fb.FbError
	if errors.As(err, &fe) {
		msg.Text = fe.Message
		if fe.SQLCode != 0 {
			msg.Code = strconv.Itoa(int(fe.SQLCode))
		}
		if fe.SQLState != "" {
			msg.Code = fe.SQLState
		}
		msg.Position = positionIn(fe.Message, stmt)
	}
	return &source.StatementError{Err: err, Message: msg}
}
