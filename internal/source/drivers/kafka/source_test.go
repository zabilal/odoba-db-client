package kafka

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/twmb/franz-go/pkg/sasl"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/capability"
)

// The connection to a Kafka cluster (T2.54).

func TestTheFormAsksForABrokerToStartFrom(t *testing.T) {
	d := Driver{}.Describe()
	if d.ID != driverID || d.Paradigm != model.ParadigmStream || d.DefaultPort != defaultPort {
		t.Errorf("the driver describes itself as %+v", d)
	}
	fields := map[string]source.Field{}
	for _, f := range d.Fields {
		fields[f.Key] = f
	}
	// A broker is required, because there is nowhere else to start; the rest
	// of the cluster is found from it, so nothing else is.
	if host := fields["host"]; !host.Required || host.Kind != source.FieldText {
		t.Errorf("the bootstrap server is asked for as %+v", host)
	}
	for _, key := range []string{"port", "servers"} {
		if f, ok := fields[key]; !ok || f.Required {
			t.Errorf("%s is asked for as %+v", key, f)
		}
	}
	// No database, no user: Kafka has no database to choose, and what it asks
	// of a person who must authenticate is SASL, which is T2.55.
	for _, key := range []string{"database", "topic"} {
		if _, ok := fields[key]; ok {
			t.Errorf("the form asks for a %s", key)
		}
	}
}

func TestTheFormAsksHowToProveWhoYouAre(t *testing.T) {
	fields := map[string]source.Field{}
	for _, f := range (Driver{}).Describe().Fields {
		fields[f.Key] = f
	}
	mech := fields["mechanism"]
	if mech.Kind != source.FieldSelect || mech.Default != mechNone {
		t.Errorf("authentication is chosen as %+v", mech)
	}
	// What is offered is what the driver can speak. OAUTHBEARER and GSSAPI
	// are real mechanisms and are not here, because offering one that nothing
	// implements would be a setting that fails at the broker.
	offered := strings.Join(mech.Options, " ")
	for _, want := range []string{mechNone, "PLAIN", "SCRAM-SHA-256", "SCRAM-SHA-512", "OAUTHBEARER", "AWS_MSK_IAM"} {
		if !strings.Contains(offered, want) {
			t.Errorf("%s is not offered: %q", want, offered)
		}
	}
	for _, unwritten := range []string{"GSSAPI"} {
		if strings.Contains(offered, unwritten) {
			t.Errorf("%s is offered and is not written", unwritten)
		}
	}
	// A password is a secret, so it is kept where secrets are kept (FR-1.5),
	// and so is a token: it is the whole of what proves who the bearer is.
	for _, key := range []string{"password", "token"} {
		if f := fields[key]; !f.Secret || f.Kind != source.FieldPassword {
			t.Errorf("the %s is asked for as %+v", key, f)
		}
	}
}

func TestHowAConnectionProvesWhoItIs(t *testing.T) {
	as := func(mechanism, user, password string) (sasl.Mechanism, error) {
		cfg := source.ConnectionConfig{Host: "b", User: user,
			Params: map[string]string{"mechanism": mechanism}}
		cfg.Secret = func(key string) (string, error) {
			if key == "password" {
				return password, nil
			}
			return "", nil
		}
		return mechanismOf(cfg)
	}
	// bearer is a connection carrying a token instead of a password.
	bearer := func(mechanism, user, token string) (sasl.Mechanism, error) {
		return mechanismOf(source.ConnectionConfig{Host: "b", User: user,
			Params: map[string]string{"mechanism": mechanism},
			Secret: func(key string) (string, error) {
				if key == "token" {
					return token, nil
				}
				return "", nil
			}})
	}

	// Nothing chosen, nothing given: a broker that asks nothing is answered
	// with nothing, rather than with an empty attempt at authenticating.
	for _, name := range []string{"", mechNone, "none"} {
		got, err := as(name, "", "")
		if got != nil || err != nil {
			t.Errorf("%q: %v, %v", name, got, err)
		}
	}
	// Each mechanism is the one asked for, however it was typed.
	for name, want := range map[string]string{
		"PLAIN": "PLAIN", "plain": "PLAIN",
		"SCRAM-SHA-256": "SCRAM-SHA-256", "scram-sha-512": "SCRAM-SHA-512",
	} {
		got, err := as(name, "ikigai", "secret")
		if err != nil || got == nil {
			t.Errorf("%q: %v", name, err)
			continue
		}
		if got.Name() != want {
			t.Errorf("%q authenticates as %q", name, got.Name())
		}
	}

	// Credentials that would be sent nowhere are refused, rather than the
	// connection quietly being made as nobody.
	_, err := as(mechNone, "ikigai", "secret")
	if kind(err) != source.ConnectConfig || !strings.Contains(err.Error(), "no authentication") {
		t.Errorf("credentials with nothing chosen: %v", err)
	}
	// A mechanism with nobody to authenticate is refused before dialling: the
	// broker would only say something less clear.
	_, err = as("PLAIN", "", "secret")
	if kind(err) != source.ConnectConfig || !strings.Contains(err.Error(), "user") {
		t.Errorf("a mechanism with no user: %v", err)
	}
	// One nobody speaks here is refused, saying what there is to choose.
	_, err = as("SCRAM-SHA-1", "ikigai", "secret")
	if kind(err) != source.ConnectConfig || !strings.Contains(err.Error(), "SCRAM-SHA-256") {
		t.Errorf("a mechanism nobody speaks: %v", err)
	}
	// A token authenticates itself, so it needs no user: whoever issued it
	// said who this is and for how long.
	got, err := bearer("OAUTHBEARER", "", "a.token.here")
	if err != nil || got == nil || got.Name() != "OAUTHBEARER" {
		t.Errorf("a token alone: %v, %v", got, err)
	}
	// Without one there is nothing to authenticate with.
	_, err = bearer("OAUTHBEARER", "ikigai", "")
	if kind(err) != source.ConnectConfig || !strings.Contains(err.Error(), "token") {
		t.Errorf("OAUTHBEARER with no token: %v", err)
	}
	// A password where a token belongs: what is missing is the token, and the
	// refusal has to name that rather than anything else.
	_, err = as("OAUTHBEARER", "ikigai", "secret")
	if kind(err) != source.ConnectConfig || !strings.Contains(err.Error(), "none was given") {
		t.Errorf("OAUTHBEARER with a password and no token: %v", err)
	}
	// Both at once is the case worth saying: one of them would be ignored, and
	// nothing on the screen would say which.
	_, err = mechanismOf(source.ConnectionConfig{Host: "b", User: "ikigai",
		Params: map[string]string{"mechanism": "OAUTHBEARER"},
		Secret: func(key string) (string, error) {
			switch key {
			case "token":
				return "a.token.here", nil
			case "password":
				return "secret", nil
			}
			return "", nil
		}})
	if kind(err) != source.ConnectConfig || !strings.Contains(err.Error(), "rather than a password") {
		t.Errorf("OAUTHBEARER with a token and a password: %v", err)
	}
	_, err = bearer("PLAIN", "ikigai", "a.token.here")
	if kind(err) != source.ConnectConfig || !strings.Contains(err.Error(), "token") {
		t.Errorf("PLAIN with a token: %v", err)
	}
	// And a token with nothing chosen would be sent nowhere, like any other
	// credential.
	_, err = bearer(mechNone, "", "a.token.here")
	if kind(err) != source.ConnectConfig || !strings.Contains(err.Error(), "no authentication") {
		t.Errorf("a token with nothing chosen: %v", err)
	}

	// Signing for a managed cluster: the access key names who this is and the
	// secret key signs, so both are wanted and neither is sent.
	got, err = as("AWS_MSK_IAM", "AKIAEXAMPLE", "a-secret-key")
	if err != nil || got == nil || got.Name() != "AWS_MSK_IAM" {
		t.Errorf("an access key and a secret key: %v, %v", got, err)
	}
	for _, missing := range []struct{ user, password string }{
		{"", "a-secret-key"}, {"AKIAEXAMPLE", ""}, {"", ""},
	} {
		_, err = as("AWS_MSK_IAM", missing.user, missing.password)
		if kind(err) != source.ConnectConfig || !strings.Contains(err.Error(), "needs both") {
			t.Errorf("signing with %q/%q: %v", missing.user, missing.password, err)
		}
	}
	// Temporary credentials carry a session token as well, which is welcome
	// here where the password mechanisms refuse one.
	got, err = mechanismOf(source.ConnectionConfig{Host: "b", User: "AKIAEXAMPLE",
		Params: map[string]string{"mechanism": "AWS_MSK_IAM"},
		Secret: func(key string) (string, error) {
			switch key {
			case "token":
				return "a-session-token", nil
			case "password":
				return "a-secret-key", nil
			}
			return "", nil
		}})
	if err != nil || got == nil || got.Name() != "AWS_MSK_IAM" {
		t.Errorf("temporary credentials: %v, %v", got, err)
	}

	// A keychain that will not answer is a fault in the settings, not a
	// refusal by the broker.
	_, err = mechanismOf(source.ConnectionConfig{Host: "b", User: "ikigai",
		Params: map[string]string{"mechanism": "PLAIN"},
		Secret: func(string) (string, error) { return "", errors.New("locked") }})
	if kind(err) != source.ConnectConfig || !strings.Contains(err.Error(), "keychain") {
		t.Errorf("a keychain that will not answer: %v", err)
	}
}

func TestWhereTheClusterIsFoundFrom(t *testing.T) {
	seeds := func(host string, port int, servers string) []string {
		t.Helper()
		got, err := bootstrapServers(source.ConnectionConfig{
			Host: host, Port: port, Params: map[string]string{"servers": servers}})
		if err != nil {
			t.Fatalf("%q/%d/%q: %v", host, port, servers, err)
		}
		return got
	}
	for name, c := range map[string]struct {
		host, servers string
		port          int
		want          string
	}{
		"a broker and nothing else":   {"localhost", "", 0, "localhost:9092"},
		"a port of its own":           {"localhost", "", 59092, "localhost:59092"},
		"the port written in":         {"localhost:59092", "", 0, "localhost:59092"},
		"further brokers":             {"a", "b,c", 9095, "a:9095 b:9095 c:9095"},
		"each with a port of its own": {"a", "b:1, c:2", 9095, "a:9095 b:1 c:2"},
		"spaces and commas alike":     {"a", " b  c,,d ", 0, "a:9092 b:9092 c:9092 d:9092"},
		"an address in six":           {"::1", "", 0, "[::1]:9092"},
		"an address in six, ported":   {"[::1]:59092", "", 0, "[::1]:59092"},
	} {
		got := strings.Join(seeds(c.host, c.port, c.servers), " ")
		if got != c.want {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}

	// Every seed is dialable as it stands: a host and a port, always.
	for _, s := range seeds("host", 0, "other") {
		if _, _, err := net.SplitHostPort(s); err != nil {
			t.Errorf("the seed %q is no address: %v", s, err)
		}
	}

	// With nowhere to start, nothing is dialled and the refusal says so.
	_, err := bootstrapServers(source.ConnectionConfig{})
	if kind(err) != source.ConnectConfig || !strings.Contains(err.Error(), "bootstrap server") {
		t.Errorf("no broker at all: %v", err)
	}
}

func TestNothingIsClaimedThatIsNotWritten(t *testing.T) {
	caps := (&kafkaSource{}).Capabilities()
	if caps.Paradigm != model.ParadigmStream {
		t.Errorf("the paradigm is %q", caps.Paradigm)
	}
	// Kafka has no query language, and this driver has not yet written a way
	// to read a record, list a topic, or say what a cluster holds. A claim is
	// a promise the suite holds a driver to.
	if caps.Query.Supported || caps.Query.Language != "" {
		t.Errorf("a query language is claimed: %+v", caps.Query)
	}
	if (caps.Stream != capability.Stream{}) {
		t.Errorf("a stream operation is claimed: %+v", caps.Stream)
	}
	if caps.Data.ServerFilter || caps.Data.ServerSort || caps.Data.ExactCount {
		t.Errorf("the grid is promised something: %+v", caps.Data)
	}
	// The cluster, its topics, their partitions and the groups reading them
	// are all in the tree now (T2.62). Schema-registry subjects are not: they
	// wait on a registry client to ask (T2.69).
	for _, listed := range []model.ObjectKind{
		model.KindCluster, model.KindTopic, model.KindPartition, model.KindConsumerGroup,
	} {
		if !caps.Objects[listed] {
			t.Errorf("%s is in the tree and not in the claim: %v", listed, caps.Objects)
		}
	}
	for _, unwritten := range []model.ObjectKind{model.KindSubject} {
		if caps.Objects[unwritten] {
			t.Errorf("%s is claimed and is not listed yet", unwritten)
		}
	}

	// And records are still nobody's to read.
	s := &kafkaSource{}
	ctx := context.Background()
	if _, err := s.Browse(ctx, model.NewRef(model.KindTopic, "t"), source.BrowseOptions{}); err == nil {
		t.Error("records were read from a driver that cannot read them")
	}
	// Describing a kind this driver does not keep asks the cluster nothing,
	// which is why this reaches no broker and still answers.
	if got, err := s.Describe(ctx, model.NewRef(model.KindPartition, "t", "0")); got != nil || err != nil {
		t.Errorf("a partition is described as %v: %v", got, err)
	}
}

func TestAFailureSaysWhatToFix(t *testing.T) {
	for name, c := range map[string]struct {
		err  error
		want source.ConnectKind
	}{
		"nothing listening":           {errors.New("dial tcp 127.0.0.1:9092: connect: connection refused"), source.ConnectRefused},
		"no such host":                {&net.DNSError{Err: "no such host", Name: "nowhere"}, source.ConnectUnreachable},
		"no answer":                   {errors.New("context deadline exceeded"), source.ConnectUnreachable},
		"given up on":                 {context.Canceled, source.ConnectUnreachable},
		"a certificate":               {errors.New("x509: certificate signed by unknown authority"), source.ConnectTLS},
		"plaintext to a TLS listener": {errors.New("first record does not look like a TLS handshake"), source.ConnectTLS},
		"credentials":                 {errors.New("SASL authentication failed"), source.ConnectAuth},
		"a token refused":             {errors.New("unexpected data in oauth response"), source.ConnectAuth},
		"a region nobody named":       {errors.New(`cannot determine the region in "kafka.example.com"`), source.ConnectConfig},
		"something else":              {errors.New("the broker said no"), source.ConnectUnknown},
	} {
		got := classifyConnectError(c.err)
		if kind(got) != c.want {
			t.Errorf("%s: %v, kind %d, want %d", name, got, kind(got), c.want)
		}
		// Every failure says something a person can act on.
		var ce *source.ConnectError
		if errors.As(got, &ce); ce == nil || ce.Hint == "" {
			t.Errorf("%s says nothing to fix: %v", name, got)
		}
	}
	// The region is a setting, so the refusal names the setting to fix rather
	// than leaving somebody looking at a broker that is answering fine.
	if got := classifyConnectError(errors.New(`cannot determine the region in "kafka.example.com"`)); !strings.Contains(got.Error(), "AWS_REGION") {
		t.Errorf("a region nobody named says %q", got)
	}

	// A failure already classified is not classified twice.
	first := classifyConnectError(errors.New("connection refused"))
	if got := classifyConnectError(first); got != first {
		t.Errorf("a failure classified twice: %v", got)
	}
}

func TestAConnectionWithNowhereToGoIsNeverDialled(t *testing.T) {
	d := Driver{}
	src, err := d.Open(context.Background(), source.ConnectionConfig{DriverID: driverID})
	if err == nil {
		src.Close()
		t.Fatal("a connection was opened to no broker at all")
	}
	if kind(err) != source.ConnectConfig {
		t.Errorf("the refusal is %v", err)
	}
	// And it says what is missing. franz-go would refuse a client with no
	// seeds too, in words about its own options; this is the driver saying
	// which field of the form is empty.
	if !strings.Contains(err.Error(), "bootstrap server") {
		t.Errorf("the refusal says %q", err)
	}
}

// kind is the kind of connection failure an error says it is.
func kind(err error) source.ConnectKind {
	var ce *source.ConnectError
	if errors.As(err, &ce) {
		return ce.Kind
	}
	return source.ConnectUnknown
}
