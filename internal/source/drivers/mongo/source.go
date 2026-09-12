// Package mongo is the MongoDB driver (T2.30), over the official
// go.mongodb.org/mongo-driver.
//
// MongoDB is the first source of the document paradigm, and the first test of
// REQ-DB-4: a database with no rows, no columns and no server-held schema has
// to reach the same explorer, the same grid and the same tabs as PostgreSQL
// does, through the same contract and with no engine knowledge in the UI.
package mongo

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	mongodriver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/capability"
	"github.com/ikigai-db/ikigai-db/internal/source/tlsconf"
)

const (
	driverID    = "mongodb"
	defaultPort = 27017
)

func init() { source.Register(Driver{}) }

// Driver connects to MongoDB servers, replica sets and clusters.
type Driver struct{}

func (Driver) Describe() source.Descriptor {
	return source.Descriptor{
		ID: driverID, Name: "MongoDB", Paradigm: model.ParadigmDocument, DefaultPort: defaultPort,
		URLSchemes: []string{"mongodb", "mongodb+srv"},
		Fields: []source.Field{
			{Key: "host", Label: "Host", Kind: source.FieldText, Required: true, Default: "localhost"},
			{Key: "port", Label: "Port", Kind: source.FieldNumber, Default: strconv.Itoa(defaultPort),
				Help: "Ignored for a DNS seed list, which carries its own."},
			{Key: "database", Label: "Database", Kind: source.FieldText,
				Help: "Optional: the database to start in."},
			{Key: "user", Label: "User", Kind: source.FieldText},
			{Key: "password", Label: "Password", Kind: source.FieldPassword, Secret: true},
			{Key: "srv", Label: "DNS seed list (mongodb+srv)", Kind: source.FieldBool,
				Help: "The host names one record naming the servers, as Atlas gives out."},
			{Key: "authSource", Label: "Authentication database", Kind: source.FieldText,
				Help: "The database the user is defined in. admin unless the server says otherwise."},
			{Key: "replicaSet", Label: "Replica set", Kind: source.FieldText,
				Help: "Optional: the set's name, which a connection checks it has reached."},
		},
	}
}

// Open connects and proves the server answers, and nothing more (FR-1.4: a
// test connection must be quick and say what is wrong).
func (Driver) Open(ctx context.Context, cfg source.ConnectionConfig) (source.Source, error) {
	opt, err := clientOptions(cfg)
	if err != nil {
		return nil, err
	}
	client, err := mongodriver.Connect(opt)
	if err != nil {
		return nil, classifyConnectError(err)
	}
	// Connect does not itself reach the server: a bad host or password fails
	// here, which is where "test connection" wants it.
	if err := client.Ping(ctx, readpref.Primary()); err != nil {
		_ = client.Disconnect(context.WithoutCancel(ctx))
		return nil, classifyConnectError(err)
	}
	return &mongoSource{client: client, cfg: cfg}, nil
}

// clientOptions turns a saved connection into what the driver takes.
//
// The credentials are set apart from the URI rather than written into it. A
// driver error quotes the URI it failed with, and a password in one would
// reach a log or a dialog through an error nobody redacted (NFR-S2).
func clientOptions(cfg source.ConnectionConfig) (*options.ClientOptions, error) {
	uri, err := connectionURI(cfg)
	if err != nil {
		return nil, err
	}
	host := hostOf(cfg)
	tlsCfg, err := tlsconf.Config(cfg.TLS, host)
	if err != nil {
		return nil, &source.ConnectError{Kind: source.ConnectConfig, Hint: "The TLS settings are not usable.", Err: err}
	}
	opt := options.Client().ApplyURI(uri).
		SetAppName("Ikigai DB").
		SetConnectTimeout(10 * time.Second).
		SetServerSelectionTimeout(10 * time.Second)
	if tlsCfg != nil {
		opt.SetTLSConfig(tlsCfg)
	}
	if user := strings.TrimSpace(cfg.User); user != "" {
		cred := options.Credential{Username: user, AuthSource: strings.TrimSpace(cfg.Params["authSource"])}
		if cfg.Secret != nil {
			pw, err := cfg.Secret("password")
			if err != nil {
				return nil, &source.ConnectError{Kind: source.ConnectConfig,
					Hint: "The password could not be read from the keychain.", Err: err}
			}
			cred.Password, cred.PasswordSet = pw, pw != ""
		}
		if cred.AuthSource == "" && cfg.Database != "" {
			// Mongo's own default is the database in the URI, then admin.
			cred.AuthSource = cfg.Database
		}
		opt.SetAuth(cred)
	}
	return opt, nil
}

// connectionURI is the address the driver resolves and the options it takes,
// with no credentials in it. A seed list is resolved by the driver while it
// reads this, which is why it is built apart and tested as text.
func connectionURI(cfg source.ConnectionConfig) (string, error) {
	host := hostOf(cfg)
	u := url.URL{Scheme: "mongodb", Host: host}
	if isTrue(cfg.Params["srv"]) {
		// One DNS record names the servers and their ports, so none is
		// written here; the driver reads the record.
		u.Scheme = "mongodb+srv"
	} else {
		port := cfg.Port
		if port == 0 {
			port = defaultPort
		}
		if port < 1 || port > 65535 {
			return "", &source.ConnectError{Kind: source.ConnectConfig,
				Hint: "The port must be between 1 and 65535."}
		}
		u.Host = net.JoinHostPort(host, strconv.Itoa(port))
	}
	q := url.Values{}
	if rs := strings.TrimSpace(cfg.Params["replicaSet"]); rs != "" {
		q.Set("replicaSet", rs)
	}
	if !cfg.TLS.Verifies() && cfg.TLS.Mode != "require" {
		// A seed list turns encryption on by itself, so refusing it has to be
		// said outright rather than left to the default (ADR-0008).
		q.Set("tls", "false")
	}
	u.RawQuery = q.Encode()
	// The driver's own parser insists on the slash before a query.
	u.Path = "/"
	return u.String(), nil
}

func hostOf(cfg source.ConnectionConfig) string {
	if host := strings.TrimSpace(cfg.Host); host != "" {
		return host
	}
	return "localhost"
}

func isTrue(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes", "on":
		return true
	}
	return false
}

// classifyConnectError says what went wrong in terms a person can act on
// (FR-1.4). The driver reports most failures as a server-selection error
// wrapping what it actually met, so the wrapped text is what is read.
func classifyConnectError(err error) error {
	var ce *source.ConnectError
	if errors.As(err, &ce) {
		return err
	}
	var dns *net.DNSError
	var ne net.Error
	text := strings.ToLower(err.Error())
	switch {
	case strings.Contains(text, "auth error") || strings.Contains(text, "authentication failed") ||
		strings.Contains(text, "not authorized"):
		return &source.ConnectError{Kind: source.ConnectAuth,
			Hint: "The user name or password was not accepted.", Err: err}
	case strings.Contains(text, "x509") || strings.Contains(text, "certificate") ||
		strings.Contains(text, "tls handshake"):
		return &source.ConnectError{Kind: source.ConnectTLS,
			Hint: "The server's certificate could not be verified.", Err: err}
	case errors.As(err, &dns) || strings.Contains(text, "no such host") ||
		strings.Contains(text, "lookup") && strings.Contains(text, "srv"):
		return &source.ConnectError{Kind: source.ConnectUnreachable,
			Hint: "That host name could not be found.", Err: err}
	case strings.Contains(text, "connection refused"):
		return &source.ConnectError{Kind: source.ConnectRefused,
			Hint: "Nothing is listening on that host and port.", Err: err}
	case errors.As(err, &ne) && ne.Timeout() || strings.Contains(text, "timeout") ||
		strings.Contains(text, "context deadline exceeded"):
		return &source.ConnectError{Kind: source.ConnectUnreachable,
			Hint: "The server did not answer in time.", Err: err}
	case strings.Contains(text, "error parsing uri") || strings.Contains(text, "invalid"):
		return &source.ConnectError{Kind: source.ConnectConfig,
			Hint: "The connection settings are not usable.", Err: err}
	}
	return &source.ConnectError{Kind: source.ConnectUnknown, Hint: "The connection failed.", Err: err}
}

// mongoSource is one live connection.
type mongoSource struct {
	client *mongodriver.Client
	cfg    source.ConnectionConfig

	mu     sync.Mutex
	closed bool
}

var (
	_ source.Source        = (*mongoSource)(nil)
	_ source.ShapeInferrer = (*mongoSource)(nil)
	_ source.Countable     = (*mongoSource)(nil)
	_ source.Writer        = (*mongoSource)(nil)
	_ source.Aggregator    = (*mongoSource)(nil)
)

func (s *mongoSource) Capabilities() capability.Capabilities {
	return capability.Capabilities{
		Paradigm: model.ParadigmDocument,
		// A collection is not created by a statement here; it appears when a
		// document is written into it, so CreateDatabase stays false until
		// the writes that would do it exist.
		Structure: capability.Structure{MultipleDatabases: true, InferredShape: true},
		// The server does the filtering, the ordering and the counting. A
		// standalone server has no transaction, so a write that fails leaves
		// the writes before it, and the UI says so before committing.
		Data: capability.Data{ServerSort: true, ServerFilter: true, ExactCount: true, ApproximateCount: true,
			Insert: true, Update: true, Delete: true, Pipeline: true},
		// A field is an object too, once inference has found one (T2.32).
		Objects: map[model.ObjectKind]bool{
			model.KindDatabase: true, model.KindFolder: true,
			model.KindCollection: true, model.KindIndex: true,
		},
	}
}

// Info reads the server's own name and version, and times the round trip.
func (s *mongoSource) Info(ctx context.Context) (_ source.ServerInfo, err error) {
	defer panics.Recover(&err, "reading the server's version")
	start := time.Now()
	var res bson.M
	if err := s.client.Database("admin").RunCommand(ctx, bson.D{{Key: "buildInfo", Value: 1}}).Decode(&res); err != nil {
		return source.ServerInfo{}, err
	}
	info := source.ServerInfo{Product: "MongoDB", Latency: time.Since(start)}
	if v, ok := res["version"].(string); ok {
		info.Version = v
	}
	// Atlas, DocumentDB and Cosmos all answer a Mongo driver; where the
	// server says what it is, the person is told rather than guessing from a
	// version number that is not the product's.
	if v, ok := res["modules"].(bson.A); ok && len(v) > 0 {
		if m, ok := v[0].(string); ok && m != "" {
			info.Attrs = map[string]string{"module": m}
		}
	}
	return info, nil
}

func (s *mongoSource) Ping(ctx context.Context) (err error) {
	defer panics.Recover(&err, "pinging the server")
	return s.client.Ping(ctx, readpref.Primary())
}

func (s *mongoSource) Close() (err error) {
	defer panics.Recover(&err, "closing the connection")
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.client.Disconnect(ctx)
}

func (s *mongoSource) Root(ctx context.Context) ([]model.Node, error) {
	return s.databases(ctx)
}

// databases lists what the connection may see.
func (s *mongoSource) databases(ctx context.Context) (_ []model.Node, err error) {
	defer panics.Recover(&err, "listing the databases")
	names, err := s.client.ListDatabaseNames(ctx, bson.D{})
	return databaseNodes(names, err, s.cfg.Database)
}

// databaseNodes turns a listing into nodes.
//
// A server that refuses the listing still shows the database the connection
// names: an Atlas user given one database and no more may read it and not
// the list it is in, and a tree that says nothing there would be wrong.
func databaseNodes(names []string, err error, current string) ([]model.Node, error) {
	if err != nil {
		if db := strings.TrimSpace(current); db != "" {
			return []model.Node{databaseNode(db, true)}, nil
		}
		return nil, err
	}
	out := make([]model.Node, 0, len(names))
	for _, n := range names {
		out = append(out, databaseNode(n, n == current))
	}
	return out, nil
}

func databaseNode(name string, current bool) model.Node {
	n := model.Node{Ref: model.NewRef(model.KindDatabase, name), Label: name, HasChildren: true}
	if current {
		n.Attrs = map[string]string{"current": "true"}
	}
	return n
}
