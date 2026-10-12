// Package pulsar is the Apache Pulsar driver (T5.16), over
// apache/pulsar-client-go.
//
// Pulsar is a log like Kafka's and is read like one: a topic, perhaps cut into
// partitions, of messages in the order they were written, read from a position
// to a bound. So a topic is shown where a Kafka topic is and a message has a
// record's columns, and the same window reads it (ADR-0177).
//
// It is spoken to two ways, and a connection needs both. What a cluster holds —
// its tenants, namespaces and topics, and what is subscribed to each — is asked
// of its admin service, over HTTP. Messages are read and written over the
// broker's own protocol, on another port.
//
// The difference that cannot be drawn away is where a message is. Kafka numbers
// the records of a partition. Pulsar addresses a message by the ledger it was
// written to and its entry there, and a topic moves from ledger to ledger as it
// goes; there is no single number that says how far along a message is. So
// what is shown as a message's offset is that address, as text, and reading
// from "an offset" or "the last few" — both of which need a number to count
// with — is refused with that reason rather than approximated.
package pulsar

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"syscall"
	"time"

	pulsargo "github.com/apache/pulsar-client-go/pulsar"
	"github.com/apache/pulsar-client-go/pulsaradmin"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/capability"
)

const (
	driverID         = "pulsar"
	defaultPort      = 6650
	defaultAdminPort = 8080
)

func init() { source.Register(Driver{}) }

// Driver connects to a Pulsar cluster.
type Driver struct{}

func (Driver) Describe() source.Descriptor {
	return source.Descriptor{
		ID: driverID, Name: "Apache Pulsar", Paradigm: model.ParadigmStream, DefaultPort: defaultPort,
		URLSchemes: []string{"pulsar"},
		Fields: []source.Field{
			{Key: "host", Label: "Host", Kind: source.FieldText, Required: true, Default: "localhost"},
			{Key: "port", Label: "Broker port", Kind: source.FieldNumber, Default: "6650",
				Help: "Where messages are read and written: 6650, or 6651 encrypted."},
			{Key: "admin_port", Label: "Admin port", Kind: source.FieldNumber, Default: "8080",
				Help: "Where the cluster says what it holds: 8080, or 8443 encrypted."},
			{Key: "user", Label: "User", Kind: source.FieldText,
				Help: "For a cluster that takes a name and a password. Leave it empty to give a token as the password."},
			{Key: "password", Label: "Password or token", Kind: source.FieldPassword, Secret: true},
		},
	}
}

// Open asks the cluster what it is, of its admin service, and makes the client
// messages will be read with. A broker is not dialled until something is read:
// which broker serves a topic is the topic's to say.
func (Driver) Open(ctx context.Context, cfg source.ConnectionConfig) (_ source.Source, err error) {
	defer panics.Recover(&err, "opening a Pulsar cluster")

	set, err := settingsOf(cfg)
	if err != nil {
		return nil, err
	}
	admin, err := pulsaradmin.NewClient(&set.admin)
	if err != nil {
		return nil, &source.ConnectError{Kind: source.ConnectConfig,
			Hint: "Those settings cannot become a connection to the admin service.", Err: err}
	}
	clusters, err := admin.Clusters().ListWithContext(ctx)
	if err != nil {
		return nil, classify(err)
	}
	client, err := pulsargo.NewClient(set.client)
	if err != nil {
		return nil, &source.ConnectError{Kind: source.ConnectConfig,
			Hint: "Those settings cannot become a connection to the brokers.", Err: err}
	}
	s := &pulsarSource{cfg: cfg, admin: admin, client: client, cluster: "pulsar"}
	if len(clusters) > 0 {
		s.cluster = clusters[0]
	}
	return s, nil
}

// settings are a connection as the two clients take it.
type settings struct {
	client pulsargo.ClientOptions
	admin  pulsaradmin.Config
}

// dialTimeout bounds one attempt to reach a broker, and operationTimeout how
// long making a reader or a producer is retried: the client's own patience is
// thirty seconds of nothing for somebody who typed the wrong port.
const (
	dialTimeout      = 10 * time.Second
	operationTimeout = 15 * time.Second
)

// settingsOf is what a connection becomes, without speaking to anything.
func settingsOf(cfg source.ConnectionConfig) (settings, error) {
	bad := func(hint string, err error) (settings, error) {
		return settings{}, &source.ConnectError{Kind: source.ConnectConfig, Hint: hint, Err: err}
	}
	host := strings.TrimSpace(cfg.Host)
	if host == "" {
		host = "localhost"
	}
	port := cfg.Port
	if port == 0 {
		port = defaultPort
	}
	adminPort := defaultAdminPort
	if v := strings.TrimSpace(cfg.Params["admin_port"]); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 || n > 65535 {
			return bad(fmt.Sprintf("%q is not a port for the admin service.", v),
				fmt.Errorf("pulsar: admin port %q", v))
		}
		adminPort = n
	}

	var out settings
	out.client.ConnectionTimeout = dialTimeout
	out.client.OperationTimeout = operationTimeout

	// Encrypted and verified unless somebody chose otherwise (NFR-S3).
	broker, web := "pulsar+ssl", "https"
	switch cfg.TLS.Mode {
	case "disable":
		broker, web = "pulsar", "http"
	case "require":
		out.client.TLSAllowInsecureConnection = true
		out.admin.TLSAllowInsecureConnection = true
	case "verify-ca":
		// The chain, and not the name on it.
	case "", "verify-full":
		out.client.TLSValidateHostname = true
		out.admin.TLSEnableHostnameVerification = true
	default:
		return bad(fmt.Sprintf("%q is not a way to secure a connection.", cfg.TLS.Mode),
			fmt.Errorf("pulsar: unknown TLS mode %q", cfg.TLS.Mode))
	}
	if cfg.TLS.ServerName != "" {
		// Two clients would each have to be told, and the admin one cannot
		// be. Checking one against a name and the other against the host
		// would be verification that is not the one asked for.
		return bad("This driver checks a certificate against the host it connects to, and cannot be given another name.",
			errors.New("pulsar: a server name to verify against is not supported"))
	}
	if web == "https" {
		out.client.TLSMinVersion = tls.VersionTLS12
		out.client.TLSTrustCertsFilePath = cfg.TLS.CAFile
		out.admin.TLSTrustCertsFilePath = cfg.TLS.CAFile
		out.client.TLSCertificateFile, out.client.TLSKeyFilePath = cfg.TLS.CertFile, cfg.TLS.KeyFile
		out.admin.TLSCertFile, out.admin.TLSKeyFile = cfg.TLS.CertFile, cfg.TLS.KeyFile
	}
	out.client.URL = broker + "://" + net.JoinHostPort(host, strconv.Itoa(port))
	out.admin.WebServiceURL = web + "://" + net.JoinHostPort(host, strconv.Itoa(adminPort))

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
		auth, err := pulsargo.NewAuthenticationBasic(user, secret)
		if err != nil {
			return bad("That user name and password cannot be sent.", err)
		}
		out.client.Authentication = auth
		out.admin.AuthPlugin = "org.apache.pulsar.client.impl.auth.AuthenticationBasic"
		out.admin.AuthParams = `{"userId":` + strconv.Quote(user) + `,"password":` + strconv.Quote(secret) + `}`
	case secret != "":
		// A secret with nobody's name on it is a token, which is how most
		// Pulsar clusters are told who is calling.
		out.client.Authentication = pulsargo.NewAuthenticationToken(secret)
		out.admin.Token = secret
	}
	return out, nil
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
	text := err.Error()
	switch {
	case strings.Contains(text, "code: 401") || strings.Contains(text, "code: 403"):
		return &source.ConnectError{Kind: source.ConnectAuth,
			Hint: "The cluster refused that user, password or token.", Err: err}
	case errors.As(err, &verify) || errors.As(err, &record) || strings.Contains(text, "x509:") ||
		strings.Contains(text, "server gave HTTP response to HTTPS client"):
		return &source.ConnectError{Kind: source.ConnectTLS,
			Hint: "The encrypted connection could not be agreed.", Err: err}
	case errors.As(err, &dns):
		return &source.ConnectError{Kind: source.ConnectUnreachable,
			Hint: "That host name could not be found.", Err: err}
	case errors.Is(err, syscall.ECONNREFUSED):
		return &source.ConnectError{Kind: source.ConnectUnreachable,
			Hint: "Nothing is listening for the admin service on that host and port.", Err: err}
	case errors.As(err, &ne) && ne.Timeout():
		return &source.ConnectError{Kind: source.ConnectUnreachable,
			Hint: "The cluster did not answer in time.", Err: err}
	}
	return &source.ConnectError{Kind: source.ConnectUnknown, Hint: "The connection failed.", Err: err}
}

// pulsarSource is one connection: an admin client for what the cluster holds,
// and a broker client that readers and producers are made from.
type pulsarSource struct {
	cfg     source.ConnectionConfig
	admin   pulsaradmin.Client
	client  pulsargo.Client
	cluster string
}

var (
	_ source.Source         = (*pulsarSource)(nil)
	_ source.Followable     = (*pulsarSource)(nil)
	_ source.StreamProducer = (*pulsarSource)(nil)
)

func (s *pulsarSource) Capabilities() capability.Capabilities {
	return capability.Capabilities{
		Paradigm: model.ParadigmStream,
		// What is read, by time or from either end, and what is written one
		// message at a time. Not claimed: subscriptions as things of their
		// own, administering topics, permissions, and a rate — each a claim
		// of its own (ADR-0107), and none written. What is subscribed to a
		// topic is said where the topic is described.
		Stream: capability.Stream{Consume: true, SeekTimestamp: true, Follow: true, Produce: true},
		Objects: map[model.ObjectKind]bool{
			model.KindCluster: true, model.KindSchema: true, model.KindFolder: true, model.KindTopic: true,
		},
	}
}

// Info is what the cluster says it is.
func (s *pulsarSource) Info(ctx context.Context) (_ source.ServerInfo, err error) {
	defer panics.Recover(&err, "reading the cluster's account of itself")
	start := time.Now()
	brokers, err := s.admin.Brokers().GetListActiveBrokersWithContext(ctx)
	if err != nil {
		return source.ServerInfo{}, err
	}
	info := source.ServerInfo{Product: "Pulsar", Latency: time.Since(start),
		Attrs: map[string]string{"cluster": s.cluster, "brokers": strconv.Itoa(len(brokers))}}
	// No version: the admin service answers one, and the client this is built
	// on has no way to ask it. Nothing is better than a guess.
	return info, nil
}

// Ping asks the admin service the cheapest thing it answers.
func (s *pulsarSource) Ping(ctx context.Context) (err error) {
	defer panics.Recover(&err, "pinging the cluster")
	_, err = s.admin.Clusters().ListWithContext(ctx)
	return err
}

// Close ends the broker client and everything made from it. More than once is
// the same as once.
func (s *pulsarSource) Close() (err error) {
	defer panics.Recover(&err, "closing the connection")
	if s.client != nil {
		s.client.Close()
		s.client = nil
	}
	return nil
}
