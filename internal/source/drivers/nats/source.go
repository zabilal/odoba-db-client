// Package nats is the NATS JetStream driver (T5.16), over nats-io/nats.go.
//
// NATS itself keeps nothing: a message is handed to whoever is listening and
// is gone. JetStream is the part that keeps them, in streams, and a stream is
// a log — one ordered sequence of messages, read from a position, with
// consumers that each remember how far they have got. That is the shape this
// application already draws for Kafka, so a stream is shown where a topic is
// and a consumer where a consumer group is, and the same window reads both.
//
// Where the two differ the difference is kept rather than hidden. A stream is
// one log and not several, so every message is in partition nought. A message
// has no key; what tells messages apart and decides where they go is the
// subject they were published on, so that is what is shown where a key would
// be (ADR-0176).
package nats

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/capability"
)

const (
	driverID    = "nats"
	defaultPort = 4222
)

func init() { source.Register(Driver{}) }

// Driver connects to a NATS server with JetStream.
type Driver struct{}

func (Driver) Describe() source.Descriptor {
	return source.Descriptor{
		ID: driverID, Name: "NATS JetStream", Paradigm: model.ParadigmStream, DefaultPort: defaultPort,
		URLSchemes: []string{"nats"},
		Fields: []source.Field{
			{Key: "host", Label: "Host", Kind: source.FieldText, Required: true, Default: "localhost"},
			{Key: "port", Label: "Port", Kind: source.FieldNumber, Default: "4222"},
			{Key: "user", Label: "User", Kind: source.FieldText,
				Help: "A server that asks nobody who they are needs none. Leave it empty to give a token as the password."},
			{Key: "password", Label: "Password or token", Kind: source.FieldPassword, Secret: true},
		},
	}
}

// Open connects, and asks the one thing that tells a server this can read
// from one it cannot: whether it keeps anything.
func (Driver) Open(ctx context.Context, cfg source.ConnectionConfig) (_ source.Source, err error) {
	defer panics.Recover(&err, "opening a NATS server")

	addr, opts, err := options(cfg)
	if err != nil {
		return nil, err
	}
	nc, err := natsgo.Connect(addr, opts...)
	if err != nil {
		return nil, classify(err)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, classify(err)
	}
	if _, err := js.AccountInfo(ctx); err != nil {
		nc.Close()
		if errors.Is(err, jetstream.ErrJetStreamNotEnabled) || errors.Is(err, jetstream.ErrJetStreamNotEnabledForAccount) {
			// A NATS server with nothing kept is a server there is nothing
			// to look at in: messages pass through it and are gone.
			return nil, &source.ConnectError{Kind: source.ConnectConfig,
				Hint: "That server answers, but JetStream is not turned on for this account, so it keeps no messages to read.",
				Err:  err}
		}
		return nil, classify(err)
	}
	return &natsSource{cfg: cfg, nc: nc, js: js}, nil
}

// dialTimeout bounds one attempt to connect: a wrong address says so in
// seconds, not when somebody gives up.
const dialTimeout = 10 * time.Second

// options are the client's settings for a connection, without speaking to
// anything.
func options(cfg source.ConnectionConfig) (string, []natsgo.Option, error) {
	bad := func(hint string, err error) (string, []natsgo.Option, error) {
		return "", nil, &source.ConnectError{Kind: source.ConnectConfig, Hint: hint, Err: err}
	}
	host := strings.TrimSpace(cfg.Host)
	if host == "" {
		host = "localhost"
	}
	port := cfg.Port
	if port == 0 {
		port = defaultPort
	}
	opts := []natsgo.Option{
		// The application's own name, so the connection is recognisable in
		// the server's list of them.
		natsgo.Name("ikigai-db"),
		natsgo.Timeout(dialTimeout),
		// One attempt, and no waiting for a server to come back before the
		// first answer: a test connection has to fail when it fails.
		natsgo.RetryOnFailedConnect(false),
	}

	// Encrypted and verified unless somebody chose otherwise (NFR-S3).
	scheme := "tls"
	switch cfg.TLS.Mode {
	case "disable":
		scheme = "nats"
	case "require":
		opts = append(opts, natsgo.Secure(&tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true}))
	case "", "verify-ca", "verify-full":
		name := host
		if cfg.TLS.ServerName != "" {
			name = cfg.TLS.ServerName
		}
		conf := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: name}
		if cfg.TLS.CAFile != "" {
			pem, err := os.ReadFile(cfg.TLS.CAFile)
			if err != nil {
				return bad("That certificate authority file could not be read.", err)
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(pem) {
				return bad("That file holds no certificate.", fmt.Errorf("nats: %s holds no certificate", cfg.TLS.CAFile))
			}
			conf.RootCAs = pool
		}
		if cfg.TLS.CertFile != "" || cfg.TLS.KeyFile != "" {
			pair, err := tls.LoadX509KeyPair(cfg.TLS.CertFile, cfg.TLS.KeyFile)
			if err != nil {
				return bad("That client certificate and key could not be read together.", err)
			}
			conf.Certificates = []tls.Certificate{pair}
		}
		opts = append(opts, natsgo.Secure(conf))
	default:
		return bad(fmt.Sprintf("%q is not a way to secure a connection.", cfg.TLS.Mode),
			fmt.Errorf("nats: unknown TLS mode %q", cfg.TLS.Mode))
	}

	secret := ""
	if cfg.Secret != nil {
		v, err := cfg.Secret("password")
		if err != nil {
			return bad("The password for this connection could not be read.", err)
		}
		secret = v
	}
	switch user := strings.TrimSpace(cfg.User); {
	case user != "":
		opts = append(opts, natsgo.UserInfo(user, secret))
	case secret != "":
		// A secret with nobody's name on it is a token, which is NATS's other
		// way of saying who is calling.
		opts = append(opts, natsgo.Token(secret))
	}
	return scheme + "://" + net.JoinHostPort(host, strconv.Itoa(port)), opts, nil
}

// classify says what went wrong in terms a person can act on (FR-1.4).
func classify(err error) error {
	var ce *source.ConnectError
	if errors.As(err, &ce) {
		return err
	}
	var (
		dns    *net.DNSError
		ne     net.Error
		verify *tls.CertificateVerificationError
		record tls.RecordHeaderError
	)
	switch {
	case errors.Is(err, natsgo.ErrAuthorization) || errors.Is(err, natsgo.ErrAuthExpired) || errors.Is(err, natsgo.ErrAuthRevoked):
		return &source.ConnectError{Kind: source.ConnectAuth,
			Hint: "The server refused that user, password or token.", Err: err}
	case errors.Is(err, natsgo.ErrSecureConnWanted):
		return &source.ConnectError{Kind: source.ConnectTLS,
			Hint: "This connection asks for encryption and the server does not offer it.", Err: err}
	case errors.Is(err, natsgo.ErrSecureConnRequired):
		return &source.ConnectError{Kind: source.ConnectTLS,
			Hint: "The server requires encryption and this connection has it turned off.", Err: err}
	case errors.As(err, &verify) || errors.As(err, &record) || strings.Contains(err.Error(), "x509:"):
		return &source.ConnectError{Kind: source.ConnectTLS,
			Hint: "The encrypted connection could not be agreed.", Err: err}
	case errors.As(err, &dns):
		return &source.ConnectError{Kind: source.ConnectUnreachable,
			Hint: "That host name could not be found.", Err: err}
	case errors.Is(err, syscall.ECONNREFUSED), errors.Is(err, natsgo.ErrNoServers):
		return &source.ConnectError{Kind: source.ConnectUnreachable,
			Hint: "Nothing is listening on that host and port.", Err: err}
	case errors.As(err, &ne) && ne.Timeout(), errors.Is(err, natsgo.ErrTimeout):
		return &source.ConnectError{Kind: source.ConnectUnreachable,
			Hint: "The server did not answer in time.", Err: err}
	}
	return &source.ConnectError{Kind: source.ConnectUnknown, Hint: "The connection failed.", Err: err}
}

// natsSource is one connection, which everything shares: a NATS connection
// carries any number of conversations at once.
type natsSource struct {
	cfg source.ConnectionConfig
	nc  *natsgo.Conn
	js  jetstream.JetStream
}

var (
	_ source.Source         = (*natsSource)(nil)
	_ source.Followable     = (*natsSource)(nil)
	_ source.GroupInspector = (*natsSource)(nil)
	_ source.StreamProducer = (*natsSource)(nil)
	_ source.TopicMeter     = (*natsSource)(nil)
)

func (s *natsSource) Capabilities() capability.Capabilities {
	return capability.Capabilities{
		Paradigm: model.ParadigmStream,
		// What is read, and what is written one message at a time. Not
		// claimed: making and removing streams, moving a consumer's place,
		// and permissions, none of which is written (ADR-0107: each is a
		// claim of its own).
		Stream: capability.Stream{Consume: true, SeekTimestamp: true, Follow: true,
			ConsumerGroups: true, Produce: true, Throughput: true},
		Objects: map[model.ObjectKind]bool{
			model.KindCluster: true, model.KindFolder: true,
			model.KindTopic: true, model.KindConsumerGroup: true,
		},
	}
}

// Info is what the server says it is, and how much it is keeping.
func (s *natsSource) Info(ctx context.Context) (_ source.ServerInfo, err error) {
	defer panics.Recover(&err, "reading the server's account of itself")
	start := time.Now()
	acct, err := s.js.AccountInfo(ctx)
	if err != nil {
		return source.ServerInfo{}, err
	}
	info := source.ServerInfo{Product: "NATS", Version: s.nc.ConnectedServerVersion(), Latency: time.Since(start),
		Attrs: map[string]string{
			"streams":   strconv.Itoa(acct.Streams),
			"consumers": strconv.Itoa(acct.Consumers),
		}}
	if name := s.nc.ConnectedServerName(); name != "" {
		info.Attrs["server"] = name
	}
	if name := s.nc.ConnectedClusterName(); name != "" {
		info.Attrs["cluster"] = name
	}
	return info, nil
}

// Ping goes to the server and back.
func (s *natsSource) Ping(ctx context.Context) (err error) {
	defer panics.Recover(&err, "pinging the server")
	// The client will not wait without being told for how long, and somebody
	// who gave no deadline did not mean for ever.
	if _, bounded := ctx.Deadline(); !bounded {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, dialTimeout)
		defer cancel()
	}
	return s.nc.FlushWithContext(ctx)
}

// Close ends the connection. More than once is the same as once: the client's
// own Close is.
func (s *natsSource) Close() (err error) {
	defer panics.Recover(&err, "closing the connection")
	s.nc.Close()
	return nil
}
