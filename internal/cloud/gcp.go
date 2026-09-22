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
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Cloud SQL's IAM database authentication takes an OAuth 2.0 access token as
// the password, and the account's email address — a service account's
// without its .gserviceaccount.com suffix — as the user.
//
// The scope is a setting rather than a constant because Google documents this
// as "the Cloud SQL Admin API scope" without always writing the URL, and an
// installation that needs the broader cloud-platform scope should not need a
// new build to say so.
const (
	gcpDefaultScope    = "https://www.googleapis.com/auth/sqlservice.login"
	gcpDefaultTokenURI = "https://oauth2.googleapis.com/token"
	gcpJWTLifetime     = time.Hour
)

// gcpToken mints an access token from application default credentials: the
// file GOOGLE_APPLICATION_CREDENTIALS names, the one gcloud writes when
// somebody signs in, or the identity the machine itself was given.
//
// No target is passed. Unlike an AWS token, this one is not signed over the
// endpoint — it is a bearer token for an account, and what limits it is its
// scope and how long it lasts.
func gcpToken(ctx context.Context, params map[string]string) (string, error) {
	scope := strings.TrimSpace(params["scope"])
	if scope == "" {
		scope = gcpDefaultScope
	}

	path, err := gcpCredentialsFile(params)
	if err != nil {
		return "", err
	}
	if path == "" {
		return gcpMetadataToken(ctx, scope)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("the application default credentials at %s could not be read: %w", path, err)
	}
	var file gcpCredentialsJSON
	if err := json.Unmarshal(raw, &file); err != nil {
		return "", fmt.Errorf("%s is not application default credentials: %w", path, err)
	}

	switch file.Type {
	case "service_account":
		return gcpServiceAccountToken(ctx, file, scope, path)
	case "authorized_user":
		return gcpRefreshedToken(ctx, file, path)
	case "external_account":
		return "", fmt.Errorf("%s is workload identity federation, which this cannot do; "+
			"run `gcloud auth application-default login` to write credentials of your own, "+
			"or point GOOGLE_APPLICATION_CREDENTIALS at a service account key", path)
	case "impersonated_service_account":
		return "", fmt.Errorf("%s impersonates another service account, which this cannot do; "+
			"use credentials for the account itself", path)
	}
	return "", fmt.Errorf("%s is credentials of a kind this does not know: %q", path, file.Type)
}

// gcpCredentialsJSON is the part of an ADC file that matters here. The file
// holds a private key, so nothing in this package writes it anywhere.
type gcpCredentialsJSON struct {
	Type         string `json:"type"`
	ClientEmail  string `json:"client_email"`
	PrivateKey   string `json:"private_key"`
	PrivateKeyID string `json:"private_key_id"`
	TokenURI     string `json:"token_uri"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	RefreshToken string `json:"refresh_token"`
}

func (f gcpCredentialsJSON) tokenURI() string {
	if f.TokenURI != "" {
		return f.TokenURI
	}
	return gcpDefaultTokenURI
}

// gcpCredentialsFile finds the credentials file, or answers empty where there
// is none and the metadata service is the remaining way in.
func gcpCredentialsFile(params map[string]string) (string, error) {
	if p := strings.TrimSpace(params["credentials_file"]); p != "" {
		return p, nil
	}
	if p := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); p != "" {
		if _, err := os.Stat(p); err != nil {
			return "", fmt.Errorf("GOOGLE_APPLICATION_CREDENTIALS names %s, which cannot be read: %w", p, err)
		}
		return p, nil
	}
	if p := gcpWellKnownFile(); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", nil
}

// gcpWellKnownFile is where gcloud writes credentials when somebody runs
// `gcloud auth application-default login`.
func gcpWellKnownFile() string {
	const name = "application_default_credentials.json"
	if runtime.GOOS == "windows" {
		if dir := os.Getenv("APPDATA"); dir != "" {
			return filepath.Join(dir, "gcloud", name)
		}
		return ""
	}
	if dir := os.Getenv("CLOUDSDK_CONFIG"); dir != "" {
		return filepath.Join(dir, name)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "gcloud", name)
}

// gcpServiceAccountToken signs a claim that this is the service account and
// exchanges it for an access token. The private key signs and is never sent.
func gcpServiceAccountToken(ctx context.Context, file gcpCredentialsJSON, scope, path string) (string, error) {
	if file.ClientEmail == "" || file.PrivateKey == "" {
		return "", fmt.Errorf("%s is a service account key with no %s", path,
			map[bool]string{true: "client_email", false: "private_key"}[file.ClientEmail == ""])
	}
	key, err := rsaKeyOf(file.PrivateKey)
	if err != nil {
		return "", fmt.Errorf("the private key in %s could not be read: %w", path, err)
	}

	now := timeNow().UTC()
	header := map[string]string{"alg": "RS256", "typ": "JWT"}
	if file.PrivateKeyID != "" {
		header["kid"] = file.PrivateKeyID
	}
	claims := map[string]any{
		"iss":   file.ClientEmail,
		"scope": scope,
		"aud":   file.tokenURI(),
		"iat":   now.Unix(),
		"exp":   now.Add(gcpJWTLifetime).Unix(),
	}
	assertion, err := signedJWT(header, claims, key)
	if err != nil {
		return "", err
	}

	return gcpExchange(ctx, file.tokenURI(), url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {assertion},
	})
}

// gcpRefreshedToken spends the refresh token gcloud left behind. Its scopes
// were fixed when somebody signed in, so none is asked for here.
func gcpRefreshedToken(ctx context.Context, file gcpCredentialsJSON, path string) (string, error) {
	if file.RefreshToken == "" || file.ClientID == "" {
		return "", fmt.Errorf("%s is a signed-in user with no refresh token; "+
			"run `gcloud auth application-default login` again", path)
	}
	return gcpExchange(ctx, file.tokenURI(), url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {file.RefreshToken},
		"client_id":     {file.ClientID},
		"client_secret": {file.ClientSecret},
	})
}

func gcpExchange(ctx context.Context, uri string, form url.Values) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uri, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("the token endpoint %s is not an address: %w", uri, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var got struct {
		AccessToken string `json:"access_token"`
	}
	if err := fetchJSON(req, &got); err != nil {
		return "", fmt.Errorf("Google would not issue an access token: %w", err)
	}
	if got.AccessToken == "" {
		return "", errors.New("Google answered without an access token in it")
	}
	return got.AccessToken, nil
}

// gcpMetadataToken asks the identity a Compute Engine instance, a Cloud Run
// service or a GKE pod was given.
func gcpMetadataToken(ctx context.Context, scope string) (string, error) {
	host := os.Getenv("GCE_METADATA_HOST")
	if host == "" {
		host = "metadata.google.internal"
	}
	ctx, cancel := context.WithTimeout(ctx, metadataTimeout)
	defer cancel()

	uri := "http://" + host + "/computeMetadata/v1/instance/service-accounts/default/token" +
		"?scopes=" + url.QueryEscape(scope)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return "", fmt.Errorf("the metadata address is not one: %w", err)
	}
	req.Header.Set("Metadata-Flavor", "Google")
	var got struct {
		AccessToken string `json:"access_token"`
	}
	if err := fetchJSON(req, &got); err != nil {
		return "", fmt.Errorf("no application default credentials were found, and this machine has no "+
			"Google identity of its own: %w", err)
	}
	if got.AccessToken == "" {
		return "", errors.New("the metadata service answered without an access token in it")
	}
	return got.AccessToken, nil
}

// rsaKeyOf reads the PEM private key out of a service account key file.
func rsaKeyOf(text string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(text))
	if block == nil {
		return nil, errors.New("it is not PEM")
	}
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("it is a %T, and a service account key is RSA", key)
		}
		return rsaKey, nil
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("it is neither a PKCS#8 nor a PKCS#1 RSA key")
	}
	return key, nil
}

// signedJWT builds the assertion Google exchanges for an access token.
func signedJWT(header map[string]string, claims map[string]any, key *rsa.PrivateKey) (string, error) {
	h, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	c, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	body := base64url(h) + "." + base64url(c)
	sum := crypto.SHA256.New()
	sum.Write([]byte(body))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum.Sum(nil))
	if err != nil {
		return "", fmt.Errorf("the assertion could not be signed: %w", err)
	}
	return body + "." + base64url(sig), nil
}

func base64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
