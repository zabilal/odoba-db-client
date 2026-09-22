package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// anAWSIdentity puts credentials in the environment, which is the first place
// the chain looks, so that nothing here reaches a file or a network.
func anAWSIdentity(t *testing.T) {
	t.Helper()
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIDEXAMPLE")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
}

const rdsHost = "db.123456789012.us-west-2.rds.amazonaws.com"

func cloudConfig() source.ConnectionConfig {
	return source.ConnectionConfig{
		Host: rdsHost, Port: 5432, User: "jane_doe",
		Cloud: &source.CloudConfig{Provider: "aws"},
	}
}

func TestAConnectionWithNoCloudIdentityAsksForNone(t *testing.T) {
	asked := 0
	cfg := source.ConnectionConfig{
		Host: "localhost", Port: 5432,
		Secret: func(key string) (string, error) { asked++; return "from-the-keychain", nil },
	}
	got := withCloudToken(context.Background(), cfg)

	pw, err := got.Secret("password")
	if err != nil {
		t.Fatalf("no password: %v", err)
	}
	if pw != "from-the-keychain" || asked != 1 {
		t.Errorf("the password is %q after %d lookups, and the keychain should have answered it", pw, asked)
	}
}

// The trap this arrangement exists to avoid. A tunnel rewrites the host and
// port to its own local end, and an AWS token is signed over the endpoint it
// may be spent at. Minted after the rewrite, the token would be signed for
// 127.0.0.1 and refused by the database it was meant for — while looking,
// from here, exactly like a token.
func TestATokenIsSignedForTheDatabaseAndNotForTheEndOfATunnel(t *testing.T) {
	anAWSIdentity(t)
	cfg := withCloudToken(context.Background(), cloudConfig())

	// What throughTunnel does next.
	cfg.Host, cfg.Port = "127.0.0.1", 54321

	token, err := cfg.Secret("password")
	if err != nil {
		t.Fatalf("no token: %v", err)
	}
	if !strings.HasPrefix(token, rdsHost+":5432/?Action=connect&DBUser=jane_doe&") {
		t.Errorf("the token is signed for\n\t%s", token)
	}
	if strings.Contains(token, "127.0.0.1") || strings.Contains(token, "54321") {
		t.Errorf("the token followed the host to the end of the tunnel:\n\t%s", token)
	}
}

func TestOnlyThePasswordIsMintedAndTheRestStillComesFromTheKeychain(t *testing.T) {
	anAWSIdentity(t)
	cfg := cloudConfig()
	cfg.Secret = func(key string) (string, error) {
		if key == "password" {
			return "the-password-nobody-should-use", nil
		}
		return "kept-for-" + key, nil
	}
	got := withCloudToken(context.Background(), cfg)

	token, err := got.Secret("password")
	if err != nil {
		t.Fatalf("no token: %v", err)
	}
	if !strings.HasPrefix(token, rdsHost+":5432/") {
		t.Errorf("the password is %q, and should have been minted", token)
	}
	// A cloud connection can still be carried through a tunnel, and the
	// tunnel's own secrets are not the database's.
	for _, key := range []string{"ssh_passphrase", "ssh_password", "registrypassword"} {
		v, err := got.Secret(key)
		if err != nil || v != "kept-for-"+key {
			t.Errorf("%s came back as %q, %v", key, v, err)
		}
	}
}

func TestACloudConnectionWithNothingBehindItIsNotACrash(t *testing.T) {
	anAWSIdentity(t)
	cfg := cloudConfig() // Secret is nil, as it is for a connection never saved
	got := withCloudToken(context.Background(), cfg)

	if v, err := got.Secret("ssh_passphrase"); err != nil || v != "" {
		t.Errorf("an unasked secret came back as %q, %v", v, err)
	}
	if _, err := got.Secret("password"); err != nil {
		t.Errorf("the token could not be minted: %v", err)
	}
}

func TestACloudThatCannotMintSaysSoWhereThePasswordWouldBe(t *testing.T) {
	anAWSIdentity(t)
	cfg := cloudConfig()
	cfg.Cloud = &source.CloudConfig{Provider: "nowhere"}
	got := withCloudToken(context.Background(), cfg)

	v, err := got.Secret("password")
	if err == nil {
		t.Fatalf("it minted %q from a provider that does not exist", v)
	}
	if !strings.Contains(err.Error(), "none of those") {
		t.Errorf("it said %v", err)
	}
	if v != "" {
		t.Errorf("it gave a password as well as an error: %q", v)
	}
}

func TestTheConnectContextEndingDoesNotStopALaterReconnectMinting(t *testing.T) {
	// Google's path is used here and not AWS's on purpose. Minting an AWS
	// token is local arithmetic that never consults a context at all, so a
	// test written against it would pass whether the context was honoured or
	// ignored. This one reaches a metadata service, which is the case where
	// a cancelled context actually decides something.
	noGoogleCredentialsFile(t)
	metadata := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"access_token":"a-token-for-the-reconnection"}`))
	}))
	defer metadata.Close()
	t.Setenv("GCE_METADATA_HOST", strings.TrimPrefix(metadata.URL, "http://"))

	cfg := source.ConnectionConfig{
		Host: "10.0.0.1", Port: 5432, User: "robot@project.iam",
		Cloud: &source.CloudConfig{Provider: "gcp"},
	}
	ctx, cancel := context.WithCancel(context.Background())
	got := withCloudToken(ctx, cfg)

	// Whoever opened the connection has stopped waiting; a pool reconnecting
	// an hour later still needs a password.
	cancel()

	token, err := got.Secret("password")
	if err != nil {
		t.Fatalf("a reconnection could not be authenticated: %v", err)
	}
	if token != "a-token-for-the-reconnection" {
		t.Errorf("the token is %q", token)
	}
}

// noGoogleCredentialsFile makes sure the search reaches the metadata service
// rather than stopping at a file this machine happens to have.
func noGoogleCredentialsFile(t *testing.T) {
	t.Helper()
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "")
	os.Unsetenv("GOOGLE_APPLICATION_CREDENTIALS")
	t.Setenv("CLOUDSDK_CONFIG", t.TempDir())
}
