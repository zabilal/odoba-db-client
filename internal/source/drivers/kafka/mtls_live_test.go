//go:build conformance

package kafka

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// A client certificate, against a broker that asks for one (T2.58).
//
// These expect the ikigai-kafka-sasl container's SSL listener on 9095, which
// requires client authentication, and the certificates the rig was built with:
// a CA of its own, a server certificate for localhost, and a client
// certificate. IKIGAI_KAFKA_CERTS says where they are; IKIGAI_REQUIRE_KAFKA_SSL
// makes a broker that is not there a failure rather than a skip.
//
// Nothing in the driver is new here. The shared TLS settings already load a
// client certificate for every driver, and this one hands whatever they build
// straight to the client — so what these tests prove is that the shared thing
// works end to end against a server that really checks it.

func sslPort() int {
	if v := os.Getenv("IKIGAI_KAFKA_SSL_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			return p
		}
	}
	return 9095
}

// certs is where the rig's certificates live: outside the repository, because
// a private key does not belong in it, and in a fixed place rather than a
// session's own, because the broker mounts them too.
func certs(t *testing.T) string {
	t.Helper()
	dir := os.Getenv("IKIGAI_KAFKA_CERTS")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Skipf("no home directory to find certificates in: %v", err)
		}
		dir = filepath.Join(home, ".claude", "projects", "-Users-one-Work-ikigai-db", "kafka-certs")
	}
	if _, err := os.Stat(filepath.Join(dir, "ca.crt")); err != nil {
		if os.Getenv("IKIGAI_REQUIRE_KAFKA_SSL") != "" {
			t.Fatalf("the rig's certificates are required but missing from %s", dir)
		}
		t.Skipf("no certificates in %s (IKIGAI_KAFKA_CERTS says where)", dir)
	}
	return dir
}

// encrypted is a connection to the listener that asks for a certificate. The
// host is the name the server's certificate is for, because that is the whole
// point of verifying it.
func encrypted(t *testing.T, mode string, withClientCert bool) source.ConnectionConfig {
	dir := certs(t)
	cfg := source.ConnectionConfig{DriverID: driverID, Host: "localhost", Port: sslPort(),
		TLS: source.TLSConfig{Mode: mode, CAFile: filepath.Join(dir, "ca.crt")}}
	if withClientCert {
		cfg.TLS.CertFile = filepath.Join(dir, "client.crt")
		cfg.TLS.KeyFile = filepath.Join(dir, "client.key")
	}
	return cfg
}

// overTLS connects to the encrypting listener, or skips where it is not there.
func overTLS(t *testing.T, cfg source.ConnectionConfig) source.Source {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	src, err := Driver{}.Open(ctx, cfg)
	if err != nil {
		if os.Getenv("IKIGAI_REQUIRE_KAFKA_SSL") != "" {
			t.Fatalf("a broker asking for a certificate is required but unavailable: %v", err)
		}
		t.Skipf("no encrypting broker on port %d (docker start ikigai-kafka-sasl): %v", sslPort(), err)
	}
	t.Cleanup(func() { src.Close() })
	return src
}

func TestLiveACertificateIsEnoughToBeLetIn(t *testing.T) {
	// No mechanism, no user, no password: the certificate says who this is,
	// and the broker asks for nothing else. A certificate is transport rather
	// than a credential, so the rule that refuses credentials with nothing
	// chosen must not refuse this.
	src := overTLS(t, encrypted(t, "verify-full", true))
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

func TestLiveWithoutACertificateTheBrokerWillNotHaveIt(t *testing.T) {
	// The broker is there, which is what makes the refusal below mean
	// something rather than nothing.
	overTLS(t, encrypted(t, "verify-full", true))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	src, err := Driver{}.Open(ctx, encrypted(t, "verify-full", false))
	if err == nil {
		src.Close()
		t.Fatal("a listener requiring a client certificate admitted a connection without one")
	}
}

func TestLiveAServerByAnotherNameIsRefused(t *testing.T) {
	overTLS(t, encrypted(t, "verify-full", true))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// The certificate is for localhost; this asks for a different name, which
	// is the check verify-full exists to make (NFR-S3).
	cfg := encrypted(t, "verify-full", true)
	cfg.TLS.ServerName = "not-the-broker.invalid"
	src, err := Driver{}.Open(ctx, cfg)
	if err == nil {
		src.Close()
		t.Fatal("a certificate for another name was accepted")
	}
	if kind(err) != source.ConnectTLS {
		t.Errorf("a name that does not match reads as %v", err)
	}
}

func TestLiveAnUnverifiedModeStillEncrypts(t *testing.T) {
	// require: encrypted, and the server's certificate not checked — an
	// explicit choice, and it must still reach a broker that only speaks TLS.
	cfg := encrypted(t, "require", true)
	cfg.TLS.CAFile = ""
	src := overTLS(t, cfg)
	if err := src.Ping(context.Background()); err != nil {
		t.Errorf("ping: %v", err)
	}
}

func TestLivePlaintextToAnEncryptingListenerSaysSo(t *testing.T) {
	overTLS(t, encrypted(t, "verify-full", true))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Speaking plainly to a listener that speaks only TLS: it fails, and it
	// must not read as a cluster nobody could find.
	cfg := source.ConnectionConfig{DriverID: driverID, Host: "localhost", Port: sslPort(),
		TLS: source.TLSConfig{Mode: "disable"}}
	src, err := Driver{}.Open(ctx, cfg)
	if err == nil {
		src.Close()
		t.Fatal("plaintext was accepted by a listener that only speaks TLS")
	}
	if strings.Contains(strings.ToLower(err.Error()), "host name could not be found") {
		t.Errorf("a TLS listener reads as a missing host: %v", err)
	}
}
