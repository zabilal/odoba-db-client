// Package kafka is the Kafka driver (T2.54), over github.com/twmb/franz-go.
//
// Kafka holds no rows. A topic is an append-only log cut into partitions:
// nothing declares what a record looks like, a record is addressed by the
// offset it was written at rather than by a key of its own, and reading is
// consuming rather than querying — the same record read again by somebody
// else, and gone when the log ages out rather than when it is deleted. That
// is the stream paradigm, and everything this application shows of Kafka
// follows from it.
//
// This is the connection alone (FR-1.2, FR-1.4): the bootstrap servers a
// person fills in, what is dialled, and what is said when it fails. The
// cluster's own account of itself is T2.59, topics and partitions in the tree
// T2.62, and messages T2.63. Nothing here is claimed before it is written: a
// claim is a promise the conformance suite holds a driver to (REQ-DRV-1).
package kafka

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kversion"
	"github.com/twmb/franz-go/pkg/sasl"
	"github.com/twmb/franz-go/pkg/sasl/aws"
	"github.com/twmb/franz-go/pkg/sasl/oauth"
	"github.com/twmb/franz-go/pkg/sasl/plain"
	"github.com/twmb/franz-go/pkg/sasl/scram"
	"github.com/twmb/franz-go/pkg/sr"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/capability"
	"github.com/ikigai-db/ikigai-db/internal/source/tlsconf"
)

const (
	driverID    = "kafka"
	defaultPort = 9092

	// timeout bounds dialling and the requests this driver sends. FR-1.4
	// wants a test connection that fails quickly and says why.
	timeout = 10 * time.Second

	// clientID is what this application calls itself to a broker. Kafka
	// records it against every request, so it is what somebody watching their
	// own cluster sees asking questions.
	clientID = "ikigai-db"
)

func init() { source.Register(Driver{}) }

// Driver connects to Kafka clusters.
type Driver struct{}

func (Driver) Describe() source.Descriptor {
	return source.Descriptor{
		ID: driverID, Name: "Kafka", Paradigm: model.ParadigmStream, DefaultPort: defaultPort,
		URLSchemes: []string{"kafka"},
		Fields: []source.Field{
			{Key: "host", Label: "Bootstrap server", Kind: source.FieldText, Required: true, Default: "localhost",
				Help: "Any broker of the cluster. The rest are found from it."},
			{Key: "port", Label: "Port", Kind: source.FieldNumber, Default: strconv.Itoa(defaultPort)},
			{Key: "servers", Label: "Further bootstrap servers", Kind: source.FieldText,
				Help: "Optional: more brokers to try, separated by commas, in case the first is down."},
			{Key: "mechanism", Label: "Authentication", Kind: source.FieldSelect, Default: mechNone,
				Options: mechanisms,
				Help:    "How the broker is told who you are. PLAIN sends the password as text, so it wants TLS unless the broker is your own."},
			{Key: "user", Label: "User", Kind: source.FieldText,
				Help: "The name the mechanism authenticates. For AWS_MSK_IAM it is the access key id."},
			{Key: "password", Label: "Password", Kind: source.FieldPassword, Secret: true,
				Help: "For AWS_MSK_IAM: the secret access key, which signs rather than being sent."},
			{Key: "registry", Label: "Schema registry", Kind: source.FieldText,
				Help: "Optional: the URL of a Confluent schema registry, such as http://localhost:8081, which says what the records mean."},
			{Key: "registryuser", Label: "Registry user", Kind: source.FieldText,
				Help: "Optional: for a registry behind basic authentication."},
			{Key: "registrypassword", Label: "Registry password", Kind: source.FieldPassword, Secret: true,
				Help: "Kept in the keychain, as every password here is."},
			{Key: "token", Label: "Token", Kind: source.FieldPassword, Secret: true,
				Help: "For OAUTHBEARER: the bearer token itself, whose issuer decides how long it lasts. For AWS_MSK_IAM: the session token, where the credentials are temporary ones."},
		},
	}
}

// Open dials the cluster, or says why it could not.
func (Driver) Open(ctx context.Context, cfg source.ConnectionConfig) (_ source.Source, err error) {
	defer panics.Recover(&err, "opening the connection")

	opts, err := clientOf(cfg)
	if err != nil {
		return nil, err
	}
	client, err := kgo.NewClient(opts...)
	if err != nil {
		// Nothing has been dialled here: NewClient only reads the options.
		return nil, &source.ConnectError{Kind: source.ConnectConfig,
			Hint: "These settings could not become a connection.", Err: err}
	}
	// franz-go connects to a broker when a request needs one, so a client
	// that was made says nothing about a cluster that answers. Ping is what
	// makes this a test connection rather than a hope: it asks a broker for
	// its own metadata — the cheapest question there is — and tries each seed
	// in turn until one answers.
	if err := client.Ping(ctx); err != nil {
		client.Close()
		return nil, classifyConnectError(err)
	}
	// A registry that was named and cannot be reached is a fault now rather
	// than a surprise later; one that was not named is no fault at all.
	registry, err := registryOf(cfg)
	if err != nil {
		client.Close()
		return nil, err
	}
	return &kafkaSource{client: client, admin: kadm.NewClient(client), registry: registry, cfg: cfg}, nil
}

// clientOf is the settings as franz-go takes them.
func clientOf(cfg source.ConnectionConfig) ([]kgo.Opt, error) {
	seeds, err := bootstrapServers(cfg)
	if err != nil {
		return nil, err
	}
	opts := []kgo.Opt{
		kgo.SeedBrokers(seeds...),
		kgo.ClientID(clientID),
		kgo.DialTimeout(timeout),
		kgo.RetryTimeout(timeout),
	}

	mech, err := mechanismOf(cfg)
	if err != nil {
		return nil, err
	}
	if mech != nil {
		// Only when there is one: franz-go authenticates whenever it has been
		// given a mechanism, and a nil one would be used as though it were a
		// way to prove something.
		opts = append(opts, kgo.SASL(mech))
	}

	tlsCfg, err := tlsconf.Config(cfg.TLS, hostOf(cfg))
	if err != nil {
		return nil, &source.ConnectError{Kind: source.ConnectConfig,
			Hint: "The TLS settings are not usable.", Err: err}
	}
	// Given whatever it is, including nothing: tlsconf answers nil for a
	// connection that is not encrypted, and franz-go dials plaintext for a
	// nil config. A guard here would only say that a second time.
	opts = append(opts, kgo.DialTLSConfig(tlsCfg))
	return opts, nil
}

// mechanisms are the ways a person may prove who they are, as a broker names
// them, with the commonest first: most brokers somebody runs for themselves
// ask for nothing at all.
//
// GSSAPI is not here, and not for want of trying: franz-go ships no Kerberos
// mechanism at all, so speaking it means a Kerberos library of this project's
// own and a KDC to prove it against. A mechanism appears in this list when the
// driver can actually speak it.
var mechanisms = []string{mechNone, "PLAIN", "SCRAM-SHA-256", "SCRAM-SHA-512", "OAUTHBEARER", "AWS_MSK_IAM"}

// mechNone is what a connection to a broker that asks nothing uses.
const mechNone = "None"

// mechanismOf is how this connection proves who it is, or nil where it does
// not have to (FR-1.11).
func mechanismOf(cfg source.ConnectionConfig) (sasl.Mechanism, error) {
	name := strings.ToUpper(strings.TrimSpace(cfg.Params["mechanism"]))
	user := strings.TrimSpace(cfg.User)
	password, err := secret(cfg, "password")
	if err != nil {
		return nil, err
	}

	token, err := secret(cfg, "token")
	if err != nil {
		return nil, err
	}

	switch name {
	case "", strings.ToUpper(mechNone):
		if user != "" || password != "" || token != "" {
			// Credentials that would be sent nowhere. Somebody who typed a
			// password is entitled to think it was used, so this says that it
			// would not be rather than connecting as nobody.
			return nil, &source.ConnectError{Kind: source.ConnectConfig,
				Hint: "A user name, password or token was given, but no authentication was chosen."}
		}
		return nil, nil

	case "OAUTHBEARER":
		// A token authenticates itself: whoever issued it said who this is
		// and for how long, and there is no password to go with it. A user
		// name, where one is given, is the identity to act as.
		if token == "" {
			return nil, &source.ConnectError{Kind: source.ConnectConfig,
				Hint: "OAUTHBEARER carries a token, and none was given."}
		}
		if password != "" {
			return nil, &source.ConnectError{Kind: source.ConnectConfig,
				Hint: "OAUTHBEARER carries a token rather than a password."}
		}
		return oauth.Auth{Token: token, Zid: user}.AsMechanism(), nil

	case "AWS_MSK_IAM":
		// AWS signs the request rather than sending a secret: the access key
		// names the identity, the secret key signs, and a session token comes
		// with credentials that are temporary. Nothing here asks for a
		// region — franz-go reads it from the broker's own address, or from
		// AWS_REGION — and nothing reads the environment for the keys
		// themselves: an ambient identity nobody typed is FR-1.14's business,
		// deliberately and separately.
		if user == "" || password == "" {
			return nil, &source.ConnectError{Kind: source.ConnectConfig,
				Hint: "AWS_MSK_IAM signs with an access key id and a secret access key, and needs both."}
		}
		return aws.Auth{AccessKey: user, SecretKey: password, SessionToken: token}.
			AsManagedStreamingIAMMechanism(), nil

	case "PLAIN", "SCRAM-SHA-256", "SCRAM-SHA-512":
		if user == "" {
			return nil, &source.ConnectError{Kind: source.ConnectConfig,
				Hint: fmt.Sprintf("%s authenticates a user, and none was given.", name)}
		}
		if token != "" {
			return nil, &source.ConnectError{Kind: source.ConnectConfig,
				Hint: fmt.Sprintf("%s carries a password rather than a token.", name)}
		}
		switch name {
		case "PLAIN":
			return plain.Auth{User: user, Pass: password}.AsMechanism(), nil
		case "SCRAM-SHA-256":
			return scram.Auth{User: user, Pass: password}.AsSha256Mechanism(), nil
		}
		return scram.Auth{User: user, Pass: password}.AsSha512Mechanism(), nil
	}

	return nil, &source.ConnectError{Kind: source.ConnectConfig,
		Hint: fmt.Sprintf("%q is not an authentication this driver speaks; choose one of %s.",
			name, strings.Join(mechanisms, ", "))}
}

// secret reads what the keychain holds for this connection, a failure to read
// it being a fault in the settings rather than a refusal by the broker.
func secret(cfg source.ConnectionConfig, key string) (string, error) {
	if cfg.Secret == nil {
		return "", nil
	}
	v, err := cfg.Secret(key)
	if err != nil {
		return "", &source.ConnectError{Kind: source.ConnectConfig,
			Hint: fmt.Sprintf("The %s could not be read from the keychain.", key), Err: err}
	}
	return v, nil
}

// bootstrapServers are the addresses this connection starts from. They are a
// way in rather than the cluster: a seed is asked who the brokers are, and
// every later request goes to the broker that holds what is being asked
// about. More than one is given so that a broker being down is not a cluster
// being unreachable.
func bootstrapServers(cfg source.ConnectionConfig) ([]string, error) {
	var out []string
	if host := strings.TrimSpace(cfg.Host); host != "" {
		out = append(out, address(host, cfg.Port))
	}
	for _, extra := range strings.FieldsFunc(cfg.Params["servers"], func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n'
	}) {
		// A further broker without a port of its own is on the same port as
		// the first: a cluster is usually configured alike throughout.
		out = append(out, address(extra, cfg.Port))
	}
	if len(out) == 0 {
		return nil, &source.ConnectError{Kind: source.ConnectConfig,
			Hint: "No bootstrap server was given: name a broker to start from."}
	}
	return out, nil
}

// address is one bootstrap server, carrying a port whether or not it was
// written with one.
func address(s string, port int) string {
	s = strings.TrimSpace(s)
	if _, _, err := net.SplitHostPort(s); err == nil {
		return s
	}
	if port <= 0 {
		port = defaultPort
	}
	return net.JoinHostPort(s, strconv.Itoa(port))
}

// hostOf is the name this connection is made to, which is the name a
// certificate has to be for.
func hostOf(cfg source.ConnectionConfig) string { return strings.TrimSpace(cfg.Host) }

// classifyConnectError says what to fix, rather than handing on what a client
// library said to itself (FR-1.4).
func classifyConnectError(err error) error {
	var ce *source.ConnectError
	if errors.As(err, &ce) {
		return err
	}
	var dns *net.DNSError
	var ne net.Error
	text := strings.ToLower(err.Error())
	switch {
	// Signing for a managed cluster needs a region, which is read from the
	// broker's address and then from the environment. Where neither says, the
	// failure arrives mid-handshake and would otherwise read as a cluster
	// nobody could reach — when what is missing is a setting.
	case strings.Contains(text, "determine the region"):
		return &source.ConnectError{Kind: source.ConnectConfig,
			Hint: "The AWS region could not be read from the broker's address; set AWS_REGION.", Err: err}
	// A broker that will not take a token says so inside the OAUTHBEARER
	// exchange itself, which franz-go reports as unexpected data in an oauth
	// response. That is a refusal, not a cluster nobody could reach, and the
	// difference is whether a person goes looking at their network or at
	// their token.
	case strings.Contains(text, "sasl") || strings.Contains(text, "authentication") ||
		strings.Contains(text, "authorize") || strings.Contains(text, "credential") ||
		strings.Contains(text, "oauth"):
		return &source.ConnectError{Kind: source.ConnectAuth,
			Hint: "The cluster did not accept these credentials.", Err: err}
	case strings.Contains(text, "x509") || strings.Contains(text, "certificate") ||
		strings.Contains(text, "tls"):
		return &source.ConnectError{Kind: source.ConnectTLS,
			Hint: "The broker's certificate could not be verified.", Err: err}
	case errors.As(err, &dns) || strings.Contains(text, "no such host"):
		return &source.ConnectError{Kind: source.ConnectUnreachable,
			Hint: "That host name could not be found.", Err: err}
	case strings.Contains(text, "connection refused"):
		return &source.ConnectError{Kind: source.ConnectRefused,
			Hint: "Nothing is listening on that host and port.", Err: err}
	case errors.Is(err, context.Canceled):
		return &source.ConnectError{Kind: source.ConnectUnreachable,
			Hint: "The connection was given up before the cluster answered.", Err: err}
	case errors.As(err, &ne) && ne.Timeout() || errors.Is(err, context.DeadlineExceeded) ||
		strings.Contains(text, "timeout") || strings.Contains(text, "i/o timeout") ||
		strings.Contains(text, "deadline exceeded"):
		return &source.ConnectError{Kind: source.ConnectUnreachable,
			Hint: "The cluster did not answer in time.", Err: err}
	}
	return &source.ConnectError{Kind: source.ConnectUnknown,
		Hint: "No broker of the cluster could be reached.", Err: err}
}

// kafkaSource is one connection to a cluster.
type kafkaSource struct {
	client *kgo.Client
	admin  *kadm.Client

	// registry describes what the records mean, where a connection names one
	// (registry.go). Nil is the ordinary case: a cluster is read without a
	// registry far more often than with one.
	registry *sr.Client

	// cfg is kept because reading records needs a client of its own: a
	// consumer is assigned partitions, and this one is the connection's.
	// Building that consumer means dialling the same brokers the same way.
	cfg source.ConnectionConfig
}

func (s *kafkaSource) Capabilities() capability.Capabilities {
	return capability.Capabilities{
		Paradigm: model.ParadigmStream,
		// There is no query language to claim — Kafka has none, which is why
		// capability.Query exists to be left zeroed. Records can be read now
		// (T2.63); producing, groups, seeking by time and following a log
		// each wait for the task that writes them.
		// SchemaRegistry is claimed only where one was named: claiming it
		// otherwise would promise subjects this connection can never list
		// (REQ-DRV-1).
		Stream: capability.Stream{Consume: true, SeekTimestamp: true, Follow: true,
			SchemaRegistry: s.registry != nil},
		Objects: map[model.ObjectKind]bool{
			model.KindCluster: true, model.KindFolder: true, model.KindTopic: true,
			model.KindPartition: true, model.KindConsumerGroup: true,
			// Subjects, like the registry itself, only where one was named.
			// A kind declared here and never in the tree, or in the tree and
			// undeclared, is the broken promise conformance looks for
			// (REQ-DRV-1).
			model.KindSubject: s.registry != nil,
		},
	}
}

// Info reads what the cluster says about itself, and times the round trip.
func (s *kafkaSource) Info(ctx context.Context) (_ source.ServerInfo, err error) {
	defer panics.Recover(&err, "reading the cluster's own account of itself")

	start := time.Now()
	md, err := s.admin.BrokerMetadata(ctx)
	if err != nil {
		return source.ServerInfo{}, err
	}
	info := source.ServerInfo{Product: "Kafka", Latency: time.Since(start),
		Attrs: map[string]string{"brokers": strconv.Itoa(len(md.Brokers))}}
	if md.Cluster != "" {
		info.Attrs["cluster id"] = md.Cluster
	}
	if md.Controller >= 0 {
		// Which broker is answering for the cluster as a whole. In KRaft it
		// is a controller of its own; either way it is the one to name.
		info.Attrs["controller"] = strconv.FormatInt(int64(md.Controller), 10)
	}
	info.Version = s.version(ctx)
	return info, nil
}

// version is the cluster's, as closely as a client can know it. A broker does
// not say what version it is; it says which versions of each API it speaks,
// and a release is read back out of that. So this is a guess, and franz-go
// says as much when it is an uncertain one ("at least v4.0").
func (s *kafkaSource) version(ctx context.Context) string {
	vs, err := s.admin.ApiVersions(ctx)
	if err != nil {
		return ""
	}
	for _, v := range vs.Sorted() {
		if v.Err != nil || v.Raw() == nil {
			continue
		}
		return kversion.FromApiVersionsResponse(v.Raw()).VersionGuess()
	}
	return ""
}

func (s *kafkaSource) Ping(ctx context.Context) (err error) {
	defer panics.Recover(&err, "pinging the cluster")
	return s.client.Ping(ctx)
}

func (s *kafkaSource) Close() (err error) {
	defer panics.Recover(&err, "closing the connection")
	// kgo's own Close is idempotent, and the teardown path can reach this
	// twice; a flag of this driver's own would only say the same thing again.
	s.client.Close()
	return nil
}
