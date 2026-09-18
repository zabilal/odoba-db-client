//go:build conformance

package kafka

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// SASL against a broker that asks for it (T2.55). These expect the
// ikigai-kafka-sasl container: a SASL_PLAINTEXT listener on 9094, with the
// user ikigai known to PLAIN through the broker's own configuration and to
// SCRAM through credentials stored in the cluster.
//
// That broker advertises the ports it is published on, so one address reaches
// it from the host and from inside the container alike — which is what lets
// its own tooling store the SCRAM credentials these tests use.
//
// IKIGAI_REQUIRE_KAFKA_SASL=1 makes a broker that is not there a failure.

const (
	saslUser     = "ikigai"
	saslPassword = "ikigai-secret"
)

func saslPort() int {
	if v := os.Getenv("IKIGAI_KAFKA_SASL_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			return p
		}
	}
	return 9094
}

// saslConfig is a connection that proves who it is the named way.
func saslConfig(mechanism, user, password string) source.ConnectionConfig {
	return source.ConnectionConfig{DriverID: driverID, Host: "127.0.0.1", Port: saslPort(),
		User: user, TLS: source.TLSConfig{Mode: "disable"},
		Params: map[string]string{"mechanism": mechanism},
		Secret: func(key string) (string, error) {
			// One secret per field: a password is not a token, and a driver
			// that asks for one must not be handed the other.
			if key == "password" {
				return password, nil
			}
			return "", nil
		}}
}

// liveSASL connects to the broker that asks for authentication, or skips.
func liveSASL(t *testing.T, cfg source.ConnectionConfig) source.Source {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	src, err := Driver{}.Open(ctx, cfg)
	if err != nil {
		if os.Getenv("IKIGAI_REQUIRE_KAFKA_SASL") != "" {
			t.Fatalf("a broker asking for SASL is required but unavailable: %v", err)
		}
		t.Skipf("no SASL broker on port %d (docker start ikigai-kafka-sasl): %v", saslPort(), err)
	}
	t.Cleanup(func() { src.Close() })
	return src
}

func TestLiveProvesWhoItIsEveryWayItSpeaks(t *testing.T) {
	for _, mechanism := range []string{"PLAIN", "SCRAM-SHA-256", "SCRAM-SHA-512"} {
		t.Run(mechanism, func(t *testing.T) {
			src := liveSASL(t, saslConfig(mechanism, saslUser, saslPassword))
			// Authenticating is not the same as being able to ask anything,
			// so the connection is put to work rather than merely opened.
			if err := src.Ping(context.Background()); err != nil {
				t.Errorf("ping: %v", err)
			}
			info, err := src.Info(context.Background())
			if err != nil {
				t.Fatalf("info: %v", err)
			}
			if info.Attrs["cluster id"] == "" {
				t.Errorf("the cluster says %+v", info.Attrs)
			}
		})
	}
}

func TestLiveAPasswordTheBrokerWillNotTakeSaysSo(t *testing.T) {
	// A broker has to be there for this to mean anything: a refusal and an
	// absence must not read alike.
	liveSASL(t, saslConfig("PLAIN", saslUser, saslPassword))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, c := range []struct{ mechanism, user, password string }{
		{"PLAIN", saslUser, "not the password"},
		{"SCRAM-SHA-256", saslUser, "not the password"},
		{"PLAIN", "nobody", saslPassword},
	} {
		src, err := Driver{}.Open(ctx, saslConfig(c.mechanism, c.user, c.password))
		if err == nil {
			src.Close()
			t.Errorf("%s as %q with the wrong password was let in", c.mechanism, c.user)
			continue
		}
		// What to fix is the credentials, and the words say that rather than
		// handing on what the library told itself.
		if kind(err) != source.ConnectAuth {
			t.Errorf("%s as %q: %v", c.mechanism, c.user, err)
		}
	}
}

func TestLiveABrokerThatAsksIsNotAnsweredWithNothing(t *testing.T) {
	liveSASL(t, saslConfig("PLAIN", saslUser, saslPassword))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cfg := saslConfig(mechNone, "", "")
	src, err := Driver{}.Open(ctx, cfg)
	if err == nil {
		src.Close()
		t.Fatal("a broker asking for SASL admitted a connection that proved nothing")
	}
	// It fails, and it does not pretend the cluster is missing: something is
	// listening, and it wanted to be told who this is.
	if strings.Contains(strings.ToLower(err.Error()), "nothing is listening") {
		t.Errorf("a broker that asked for authentication reads as absent: %v", err)
	}
}

// unsecuredJWT is a token in the one form a broker can check with no issuer to
// ask: a JWS that says it is unsigned, carrying who this is and when it stops
// being true. It is made here rather than written down, so that it is never a
// token which expired last year.
func unsecuredJWT(subject string, life time.Duration) string {
	part := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			panic(err)
		}
		return base64.RawURLEncoding.EncodeToString(b)
	}
	now := time.Now()
	return part(map[string]string{"alg": "none"}) + "." +
		part(map[string]any{"sub": subject, "iat": now.Unix(), "exp": now.Add(life).Unix()}) + "."
}

// tokenConfig is a connection that proves who it is by bearing a token.
func tokenConfig(token string) source.ConnectionConfig {
	return source.ConnectionConfig{DriverID: driverID, Host: "127.0.0.1", Port: saslPort(),
		TLS: source.TLSConfig{Mode: "disable"}, Params: map[string]string{"mechanism": "OAUTHBEARER"},
		Secret: func(key string) (string, error) {
			if key == "token" {
				return token, nil
			}
			return "", nil
		}}
}

func TestLiveProvesWhoItIsWithATokenAlone(t *testing.T) {
	// No user name and no password: the token says who this is, and the
	// broker believes it or does not.
	src := liveSASL(t, tokenConfig(unsecuredJWT(saslUser, time.Hour)))
	if err := src.Ping(context.Background()); err != nil {
		t.Errorf("ping: %v", err)
	}
	info, err := src.Info(context.Background())
	if err != nil {
		t.Fatalf("info: %v", err)
	}
	if info.Attrs["cluster id"] == "" {
		t.Errorf("the cluster says %+v", info.Attrs)
	}
}

func TestLiveATokenTheBrokerWillNotTakeSaysSo(t *testing.T) {
	// A broker has to be there for this to mean anything.
	liveSASL(t, tokenConfig(unsecuredJWT(saslUser, time.Hour)))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for name, token := range map[string]string{
		"a token that ran out": unsecuredJWT(saslUser, -time.Hour),
		"no token at all":      "not-a-token",
	} {
		src, err := Driver{}.Open(ctx, tokenConfig(token))
		if err == nil {
			src.Close()
			t.Errorf("%s was accepted", name)
			continue
		}
		if kind(err) != source.ConnectAuth {
			t.Errorf("%s: %v", name, err)
		}
	}
}
