package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// An RDS authentication token is a presigned request for the rds-db:connect
// action, handed over where a password would go. AWS documents it as valid
// for fifteen minutes, which is why nothing here caches one: a token is
// minted for each connection as it is made, and a connection made twenty
// minutes later mints another.
const (
	rdsService = "rds-db"
	rdsExpires = "900"
)

// metadataTimeout bounds every call to a metadata service. On a machine that
// is not in AWS at all, 169.254.169.254 is a black hole rather than a closed
// port, so without this the connection dialog would hang on a network that
// simply drops the packets.
const metadataTimeout = 2 * time.Second

// awsToken mints an RDS IAM authentication token for one database account on
// one endpoint (FR-1.14).
//
// It signs rather than sends: the secret access key never leaves this
// machine, and what the database receives is a signature over the endpoint,
// the user and the minute, which AWS then checks with IAM.
func awsToken(ctx context.Context, params map[string]string, target Target) (string, error) {
	if target.User == "" {
		return "", errors.New("an RDS token is minted for one database account and this connection names no user")
	}
	region, err := awsRegion(params, target.Host)
	if err != nil {
		return "", err
	}
	creds, err := awsCredentials(ctx, params)
	if err != nil {
		return "", err
	}

	at := timeNow().UTC()
	host := net.JoinHostPort(target.Host, strconv.Itoa(target.Port))
	query := []pair{
		{"Action", "connect"},
		{"DBUser", target.User},
		{"X-Amz-Algorithm", algorithm},
		{"X-Amz-Credential", creds.accessKey + "/" + credentialScope(at, region, rdsService)},
		{"X-Amz-Date", at.Format(longDate)},
		{"X-Amz-Expires", rdsExpires},
		{"X-Amz-SignedHeaders", "host"},
	}
	if creds.sessionToken != "" {
		// Temporary credentials carry their session token in the signature
		// rather than beside it, so that it cannot be swapped for another.
		query = append(query, pair{"X-Amz-Security-Token", creds.sessionToken})
	}

	canonicalQ := canonicalQuery(query)
	canonical, _ := canonicalRequest("GET", "/", canonicalQ, []pair{{"host", host}}, sha256Hex(nil))
	sig := signature(
		signingKey(creds.secretKey, at, region, rdsService),
		stringToSign(at, credentialScope(at, region, rdsService), canonical),
	)

	// No scheme: what AWS's own tooling hands to psql is the endpoint, the
	// path and the query, and a token with "https://" on the front is
	// rejected.
	return host + "/?" + canonicalQ + "&X-Amz-Signature=" + sig, nil
}

// awsRegion decides which region's key signs, which is not a free choice: a
// signature scoped to the wrong region is refused by the right one.
//
// The endpoint is asked before the environment because the endpoint is the
// database being connected to, while AWS_REGION is only wherever this shell
// happens to be pointed. An explicit setting still wins over both, for the
// endpoints that are nobody's standard shape.
func awsRegion(params map[string]string, host string) (string, error) {
	if r := strings.TrimSpace(params["region"]); r != "" {
		return r, nil
	}
	if r := regionOfEndpoint(host); r != "" {
		return r, nil
	}
	for _, env := range []string{"AWS_REGION", "AWS_DEFAULT_REGION"} {
		if r := strings.TrimSpace(os.Getenv(env)); r != "" {
			return r, nil
		}
	}
	return "", fmt.Errorf("no region could be determined for %s: it is not an "+
		"*.<region>.rds.amazonaws.com endpoint and neither AWS_REGION nor "+
		"AWS_DEFAULT_REGION is set, so set the region on the connection", host)
}

// regionOfEndpoint reads the region out of an RDS endpoint, which is where it
// is already written down: name.account.<region>.rds.amazonaws.com, and the
// same one label further along for a proxy.
func regionOfEndpoint(host string) string {
	labels := strings.Split(strings.TrimSuffix(strings.ToLower(host), "."), ".")
	for i, l := range labels {
		if l == "rds" && i > 0 {
			return labels[i-1]
		}
	}
	return ""
}

// credentials are what signs. They are never written anywhere by this
// package, and the token they produce carries only the access key id, which
// is an identifier rather than a secret (NFR-S2).
type credentials struct {
	accessKey    string
	secretKey    string
	sessionToken string
}

// awsCredentials finds the identity this machine is already running under,
// in the order the AWS tools themselves look: the environment, then the
// shared credentials file, then the metadata service. Nothing here asks a
// person for anything, which is the whole point of FR-1.14 — a credential
// somebody typed belongs in the keychain instead (FR-1.5).
func awsCredentials(ctx context.Context, params map[string]string) (credentials, error) {
	if c, ok := awsEnvCredentials(); ok {
		return c, nil
	}
	profile := strings.TrimSpace(params["profile"])
	if profile == "" {
		profile = os.Getenv("AWS_PROFILE")
	}
	if profile == "" {
		profile = "default"
	}
	c, err := awsFileCredentials(profile)
	if err != nil {
		return credentials{}, err
	}
	if c.accessKey != "" {
		return c, nil
	}
	return awsMetadataCredentials(ctx)
}

func awsEnvCredentials() (credentials, bool) {
	id := os.Getenv("AWS_ACCESS_KEY_ID")
	secret := os.Getenv("AWS_SECRET_ACCESS_KEY")
	if id == "" || secret == "" {
		return credentials{}, false
	}
	return credentials{id, secret, os.Getenv("AWS_SESSION_TOKEN")}, true
}

// awsFileCredentials reads one profile out of the shared credentials file.
//
// A profile that exists but keeps its credentials somewhere this cannot
// follow — single sign-on, a role to assume, an external process — is said
// so by name. That is the difference between being told what to fix and
// watching a connection attempt time out against a metadata service that was
// never going to answer.
func awsFileCredentials(profile string) (credentials, error) {
	path := os.Getenv("AWS_SHARED_CREDENTIALS_FILE")
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return credentials{}, nil
		}
		path = filepath.Join(home, ".aws", "credentials")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return credentials{}, nil // no file is not an error; there are other ways in
	}
	section, ok := iniSection(string(raw), profile)
	if !ok {
		return credentials{}, nil
	}
	c := credentials{
		accessKey:    section["aws_access_key_id"],
		secretKey:    section["aws_secret_access_key"],
		sessionToken: section["aws_session_token"],
	}
	if c.accessKey != "" && c.secretKey == "" {
		return credentials{}, fmt.Errorf("the profile %q in %s has an access key id and no secret access key", profile, path)
	}
	if c.accessKey != "" {
		return c, nil
	}
	for key, what := range map[string]string{
		"sso_start_url":      "single sign-on",
		"sso_session":        "single sign-on",
		"role_arn":           "a role to assume",
		"credential_process": "an external credential process",
	} {
		if section[key] != "" {
			return credentials{}, fmt.Errorf("the profile %q in %s signs in with %s, which this cannot do; "+
				"run the AWS CLI to refresh it and export the credentials, or use a profile holding keys directly",
				profile, path, what)
		}
	}
	return credentials{}, nil
}

// iniSection reads one [section] out of an AWS configuration file. This is
// not a general INI parser and does not want to be: it reads the four keys
// that matter, ignores comments, and leaves everything else alone.
func iniSection(text, want string) (map[string]string, bool) {
	found := false
	out := map[string]string{}
	in := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			name := strings.TrimSpace(line[1 : len(line)-1])
			// The config file writes "[profile x]" where the credentials
			// file writes "[x]", and both are read here.
			name = strings.TrimSpace(strings.TrimPrefix(name, "profile "))
			in = name == want
			found = found || in
			continue
		}
		if !in {
			continue
		}
		if key, value, ok := strings.Cut(line, "="); ok {
			out[strings.ToLower(strings.TrimSpace(key))] = strings.TrimSpace(value)
		}
	}
	return out, found
}

// awsMetadataCredentials asks the identity the machine itself was given: a
// task role in ECS, or an instance profile on EC2.
func awsMetadataCredentials(ctx context.Context) (credentials, error) {
	if strings.EqualFold(os.Getenv("AWS_EC2_METADATA_DISABLED"), "true") {
		return credentials{}, errors.New("no AWS credentials were found, and the metadata service is " +
			"switched off by AWS_EC2_METADATA_DISABLED")
	}
	if uri := containerCredentialsURI(); uri != "" {
		return containerCredentials(ctx, uri)
	}
	return instanceCredentials(ctx)
}

func containerCredentialsURI() string {
	if full := os.Getenv("AWS_CONTAINER_CREDENTIALS_FULL_URI"); full != "" {
		return full
	}
	if rel := os.Getenv("AWS_CONTAINER_CREDENTIALS_RELATIVE_URI"); rel != "" {
		return "http://169.254.170.2" + rel
	}
	return ""
}

// metadataCredentials is the shape both metadata services answer in.
type metadataCredentials struct {
	AccessKeyID     string `json:"AccessKeyId"`
	SecretAccessKey string `json:"SecretAccessKey"`
	Token           string `json:"Token"`
}

func containerCredentials(ctx context.Context, uri string) (credentials, error) {
	ctx, cancel := context.WithTimeout(ctx, metadataTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return credentials{}, fmt.Errorf("the container credentials address is not one: %w", err)
	}
	if tok := os.Getenv("AWS_CONTAINER_AUTHORIZATION_TOKEN"); tok != "" {
		req.Header.Set("Authorization", tok)
	}
	var got metadataCredentials
	if err := fetchJSON(req, &got); err != nil {
		return credentials{}, fmt.Errorf("the container's credentials could not be read: %w", err)
	}
	return asCredentials(got, "the container's credentials")
}

func instanceCredentials(ctx context.Context) (credentials, error) {
	ctx, cancel := context.WithTimeout(ctx, metadataTimeout)
	defer cancel()
	base := os.Getenv("AWS_EC2_METADATA_SERVICE_ENDPOINT")
	if base == "" {
		base = "http://169.254.169.254"
	}
	base = strings.TrimSuffix(base, "/")

	// IMDSv2 first, and only IMDSv2: the older unauthenticated version is
	// what made instance credentials reachable through a request-forgery bug
	// in an application running on the instance.
	tokenReq, err := http.NewRequestWithContext(ctx, http.MethodPut, base+"/latest/api/token", nil)
	if err != nil {
		return credentials{}, fmt.Errorf("the metadata address is not one: %w", err)
	}
	tokenReq.Header.Set("X-aws-ec2-metadata-token-ttl-seconds", "21600")
	session, err := fetchText(tokenReq)
	if err != nil {
		return credentials{}, fmt.Errorf("no AWS credentials were found in the environment, in the shared "+
			"credentials file, or from the instance metadata service: %w", err)
	}

	role, err := fetchWithSession(ctx, base+"/latest/meta-data/iam/security-credentials/", session)
	if err != nil {
		return credentials{}, fmt.Errorf("this instance has no role to borrow an identity from: %w", err)
	}
	role = strings.TrimSpace(strings.SplitN(role, "\n", 2)[0])
	if role == "" {
		return credentials{}, errors.New("this instance has no role to borrow an identity from")
	}

	body, err := fetchWithSession(ctx, base+"/latest/meta-data/iam/security-credentials/"+role, session)
	if err != nil {
		return credentials{}, fmt.Errorf("the role %s would not give up its credentials: %w", role, err)
	}
	var got metadataCredentials
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		return credentials{}, fmt.Errorf("the role %s answered with something that is not credentials: %w", role, err)
	}
	return asCredentials(got, "the role "+role)
}

func asCredentials(m metadataCredentials, whose string) (credentials, error) {
	if m.AccessKeyID == "" || m.SecretAccessKey == "" {
		return credentials{}, fmt.Errorf("%s came back without a key to sign with", whose)
	}
	return credentials{m.AccessKeyID, m.SecretAccessKey, m.Token}, nil
}

func fetchWithSession(ctx context.Context, url, session string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("X-aws-ec2-metadata-token", session)
	return fetchText(req)
}
