package cloud

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// noAmbientIdentity clears everything these tests would otherwise inherit.
// Without it a machine that really is signed in to something would change
// what the tests prove, and a machine that is not would spend two seconds
// per test waiting for 169.254.169.254 to not answer.
func noAmbientIdentity(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN",
		"AWS_PROFILE", "AWS_REGION", "AWS_DEFAULT_REGION", "AWS_SHARED_CREDENTIALS_FILE",
		"AWS_CONTAINER_CREDENTIALS_FULL_URI", "AWS_CONTAINER_CREDENTIALS_RELATIVE_URI",
		"AWS_CONTAINER_AUTHORIZATION_TOKEN", "AWS_EC2_METADATA_SERVICE_ENDPOINT",
		"GOOGLE_APPLICATION_CREDENTIALS", "CLOUDSDK_CONFIG", "GCE_METADATA_HOST",
		"AZURE_TENANT_ID", "AZURE_CLIENT_ID", "AZURE_CLIENT_SECRET",
		"AZURE_AUTHORITY_HOST", "IDENTITY_ENDPOINT", "IDENTITY_HEADER",
	} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	// The shared credentials file is looked for under the home directory,
	// and a developer running this has one.
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(t.TempDir(), "no-such-file"))
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("CLOUDSDK_CONFIG", t.TempDir())
}

// frozen fixes the clock, because a signature is over a minute among other
// things and nothing can be compared while it moves.
func frozen(t *testing.T, stamp string) {
	t.Helper()
	at, err := time.Parse(longDate, stamp)
	if err != nil {
		t.Fatalf("bad stamp %q: %v", stamp, err)
	}
	old := timeNow
	timeNow = func() time.Time { return at }
	t.Cleanup(func() { timeNow = old })
}

func withKeys(t *testing.T, id, secret, session string) {
	t.Helper()
	t.Setenv("AWS_ACCESS_KEY_ID", id)
	t.Setenv("AWS_SECRET_ACCESS_KEY", secret)
	if session != "" {
		t.Setenv("AWS_SESSION_TOKEN", session)
	}
}

// rdsTarget is an endpoint of the shape AWS documents, which is also where
// the region is read from.
var rdsTarget = Target{Host: "rdspostgres.123456789012.us-west-2.rds.amazonaws.com", Port: 5432, User: "jane_doe"}

func mint(t *testing.T, cfg Config, target Target) string {
	t.Helper()
	token, err := Token(context.Background(), cfg, target)
	if err != nil {
		t.Fatalf("no token: %v", err)
	}
	return token
}

func TestATokenLooksLikeTheOneAWSDocuments(t *testing.T) {
	noAmbientIdentity(t)
	frozen(t, "20150830T123600Z")
	withKeys(t, "AKIDEXAMPLE", vectorSecretKey, "")

	token := mint(t, Config{Provider: AWS}, rdsTarget)

	// AWS's own documentation shows a token beginning like this, with no
	// scheme in front of it and the port in it.
	prefix := "rdspostgres.123456789012.us-west-2.rds.amazonaws.com:5432/?Action=connect&DBUser=jane_doe&"
	if !strings.HasPrefix(token, prefix) {
		t.Errorf("the token begins\n\t%s\nand AWS documents\n\t%s", token[:min(len(token), len(prefix))], prefix)
	}
	if strings.Contains(token, "://") {
		t.Errorf("the token carries a scheme, which AWS rejects: %s", token)
	}

	q, err := url.ParseQuery(strings.SplitN(token, "?", 2)[1])
	if err != nil {
		t.Fatalf("the token's query will not parse: %v", err)
	}
	for _, c := range []struct{ key, want string }{
		{"Action", "connect"},
		{"DBUser", "jane_doe"},
		{"X-Amz-Algorithm", "AWS4-HMAC-SHA256"},
		{"X-Amz-Expires", "900"},
		{"X-Amz-SignedHeaders", "host"},
		{"X-Amz-Date", "20150830T123600Z"},
		{"X-Amz-Credential", "AKIDEXAMPLE/20150830/us-west-2/rds-db/aws4_request"},
	} {
		if got := q.Get(c.key); got != c.want {
			t.Errorf("%s is %q, want %q", c.key, got, c.want)
		}
	}
	if len(q.Get("X-Amz-Signature")) != 64 {
		t.Errorf("the signature is %q, which is not a SHA-256", q.Get("X-Amz-Signature"))
	}
	if _, ok := q["X-Amz-Security-Token"]; ok {
		t.Error("a session token appeared where there was none to carry")
	}
}

func TestATokenIsSignedOverEverythingThatDecidesWhereItMayBeSpent(t *testing.T) {
	noAmbientIdentity(t)
	frozen(t, "20150830T123600Z")
	withKeys(t, "AKIDEXAMPLE", vectorSecretKey, "")

	base := mint(t, Config{Provider: AWS}, rdsTarget)
	same := mint(t, Config{Provider: AWS}, rdsTarget)
	if base != same {
		t.Error("the same request signed twice gave two different tokens")
	}

	otherHost := rdsTarget
	otherHost.Host = "other.123456789012.us-west-2.rds.amazonaws.com"
	otherPort := rdsTarget
	otherPort.Port = 5433
	otherUser := rdsTarget
	otherUser.User = "mary_roe"

	for _, c := range []struct {
		what  string
		token string
	}{
		{"a different endpoint", mint(t, Config{Provider: AWS}, otherHost)},
		{"a different port", mint(t, Config{Provider: AWS}, otherPort)},
		{"a different user", mint(t, Config{Provider: AWS}, otherUser)},
		{"a different region", mint(t, Config{Provider: AWS, Params: map[string]string{"region": "eu-west-1"}}, rdsTarget)},
	} {
		if signatureOf(t, c.token) == signatureOf(t, base) {
			t.Errorf("%s signed to the same thing, so the signature does not cover it", c.what)
		}
	}

	// And the minute it was made: a token is good for fifteen of them.
	frozen(t, "20150830T124600Z")
	if signatureOf(t, mint(t, Config{Provider: AWS}, rdsTarget)) == signatureOf(t, base) {
		t.Error("ten minutes later signed to the same thing, so the signature does not cover when it was made")
	}
}

func signatureOf(t *testing.T, token string) string {
	t.Helper()
	q, err := url.ParseQuery(strings.SplitN(token, "?", 2)[1])
	if err != nil {
		t.Fatalf("the token's query will not parse: %v", err)
	}
	return q.Get("X-Amz-Signature")
}

func TestTemporaryCredentialsCarryTheirSessionTokenInsideTheSignature(t *testing.T) {
	noAmbientIdentity(t)
	frozen(t, "20150830T123600Z")
	withKeys(t, "ASIAEXAMPLE", vectorSecretKey, "a-session-token")

	token := mint(t, Config{Provider: AWS}, rdsTarget)
	q, _ := url.ParseQuery(strings.SplitN(token, "?", 2)[1])
	if got := q.Get("X-Amz-Security-Token"); got != "a-session-token" {
		t.Fatalf("the session token is %q", got)
	}
	// Signed, not merely attached: it must appear before the signature in the
	// query that was signed, which is where AWS will look for it.
	sig := strings.Index(token, "&X-Amz-Signature=")
	if tok := strings.Index(token, "X-Amz-Security-Token="); tok < 0 || tok > sig {
		t.Error("the session token is outside the signed part of the token")
	}
}

func TestWhereTheRegionComesFrom(t *testing.T) {
	noAmbientIdentity(t)
	for _, c := range []struct {
		what   string
		params map[string]string
		env    map[string]string
		host   string
		want   string
		says   string
	}{
		{what: "the endpoint says so", host: rdsTarget.Host, want: "us-west-2"},
		{what: "a proxy endpoint says so", host: "proxy.proxy-abc.eu-central-1.rds.amazonaws.com", want: "eu-central-1"},
		{what: "a setting overrules the endpoint", params: map[string]string{"region": "ap-south-1"},
			host: rdsTarget.Host, want: "ap-south-1"},
		{what: "the environment, where the endpoint is nobody's shape",
			env: map[string]string{"AWS_REGION": "eu-west-1"}, host: "db.example.com", want: "eu-west-1"},
		{what: "the older environment variable too",
			env: map[string]string{"AWS_DEFAULT_REGION": "sa-east-1"}, host: "db.example.com", want: "sa-east-1"},
		{what: "the endpoint before the environment, because the endpoint is the database",
			env: map[string]string{"AWS_REGION": "eu-west-1"}, host: rdsTarget.Host, want: "us-west-2"},
		{what: "nowhere at all", host: "db.example.com", says: "no region could be determined"},
	} {
		t.Run(c.what, func(t *testing.T) {
			noAmbientIdentity(t)
			for k, v := range c.env {
				t.Setenv(k, v)
			}
			got, err := awsRegion(c.params, c.host)
			if c.says != "" {
				if err == nil || !strings.Contains(err.Error(), c.says) {
					t.Fatalf("it said %v, wanted something about %q", err, c.says)
				}
				return
			}
			if err != nil {
				t.Fatalf("no region: %v", err)
			}
			if got != c.want {
				t.Errorf("the region is %q, want %q", got, c.want)
			}
		})
	}
}

func TestCredentialsComeFromWhereTheAWSToolsKeepThem(t *testing.T) {
	const file = `
# a comment
[default]
aws_access_key_id = AKIADEFAULT
aws_secret_access_key = default-secret

[work]
aws_access_key_id = AKIAWORK
aws_secret_access_key = work-secret
aws_session_token = work-session
`
	for _, c := range []struct {
		what    string
		env     map[string]string
		params  map[string]string
		wantKey string
	}{
		{what: "the environment first of all",
			env:     map[string]string{"AWS_ACCESS_KEY_ID": "AKIAENV", "AWS_SECRET_ACCESS_KEY": "env-secret"},
			wantKey: "AKIAENV"},
		{what: "the default profile", wantKey: "AKIADEFAULT"},
		{what: "a profile named in the environment",
			env: map[string]string{"AWS_PROFILE": "work"}, wantKey: "AKIAWORK"},
		{what: "a profile named on the connection",
			params: map[string]string{"profile": "work"}, wantKey: "AKIAWORK"},
		{what: "the connection's profile before the environment's",
			env: map[string]string{"AWS_PROFILE": "default"}, params: map[string]string{"profile": "work"},
			wantKey: "AKIAWORK"},
	} {
		t.Run(c.what, func(t *testing.T) {
			noAmbientIdentity(t)
			path := filepath.Join(t.TempDir(), "credentials")
			if err := os.WriteFile(path, []byte(file), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("AWS_SHARED_CREDENTIALS_FILE", path)
			for k, v := range c.env {
				t.Setenv(k, v)
			}
			got, err := awsCredentials(context.Background(), c.params)
			if err != nil {
				t.Fatalf("no credentials: %v", err)
			}
			if got.accessKey != c.wantKey {
				t.Errorf("signed with %q, want %q", got.accessKey, c.wantKey)
			}
			if got.accessKey == "AKIAWORK" && got.sessionToken != "work-session" {
				t.Errorf("the session token was left behind: %q", got.sessionToken)
			}
		})
	}
}

func TestAProfileThatSignsInSomeOtherWaySaysWhichWay(t *testing.T) {
	for _, c := range []struct{ body, says string }{
		{"[default]\nsso_start_url = https://example.awsapps.com/start\nsso_account_id = 1\n", "single sign-on"},
		{"[default]\nsso_session = corp\n", "single sign-on"},
		{"[default]\nrole_arn = arn:aws:iam::1:role/r\nsource_profile = other\n", "a role to assume"},
		{"[default]\ncredential_process = /usr/bin/get-creds\n", "an external credential process"},
		{"[default]\naws_access_key_id = AKIA1\n", "no secret access key"},
	} {
		t.Run(c.says, func(t *testing.T) {
			noAmbientIdentity(t)
			path := filepath.Join(t.TempDir(), "credentials")
			if err := os.WriteFile(path, []byte(c.body), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("AWS_SHARED_CREDENTIALS_FILE", path)
			_, err := awsCredentials(context.Background(), nil)
			if err == nil || !strings.Contains(err.Error(), c.says) {
				t.Fatalf("it said %v, wanted something about %q", err, c.says)
			}
		})
	}
}

func TestAnInstanceIsAskedWithASessionAndNotWithout(t *testing.T) {
	noAmbientIdentity(t)
	var asked []string
	imds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodPut && r.URL.Path == "/latest/api/token" {
			if r.Header.Get("X-aws-ec2-metadata-token-ttl-seconds") == "" {
				t.Error("a session was asked for without saying how long it should last")
			}
			w.Write([]byte("a-session"))
			return
		}
		// Version 1 is not enough: without the session header this is the
		// request a forgery bug in some other application would make.
		if r.Header.Get("X-aws-ec2-metadata-token") != "a-session" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/latest/meta-data/iam/security-credentials/":
			w.Write([]byte("the-role"))
		case "/latest/meta-data/iam/security-credentials/the-role":
			w.Write([]byte(`{"AccessKeyId":"AKIAROLE","SecretAccessKey":"role-secret","Token":"role-session"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer imds.Close()

	t.Setenv("AWS_EC2_METADATA_DISABLED", "false")
	t.Setenv("AWS_EC2_METADATA_SERVICE_ENDPOINT", imds.URL)

	got, err := awsCredentials(context.Background(), nil)
	if err != nil {
		t.Fatalf("no credentials: %v", err)
	}
	if got.accessKey != "AKIAROLE" || got.secretKey != "role-secret" || got.sessionToken != "role-session" {
		t.Errorf("borrowed %+v", got)
	}
	if len(asked) != 3 || asked[0] != "PUT /latest/api/token" {
		t.Errorf("it asked %v, and a session must be got first", asked)
	}
}

func TestATaskBorrowsItsContainersIdentity(t *testing.T) {
	noAmbientIdentity(t)
	ecs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "a-shared-secret" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Write([]byte(`{"AccessKeyId":"AKIATASK","SecretAccessKey":"task-secret","Token":"task-session"}`))
	}))
	defer ecs.Close()

	t.Setenv("AWS_EC2_METADATA_DISABLED", "false")
	t.Setenv("AWS_CONTAINER_CREDENTIALS_FULL_URI", ecs.URL)
	t.Setenv("AWS_CONTAINER_AUTHORIZATION_TOKEN", "a-shared-secret")

	got, err := awsCredentials(context.Background(), nil)
	if err != nil {
		t.Fatalf("no credentials: %v", err)
	}
	if got.accessKey != "AKIATASK" {
		t.Errorf("borrowed %q", got.accessKey)
	}
}

func TestWhatAnAWSTokenWillNotEvenTry(t *testing.T) {
	for _, c := range []struct {
		what   string
		target Target
		says   string
	}{
		{"no user", Target{Host: rdsTarget.Host, Port: 5432}, "names no user"},
		{"no host", Target{Port: 5432, User: "jane_doe"}, "has no host"},
	} {
		t.Run(c.what, func(t *testing.T) {
			noAmbientIdentity(t)
			withKeys(t, "AKIDEXAMPLE", vectorSecretKey, "")
			_, err := Token(context.Background(), Config{Provider: AWS}, c.target)
			if err == nil || !strings.Contains(err.Error(), c.says) {
				t.Fatalf("it said %v, wanted something about %q", err, c.says)
			}
		})
	}
}

func TestWithNoIdentityAnywhereItSaysSoRatherThanSigningWithNothing(t *testing.T) {
	noAmbientIdentity(t)
	_, err := Token(context.Background(), Config{Provider: AWS}, rdsTarget)
	if err == nil {
		t.Fatal("it minted a token with no credentials at all")
	}
	if !strings.Contains(err.Error(), "metadata service is switched off") {
		t.Errorf("it said %v", err)
	}
}
