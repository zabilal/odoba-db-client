package cloud

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/quick"
)

// serviceAccountKey makes a real RSA key, so that the assertion this package
// signs can be checked the way Google checks it rather than merely looked at.
func serviceAccountKey(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return key, string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

func adcFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "adc.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAServiceAccountSignsAClaimItNeverSends(t *testing.T) {
	noAmbientIdentity(t)
	key, keyPEM := serviceAccountKey(t)

	var assertion, grant string
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		grant = r.Form.Get("grant_type")
		assertion = r.Form.Get("assertion")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"an-access-token","expires_in":3600}`))
	}))
	defer issuer.Close()

	body, err := json.Marshal(map[string]string{
		"type": "service_account", "client_email": "robot@project.iam.gserviceaccount.com",
		"private_key": keyPEM, "private_key_id": "key-1", "token_uri": issuer.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", adcFile(t, string(body)))

	token, err := Token(context.Background(), Config{Provider: GCP},
		Target{Host: "10.0.0.1", Port: 5432, User: "robot@project.iam"})
	if err != nil {
		t.Fatalf("no token: %v", err)
	}
	if token != "an-access-token" {
		t.Errorf("the token is %q", token)
	}
	if grant != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
		t.Errorf("the grant is %q", grant)
	}

	// The private key must never be what is sent.
	if strings.Contains(assertion, "PRIVATE KEY") {
		t.Fatal("the private key was sent to the token endpoint")
	}

	parts := strings.Split(assertion, ".")
	if len(parts) != 3 {
		t.Fatalf("the assertion is not a JWT: %q", assertion)
	}
	var header map[string]string
	var claims map[string]any
	unpack(t, parts[0], &header)
	unpack(t, parts[1], &claims)

	if header["alg"] != "RS256" || header["kid"] != "key-1" {
		t.Errorf("the header is %v", header)
	}
	for _, c := range []struct {
		key  string
		want string
	}{
		{"iss", "robot@project.iam.gserviceaccount.com"},
		{"aud", issuer.URL},
		{"scope", gcpDefaultScope},
	} {
		if got, _ := claims[c.key].(string); got != c.want {
			t.Errorf("%s is %q, want %q", c.key, got, c.want)
		}
	}
	iat, exp := claims["iat"].(float64), claims["exp"].(float64)
	if exp-iat != gcpJWTLifetime.Seconds() {
		t.Errorf("the assertion lasts %v seconds", exp-iat)
	}

	// And it verifies: this is the check Google makes.
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatal(err)
	}
	sum := crypto.SHA256.New()
	sum.Write([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, sum.Sum(nil), sig); err != nil {
		t.Errorf("the assertion does not verify against the key that signed it: %v", err)
	}
}

func unpack(t *testing.T, segment string, into any) {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		t.Fatalf("a JWT segment is not base64url: %v", err)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("a JWT segment is not JSON: %v", err)
	}
}

func TestAnAskedForScopeIsTheOneSigned(t *testing.T) {
	noAmbientIdentity(t)
	_, keyPEM := serviceAccountKey(t)
	var scope string
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		var claims map[string]any
		unpack(t, strings.Split(r.Form.Get("assertion"), ".")[1], &claims)
		scope, _ = claims["scope"].(string)
		w.Write([]byte(`{"access_token":"t"}`))
	}))
	defer issuer.Close()

	body, _ := json.Marshal(map[string]string{
		"type": "service_account", "client_email": "robot@p.iam.gserviceaccount.com",
		"private_key": keyPEM, "token_uri": issuer.URL,
	})
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", adcFile(t, string(body)))

	const wider = "https://www.googleapis.com/auth/cloud-platform"
	if _, err := Token(context.Background(),
		Config{Provider: GCP, Params: map[string]string{"scope": wider}},
		Target{Host: "10.0.0.1", Port: 5432, User: "robot@p.iam"}); err != nil {
		t.Fatalf("no token: %v", err)
	}
	if scope != wider {
		t.Errorf("it asked for %q, want %q", scope, wider)
	}
}

func TestASignedInUserSpendsTheRefreshTokenGcloudLeft(t *testing.T) {
	noAmbientIdentity(t)
	var form map[string]string
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		form = map[string]string{}
		for k := range r.Form {
			form[k] = r.Form.Get(k)
		}
		w.Write([]byte(`{"access_token":"a-users-token"}`))
	}))
	defer issuer.Close()

	body, _ := json.Marshal(map[string]string{
		"type": "authorized_user", "client_id": "an-id.apps.googleusercontent.com",
		"client_secret": "a-secret", "refresh_token": "a-refresh-token", "token_uri": issuer.URL,
	})
	// Written where gcloud writes it, and found without being named.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "application_default_credentials.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLOUDSDK_CONFIG", dir)

	token, err := Token(context.Background(), Config{Provider: GCP},
		Target{Host: "10.0.0.1", Port: 5432, User: "someone@example.com"})
	if err != nil {
		t.Fatalf("no token: %v", err)
	}
	if token != "a-users-token" {
		t.Errorf("the token is %q", token)
	}
	if form["grant_type"] != "refresh_token" || form["refresh_token"] != "a-refresh-token" ||
		form["client_id"] != "an-id.apps.googleusercontent.com" {
		t.Errorf("it asked with %v", form)
	}
}

func TestAMachineWithAGoogleIdentityOfItsOwn(t *testing.T) {
	noAmbientIdentity(t)
	var flavor, scopes string
	metadata := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flavor = r.Header.Get("Metadata-Flavor")
		scopes = r.URL.Query().Get("scopes")
		w.Write([]byte(`{"access_token":"the-machines-token","expires_in":3599}`))
	}))
	defer metadata.Close()
	t.Setenv("GCE_METADATA_HOST", strings.TrimPrefix(metadata.URL, "http://"))

	token, err := Token(context.Background(), Config{Provider: GCP},
		Target{Host: "10.0.0.1", Port: 5432, User: "robot@p.iam"})
	if err != nil {
		t.Fatalf("no token: %v", err)
	}
	if token != "the-machines-token" {
		t.Errorf("the token is %q", token)
	}
	// Without this header the metadata service refuses, because its absence
	// is what a request forged through some other application looks like.
	if flavor != "Google" {
		t.Errorf("the metadata service was asked without Metadata-Flavor: %q", flavor)
	}
	if scopes != gcpDefaultScope {
		t.Errorf("it asked for scopes %q", scopes)
	}
}

func TestWhatAGoogleTokenWillNotEvenTry(t *testing.T) {
	for _, c := range []struct{ what, body, says string }{
		{"federated", `{"type":"external_account","audience":"//iam.googleapis.com/x"}`, "workload identity federation"},
		{"impersonating", `{"type":"impersonated_service_account"}`, "impersonates another service account"},
		{"a kind nobody has heard of", `{"type":"wishful_thinking"}`, "a kind this does not know"},
		{"not JSON at all", `not json`, "is not application default credentials"},
		{"a key with no key", `{"type":"service_account","client_email":"a@b.iam.gserviceaccount.com"}`, "no private_key"},
		{"a key that is not one", `{"type":"service_account","client_email":"a@b","private_key":"-----BEGIN PRIVATE KEY-----\nbm90YWtleQ==\n-----END PRIVATE KEY-----\n"}`, "could not be read"},
		{"a user with no refresh token", `{"type":"authorized_user","client_id":"x"}`, "gcloud auth application-default login"},
	} {
		t.Run(c.what, func(t *testing.T) {
			noAmbientIdentity(t)
			t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", adcFile(t, c.body))
			_, err := Token(context.Background(), Config{Provider: GCP},
				Target{Host: "10.0.0.1", Port: 5432, User: "someone"})
			if err == nil || !strings.Contains(err.Error(), c.says) {
				t.Fatalf("it said %v, wanted something about %q", err, c.says)
			}
		})
	}
}

func TestCredentialsNamedButAbsentAreSaidRatherThanSkipped(t *testing.T) {
	noAmbientIdentity(t)
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", filepath.Join(t.TempDir(), "gone.json"))
	_, err := Token(context.Background(), Config{Provider: GCP},
		Target{Host: "10.0.0.1", Port: 5432, User: "someone"})
	if err == nil || !strings.Contains(err.Error(), "GOOGLE_APPLICATION_CREDENTIALS names") {
		t.Fatalf("it said %v", err)
	}
}

// A JWT segment is base64url without padding, which is not the encoding most
// libraries reach for first.
func TestJWTSegmentsAreUnpaddedBase64URL(t *testing.T) {
	if err := quick.Check(func(b []byte) bool {
		s := base64url(b)
		return !strings.ContainsAny(s, "=+/")
	}, nil); err != nil {
		t.Error(err)
	}
}
