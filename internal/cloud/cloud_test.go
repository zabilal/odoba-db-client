package cloud

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestATokenComesFromAProviderThatExists(t *testing.T) {
	noAmbientIdentity(t)
	for _, provider := range []string{"", "ibm", "AWS "} {
		_, err := Token(context.Background(), Config{Provider: provider}, rdsTarget)
		if err == nil {
			t.Fatalf("%q minted a token", provider)
		}
		if provider == "AWS " {
			// Trimmed and lowercased: a provider is a setting somebody may
			// have typed, not a constant in this program.
			if strings.Contains(err.Error(), "none of those") {
				t.Errorf("%q was not recognised", provider)
			}
			continue
		}
		if !strings.Contains(err.Error(), "none of those") {
			t.Errorf("%q said %v", provider, err)
		}
	}
}

func TestEveryConnectionMintsItsOwnToken(t *testing.T) {
	noAmbientIdentity(t)
	asked := 0
	metadata := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked++
		w.Write([]byte(`{"access_token":"a-token"}`))
	}))
	defer metadata.Close()
	t.Setenv("GCE_METADATA_HOST", strings.TrimPrefix(metadata.URL, "http://"))

	target := Target{Host: "10.0.0.1", Port: 5432, User: "robot@p.iam"}
	for i := 0; i < 3; i++ {
		if _, err := Token(context.Background(), Config{Provider: GCP}, target); err != nil {
			t.Fatalf("no token: %v", err)
		}
	}
	// Nothing is cached. Every one of these expires, and a token held over
	// from earlier is a login failure with no visible cause.
	if asked != 3 {
		t.Errorf("three connections asked for %d tokens", asked)
	}
}

func TestCredentialsAreNotFollowedToWhereverARedirectPoints(t *testing.T) {
	noAmbientIdentity(t)
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"access_token":"a-token-from-somewhere-else"}`))
	}))
	defer elsewhere.Close()
	metadata := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL, http.StatusFound)
	}))
	defer metadata.Close()
	t.Setenv("GCE_METADATA_HOST", strings.TrimPrefix(metadata.URL, "http://"))

	token, err := Token(context.Background(), Config{Provider: GCP},
		Target{Host: "10.0.0.1", Port: 5432, User: "robot@p.iam"})
	if err == nil {
		t.Fatalf("it followed a redirect and came back with %q", token)
	}
	if !strings.Contains(err.Error(), "refused to follow a redirect") {
		t.Errorf("it said %v", err)
	}
}

func TestARefusalIsKeptToOneLine(t *testing.T) {
	noAmbientIdentity(t)
	metadata := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("<html>\n<head><title>403</title></head>\n<body>the page goes on</body>\n</html>"))
	}))
	defer metadata.Close()
	t.Setenv("GCE_METADATA_HOST", strings.TrimPrefix(metadata.URL, "http://"))

	_, err := Token(context.Background(), Config{Provider: GCP},
		Target{Host: "10.0.0.1", Port: 5432, User: "robot@p.iam"})
	if err == nil {
		t.Fatal("a refusal was taken for a token")
	}
	if strings.Count(err.Error(), "\n") != 0 {
		t.Errorf("the refusal runs over several lines:\n%v", err)
	}
	if !strings.Contains(err.Error(), "403 Forbidden") {
		t.Errorf("the refusal does not say what was refused: %v", err)
	}
}
