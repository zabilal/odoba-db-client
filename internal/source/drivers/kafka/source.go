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
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kversion"

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
	return &kafkaSource{client: client, admin: kadm.NewClient(client)}, nil
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
	case strings.Contains(text, "sasl") || strings.Contains(text, "authentication") ||
		strings.Contains(text, "authorize") || strings.Contains(text, "credential"):
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
}

func (s *kafkaSource) Capabilities() capability.Capabilities {
	return capability.Capabilities{
		Paradigm: model.ParadigmStream,
		// Nothing else yet. There is no query language to claim — Kafka has
		// none, which is why capability.Query exists to be left zeroed — and
		// consuming, producing, groups and topic administration each wait for
		// the task that writes them, as the tree does (T2.59 onward).
		Objects: map[model.ObjectKind]bool{},
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

// Root is nothing yet: the brokers, the topics and what is in them are T2.59
// onward. An empty tree is what a driver that cannot introspect shows, rather
// than a node it has invented.
func (s *kafkaSource) Root(context.Context) ([]model.Node, error) { return nil, nil }

func (s *kafkaSource) Children(context.Context, model.ObjectRef) ([]model.Node, error) {
	return nil, nil
}

func (s *kafkaSource) Describe(context.Context, model.ObjectRef) (any, error) {
	return nil, nil
}

func (s *kafkaSource) Badge(context.Context, model.ObjectRef) (model.Badge, bool, error) {
	return model.Badge{}, false, nil
}

// Browse reads no records yet (T2.63). Consuming a topic is not reading a
// table: it means assigning partitions, choosing where in each log to start,
// and stopping somewhere — and doing it without joining a consumer group or
// committing anybody's offsets (FR-13.19). None of that is written, so this
// says so rather than half-doing it.
func (s *kafkaSource) Browse(context.Context, model.ObjectRef, source.BrowseOptions) (model.RowStream, error) {
	return nil, errors.New("kafka: this connection does not read records yet")
}
