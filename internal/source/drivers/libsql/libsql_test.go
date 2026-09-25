package libsql

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// What this package decides on its own, without a server: which addresses
// it will open, how a token is carried, and what a failure to dial is
// called. Everything below Open is the SQLite driver's and is tested there.

func kindOf(t *testing.T, err error) source.ConnectKind {
	t.Helper()
	var ce *source.ConnectError
	if !errors.As(err, &ce) {
		t.Fatalf("that was not a connect error: %v", err)
	}
	return ce.Kind
}

func TestTheAddressIsReadFromTheFieldOrTheHost(t *testing.T) {
	for name, c := range map[string]struct {
		cfg  source.ConnectionConfig
		want string
	}{
		"the field": {source.ConnectionConfig{Params: map[string]string{"url": "libsql://db.turso.io"}},
			"libsql://db.turso.io"},
		"a host, for a URL somebody pasted": {source.ConnectionConfig{Host: "https://db.turso.io"},
			"https://db.turso.io"},
		"the field wins": {source.ConnectionConfig{Host: "https://elsewhere",
			Params: map[string]string{"url": "https://db.turso.io"}}, "https://db.turso.io"},
		"surrounded by spaces": {source.ConnectionConfig{Params: map[string]string{"url": "  http://h:8080  "}},
			"http://h:8080"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := address(c.cfg)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("it would dial %q, want %q", got, c.want)
			}
		})
	}
}

func TestAnAddressItCannotOpenIsRefusedBeforeDialling(t *testing.T) {
	for name, c := range map[string]struct{ raw, says string }{
		"nothing at all":      {"", "Give the database's URL."},
		"only spaces":         {"   ", "Give the database's URL."},
		"a bare host":         {"db.turso.io", "scheme"},
		"another engine":      {"postgres://h/db", "postgres://"},
		"a file":              {"file:///tmp/x.db", "file://"},
		"not an address":      {"http://[::1", "not an address"},
		"a scheme on its own": {"libsql", "scheme"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := address(source.ConnectionConfig{Params: map[string]string{"url": c.raw}})
			if err == nil {
				t.Fatal("it accepted that")
			}
			if k := kindOf(t, err); k != source.ConnectConfig {
				t.Errorf("it called that %v", k)
			}
			var ce *source.ConnectError
			errors.As(err, &ce)
			if !strings.Contains(ce.Hint, c.says) {
				t.Errorf("it says %q, which does not mention %q", ce.Hint, c.says)
			}
		})
	}
}

func TestEverySchemeTheDescriptorClaimsIsOneItOpens(t *testing.T) {
	d := Driver{}.Describe()
	if len(d.URLSchemes) == 0 {
		t.Fatal("it claims no schemes")
	}
	for _, s := range d.URLSchemes {
		if _, err := address(source.ConnectionConfig{
			Params: map[string]string{"url": s + "://db.turso.io"}}); err != nil {
			t.Errorf("it claims %s:// and refuses it: %v", s, err)
		}
	}
}

func TestTheTokenIsCarriedOnTheAddress(t *testing.T) {
	for name, c := range map[string]struct{ addr, token, want string }{
		"no token, nothing added":  {"libsql://db.turso.io", "", "libsql://db.turso.io"},
		"a token becomes a query":  {"libsql://db.turso.io", "abc", "libsql://db.turso.io?authToken=abc"},
		"beside one already there": {"http://h:8080?tls=0", "abc", "http://h:8080?tls=0&authToken=abc"},
		"escaped, being a JWT":     {"http://h", "a.b+c/d=", "http://h?authToken=a.b%2Bc%2Fd%3D"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := dsn(c.addr, c.token); got != c.want {
				t.Errorf("it dials %q, want %q", got, c.want)
			}
		})
	}
}

// A driver is asked for its secrets rather than given them, and a
// connection with none is a server that wants none rather than a failure.
func TestAMissingTokenIsNotAFailure(t *testing.T) {
	cfg := source.ConnectionConfig{Params: map[string]string{"url": "http://127.0.0.1:1"}}
	if _, err := (Driver{}).Open(context.Background(), cfg); kindOf(t, err) != source.ConnectUnreachable {
		t.Errorf("with no way to ask for a token it said %v", err)
	}
	cfg.Secret = func(string) (string, error) { return "", errors.New("no such secret") }
	if _, err := (Driver{}).Open(context.Background(), cfg); kindOf(t, err) != source.ConnectUnreachable {
		t.Errorf("with no token to be had it said %v", err)
	}
}

func TestAFailureToDialIsCalledWhatItIs(t *testing.T) {
	// Each message here is one the client really produces, cut down so that
	// exactly one of the words this reads for is in it. A table of realistic
	// messages would pass with most of those words deleted, and say nothing
	// about which of them is doing the work.
	for name, c := range map[string]struct {
		text    string
		noToken bool
		want    source.ConnectKind
		says    string
	}{
		"a status code":            {"Server returned HTTP status 401", false, source.ConnectAuth, "rejected that token"},
		"in words":                 {"Unauthorized", false, source.ConnectAuth, "rejected that token"},
		"about authentication":     {"authentication failed", false, source.ConnectAuth, "rejected that token"},
		"a malformed token":        {"failed to parse jwt", false, source.ConnectAuth, "rejected that token"},
		"none given":               {"Unauthorized", true, source.ConnectAuth, "wants an auth token"},
		"not found":                {"Server returned HTTP status 404", false, source.ConnectNoDatabase, "no database"},
		"named as missing":         {"no such database", false, source.ConnectNoDatabase, "no database"},
		"an unknown namespace":     {"unknown namespace", false, source.ConnectNoDatabase, "no database"},
		"a name that is not there": {"lookup nope.turso.io: no such host", false, source.ConnectUnreachable, "reached"},
		"nothing listening":        {"connect: connection refused", false, source.ConnectUnreachable, "reached"},
		"nothing in time":          {"context deadline exceeded: i/o timeout", false, source.ConnectUnreachable, "reached"},
		"the socket itself":        {"dial tcp: operation not permitted", false, source.ConnectUnreachable, "reached"},
		"cut off":                  {"unexpected EOF", false, source.ConnectUnreachable, "reached"},
		"a certificate":            {"tls: failed to verify certificate", false, source.ConnectUnreachable, "reached"},
		"something else entirely":  {"near \"x\": syntax error", false, source.ConnectConfig, "did not answer as a libSQL database"},
	} {
		t.Run(name, func(t *testing.T) {
			err := classify(errors.New(c.text), c.noToken)
			if k := kindOf(t, err); k != c.want {
				t.Errorf("%q was called %v, want %v", c.text, k, c.want)
			}
			var ce *source.ConnectError
			errors.As(err, &ce)
			if !strings.Contains(ce.Hint, c.says) {
				t.Errorf("it says %q, which does not mention %q", ce.Hint, c.says)
			}
			if !errors.Is(err, ce.Err) {
				t.Error("it dropped what the server said")
			}
		})
	}
}

// The token is fetched in one place and put in one place, and both are
// held here rather than against a server that demands one.
func TestTheTokenIsFetchedAndCarried(t *testing.T) {
	base := source.ConnectionConfig{Params: map[string]string{"url": "https://db.turso.io"}}

	withToken := base
	withToken.Secret = func(key string) (string, error) {
		if key != "token" {
			t.Errorf("it asked for the secret called %q", key)
		}
		return "abc", nil
	}
	got, err := resolve(withToken)
	if err != nil {
		t.Fatal(err)
	}
	if got.dial != "https://db.turso.io?authToken=abc" {
		t.Errorf("it would dial %q", got.dial)
	}
	if got.addr != "https://db.turso.io" {
		t.Errorf("it would show %q, and a token does not belong on a screen", got.addr)
	}
	if got.noToken {
		t.Error("it found a token and says it did not")
	}

	// Nothing to ask, and nothing to be had, are both a connection with no
	// token rather than a connection that failed.
	for name, secret := range map[string]func(string) (string, error){
		"nothing to ask":  nil,
		"nothing to give": func(string) (string, error) { return "", errors.New("no such secret") },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := base
			cfg.Secret = secret
			got, err := resolve(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if !got.noToken {
				t.Error("it says it found a token")
			}
			if got.dial != "https://db.turso.io" {
				t.Errorf("it would dial %q", got.dial)
			}
		})
	}

	if _, err := resolve(source.ConnectionConfig{}); err == nil {
		t.Error("with no address at all it found something to dial")
	}
}

// A token is a secret, and a secret must not reach a log or an error
// (NFR-S1, NFR-S2). The only place one is written is the string handed to
// the client, and nothing this package returns may carry it.
func TestTheTokenIsNowhereButTheDial(t *testing.T) {
	const token = "eyJhbGciOiJFZERTQSJ9.secret-token-value"
	cfg := source.ConnectionConfig{
		Params: map[string]string{"url": "http://127.0.0.1:1"},
		Secret: func(string) (string, error) { return token, nil },
	}
	_, err := (Driver{}).Open(context.Background(), cfg)
	if err == nil {
		t.Fatal("something answered on port 1")
	}
	for _, s := range []string{err.Error(), fmt.Sprintf("%v", err), fmt.Sprintf("%+v", err)} {
		if strings.Contains(s, token) || strings.Contains(s, "secret-token-value") {
			t.Errorf("the token is in what it said: %s", s)
		}
	}
}

// The descriptor is what the connection editor draws, so what it says about
// the token is what decides whether the token is kept in the keychain.
func TestTheTokenIsDescribedAsASecret(t *testing.T) {
	d := Driver{}.Describe()
	if d.ID != driverID {
		t.Errorf("it registers as %q", d.ID)
	}
	var token, url *source.Field
	for i := range d.Fields {
		switch d.Fields[i].Key {
		case "token":
			token = &d.Fields[i]
		case "url":
			url = &d.Fields[i]
		}
	}
	if token == nil || url == nil {
		t.Fatalf("its fields are %v", d.Fields)
	}
	if !token.Secret {
		t.Error("the token is not marked a secret, so it would be written to settings.json")
	}
	if token.Required {
		t.Error("a token is required, and a server of one's own needs none")
	}
	if !url.Required {
		t.Error("the address is optional, and there is nothing to dial without it")
	}
	if url.Secret {
		t.Error("the address is a secret, so nobody could see what they typed")
	}
}
