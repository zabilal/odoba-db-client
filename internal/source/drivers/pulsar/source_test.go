package pulsar

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	pulsargo "github.com/apache/pulsar-client-go/pulsar"
	"github.com/apache/pulsar-client-go/pulsaradmin"
	"github.com/apache/pulsar-client-go/pulsaradmin/pkg/utils"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// What needs no cluster to be true: what a connection's settings become, where
// a read begins, and what is said of a topic. What a cluster makes of it is the
// live suite's.

func kindOf(err error) source.ConnectKind {
	var ce *source.ConnectError
	if errors.As(err, &ce) {
		return ce.Kind
	}
	return 255 // no kind at all
}

func hintOf(err error) string {
	var ce *source.ConnectError
	if errors.As(err, &ce) {
		return ce.Hint
	}
	return ""
}

func TestTheDescriptorIsWhatTheFormNeeds(t *testing.T) {
	d := Driver{}.Describe()
	if d.ID != "pulsar" || d.Name != "Apache Pulsar" || d.Paradigm != model.ParadigmStream || d.DefaultPort != 6650 ||
		!reflect.DeepEqual(d.URLSchemes, []string{"pulsar"}) {
		t.Errorf("described as %+v", d)
	}
	fields := map[string]source.Field{}
	for _, f := range d.Fields {
		fields[f.Key] = f
	}
	if !fields["host"].Required || fields["port"].Default != "6650" || fields["admin_port"].Default != "8080" || fields["user"].Required {
		t.Errorf("the form asks for %+v", d.Fields)
	}
	if p := fields["password"]; !p.Secret || p.Kind != source.FieldPassword {
		t.Errorf("the password is %+v", p)
	}
	got, err := source.Lookup(driverID)
	if err != nil || got.Describe().ID != driverID {
		t.Errorf("it is not registered: %v", err)
	}
}

func TestWhatAConnectionBecomes(t *testing.T) {
	secret := func(string) (string, error) { return `s3"cret`, nil }

	set, err := settingsOf(source.ConnectionConfig{Host: " pulsar.example ", User: " ada ", Secret: secret,
		TLS: source.TLSConfig{CAFile: "ca.pem", CertFile: "c.pem", KeyFile: "k.pem"}})
	if err != nil {
		t.Fatal(err)
	}
	// Nothing said about encryption is encrypted and verified, name and all
	// (NFR-S3), on the ports Pulsar is installed on, for both the things a
	// connection speaks to.
	if set.client.URL != "pulsar+ssl://pulsar.example:6650" || set.admin.WebServiceURL != "https://pulsar.example:8080" {
		t.Errorf("it would dial %s and %s", set.client.URL, set.admin.WebServiceURL)
	}
	if set.client.TLSAllowInsecureConnection || !set.client.TLSValidateHostname || set.client.TLSMinVersion != tls.VersionTLS12 ||
		set.admin.TLSAllowInsecureConnection || !set.admin.TLSEnableHostnameVerification {
		t.Errorf("said nothing about encryption, and got %+v and %+v", set.client, set.admin)
	}
	if set.client.TLSTrustCertsFilePath != "ca.pem" || set.admin.TLSTrustCertsFilePath != "ca.pem" ||
		set.client.TLSCertificateFile != "c.pem" || set.client.TLSKeyFilePath != "k.pem" ||
		set.admin.TLSCertFile != "c.pem" || set.admin.TLSKeyFile != "k.pem" {
		t.Errorf("the certificates given are not the ones used: %+v, %+v", set.client, set.admin)
	}
	if set.client.ConnectionTimeout != dialTimeout || set.client.OperationTimeout != operationTimeout {
		t.Errorf("it would wait %v and %v", set.client.ConnectionTimeout, set.client.OperationTimeout)
	}
	// A name and a password, to both, with the password written so that a
	// quote in it is a quote and not the end of it.
	if set.client.Authentication == nil || set.admin.AuthPlugin != "org.apache.pulsar.client.impl.auth.AuthenticationBasic" ||
		set.admin.AuthParams != `{"userId":"ada","password":"s3\"cret"}` || set.admin.Token != "" {
		t.Errorf("as ada: %+v, plugin %q, params %s", set.client.Authentication, set.admin.AuthPlugin, set.admin.AuthParams)
	}

	// A secret with nobody's name on it is a token.
	set, err = settingsOf(source.ConnectionConfig{Host: "h", Port: 9, Params: map[string]string{"admin_port": " 99 "},
		Secret: secret, TLS: source.TLSConfig{Mode: "disable", CAFile: "ca.pem", CertFile: "c.pem"}})
	if err != nil {
		t.Fatal(err)
	}
	if set.client.URL != "pulsar://h:9" || set.admin.WebServiceURL != "http://h:99" {
		t.Errorf("unencrypted, it would dial %s and %s", set.client.URL, set.admin.WebServiceURL)
	}
	if set.admin.Token != `s3"cret` || set.admin.AuthPlugin != "" || set.client.Authentication == nil {
		t.Errorf("a secret alone: token %q, plugin %q", set.admin.Token, set.admin.AuthPlugin)
	}
	// Certificates say nothing to a connection that is not encrypted.
	if set.client.TLSTrustCertsFilePath+set.client.TLSCertificateFile+set.admin.TLSTrustCertsFilePath+set.admin.TLSCertFile != "" {
		t.Errorf("unencrypted, and carrying certificates: %+v", set.client)
	}
	// And nobody with nothing is nobody, on this machine.
	set, err = settingsOf(source.ConnectionConfig{TLS: source.TLSConfig{Mode: "disable"}})
	if err != nil || set.client.URL != "pulsar://localhost:6650" || set.admin.WebServiceURL != "http://localhost:8080" ||
		set.client.Authentication != nil || set.admin.Token != "" || set.admin.AuthPlugin != "" {
		t.Errorf("with nothing given: %+v, %+v, %v", set.client.URL, set.admin, err)
	}

	for mode, want := range map[string]struct{ insecure, name bool }{
		"require": {true, false}, "verify-ca": {false, false}, "verify-full": {false, true},
	} {
		set, err := settingsOf(source.ConnectionConfig{Host: "h", TLS: source.TLSConfig{Mode: mode}})
		if err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		if !strings.HasPrefix(set.client.URL, "pulsar+ssl://") || !strings.HasPrefix(set.admin.WebServiceURL, "https://") ||
			set.client.TLSAllowInsecureConnection != want.insecure || set.admin.TLSAllowInsecureConnection != want.insecure ||
			set.client.TLSValidateHostname != want.name || set.admin.TLSEnableHostnameVerification != want.name {
			t.Errorf("%s: %+v, %+v", mode, set.client, set.admin)
		}
	}

	for name, c := range map[string]struct {
		cfg  source.ConnectionConfig
		says string
	}{
		"a wrong mode":            {source.ConnectionConfig{Host: "h", TLS: source.TLSConfig{Mode: "sometimes"}}, `"sometimes"`},
		"another name to verify":  {source.ConnectionConfig{Host: "h", TLS: source.TLSConfig{ServerName: "other"}}, "cannot be given another name"},
		"an admin port in words":  {source.ConnectionConfig{Host: "h", Params: map[string]string{"admin_port": "http"}}, `"http" is not a port`},
		"an admin port of nought": {source.ConnectionConfig{Host: "h", Params: map[string]string{"admin_port": "0"}}, "is not a port"},
		"an admin port too high":  {source.ConnectionConfig{Host: "h", Params: map[string]string{"admin_port": "70000"}}, "is not a port"},
		"a password that cannot be read": {source.ConnectionConfig{Host: "h", User: "ada",
			Secret: func(string) (string, error) { return "", errors.New("keychain locked") }}, "could not be read"},
	} {
		_, err := settingsOf(c.cfg)
		if kindOf(err) != source.ConnectConfig || !strings.Contains(hintOf(err), c.says) {
			t.Errorf("%s: %v (%q), want a configuration error mentioning %q", name, err, hintOf(err), c.says)
		}
	}
	if _, err := (Driver{}).Open(context.Background(), source.ConnectionConfig{Host: "h", TLS: source.TLSConfig{Mode: "sometimes"}}); kindOf(err) != source.ConnectConfig {
		t.Errorf("Open with a wrong mode: %v", err)
	}
}

type timeout struct{}

func (timeout) Error() string   { return "i/o timeout" }
func (timeout) Timeout() bool   { return true }
func (timeout) Temporary() bool { return true }

func TestAFailureToConnectIsCalledWhatItIs(t *testing.T) {
	for name, c := range map[string]struct {
		err  error
		kind source.ConnectKind
		says string
	}{
		"refused, who":            {errors.New("code: 401 reason: Unauthorized"), source.ConnectAuth, "user, password or token"},
		"refused, forbidden":      {errors.New("code: 403 reason: Forbidden"), source.ConnectAuth, "user, password or token"},
		"a certificate":           {fmt.Errorf("dial: %w", &tls.CertificateVerificationError{Err: errors.New("unknown authority")}), source.ConnectTLS, "could not be agreed"},
		"a certificate, in words": {errors.New("x509: certificate signed by unknown authority"), source.ConnectTLS, "could not be agreed"},
		"not TLS at all":          {fmt.Errorf("dial: %w", tls.RecordHeaderError{Msg: "first record does not look like a TLS handshake"}), source.ConnectTLS, "could not be agreed"},
		"not encrypted at all":    {errors.New(`Get "https://h:1/admin/v2/clusters": http: server gave HTTP response to HTTPS client`), source.ConnectTLS, "could not be agreed"},
		"no such host":            {fmt.Errorf("dial: %w", &net.DNSError{Name: "h", IsNotFound: true}), source.ConnectUnreachable, "host name"},
		"nothing listening":       {fmt.Errorf("dial: %w", syscall.ECONNREFUSED), source.ConnectUnreachable, "Nothing is listening"},
		"no answer":               {fmt.Errorf("read: %w", &net.OpError{Op: "read", Err: timeout{}}), source.ConnectUnreachable, "in time"},
		"something else":          {errors.New("code: 500 reason: odd"), source.ConnectUnknown, "The connection failed."},
	} {
		got := classify(c.err)
		if kindOf(got) != c.kind || !strings.Contains(hintOf(got), c.says) || !errors.Is(got, c.err) {
			t.Errorf("%s: %v (%q), want kind %v mentioning %q", name, got, hintOf(got), c.kind, c.says)
		}
	}
	already := &source.ConnectError{Kind: source.ConnectConfig, Hint: "x", Err: errors.New("y")}
	if got := classify(fmt.Errorf("wrapped: %w", already)); kindOf(got) != source.ConnectConfig || hintOf(got) != "x" {
		t.Errorf("an error already classified became %v", got)
	}
}

// Where a reader begins. What cannot be counted to is refused rather than
// answered with something near it.
func TestWhereAReadBegins(t *testing.T) {
	earliest, latest := pulsargo.EarliestMessageID(), pulsargo.LatestMessageID()
	at := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	for name, c := range map[string]struct {
		seek      *source.Seek
		following bool
		want      pulsargo.MessageID
		byTime    bool
	}{
		"nothing said":             {nil, false, earliest, false},
		"nothing said, following":  {nil, true, latest, false},
		"the beginning":            {&source.Seek{Mode: source.SeekBeginning}, false, earliest, false},
		"the beginning, following": {&source.Seek{Mode: source.SeekBeginning}, true, latest, false},
		"the end, following":       {&source.Seek{Mode: source.SeekEnd}, true, latest, false},
		"a time":                   {&source.Seek{Mode: source.SeekTimestamp, Time: at}, false, earliest, true},
		"a time, following":        {&source.Seek{Mode: source.SeekTimestamp, Time: at}, true, earliest, true},
	} {
		got, byTime, err := positionOf(c.seek, c.following)
		if err != nil || byTime != c.byTime || got == nil || got.String() != c.want.String() {
			t.Errorf("%s: from %v, by time %v, %v; want %v, %v", name, got, byTime, err, c.want, c.byTime)
		}
	}
	for name, c := range map[string]struct {
		seek      *source.Seek
		following bool
		says      string
	}{
		"the end, not followed":  {&source.Seek{Mode: source.SeekEnd}, false, "unless the topic is being followed"},
		"an offset":              {&source.Seek{Mode: source.SeekOffset, Offset: 5}, false, "no offset to read from"},
		"an offset, following":   {&source.Seek{Mode: source.SeekOffset, Offset: 5}, true, "no offset to read from"},
		"the last few":           {&source.Seek{Mode: source.SeekLast, Count: 5}, false, "no counting back"},
		"one partition, by time": {&source.Seek{Mode: source.SeekTimestamp, Time: at, Partitions: []int32{1}}, false, "a topic of its own"},
		"one partition, the end": {&source.Seek{Mode: source.SeekEnd, Partitions: []int32{0}}, true, "a topic of its own"},
		"a way nobody knows":     {&source.Seek{Mode: 99}, false, "no way to read"},
	} {
		got, _, err := positionOf(c.seek, c.following)
		if err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: from %v, %v; want a refusal mentioning %q", name, got, err, c.says)
		}
	}
}

func TestHowManyAReadStopsAfter(t *testing.T) {
	for name, c := range map[string]struct {
		opt  source.BrowseOptions
		want int64
	}{
		"said": {source.BrowseOptions{Limit: 7}, 7}, "not said": {source.BrowseOptions{}, records},
		"less than none": {source.BrowseOptions{Limit: -1}, records}, "following": {source.BrowseOptions{Follow: true, Limit: 7}, 0},
	} {
		if got := boundOf(c.opt); got != c.want {
			t.Errorf("%s: %d, want %d", name, got, c.want)
		}
	}
}

func TestWhereAMessageIs(t *testing.T) {
	for name, c := range map[string]struct {
		id   pulsargo.MessageID
		want string
	}{
		"an entry":           {pulsargo.NewMessageID(12, 34, -1, 0), "12:34"},
		"the first entry":    {pulsargo.NewMessageID(0, 0, -1, -1), "0:0"},
		"alone in its batch": {pulsargo.NewMessageID(12, 34, 0, 0), "12:34"},
	} {
		if got := address(c.id); got != c.want {
			t.Errorf("%s is at %q, want %q", name, got, c.want)
		}
	}
}

func TestATopicsOwnName(t *testing.T) {
	for full, want := range map[string]string{
		"persistent://public/default/orders": "orders",
		"non-persistent://t/ns/x":            "x",
		"orders":                             "orders",
		"persistent://public/default/a.b-c":  "a.b-c",
		"persistent://public/default/":       "",
	} {
		if got := short(full); got != want {
			t.Errorf("%q is called %q, want %q", full, got, want)
		}
	}
	for name, want := range map[string]bool{
		"persistent://public/default/orders-partition-0":  true,
		"persistent://public/default/orders-partition-12": true,
		"persistent://public/default/orders":              false,
		"persistent://public/default/orders-partition-":   false,
		"persistent://public/default/orders-partition-x":  false,
		"persistent://public/default/partition-3-orders":  false,
	} {
		if partition.MatchString(name) != want {
			t.Errorf("%s is a partition: %v", name, !want)
		}
	}
}

func TestWhoIsSubscribed(t *testing.T) {
	subs := map[string]utils.SubscriptionStats{
		"shipping":             {MsgBacklog: 0},
		"billing":              {MsgBacklog: 12},
		"ikigai-reader-a1b2c3": {MsgBacklog: 99}, // this application's own, looking
		"reader-of-somebody's": {MsgBacklog: 3},
	}
	if got := subscriptions(subs); got != "billing (12 behind), reader-of-somebody's (3 behind), shipping (0 behind)" {
		t.Errorf("subscribed: %q", got)
	}
	if got := subscriptions(nil); got != "" {
		t.Errorf("with nobody subscribed: %q", got)
	}
	if got := subscriptions(map[string]utils.SubscriptionStats{"ikigai-reader-x": {}}); got != "" {
		t.Errorf("with only this application looking: %q", got)
	}
}

func TestWhatItClaims(t *testing.T) {
	caps := (&pulsarSource{}).Capabilities()
	if caps.Paradigm != model.ParadigmStream || caps.Query.Supported {
		t.Errorf("it is a %v with a query language: %v", caps.Paradigm, caps.Query.Supported)
	}
	if s := caps.Stream; !s.Consume || !s.SeekTimestamp || !s.Follow || !s.Produce ||
		s.ConsumerGroups || s.TopicAdmin || s.ResetOffsets || s.ACLs || s.ManageACLs || s.SchemaRegistry || s.Throughput {
		t.Errorf("of streams it claims %+v", s)
	}
	for _, k := range []model.ObjectKind{model.KindCluster, model.KindSchema, model.KindFolder, model.KindTopic} {
		if !caps.Supports(k) {
			t.Errorf("it does not hold a %s", k)
		}
	}
	if caps.Supports(model.KindConsumerGroup) || caps.Supports(model.KindPartition) {
		t.Error("it claims kinds the tree never shows")
	}
	var src any = &pulsarSource{}
	for name, has := range map[string]bool{
		"GroupInspector": func() bool { _, ok := src.(source.GroupInspector); return ok }(),
		"TopicAdmin":     func() bool { _, ok := src.(source.TopicAdmin); return ok }(),
		"TopicMeter":     func() bool { _, ok := src.(source.TopicMeter); return ok }(),
		"Writer":         func() bool { _, ok := src.(source.Writer); return ok }(),
	} {
		if has {
			t.Errorf("it implements %s, and claims it does not", name)
		}
	}
	// A badge is never answered, for anything.
	if _, ok, err := (&pulsarSource{}).Badge(context.Background(), model.NewRef(model.KindTopic, "c", "t/n", "x")); ok || err != nil {
		t.Errorf("a badge: %v, %v", ok, err)
	}
}

// A ping asks something, and what the cluster is, is asked too: a connection
// whose admin service is not there says so to both.
func TestAConnectionThatCannotAskSaysSo(t *testing.T) {
	set, err := settingsOf(source.ConnectionConfig{Host: "127.0.0.1", Params: map[string]string{"admin_port": "59"},
		TLS: source.TLSConfig{Mode: "disable"}})
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pulsaradmin.NewClient(&set.admin)
	if err != nil {
		t.Fatal(err)
	}
	s := &pulsarSource{admin: admin, cluster: "c"}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.Ping(ctx); err == nil {
		t.Error("nothing is listening, and the ping was answered")
	}
	if _, err := s.Info(ctx); err == nil {
		t.Error("nothing is listening, and the cluster said what it is")
	}
	if err := s.Close(); err != nil {
		t.Errorf("closing a connection with no broker client: %v", err)
	}
}
