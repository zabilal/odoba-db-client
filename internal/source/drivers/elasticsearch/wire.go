// Package elasticsearch is the Elasticsearch and OpenSearch driver (T5.15).
//
// One driver for both, because they are one protocol for everything this
// application asks: the root says what the server is, _cat/indices lists what it
// holds, _mapping says what a document looks like, _search reads documents and
// _count counts them. OpenSearch forked Elasticsearch over a licence rather than
// over an API, and the parts that diverged since — the security plugin's
// endpoints, the machine-learning ones, the licence endpoint — are parts a
// database client has no business in.
//
// It speaks the protocol directly, with net/http and encoding/json, rather than
// through either project's client library. Three reasons, in order of weight:
// the official Elasticsearch client refuses to talk to OpenSearch at all, having
// been taught to check a version header, so a library choice would have been a
// choice of which half of the users to serve; the endpoints needed are seven,
// and a library that speaks four hundred is several megabytes of binary for the
// other three hundred and ninety-three (NFR-P8); and what a search engine
// returns is JSON whose shape this has to understand anyway, because a document
// is not a row until something decides which of its fields are columns.
package elasticsearch

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// maxAnswer bounds what one request may answer, so that a server answering
// something enormous cannot take the window with it. A page of documents is
// kilobytes; a mapping of a wide index is tens of them.
const maxAnswer = 64 << 20

// client speaks to one server.
type client struct {
	http *http.Client
	base string // scheme://host:port, with no trailing slash
	user string
	pass string
}

// dial builds a client from a connection, without speaking to anything: what
// cannot become a request is said before the network is touched.
func dial(cfg source.ConnectionConfig) (*client, error) {
	host := strings.TrimSpace(cfg.Host)
	if host == "" {
		return nil, &source.ConnectError{Kind: source.ConnectConfig,
			Hint: "This connection has no host.", Err: errors.New("elasticsearch: no host")}
	}
	port := cfg.Port
	if port == 0 {
		port = 9200
	}
	scheme := "https"
	if insecure(cfg.TLS.Mode) {
		scheme = "http"
	}
	name := host
	if cfg.TLS.ServerName != "" {
		name = cfg.TLS.ServerName
	}
	tlsCfg, err := transportSecurity(cfg.TLS, name)
	if err != nil {
		return nil, err
	}
	c := &client{
		base: scheme + "://" + net.JoinHostPort(host, strconv.Itoa(port)),
		user: strings.TrimSpace(cfg.User),
		http: &http.Client{
			Timeout:   30 * time.Second,
			Transport: &http.Transport{TLSClientConfig: tlsCfg},
		},
	}
	if c.user != "" && cfg.Secret != nil {
		pass, err := cfg.Secret("password")
		if err != nil {
			return nil, &source.ConnectError{Kind: source.ConnectConfig,
				Hint: "The password for this connection could not be read.", Err: err}
		}
		c.pass = pass
	}
	return c, nil
}

// insecure reports whether a TLS mode means plain HTTP. A server reached over
// http is the usual shape of one on a laptop, and saying so has to be a choice
// somebody made rather than a default: a connection that says nothing is
// encrypted and verified (NFR-S3).
func insecure(mode string) bool {
	return mode == "disable"
}

// transportSecurity is the TLS configuration a mode asks for. Verification is on
// unless a mode explicitly turns it off (NFR-S3).
func transportSecurity(t source.TLSConfig, host string) (*tls.Config, error) {
	if insecure(t.Mode) {
		return nil, nil
	}
	out := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host}
	switch t.Mode {
	case "require":
		// Encrypted and unverified: a choice, and one the connection had to be
		// told to make.
		out.InsecureSkipVerify = true
	case "", "verify-ca", "verify-full":
	default:
		return nil, &source.ConnectError{Kind: source.ConnectConfig,
			Hint: fmt.Sprintf("%q is not a way to secure a connection.", t.Mode),
			Err:  fmt.Errorf("elasticsearch: unknown TLS mode %q", t.Mode)}
	}
	if t.CAFile != "" {
		pem, err := os.ReadFile(t.CAFile)
		if err != nil {
			return nil, &source.ConnectError{Kind: source.ConnectConfig,
				Hint: "That certificate authority file could not be read.", Err: err}
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, &source.ConnectError{Kind: source.ConnectConfig,
				Hint: "That file holds no certificate.",
				Err:  fmt.Errorf("elasticsearch: %s holds no certificate", t.CAFile)}
		}
		out.RootCAs = pool
	}
	if t.CertFile != "" || t.KeyFile != "" {
		pair, err := tls.LoadX509KeyPair(t.CertFile, t.KeyFile)
		if err != nil {
			return nil, &source.ConnectError{Kind: source.ConnectConfig,
				Hint: "That client certificate and key could not be read together.", Err: err}
		}
		out.Certificates = []tls.Certificate{pair}
	}
	return out, nil
}

// ask makes a request and reads the answer into out, which may be nil.
//
// Every path this sends is built here from segments that are escaped, so an
// index named with a slash or a question mark cannot become a different request
// (NFR-S6 is about statements; this is the same rule where there are none).
func (c *client) ask(ctx context.Context, method, path string, query url.Values, body, out any) error {
	addr := c.base + path
	if len(query) > 0 {
		addr += "?" + query.Encode()
	}
	var payload io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("elasticsearch: that request could not be written: %w", err)
		}
		payload = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, addr, payload)
	if err != nil {
		return fmt.Errorf("elasticsearch: that address could not be asked: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.user != "" {
		req.SetBasicAuth(c.user, c.pass)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxAnswer))
	if err != nil {
		return fmt.Errorf("elasticsearch: the server's answer could not be read: %w", err)
	}
	if res.StatusCode >= 400 {
		return said(res.StatusCode, raw)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("elasticsearch: the server answered something this cannot read: %w", err)
	}
	return nil
}

// get asks for something, and post asks with a body: the two ways this driver
// speaks, which reads nothing it has to send a body for and changes nothing at
// all.
func (c *client) get(ctx context.Context, path string, query url.Values, out any) error {
	return c.ask(ctx, http.MethodGet, path, query, nil, out)
}

func (c *client) post(ctx context.Context, path string, body, out any) error {
	return c.ask(ctx, http.MethodPost, path, nil, body, out)
}

// segment escapes one part of a path.
func segment(s string) string { return url.PathEscape(s) }

// failure is what a search engine says went wrong.
type failure struct {
	Error struct {
		Type   string `json:"type"`
		Reason string `json:"reason"`
		Caused *struct {
			Type   string `json:"type"`
			Reason string `json:"reason"`
		} `json:"caused_by"`
	} `json:"error"`
	Status int `json:"status"`
}

// said turns a refusal into what it means. A search engine's errors are typed,
// and the type is the thing worth saying: "index_not_found_exception" is a
// missing index however the reason is worded.
func said(status int, raw []byte) error {
	var f failure
	_ = json.Unmarshal(raw, &f)
	kind, reason := f.Error.Type, f.Error.Reason
	if kind == "" {
		// Not one of its own errors: a proxy, a wrong port, a server that is
		// not a search engine at all. What it said is all there is.
		text := strings.TrimSpace(string(raw))
		if len(text) > 200 {
			text = text[:200] + "…"
		}
		if text == "" {
			text = http.StatusText(status)
		}
		return &httpError{status: status, kind: "", reason: text}
	}
	if f.Error.Caused != nil && f.Error.Caused.Reason != "" && f.Error.Caused.Reason != reason {
		reason += ": " + f.Error.Caused.Reason
	}
	return &httpError{status: status, kind: kind, reason: reason}
}

// httpError is a refusal with its status and its type kept, so that what reads
// it can tell a missing index from a rejected query without matching words.
type httpError struct {
	status int
	kind   string
	reason string
}

func (e *httpError) Error() string {
	switch {
	case e.kind == "":
		return fmt.Sprintf("elasticsearch: the server answered %d: %s", e.status, e.reason)
	case e.reason == "":
		return "elasticsearch: " + plainly(e.kind)
	}
	return "elasticsearch: " + plainly(e.kind) + ": " + e.reason
}

// plainly is an error type as a person reads it: the protocol writes
// index_not_found_exception, and a person reads "index not found".
func plainly(kind string) string {
	kind = strings.TrimSuffix(kind, "_exception")
	return strings.ReplaceAll(kind, "_", " ")
}

// refusedWho reports whether a refusal is about who is asking, and notReady
// whether it is a server that is there and not yet answering.
func refusedWho(err error) bool {
	var e *httpError
	return errors.As(err, &e) && (e.status == http.StatusUnauthorized || e.status == http.StatusForbidden)
}

func notReady(err error) bool {
	var e *httpError
	return errors.As(err, &e) && e.status == http.StatusServiceUnavailable
}

// notFound reports whether a refusal is about something that is not there.
func notFound(err error) bool {
	var e *httpError
	return errors.As(err, &e) && (e.status == http.StatusNotFound ||
		e.kind == "index_not_found_exception")
}
