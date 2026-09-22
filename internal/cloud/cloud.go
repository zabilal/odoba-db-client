// Package cloud mints the short-lived token a cloud database accepts in
// place of a password (FR-1.14).
//
// The identity behind it is one this machine already holds — an environment
// variable, a profile, an instance role, a signed-in gcloud — rather than
// anything somebody types here. A credential somebody types belongs in the
// keychain instead (FR-1.5); what this package is for is the identity
// nobody typed.
//
// No driver knows any of this exists, for the reason ADR-0110 gives: the
// token is handed over as the password, and a driver asking for a password
// is handed a fresh one. That is what keeps this one implementation instead
// of one per driver.
package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Providers, as they are written in a connection's settings.
const (
	AWS   = "aws"
	GCP   = "gcp"
	Azure = "azure"
)

// Config says which cloud is being asked, and carries the few non-secret
// settings that cannot be worked out from the connection itself.
type Config struct {
	// Provider is one of "aws", "gcp", "azure".
	Provider string

	// Params holds provider-specific settings: an AWS profile or region, a
	// GCP impersonated service account, an Azure tenant. Nothing secret goes
	// in here — it is written to the settings file in plain sight.
	Params map[string]string
}

// Target is the database the token is for. A token is not a password: it is
// signed over where it may be spent, so a token minted for one endpoint and
// user is worthless at another.
type Target struct {
	Host string
	Port int
	User string
}

// timeNow is the clock, replaced in tests so that a signature can be
// compared against one computed by hand.
var timeNow = time.Now

// Token mints one token for one connection. It is called each time a
// connection is made rather than cached, because every provider here issues
// something that expires: fifteen minutes for AWS, an hour or so for the
// others, and a token held over from a previous session is a login failure
// nobody can explain.
func Token(ctx context.Context, cfg Config, target Target) (string, error) {
	if target.Host == "" {
		return "", errors.New("a cloud token is signed for one database endpoint and this connection has no host")
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Provider)) {
	case AWS:
		return awsToken(ctx, cfg.Params, target)
	case GCP:
		return gcpToken(ctx, cfg.Params)
	case Azure:
		return azureToken(ctx, cfg.Params)
	}
	return "", fmt.Errorf("a cloud token comes from aws, gcp or azure, and %q is none of those", cfg.Provider)
}

// httpClient is used for every metadata and token endpoint.
//
// Redirects are refused rather than followed. These requests carry, or are
// about to receive, credentials; a metadata service that answered with a
// redirect to somewhere else would be either broken or an attempt to have
// this send them there.
var httpClient = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("refused to follow a redirect while fetching credentials")
	},
}

// maxTokenResponse bounds what any of these endpoints can make this read. A
// token is a few kilobytes at the outside.
const maxTokenResponse = 1 << 20

func fetchText(req *http.Request) (string, error) {
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxTokenResponse))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		// The body of a refusal often says which scope or role was wrong,
		// and is worth more than the status code alone.
		return "", fmt.Errorf("%s said %s: %s", req.URL.Host, resp.Status, firstLine(string(body)))
	}
	return string(body), nil
}

func fetchJSON(req *http.Request, into any) error {
	body, err := fetchText(req)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(body), into)
}

// firstLine keeps an error to one line. Some of these endpoints answer a
// refusal with an HTML page.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	if s == "" {
		return "nothing"
	}
	return s
}
