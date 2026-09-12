package redact

import (
	"errors"
	"strings"
	"testing"
)

func TestStringRedactsConnectionURLs(t *testing.T) {
	cases := []struct {
		name string
		in   string
		leak string
	}{
		{"postgres", "postgres://alice:hunter2@db.example.com:5432/sales", "hunter2"},
		{"mongodb+srv", "mongodb+srv://svc:S3cr3t!@cluster0.mongodb.net/app", "S3cr3t!"},
		{"redis", "redis://default:p%40ssw0rd@cache:6379/0", "p%40ssw0rd"},
		{"mysql dsn", "user=root password=toor host=127.0.0.1", "toor"},
		{"kafka jaas", `sasl.jaas.config=org.apache.kafka.PlainLoginModule required password="topsecret";`, "topsecret"},
		{"bearer", "Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.abcdefgh", "eyJhbGciOiJIUzI1NiJ9.abcdefgh"},
		{"api key", "api_key=sk-live-9f8a7b6c5d4e", "sk-live-9f8a7b6c5d4e"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := String(tc.in)
			if strings.Contains(got, tc.leak) {
				t.Errorf("secret leaked\n input: %s\noutput: %s\n  leak: %s", tc.in, got, tc.leak)
			}
			if !strings.Contains(got, Mask) {
				t.Errorf("expected mask in output, got: %s", got)
			}
		})
	}
}

func TestStringKeepsDiagnosticContext(t *testing.T) {
	// Redaction must not destroy the information that makes a log useful:
	// which host, which database, which user failed.
	got := String("postgres://alice:hunter2@db.example.com:5432/sales")

	for _, keep := range []string{"db.example.com", "5432", "sales", "alice"} {
		if !strings.Contains(got, keep) {
			t.Errorf("over-redacted: lost %q from %q", keep, got)
		}
	}
}

func TestStringLeavesCleanTextAlone(t *testing.T) {
	in := "connected to postgres 16.2 in 42ms"
	if got := String(in); got != in {
		t.Errorf("modified clean text:\n got: %s\nwant: %s", got, in)
	}
}

func TestWrapPreservesErrorIdentity(t *testing.T) {
	sentinel := errors.New("dial failed")
	wrapped := Wrap(errors.Join(sentinel, errors.New("dsn=postgres://u:pw@h/d")))

	if !errors.Is(wrapped, sentinel) {
		t.Error("errors.Is broken by Wrap")
	}
	if strings.Contains(wrapped.Error(), "pw@") {
		t.Errorf("secret leaked through Wrap: %s", wrapped.Error())
	}
}

func TestMapRedactsSecretKeys(t *testing.T) {
	in := map[string]string{
		"host":     "db.internal",
		"password": "hunter2",
		"sslmode":  "verify-full",
		"apiKey":   "sk-123",
	}
	got := Map(in)

	if got["host"] != "db.internal" || got["sslmode"] != "verify-full" {
		t.Errorf("redacted a non-secret: %#v", got)
	}
	if got["password"] != Mask || got["apiKey"] != Mask {
		t.Errorf("failed to redact a secret: %#v", got)
	}
}

func TestValueDistinguishesEmpty(t *testing.T) {
	// "the password was empty" is frequently the actual bug, so it survives.
	if Value("") != "[empty]" {
		t.Error("empty secret should be reported as empty")
	}
	if Value("x") != Mask {
		t.Error("non-empty secret should be masked")
	}
}

func TestIsSecretKey(t *testing.T) {
	secret := []string{"password", "PWD", "api_key", "sasl.jaas.config", "privateKey"}
	safe := []string{"host", "port", "database", "sslmode", "user"}

	for _, k := range secret {
		if !IsSecretKey(k) {
			t.Errorf("%q should be treated as secret", k)
		}
	}
	for _, k := range safe {
		if IsSecretKey(k) {
			t.Errorf("%q should not be treated as secret", k)
		}
	}
}

func TestIsSecretKeyKnowsWhatIsNotOne(t *testing.T) {
	for _, k := range []string{"password", "PASSWD", "api_key", "auth_token", "authToken", "credential"} {
		if !IsSecretKey(k) {
			t.Errorf("%q is not taken for a secret", k)
		}
	}
	// A MongoDB connection's authSource is a database's name, and its
	// mechanism is SCRAM-SHA-256: neither is a secret, and redacting them
	// would lose the connection's settings.
	for _, k := range []string{"authSource", "authmechanism", "authDatabase"} {
		if IsSecretKey(k) {
			t.Errorf("%q is taken for a secret", k)
		}
	}
}
