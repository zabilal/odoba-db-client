package nats

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// What needs no server to be true: what a connection's settings become, where
// a read begins, which subject a message goes out on, and what a stream and a
// consumer are to the model. What a server makes of it is the live suite's.

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
	if d.ID != "nats" || d.Name != "NATS JetStream" || d.Paradigm != model.ParadigmStream || d.DefaultPort != 4222 ||
		!reflect.DeepEqual(d.URLSchemes, []string{"nats"}) {
		t.Errorf("described as %+v", d)
	}
	fields := map[string]source.Field{}
	for _, f := range d.Fields {
		fields[f.Key] = f
	}
	if !fields["host"].Required || fields["port"].Default != "4222" || fields["user"].Required {
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

// applied is the client's options after a connection's settings are applied.
func applied(t *testing.T, opts []natsgo.Option) natsgo.Options {
	t.Helper()
	var o natsgo.Options
	for _, opt := range opts {
		if err := opt(&o); err != nil {
			t.Fatal(err)
		}
	}
	return o
}

func TestWhatAConnectionBecomes(t *testing.T) {
	secret := func(string) (string, error) { return "s3cret", nil }

	addr, opts, err := options(source.ConnectionConfig{Host: " nats.example ", User: " ada ", Secret: secret})
	if err != nil {
		t.Fatal(err)
	}
	o := applied(t, opts)
	// Nothing said about encryption is encrypted and verified (NFR-S3), on
	// the port NATS listens on, by name, and with one attempt at it.
	if addr != "tls://nats.example:4222" || o.Name != "ikigai-db" || o.User != "ada" || o.Password != "s3cret" || o.Token != "" {
		t.Errorf("it would dial %s as %q/%q, calling itself %q", addr, o.User, o.Password, o.Name)
	}
	if !o.Secure || o.TLSConfig == nil || o.TLSConfig.InsecureSkipVerify || o.TLSConfig.ServerName != "nats.example" ||
		o.TLSConfig.MinVersion != tls.VersionTLS12 {
		t.Errorf("said nothing about encryption, and got %+v", o.TLSConfig)
	}
	if o.RetryOnFailedConnect || o.Timeout != dialTimeout {
		t.Errorf("it would retry (%v) and wait %v", o.RetryOnFailedConnect, o.Timeout)
	}

	// A secret with nobody's name on it is a token.
	_, opts, err = options(source.ConnectionConfig{Host: "h", Port: 9, Secret: secret, TLS: source.TLSConfig{Mode: "disable"}})
	if o := applied(t, opts); err != nil || o.Token != "s3cret" || o.User != "" || o.Password != "" {
		t.Errorf("a secret alone: token %q, user %q, %v", o.Token, o.User, err)
	}
	// And nobody with nothing is nobody.
	addr, opts, err = options(source.ConnectionConfig{TLS: source.TLSConfig{Mode: "disable"}})
	if o := applied(t, opts); err != nil || addr != "nats://localhost:4222" || o.Token != "" || o.User != "" || o.Secure {
		t.Errorf("with nothing given: %s, %+v, %v", addr, o, err)
	}

	for mode, want := range map[string]struct {
		scheme string
		skip   bool
		name   string
	}{
		"require": {"tls", true, ""}, "verify-ca": {"tls", false, "other"}, "verify-full": {"tls", false, "other"},
	} {
		addr, opts, err := options(source.ConnectionConfig{Host: "h", Port: 9, TLS: source.TLSConfig{Mode: mode, ServerName: "other"}})
		if err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		o := applied(t, opts)
		if addr != want.scheme+"://h:9" || !o.Secure || o.TLSConfig.InsecureSkipVerify != want.skip || o.TLSConfig.ServerName != want.name {
			t.Errorf("%s dials %s with %+v", mode, addr, o.TLSConfig)
		}
	}

	// A certificate authority given is the one a certificate is checked
	// against, and a server whose certificate nobody else vouches for is then
	// vouched for.
	srv := httptest.NewTLSServer(nil)
	defer srv.Close()
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	_, opts, err = options(source.ConnectionConfig{Host: "h", TLS: source.TLSConfig{CAFile: ca}})
	if err != nil {
		t.Fatal(err)
	}
	if o := applied(t, opts); o.TLSConfig.RootCAs == nil || o.TLSConfig.RootCAs.Equal(x509.NewCertPool()) {
		t.Error("an authority was given and is not what a certificate is checked against")
	}
	if _, opts, _ := options(source.ConnectionConfig{Host: "h"}); applied(t, opts).TLSConfig.RootCAs != nil {
		t.Error("with no authority given, the system's are not what is used")
	}

	missing := filepath.Join(t.TempDir(), "none.pem")
	empty := filepath.Join(t.TempDir(), "empty.pem")
	if err := os.WriteFile(empty, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		cfg  source.ConnectionConfig
		says string
	}{
		"a wrong mode": {source.ConnectionConfig{Host: "h", TLS: source.TLSConfig{Mode: "sometimes"}}, `"sometimes"`},
		"a CA file that is not there": {source.ConnectionConfig{Host: "h",
			TLS: source.TLSConfig{CAFile: missing}}, "could not be read"},
		"a CA file with no certificate": {source.ConnectionConfig{Host: "h",
			TLS: source.TLSConfig{Mode: "verify-full", CAFile: empty}}, "no certificate"},
		"a client certificate without its key": {source.ConnectionConfig{Host: "h",
			TLS: source.TLSConfig{Mode: "verify-full", CertFile: empty}}, "certificate and key"},
		"a password that cannot be read": {source.ConnectionConfig{Host: "h", User: "ada",
			Secret: func(string) (string, error) { return "", errors.New("keychain locked") }}, "password"},
	} {
		_, _, err := options(c.cfg)
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
		"refused, who":            {natsgo.ErrAuthorization, source.ConnectAuth, "user, password or token"},
		"refused, expired":        {fmt.Errorf("x: %w", natsgo.ErrAuthExpired), source.ConnectAuth, "user, password or token"},
		"refused, revoked":        {natsgo.ErrAuthRevoked, source.ConnectAuth, "user, password or token"},
		"no encryption there":     {natsgo.ErrSecureConnWanted, source.ConnectTLS, "does not offer it"},
		"encryption required":     {natsgo.ErrSecureConnRequired, source.ConnectTLS, "has it turned off"},
		"a certificate":           {fmt.Errorf("dial: %w", &tls.CertificateVerificationError{Err: errors.New("unknown authority")}), source.ConnectTLS, "could not be agreed"},
		"a certificate, in words": {errors.New("x509: certificate signed by unknown authority"), source.ConnectTLS, "could not be agreed"},
		"not TLS at all":          {fmt.Errorf("dial: %w", tls.RecordHeaderError{Msg: "first record does not look like a TLS handshake"}), source.ConnectTLS, "could not be agreed"},
		"no such host":            {fmt.Errorf("dial: %w", &net.DNSError{Name: "h", IsNotFound: true}), source.ConnectUnreachable, "host name"},
		"nothing listening":       {fmt.Errorf("dial: %w", syscall.ECONNREFUSED), source.ConnectUnreachable, "Nothing is listening"},
		"no servers":              {natsgo.ErrNoServers, source.ConnectUnreachable, "Nothing is listening"},
		"no answer":               {fmt.Errorf("read: %w", &net.OpError{Op: "read", Err: timeout{}}), source.ConnectUnreachable, "in time"},
		"no answer, the client's": {natsgo.ErrTimeout, source.ConnectUnreachable, "in time"},
		"something else":          {errors.New("odd"), source.ConnectUnknown, "The connection failed."},
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

// Where a read begins, in a stream whose first message is 10 and whose last is
// 20.
func TestWhereAReadBegins(t *testing.T) {
	at := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	type want struct {
		found  bool
		policy jetstream.DeliverPolicy
		seq    uint64
	}
	seq := func(n uint64) want { return want{true, jetstream.DeliverByStartSequencePolicy, n} }
	none := want{}
	next := want{true, jetstream.DeliverNewPolicy, 0}
	for name, c := range map[string]struct {
		seek      *source.Seek
		following bool
		want      want
	}{
		"nothing said":                       {nil, false, seq(10)},
		"nothing said, following":            {nil, true, next},
		"the beginning":                      {&source.Seek{Mode: source.SeekBeginning}, false, seq(10)},
		"the beginning, following":           {&source.Seek{Mode: source.SeekBeginning}, true, next},
		"the end, following":                 {&source.Seek{Mode: source.SeekEnd}, true, next},
		"the end, not":                       {&source.Seek{Mode: source.SeekEnd}, false, none},
		"a sequence":                         {&source.Seek{Mode: source.SeekOffset, Offset: 15}, false, seq(15)},
		"the first sequence":                 {&source.Seek{Mode: source.SeekOffset, Offset: 10}, false, seq(10)},
		"the last sequence":                  {&source.Seek{Mode: source.SeekOffset, Offset: 20}, false, seq(20)},
		"a sequence before the first":        {&source.Seek{Mode: source.SeekOffset, Offset: 3}, false, seq(10)},
		"a sequence below nought":            {&source.Seek{Mode: source.SeekOffset, Offset: -3}, false, seq(10)},
		"a sequence past the end":            {&source.Seek{Mode: source.SeekOffset, Offset: 21}, false, none},
		"a sequence past the end, following": {&source.Seek{Mode: source.SeekOffset, Offset: 21}, true, next},
		"a sequence, following":              {&source.Seek{Mode: source.SeekOffset, Offset: 15}, true, seq(15)},
		"the last three":                     {&source.Seek{Mode: source.SeekLast, Count: 3}, false, seq(18)},
		"the last one":                       {&source.Seek{Mode: source.SeekLast, Count: 1}, false, seq(20)},
		"the last eleven, which is all":      {&source.Seek{Mode: source.SeekLast, Count: 11}, false, seq(10)},
		"the last ten":                       {&source.Seek{Mode: source.SeekLast, Count: 10}, false, seq(11)},
		"the last hundred":                   {&source.Seek{Mode: source.SeekLast, Count: 100}, false, seq(10)},
		"the last none":                      {&source.Seek{Mode: source.SeekLast, Count: 0}, false, none},
		"the last none, following":           {&source.Seek{Mode: source.SeekLast, Count: 0}, true, next},
		"the last minus two":                 {&source.Seek{Mode: source.SeekLast, Count: -2}, false, none},
		"a time":                             {&source.Seek{Mode: source.SeekTimestamp, Time: at}, false, want{true, jetstream.DeliverByStartTimePolicy, 0}},
		"its one partition":                  {&source.Seek{Mode: source.SeekOffset, Offset: 12, Partitions: []int32{0}}, false, seq(12)},
		"its one partition among others":     {&source.Seek{Mode: source.SeekOffset, Offset: 12, Partitions: []int32{4, 0}}, false, seq(12)},
		"a partition it has not":             {&source.Seek{Mode: source.SeekOffset, Offset: 12, Partitions: []int32{1}}, false, none},
		"a partition it has not, following":  {&source.Seek{Mode: source.SeekEnd, Partitions: []int32{1, 2}}, true, none},
	} {
		conf, found, err := positionOf(10, 20, c.seek, c.following)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if found != c.want.found || (found && (conf.DeliverPolicy != c.want.policy || conf.OptStartSeq != c.want.seq)) {
			t.Errorf("%s: found %v, policy %v from %d; want %+v", name, found, conf.DeliverPolicy, conf.OptStartSeq, c.want)
		}
		if c.seek != nil && c.seek.Mode == source.SeekTimestamp && (conf.OptStartTime == nil || !conf.OptStartTime.Equal(at)) {
			t.Errorf("%s: from %v", name, conf.OptStartTime)
		}
	}
	// An empty stream, whose first is one past its last: nothing to read, and
	// something to wait for.
	for name, c := range map[string]struct {
		seek      *source.Seek
		following bool
		found     bool
	}{
		"read":                 {nil, false, false},
		"read from a time":     {&source.Seek{Mode: source.SeekTimestamp, Time: at}, false, false},
		"followed":             {nil, true, true},
		"followed from a time": {&source.Seek{Mode: source.SeekTimestamp, Time: at}, true, true},
		"the last five of it":  {&source.Seek{Mode: source.SeekLast, Count: 5}, false, false},
	} {
		if _, found, err := positionOf(21, 20, c.seek, c.following); err != nil || found != c.found {
			t.Errorf("an empty stream, %s: found %v, %v", name, found, err)
		}
	}
	if _, _, err := positionOf(10, 20, &source.Seek{Mode: 99}, false); err == nil || !strings.Contains(err.Error(), "no way to read") {
		t.Errorf("a way nobody knows: %v", err)
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

// A read with nothing in it is the zero value, and behaves.
func TestAReadOfNothing(t *testing.T) {
	r := &messageRows{}
	if cols := r.Columns(); len(cols) != 6 || cols[0].Name != "partition" || cols[3].Name != "key" || cols[3].Type.Class != model.TypeString {
		t.Errorf("its columns are %+v", cols)
	}
	if _, err := r.Next(context.Background()); !errors.Is(err, io.EOF) {
		t.Errorf("its first row: %v", err)
	}
	stopped, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Next(stopped); !errors.Is(err, context.Canceled) {
		t.Errorf("a stopped read of nothing: %v", err)
	}
	for range 2 {
		if err := r.Close(); err != nil {
			t.Errorf("closing it, and closing it again: %v", err)
		}
	}
}

func TestWhichSubjectsAPatternKeeps(t *testing.T) {
	for _, c := range []struct {
		pattern, subject string
		want             bool
	}{
		{"orders", "orders", true}, {"orders", "orders.new", false}, {"orders.new", "orders", false},
		{"orders.*", "orders.new", true}, {"orders.*", "orders", false}, {"orders.*", "orders.new.eu", false},
		{"orders.*.eu", "orders.new.eu", true}, {"orders.*.eu", "orders.new.us", false},
		{"orders.>", "orders.new", true}, {"orders.>", "orders.new.eu", true}, {"orders.>", "orders", false},
		{">", "anything.at.all", true}, {">", "one", true},
		{"*", "one", true}, {"*", "one.two", false},
		{"orders.>", "payments.new", false}, {"orders.*", "orders.", false}, {"Orders", "orders", false},
		{"orders.>.eu", "orders.new.eu", false}, // a tail is the end of a pattern or it is nothing
	} {
		if matches(c.pattern, c.subject) != c.want {
			t.Errorf("%q keeps %q: %v", c.pattern, c.subject, !c.want)
		}
	}
}

func TestTheSubjectAMessageGoesOutOn(t *testing.T) {
	for name, c := range map[string]struct {
		given    string
		subjects []string
		want     string
		says     string
	}{
		"the one given":              {"orders.new", []string{"orders.>"}, "orders.new", ""},
		"given, with space round it": {" orders.new ", []string{"orders.*"}, "orders.new", ""},
		"given, kept by the second":  {"audit", []string{"orders.>", "audit"}, "audit", ""},
		"the stream's own":           {"", []string{"audit"}, "audit", ""},
		"none given, a pattern":      {"", []string{"orders.>"}, "", "has to be given as the key"},
		"none given, a star":         {"", []string{"orders.*"}, "", "has to be given as the key"},
		"none given, two":            {"", []string{"a", "b"}, "", "keeps a, b"},
		"none given, none kept":      {"", nil, "", "has to be given as the key"},
		"a star given":               {"orders.*", []string{"orders.>"}, "", "is a pattern"},
		"a tail given":               {"orders.>", []string{"orders.>"}, "", "is a pattern"},
		"a space in it":              {"orders new", []string{"orders.>"}, "", "has a space in it"},
		"another stream's":           {"payments.new", []string{"orders.>"}, "", `"payments.new" is not a subject stream S keeps (orders.>)`},
	} {
		got, err := subjectFor(c.given, c.subjects, "S")
		if c.says == "" {
			if err != nil || got != c.want {
				t.Errorf("%s: %q, %v; want %q", name, got, err, c.want)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: %q, %v; want a refusal mentioning %q", name, got, err, c.says)
		}
	}
}

func TestAConsumerIsNamedWithItsStream(t *testing.T) {
	if id := groupID("orders", "billing"); id != "orders/billing" {
		t.Errorf("named %q", id)
	}
	if s, c, err := splitGroup("orders/billing"); err != nil || s != "orders" || c != "billing" {
		t.Errorf("taken apart as %q, %q, %v", s, c, err)
	}
	for _, bad := range []string{"", "orders", "/billing", "orders/", "a/b/c", "/"} {
		if s, c, err := splitGroup(bad); err == nil {
			t.Errorf("%q was taken apart as %q and %q", bad, s, c)
		}
	}
}

func TestWhatKindOfConsumerOneIs(t *testing.T) {
	durable := jetstream.ConsumerConfig{Durable: "billing"}
	for name, c := range map[string]struct {
		info jetstream.ConsumerInfo
		want string
	}{
		"durable, nobody reading":   {jetstream.ConsumerInfo{Config: durable}, "durable, idle"},
		"durable, somebody waiting": {jetstream.ConsumerInfo{Config: durable, NumWaiting: 1}, "durable, being read"},
		"durable, pushed to":        {jetstream.ConsumerInfo{Config: durable, PushBound: true}, "durable, being read"},
		"ephemeral":                 {jetstream.ConsumerInfo{}, "ephemeral, idle"},
		"ephemeral, read":           {jetstream.ConsumerInfo{NumWaiting: 2}, "ephemeral, being read"},
	} {
		if got := stateOf(&c.info); got != c.want {
			t.Errorf("%s: %q", name, got)
		}
	}
}

func TestAConsumersPlace(t *testing.T) {
	for name, c := range map[string]struct {
		info jetstream.ConsumerInfo
		last uint64
		want model.GroupOffset
	}{
		"never sent anything": {jetstream.ConsumerInfo{Stream: "orders", NumPending: 10}, 10,
			model.GroupOffset{Topic: "orders", Current: -1, End: 11, Lag: 10}},
		"sent four, three acknowledged": {jetstream.ConsumerInfo{Stream: "orders", NumPending: 6, NumAckPending: 1,
			Delivered: jetstream.SequenceInfo{Consumer: 4, Stream: 4}, AckFloor: jetstream.SequenceInfo{Consumer: 3, Stream: 3}}, 10,
			model.GroupOffset{Topic: "orders", Current: 4, End: 11, Lag: 7}},
		"sent, nothing acknowledged yet": {jetstream.ConsumerInfo{Stream: "orders", NumAckPending: 2,
			Delivered: jetstream.SequenceInfo{Consumer: 2, Stream: 2}}, 2,
			model.GroupOffset{Topic: "orders", Current: 1, End: 3, Lag: 2}},
		"all caught up": {jetstream.ConsumerInfo{Stream: "orders",
			Delivered: jetstream.SequenceInfo{Consumer: 10, Stream: 10}, AckFloor: jetstream.SequenceInfo{Consumer: 10, Stream: 10}}, 10,
			model.GroupOffset{Topic: "orders", Current: 11, End: 11, Lag: 0}},
	} {
		if got := progressOf(&c.info, c.last); got != c.want {
			t.Errorf("%s: %+v, want %+v", name, got, c.want)
		}
	}
}

func TestAStreamAsALog(t *testing.T) {
	info := &jetstream.StreamInfo{
		Config: jetstream.StreamConfig{Name: "orders", Subjects: []string{"orders.>", "audit"}, Replicas: 3,
			Retention: jetstream.WorkQueuePolicy, Storage: jetstream.FileStorage, Discard: jetstream.DiscardNew,
			MaxMsgs: 1000, MaxBytes: -1, MaxAge: 36 * time.Hour},
		State: jetstream.StreamState{Msgs: 5, FirstSeq: 7, LastSeq: 12},
	}
	tp := topicOf(info)
	if tp.Name != "orders" || tp.ReplicationFactor != 3 || len(tp.Partitions) != 1 || tp.Attrs["messages"] != "5" {
		t.Fatalf("%+v", tp)
	}
	if _, said := tp.Attrs["description"]; said {
		t.Error("a stream with nothing written about it has a description")
	}
	if p := tp.Partitions[0]; p.ID != 0 || p.LowWatermark != 7 || p.HighWatermark != 13 {
		t.Errorf("its log is %+v", p)
	}
	var names []string
	conf := map[string]string{}
	for _, c := range tp.Config {
		names = append(names, c.Name)
		conf[c.Name] = c.Value
	}
	// In an order of this driver's choosing, the same every time.
	if !reflect.DeepEqual(names, []string{"subjects", "retention", "storage", "discard", "max messages", "max bytes", "max age", "replicas"}) {
		t.Errorf("its settings are %v", names)
	}
	for name, want := range map[string]string{
		"subjects": "orders.>, audit", "retention": "WorkQueue", "storage": "File", "discard": "DiscardNew",
		"max messages": "1000", "max bytes": "unlimited", "max age": "36h0m0s", "replicas": "3",
	} {
		if conf[name] != want {
			t.Errorf("%s is %q, want %q", name, conf[name], want)
		}
	}
	for n, want := range map[int64]string{-1: "unlimited", 0: "unlimited", 1: "1", 5000: "5000"} {
		if got := limit(n); got != want {
			t.Errorf("a limit of %d is said %q", n, got)
		}
	}
	for d, want := range map[time.Duration]string{-1: "unlimited", 0: "unlimited", time.Minute: "1m0s"} {
		if got := age(d); got != want {
			t.Errorf("an age of %v is said %q", d, got)
		}
	}
}

func TestWhatItClaims(t *testing.T) {
	caps := (&natsSource{}).Capabilities()
	if caps.Paradigm != model.ParadigmStream || caps.Query.Supported {
		t.Errorf("it is a %v with a query language: %v", caps.Paradigm, caps.Query.Supported)
	}
	if s := caps.Stream; !s.Consume || !s.SeekTimestamp || !s.Follow || !s.ConsumerGroups || !s.Produce || !s.Throughput ||
		s.TopicAdmin || s.ResetOffsets || s.ACLs || s.ManageACLs || s.SchemaRegistry {
		t.Errorf("of streams it claims %+v", s)
	}
	for _, k := range []model.ObjectKind{model.KindCluster, model.KindFolder, model.KindTopic, model.KindConsumerGroup} {
		if !caps.Supports(k) {
			t.Errorf("it does not hold a %s", k)
		}
	}
	if caps.Supports(model.KindPartition) {
		t.Error("it claims partitions, and a stream has none to show")
	}
	var src any = &natsSource{}
	for name, has := range map[string]bool{
		"TopicAdmin":     func() bool { _, ok := src.(source.TopicAdmin); return ok }(),
		"OffsetResetter": func() bool { _, ok := src.(source.OffsetResetter); return ok }(),
		"ACLInspector":   func() bool { _, ok := src.(source.ACLInspector); return ok }(),
		"Writer":         func() bool { _, ok := src.(source.Writer); return ok }(),
	} {
		if has {
			t.Errorf("it implements %s, and claims it does not", name)
		}
	}
}

// A refusal about a stream that is not there is said in this driver's words,
// and nothing else is touched.
func TestAMissingStreamIsSaidPlainly(t *testing.T) {
	if got := missing(fmt.Errorf("x: %w", jetstream.ErrStreamNotFound), "orders"); got.Error() != `nats: this server has no stream "orders"` {
		t.Errorf("a missing stream: %v", got)
	}
	other := errors.New("odd")
	if got := missing(other, "orders"); !errors.Is(got, other) || got.Error() != "odd" {
		t.Errorf("another failure became %v", got)
	}
}
