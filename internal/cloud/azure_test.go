package cloud

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// noManagedIdentity points the managed-identity step at something that
// refuses at once, so a test of what comes after it does not spend two
// seconds waiting for an address that is not on this network.
func noManagedIdentity(t *testing.T) {
	t.Helper()
	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closed.Close()
	t.Setenv("IDENTITY_ENDPOINT", closed.URL)
	t.Setenv("IDENTITY_HEADER", "unused")
}

func azureTarget() Target {
	return Target{Host: "server.postgres.database.azure.com", Port: 5432, User: "someone@example.com"}
}

func TestARegisteredApplicationSignsInWithItsSecret(t *testing.T) {
	noAmbientIdentity(t)
	var path string
	var form url.Values
	entra := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		path, form = r.URL.Path, r.Form
		w.Write([]byte(`{"access_token":"an-entra-token","token_type":"Bearer"}`))
	}))
	defer entra.Close()

	t.Setenv("AZURE_TENANT_ID", "a-tenant")
	t.Setenv("AZURE_CLIENT_ID", "an-app")
	t.Setenv("AZURE_CLIENT_SECRET", "a-secret")

	token, err := Token(context.Background(),
		Config{Provider: Azure, Params: map[string]string{"authority": entra.URL}}, azureTarget())
	if err != nil {
		t.Fatalf("no token: %v", err)
	}
	if token != "an-entra-token" {
		t.Errorf("the token is %q", token)
	}
	if want := "/a-tenant/oauth2/v2.0/token"; path != want {
		t.Errorf("it asked %s, want %s", path, want)
	}
	if form.Get("grant_type") != "client_credentials" || form.Get("client_id") != "an-app" ||
		form.Get("client_secret") != "a-secret" {
		t.Errorf("it asked with %v", form)
	}
	// The v2.0 endpoint wants a scope, and a whole resource is named by
	// putting /.default after it.
	if want := azureDefaultResource + "/.default"; form.Get("scope") != want {
		t.Errorf("the scope is %q, want %q", form.Get("scope"), want)
	}
}

func TestASecretWithNoApplicationBehindItSaysSo(t *testing.T) {
	noAmbientIdentity(t)
	t.Setenv("AZURE_CLIENT_SECRET", "a-secret")
	_, err := Token(context.Background(), Config{Provider: Azure}, azureTarget())
	if err == nil || !strings.Contains(err.Error(), "AZURE_TENANT_ID and AZURE_CLIENT_ID are both needed") {
		t.Fatalf("it said %v", err)
	}
}

func TestAHostWithAManagedIdentityOfItsOwn(t *testing.T) {
	noAmbientIdentity(t)
	var header, resource, which string
	identity := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header = r.Header.Get("X-IDENTITY-HEADER")
		resource = r.URL.Query().Get("resource")
		which = r.URL.Query().Get("client_id")
		w.Write([]byte(`{"access_token":"the-hosts-token"}`))
	}))
	defer identity.Close()
	t.Setenv("IDENTITY_ENDPOINT", identity.URL)
	t.Setenv("IDENTITY_HEADER", "a-shared-secret")

	token, err := Token(context.Background(),
		Config{Provider: Azure, Params: map[string]string{"client_id": "the-second-identity"}}, azureTarget())
	if err != nil {
		t.Fatalf("no token: %v", err)
	}
	if token != "the-hosts-token" {
		t.Errorf("the token is %q", token)
	}
	if header != "a-shared-secret" {
		t.Errorf("the identity endpoint was asked without its header: %q", header)
	}
	if resource != azureDefaultResource {
		t.Errorf("it asked for resource %q", resource)
	}
	// A host may hold more than one, and then which one has to be said.
	if which != "the-second-identity" {
		t.Errorf("it did not say which identity: %q", which)
	}
}

func TestAnIdentityEndpointWithNoHeaderIsAnErrorNotAFallThrough(t *testing.T) {
	noAmbientIdentity(t)
	t.Setenv("IDENTITY_ENDPOINT", "http://127.0.0.1:1")
	_, err := Token(context.Background(), Config{Provider: Azure}, azureTarget())
	if err == nil || !strings.Contains(err.Error(), "without IDENTITY_HEADER") {
		t.Fatalf("it said %v", err)
	}
}

// stubAZ puts a program called az on the path, so that the way this runs the
// Azure CLI is proved rather than assumed. It is not the real CLI, which is
// not installed here; what it proves is the invocation and what is made of
// the answer.
func stubAZ(t *testing.T, script string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stub is a shell script")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "az")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

func TestWhoeverIsSignedInToTheAzureCLI(t *testing.T) {
	noAmbientIdentity(t)
	noManagedIdentity(t)
	args := filepath.Join(t.TempDir(), "args")
	stubAZ(t, `echo "$@" > `+args+`
echo '{"accessToken":"the-signed-in-token","expiresOn":"2026-01-01 00:00:00"}'`)

	token, err := Token(context.Background(),
		Config{Provider: Azure, Params: map[string]string{"tenant": "a-tenant"}}, azureTarget())
	if err != nil {
		t.Fatalf("no token: %v", err)
	}
	if token != "the-signed-in-token" {
		t.Errorf("the token is %q", token)
	}
	got, err := os.ReadFile(args)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"account get-access-token",
		"--resource " + azureDefaultResource,
		"--tenant a-tenant",
		"--output json",
	} {
		if !strings.Contains(string(got), want) {
			t.Errorf("it ran az %s, which does not contain %q", strings.TrimSpace(string(got)), want)
		}
	}
}

func TestTheAzureCLIRefusingIsSaidAsNobodyBeingSignedIn(t *testing.T) {
	noAmbientIdentity(t)
	noManagedIdentity(t)
	stubAZ(t, `echo "Please run 'az login' to setup account." >&2; exit 1`)

	_, err := Token(context.Background(), Config{Provider: Azure}, azureTarget())
	if err == nil || !strings.Contains(err.Error(), "az login") {
		t.Fatalf("it said %v", err)
	}
	if !strings.Contains(err.Error(), "Please run") {
		t.Errorf("the CLI's own account of it was dropped: %v", err)
	}
}

func TestWithNoAzureIdentityAnywhereItNamesWhatWasTried(t *testing.T) {
	noAmbientIdentity(t)
	noManagedIdentity(t)
	t.Setenv("PATH", t.TempDir())

	_, err := Token(context.Background(), Config{Provider: Azure}, azureTarget())
	if err == nil {
		t.Fatal("it found an identity that is not there")
	}
	for _, want := range []string{"AZURE_CLIENT_SECRET", "managed identity", "Azure CLI"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("it said %v, which does not mention %q", err, want)
		}
	}
}

func TestAResourceThatWouldBeReadAsAFlagIsRefused(t *testing.T) {
	noAmbientIdentity(t)
	_, err := Token(context.Background(),
		Config{Provider: Azure, Params: map[string]string{"resource": "--query"}}, azureTarget())
	if err == nil || !strings.Contains(err.Error(), "a resource is a URL") {
		t.Fatalf("it said %v", err)
	}
}
