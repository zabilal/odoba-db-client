package elasticsearch

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/capability"
)

const driverID = "elasticsearch"

func init() { source.Register(Driver{}) }

// Driver opens Elasticsearch and OpenSearch servers.
type Driver struct{}

func (Driver) Describe() source.Descriptor {
	return source.Descriptor{
		ID: driverID, Name: "Elasticsearch / OpenSearch", Paradigm: model.ParadigmDocument,
		DefaultPort: 9200,
		URLSchemes:  []string{"elasticsearch", "opensearch"},
		Fields: []source.Field{
			{Key: "host", Label: "Host", Kind: source.FieldText, Required: true},
			{Key: "port", Label: "Port", Kind: source.FieldNumber, Default: "9200"},
			{Key: "user", Label: "User", Kind: source.FieldText,
				Help: "A server with security turned off needs none."},
			{Key: "password", Label: "Password", Kind: source.FieldPassword, Secret: true},
		},
	}
}

// Open dials the server and reads what it says it is, which is the smallest
// request there is and the one that tells the four things apart: a wrong
// address, a wrong password, a server that is not a search engine, and one that
// is still starting.
func (Driver) Open(ctx context.Context, cfg source.ConnectionConfig) (_ source.Source, err error) {
	defer panics.Recover(&err, "opening a search server")

	c, err := dial(cfg)
	if err != nil {
		return nil, err
	}
	var root struct {
		Name    string `json:"name"`
		Cluster string `json:"cluster_name"`
		Version struct {
			Number       string `json:"number"`
			Distribution string `json:"distribution"`
		} `json:"version"`
		Tagline string `json:"tagline"`
	}
	if err := c.get(ctx, "/", nil, &root); err != nil {
		return nil, classify(err, c)
	}
	if root.Version.Number == "" {
		// Something answered and it was not a search engine: a proxy, a
		// different service on that port, a login page.
		return nil, &source.ConnectError{Kind: source.ConnectConfig,
			Hint: "Something answered at that address, but it is not Elasticsearch or OpenSearch.",
			Err:  errors.New("elasticsearch: the server does not say what version it is")}
	}
	return &esSource{c: c, cfg: cfg, product: product(root.Version.Distribution),
		version: root.Version.Number, cluster: root.Cluster, node: root.Name}, nil
}

// product is what the server calls itself. Elasticsearch says nothing, because
// it was there first; OpenSearch says so.
func product(distribution string) string {
	if strings.EqualFold(distribution, "opensearch") {
		return "OpenSearch"
	}
	return "Elasticsearch"
}

// classify turns a failed first request into what a person should look at.
func classify(err error, c *client) error {
	switch {
	case refusedWho(err):
		hint := "The server refused those credentials."
		if c.user == "" {
			hint = "That server wants a user name and password, and this connection has none."
		}
		return &source.ConnectError{Kind: source.ConnectAuth, Hint: hint, Err: err}
	case notReady(err):
		return &source.ConnectError{Kind: source.ConnectRefused,
			Hint: "The server is there but not ready: it may still be starting.", Err: err}
	case isTLS(err):
		return &source.ConnectError{Kind: source.ConnectTLS,
			Hint: "The encrypted connection could not be agreed.", Err: err}
	case isUnreachable(err):
		return &source.ConnectError{Kind: source.ConnectUnreachable,
			Hint: "Nothing is listening at that address.", Err: err}
	}
	return &source.ConnectError{Kind: source.ConnectUnknown,
		Hint: "That server could not be read.", Err: err}
}

// isTLS and isUnreachable are the two network failures worth telling apart: one
// is about certificates and the other about addresses, and they send somebody to
// different places.
func isTLS(err error) bool {
	var (
		verify *tls.CertificateVerificationError
		record tls.RecordHeaderError
		alert  tls.AlertError
	)
	if errors.As(err, &verify) || errors.As(err, &record) || errors.As(err, &alert) {
		return true
	}
	// The one that has no type: a server speaking plain HTTP on a port this
	// was told to encrypt, which the standard library says only in words.
	return strings.Contains(err.Error(), "server gave HTTP response to HTTPS client")
}

func isUnreachable(err error) bool {
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return true
	}
	var op *net.OpError
	if errors.As(err, &op) {
		return true
	}
	return errors.Is(err, context.DeadlineExceeded)
}

// esSource is one connection to a search server.
type esSource struct {
	c   *client
	cfg source.ConnectionConfig

	product string
	version string
	cluster string
	node    string
}

var (
	_ source.Source    = (*esSource)(nil)
	_ source.Countable = (*esSource)(nil)
)

func (s *esSource) Capabilities() capability.Capabilities {
	return capability.Capabilities{
		Paradigm: model.ParadigmDocument,
		// One level: a search engine has indices and no databases above them.
		Structure: capability.Structure{},
		// The server filters, sorts and counts exactly: a query is what it is
		// for. What it will not do is any of them on analysed text that has no
		// keyword beside it, and that is refused field by field, with the
		// reason, rather than by not offering the rest (query.go).
		Data: capability.Data{ServerFilter: true, ServerSort: true, ExactCount: true, ApproximateCount: true},
		Objects: map[model.ObjectKind]bool{
			model.KindFolder: true, model.KindCollection: true,
		},
	}
}

// Info is what the server says it is, and how long it took to say it.
func (s *esSource) Info(ctx context.Context) (_ source.ServerInfo, err error) {
	defer panics.Recover(&err, "reading the server's version")
	start := time.Now()
	var health struct {
		Status string `json:"status"`
		Nodes  int    `json:"number_of_nodes"`
	}
	if err := s.c.get(ctx, "/_cluster/health", nil, &health); err != nil {
		return source.ServerInfo{}, err
	}
	attrs := map[string]string{"cluster": s.cluster, "node": s.node}
	if health.Status != "" {
		// green, yellow or red: the one thing an operator looks at first, and a
		// single-node server is yellow by design rather than by fault.
		attrs["health"] = health.Status
	}
	if health.Nodes > 0 {
		attrs["nodes"] = strconv.Itoa(health.Nodes)
	}
	return source.ServerInfo{Product: s.product, Version: s.version,
		Latency: time.Since(start), Attrs: attrs}, nil
}

// Ping asks the cheapest question there is.
func (s *esSource) Ping(ctx context.Context) (err error) {
	defer panics.Recover(&err, "checking the connection")
	return s.c.get(ctx, "/", nil, nil)
}

// Close lets go of the connections the client is holding. There is no session to
// end: every request is its own.
func (s *esSource) Close() error {
	s.c.http.CloseIdleConnections()
	return nil
}
