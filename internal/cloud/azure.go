package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
)

// Azure Database for PostgreSQL and MySQL take a Microsoft Entra access token
// as the password, issued for the open-source relational database resource
// rather than for Azure as a whole.
const (
	azureDefaultResource  = "https://ossrdbms-aad.database.windows.net"
	azureDefaultAuthority = "https://login.microsoftonline.com"
	azureIMDS             = "http://169.254.169.254/metadata/identity/oauth2/token"
)

// azureToken mints an Entra access token from whichever identity this
// machine holds: a registered application in the environment, a managed
// identity given to the host, or whoever is signed in to the Azure CLI.
func azureToken(ctx context.Context, params map[string]string) (string, error) {
	resource := strings.TrimSpace(params["resource"])
	if resource == "" {
		resource = azureDefaultResource
	}
	if strings.HasPrefix(resource, "-") {
		// The resource reaches a command line further down, where a leading
		// dash would be read as a flag rather than a value.
		return "", fmt.Errorf("%q is not a resource: a resource is a URL", resource)
	}

	if tenant, id, secret := azureApp(params); secret != "" {
		return azureAppToken(ctx, params, tenant, id, secret, resource)
	}
	if token, err := azureManagedIdentity(ctx, params, resource); err == nil {
		return token, nil
	} else if !errors.Is(err, errNoManagedIdentity) {
		return "", err
	}
	return azureCLIToken(ctx, params, resource)
}

func azureApp(params map[string]string) (tenant, id, secret string) {
	tenant = strings.TrimSpace(params["tenant"])
	if tenant == "" {
		tenant = os.Getenv("AZURE_TENANT_ID")
	}
	id = strings.TrimSpace(params["client_id"])
	if id == "" {
		id = os.Getenv("AZURE_CLIENT_ID")
	}
	// The secret is read from the environment and never from the settings
	// file, which is written in plain sight (FR-1.5).
	return tenant, id, os.Getenv("AZURE_CLIENT_SECRET")
}

// azureAppToken signs in as a registered application.
func azureAppToken(ctx context.Context, params map[string]string, tenant, id, secret, resource string) (string, error) {
	if tenant == "" || id == "" {
		return "", errors.New("AZURE_CLIENT_SECRET is set but the application it belongs to is not: " +
			"AZURE_TENANT_ID and AZURE_CLIENT_ID are both needed with it")
	}
	authority := strings.TrimSpace(params["authority"])
	if authority == "" {
		authority = os.Getenv("AZURE_AUTHORITY_HOST")
	}
	if authority == "" {
		authority = azureDefaultAuthority
	}
	uri := strings.TrimSuffix(authority, "/") + "/" + tenant + "/oauth2/v2.0/token"

	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {id},
		"client_secret": {secret},
		// The v2.0 endpoint takes a scope, which for a whole resource is the
		// resource with /.default after it.
		"scope": {strings.TrimSuffix(resource, "/") + "/.default"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uri, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("the authority %s is not an address: %w", uri, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var got struct {
		AccessToken string `json:"access_token"`
	}
	if err := fetchJSON(req, &got); err != nil {
		return "", fmt.Errorf("Entra would not issue an access token: %w", err)
	}
	if got.AccessToken == "" {
		return "", errors.New("Entra answered without an access token in it")
	}
	return got.AccessToken, nil
}

// errNoManagedIdentity says there is no managed identity here to ask, as
// opposed to one that was asked and refused. Only the first is worth falling
// through from: a managed identity that exists and says no is an answer.
var errNoManagedIdentity = errors.New("no managed identity")

func azureManagedIdentity(ctx context.Context, params map[string]string, resource string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, metadataTimeout)
	defer cancel()

	var uri string
	var header, headerValue string
	if endpoint := os.Getenv("IDENTITY_ENDPOINT"); endpoint != "" {
		// App Service, Container Apps and Functions, which put the endpoint
		// and a header in the environment rather than using a fixed address.
		secret := os.Getenv("IDENTITY_HEADER")
		if secret == "" {
			return "", errors.New("IDENTITY_ENDPOINT is set without IDENTITY_HEADER, so the managed identity cannot be asked")
		}
		uri = endpoint + "?api-version=2019-08-01&resource=" + url.QueryEscape(resource)
		header, headerValue = "X-IDENTITY-HEADER", secret
	} else {
		uri = azureIMDS + "?api-version=2018-02-01&resource=" + url.QueryEscape(resource)
		header, headerValue = "Metadata", "true"
	}
	if id := strings.TrimSpace(params["client_id"]); id != "" {
		// Where a host has more than one managed identity, which one is
		// being asked for has to be said.
		uri += "&client_id=" + url.QueryEscape(id)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return "", fmt.Errorf("the managed identity address is not one: %w", err)
	}
	req.Header.Set(header, headerValue)
	var got struct {
		AccessToken string `json:"access_token"`
	}
	if err := fetchJSON(req, &got); err != nil {
		return "", fmt.Errorf("%w: %v", errNoManagedIdentity, err)
	}
	if got.AccessToken == "" {
		return "", fmt.Errorf("%w: it answered without an access token in it", errNoManagedIdentity)
	}
	return got.AccessToken, nil
}

// azureCLIToken borrows whoever is signed in to the Azure CLI, which on a
// developer's own machine is usually the only identity there is.
//
// This runs a program, which nothing else in this package does. It is how
// Microsoft's own libraries reach the same identity, and the alternative is
// telling somebody to paste a token that expires in an hour.
func azureCLIToken(ctx context.Context, params map[string]string, resource string) (string, error) {
	az, err := exec.LookPath("az")
	if err != nil {
		return "", errors.New("no Azure identity was found: there is no application in the environment " +
			"(AZURE_TENANT_ID, AZURE_CLIENT_ID and AZURE_CLIENT_SECRET), no managed identity on this host, " +
			"and the Azure CLI is not installed to borrow a signed-in one from")
	}
	args := []string{"account", "get-access-token", "--resource", resource, "--output", "json"}
	if tenant := strings.TrimSpace(params["tenant"]); tenant != "" && !strings.HasPrefix(tenant, "-") {
		args = append(args, "--tenant", tenant)
	}
	out, err := exec.CommandContext(ctx, az, args...).Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return "", fmt.Errorf("the Azure CLI would not give up a token, which usually means nobody is "+
				"signed in — try `az login`: %s", firstLine(string(exit.Stderr)))
		}
		return "", fmt.Errorf("the Azure CLI could not be run: %w", err)
	}
	var got struct {
		AccessToken string `json:"accessToken"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		return "", fmt.Errorf("the Azure CLI answered with something that is not a token: %w", err)
	}
	if got.AccessToken == "" {
		return "", errors.New("the Azure CLI answered without an access token in it")
	}
	return got.AccessToken, nil
}
