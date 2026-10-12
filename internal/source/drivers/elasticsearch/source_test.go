package elasticsearch

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// What needs no search engine to be true. A server made for the test answers
// what a real one would, where the thing being held is what this does with the
// answer; what a real one actually answers is the live suite's to say.

// served is a server answering with handler, and a connection's settings for it.
func served(t *testing.T, handler http.HandlerFunc) source.ConnectionConfig {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return configFor(t, srv.URL, "disable")
}

func configFor(t *testing.T, addr, mode string) source.ConnectionConfig {
	t.Helper()
	u, err := url.Parse(addr)
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(u.Port())
	return source.ConnectionConfig{DriverID: driverID, Host: u.Hostname(), Port: port,
		TLS: source.TLSConfig{Mode: mode}}
}

// hello is what a search engine says at its root.
func hello(distribution string) string {
	d := ""
	if distribution != "" {
		d = `"distribution":"` + distribution + `",`
	}
	return `{"name":"node-1","cluster_name":"c","version":{` + d + `"number":"8.1.0"},"tagline":"You Know, for Search"}`
}

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
	if d.ID != "elasticsearch" || d.Paradigm != model.ParadigmDocument || d.DefaultPort != 9200 {
		t.Errorf("described as %+v", d)
	}
	if !reflect.DeepEqual(d.URLSchemes, []string{"elasticsearch", "opensearch"}) {
		t.Errorf("its URLs begin %v", d.URLSchemes)
	}
	fields := map[string]source.Field{}
	for _, f := range d.Fields {
		fields[f.Key] = f
	}
	if !fields["host"].Required || fields["port"].Default != "9200" || fields["user"].Required {
		t.Errorf("the form asks for %+v", d.Fields)
	}
	// The password is a secret and is kept as one (FR-1.5).
	if p := fields["password"]; !p.Secret || p.Kind != source.FieldPassword {
		t.Errorf("the password is %+v", p)
	}
	got, err := source.Lookup(driverID)
	if err != nil || got.Describe().ID != driverID {
		t.Errorf("it is not registered: %v", err)
	}
}

// What cannot become a request is said before the network is touched.
func TestWhatAConnectionBecomes(t *testing.T) {
	secret := func(string) (string, error) { return "s3cret", nil }
	c, err := dial(source.ConnectionConfig{Host: " search.example ", User: " ada ", Secret: secret})
	if err != nil {
		t.Fatal(err)
	}
	// Nothing said about encryption is encrypted and verified (NFR-S3), on
	// the port a search engine listens on.
	if c.base != "https://search.example:9200" || c.user != "ada" || c.pass != "s3cret" {
		t.Errorf("it would ask %s as %q with %q", c.base, c.user, c.pass)
	}
	tr := c.http.Transport.(*http.Transport).TLSClientConfig
	if tr == nil || tr.InsecureSkipVerify || tr.ServerName != "search.example" {
		t.Errorf("said nothing about encryption, and got %+v", tr)
	}

	for mode, want := range map[string]struct {
		scheme string
		skip   bool
	}{
		"disable": {"http", false}, "require": {"https", true},
		"verify-ca": {"https", false}, "verify-full": {"https", false},
	} {
		c, err := dial(source.ConnectionConfig{Host: "h", Port: 9, TLS: source.TLSConfig{Mode: mode, ServerName: "other"}})
		if err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		tr := c.http.Transport.(*http.Transport).TLSClientConfig
		if c.base != want.scheme+"://h:9" {
			t.Errorf("%s asks %s", mode, c.base)
		}
		if mode == "disable" {
			if tr != nil {
				t.Errorf("disable still configures encryption: %+v", tr)
			}
			continue
		}
		if tr.InsecureSkipVerify != want.skip || tr.ServerName != "other" {
			t.Errorf("%s: skip %v, name %q", mode, tr.InsecureSkipVerify, tr.ServerName)
		}
	}

	// No password is read for nobody: a server with security off has no user.
	asked := false
	if _, err := dial(source.ConnectionConfig{Host: "h", Secret: func(string) (string, error) {
		asked = true
		return "", nil
	}}); err != nil || asked {
		t.Errorf("a password was asked for with no user: %v, %v", asked, err)
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
		"no host":      {source.ConnectionConfig{Host: "  "}, "no host"},
		"a wrong mode": {source.ConnectionConfig{Host: "h", TLS: source.TLSConfig{Mode: "sometimes"}}, `"sometimes"`},
		"a CA file that is not there": {source.ConnectionConfig{Host: "h",
			TLS: source.TLSConfig{Mode: "verify-full", CAFile: missing}}, "could not be read"},
		"a CA file with no certificate": {source.ConnectionConfig{Host: "h",
			TLS: source.TLSConfig{Mode: "verify-full", CAFile: empty}}, "no certificate"},
		"a client certificate without its key": {source.ConnectionConfig{Host: "h",
			TLS: source.TLSConfig{Mode: "verify-full", CertFile: empty}}, "certificate and key"},
		"a password that cannot be read": {source.ConnectionConfig{Host: "h", User: "ada",
			Secret: func(string) (string, error) { return "", errors.New("keychain locked") }}, "password"},
	} {
		_, err := dial(c.cfg)
		if kindOf(err) != source.ConnectConfig || !strings.Contains(hintOf(err), c.says) {
			t.Errorf("%s: %v (%q), want a configuration error mentioning %q", name, err, hintOf(err), c.says)
		}
	}
}

// The first request tells four things apart, and each sends somebody
// somewhere different.
func TestAFailureToConnectIsCalledWhatItIs(t *testing.T) {
	answer := func(status int, body string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			_, _ = io.WriteString(w, body)
		}
	}
	for name, c := range map[string]struct {
		handler http.HandlerFunc
		user    string
		kind    source.ConnectKind
		says    string
	}{
		"credentials wanted and none given": {answer(401, `{"error":{"type":"security_exception","reason":"missing authentication"},"status":401}`),
			"", source.ConnectAuth, "has none"},
		"credentials refused": {answer(401, "Unauthorized"), "ada", source.ConnectAuth, "refused those credentials"},
		"forbidden":           {answer(403, "no"), "ada", source.ConnectAuth, "refused those credentials"},
		"still starting":      {answer(503, "starting"), "", source.ConnectRefused, "still be starting"},
		"something else on that port, speaking JSON": {answer(200, `{"service":"other"}`),
			"", source.ConnectConfig, "not Elasticsearch or OpenSearch"},
		"something else, not speaking JSON": {answer(200, "<html>hello</html>"), "", source.ConnectUnknown, "could not be read"},
		"something else, refusing":          {answer(404, "nothing here"), "", source.ConnectUnknown, "could not be read"},
	} {
		cfg := served(t, c.handler)
		cfg.User = c.user
		cfg.Secret = func(string) (string, error) { return "pw", nil }
		_, err := Driver{}.Open(context.Background(), cfg)
		if kindOf(err) != c.kind || !strings.Contains(hintOf(err), c.says) {
			t.Errorf("%s: %v (%q), want kind %v mentioning %q", name, err, hintOf(err), c.kind, c.says)
		}
	}

	// Nothing there at all.
	srv := httptest.NewServer(http.NotFoundHandler())
	cfg := configFor(t, srv.URL, "disable")
	srv.Close()
	if _, err := (Driver{}).Open(context.Background(), cfg); kindOf(err) != source.ConnectUnreachable {
		t.Errorf("nothing listening: %v", err)
	}
	cfg.Host = "no-such-host.invalid"
	if _, err := (Driver{}).Open(context.Background(), cfg); kindOf(err) != source.ConnectUnreachable {
		t.Errorf("no such host: %v", err)
	}
}

// A server is what it says it is, and is asked as who the connection says.
func TestWhatAServerSaysItIs(t *testing.T) {
	for distribution, want := range map[string]string{"": "Elasticsearch", "opensearch": "OpenSearch", "OpenSearch": "OpenSearch"} {
		var user, pass string
		var authed bool
		cfg := served(t, func(w http.ResponseWriter, r *http.Request) {
			user, pass, authed = r.BasicAuth()
			if r.URL.Path == "/_cluster/health" {
				_, _ = io.WriteString(w, `{"status":"yellow","number_of_nodes":3}`)
				return
			}
			_, _ = io.WriteString(w, hello(distribution))
		})
		cfg.User = "ada"
		cfg.Secret = func(key string) (string, error) { return "pw for " + key, nil }
		src, err := Driver{}.Open(context.Background(), cfg)
		if err != nil {
			t.Fatalf("%q: %v", distribution, err)
		}
		if !authed || user != "ada" || pass != "pw for password" {
			t.Errorf("asked as %q with %q (%v)", user, pass, authed)
		}
		info, err := src.(*esSource).Info(context.Background())
		if err != nil || info.Product != want || info.Version != "8.1.0" {
			t.Errorf("%q says it is %+v, %v", distribution, info, err)
		}
		if !reflect.DeepEqual(info.Attrs, map[string]string{"cluster": "c", "node": "node-1", "health": "yellow", "nodes": "3"}) {
			t.Errorf("about itself: %v", info.Attrs)
		}
		if err := src.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}

	// And nobody is nobody: no credentials are sent where there is no user.
	var authed bool
	cfg := served(t, func(w http.ResponseWriter, r *http.Request) {
		_, _, authed = r.BasicAuth()
		_, _ = io.WriteString(w, hello(""))
	})
	if _, err := (Driver{}).Open(context.Background(), cfg); err != nil || authed {
		t.Errorf("with no user, credentials were sent: %v, %v", authed, err)
	}
}

// Encryption is verified unless a connection was told not to, and a
// certificate nobody vouches for is a failure about certificates.
func TestACertificateIsCheckedUnlessToldNotTo(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, hello(""))
	}))
	t.Cleanup(srv.Close)
	ctx := context.Background()

	for _, mode := range []string{"", "verify-full", "verify-ca"} {
		if _, err := (Driver{}).Open(ctx, configFor(t, srv.URL, mode)); kindOf(err) != source.ConnectTLS {
			t.Errorf("mode %q against a certificate nobody vouches for: %v", mode, err)
		}
	}
	if _, err := (Driver{}).Open(ctx, configFor(t, srv.URL, "require")); err != nil {
		t.Errorf("told not to check: %v", err)
	}
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := configFor(t, srv.URL, "verify-full")
	cfg.TLS.CAFile = ca
	cfg.TLS.ServerName = "example.com" // which is who the test certificate says it is
	if _, err := (Driver{}).Open(ctx, cfg); err != nil {
		t.Errorf("with its authority given: %v", err)
	}
	cfg.TLS.ServerName = "somebody.else"
	if _, err := (Driver{}).Open(ctx, cfg); kindOf(err) != source.ConnectTLS {
		t.Errorf("a certificate for somebody else: %v", err)
	}
}

// What the engine says went wrong is typed, and the type is what is said.
func TestARefusalSaysWhatWentWrong(t *testing.T) {
	long := strings.Repeat("x", 500)
	for name, c := range map[string]struct {
		status int
		body   string
		want   string
	}{
		"typed": {404, `{"error":{"type":"index_not_found_exception","reason":"no such index [a]"},"status":404}`,
			"elasticsearch: index not found: no such index [a]"},
		"with its cause": {400, `{"error":{"type":"search_phase_execution_exception","reason":"all shards failed","caused_by":{"type":"x","reason":"the real reason"}}}`,
			"elasticsearch: search phase execution: all shards failed: the real reason"},
		"its cause said once": {400, `{"error":{"type":"x_exception","reason":"same","caused_by":{"type":"y","reason":"same"}}}`,
			"elasticsearch: x: same"},
		"typed with no reason": {400, `{"error":{"type":"too_many_requests_exception"}}`, "elasticsearch: too many requests"},
		"not its own":          {502, "Bad Gateway from a proxy", "elasticsearch: the server answered 502: Bad Gateway from a proxy"},
		"not its own, long":    {500, long, "elasticsearch: the server answered 500: " + long[:200] + "…"},
		"nothing at all":       {504, "  ", "elasticsearch: the server answered 504: Gateway Timeout"},
	} {
		if got := said(c.status, []byte(c.body)).Error(); got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", name, got, c.want)
		}
	}
	if !notFound(said(404, nil)) || !notFound(said(400, []byte(`{"error":{"type":"index_not_found_exception"}}`))) ||
		notFound(said(400, []byte(`{"error":{"type":"parsing_exception"}}`))) || notFound(errors.New("other")) {
		t.Error("what is not there is told from what is refused by its status or its type")
	}
}

// A request is what was meant, whatever the names in it.
func TestWhatIsAsked(t *testing.T) {
	var got *http.Request
	var body string
	cfg := served(t, func(w http.ResponseWriter, r *http.Request) {
		got = r
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		_, _ = io.WriteString(w, `{"count":7}`)
	})
	c, err := dial(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var out struct{ Count int }
	err = c.ask(context.Background(), http.MethodPost, "/"+segment("a/b?c d")+"/_count",
		url.Values{"q": {"x y&z"}}, map[string]any{"k": "v"}, &out)
	if err != nil || out.Count != 7 {
		t.Fatalf("answered %+v, %v", out, err)
	}
	// One segment, though the name has a slash and a question mark in it.
	if got.URL.EscapedPath() != "/a%2Fb%3Fc%20d/_count" || got.URL.Query().Get("q") != "x y&z" {
		t.Errorf("asked %s ? %s", got.URL.EscapedPath(), got.URL.RawQuery)
	}
	if got.Method != http.MethodPost || body != `{"k":"v"}` || got.Header.Get("Content-Type") != "application/json" ||
		got.Header.Get("Accept") != "application/json" {
		t.Errorf("sent %s %q as %q", got.Method, body, got.Header.Get("Content-Type"))
	}
	// With nothing to send, nothing is said about what is sent.
	if err := c.ask(context.Background(), http.MethodGet, "/", nil, nil, nil); err != nil || body != "" ||
		got.Header.Get("Content-Type") != "" || got.URL.RawQuery != "" {
		t.Errorf("a bare request sent %q as %q ? %q: %v", body, got.Header.Get("Content-Type"), got.URL.RawQuery, err)
	}
	// A request that is stopped stops.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.ask(ctx, http.MethodGet, "/", nil, nil, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("a stopped request: %v", err)
	}
	if err := c.ask(context.Background(), http.MethodGet, "/", nil, make(chan int), nil); err == nil {
		t.Error("a body that cannot be written was sent")
	}
}

func TestWhichIndicesAreTheEnginesOwn(t *testing.T) {
	for name, want := range map[string]bool{".kibana_1": true, ".ds-logs-2026": true, "logs": false, "a.b": false, "": false} {
		if hidden(name) != want {
			t.Errorf("hidden(%q) = %v", name, !want)
		}
	}
}

func TestOnlyAnIndexHoldsDocuments(t *testing.T) {
	if name, err := indexOf(model.NewRef(model.KindCollection, "logs")); err != nil || name != "logs" {
		t.Errorf("an index: %q, %v", name, err)
	}
	for _, bad := range []model.ObjectRef{
		{}, model.NewRef(model.KindTable, "logs"), model.NewRef(model.KindCollection, "a", "b"),
		model.NewRef(model.KindCollection, ""), model.ClassRef(model.ObjectRef{}, model.KindCollection),
	} {
		if _, err := indexOf(bad); err == nil {
			t.Errorf("%v was taken for an index", bad)
		}
	}
}

// mapping is the fields a mapping written as JSON flattens to, by name.
func mapping(t *testing.T, props string) map[string]field {
	t.Helper()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(props), &raw); err != nil {
		t.Fatal(err)
	}
	out := map[string]field{}
	for _, f := range flatten("", "", raw) {
		if _, twice := out[f.name]; twice {
			t.Errorf("%s is a column twice", f.name)
		}
		out[f.name] = f
	}
	return out
}

const sample = `{
	"id":      {"type":"long"},
	"title":   {"type":"text","fields":{"raw":{"type":"keyword"},"keyword":{"type":"keyword"},"en":{"type":"text"}}},
	"body":    {"type":"text","fields":{"en":{"type":"text"}}},
	"quick":   {"type":"search_as_you_type"},
	"tag":     {"type":"keyword"},
	"at":      {"type":"date"},
	"where":   {"type":"geo_point"},
	"blob":    {"type":"binary"},
	"labels":  {"type":"flattened"},
	"empty":   {"type":"object"},
	"untyped": {},
	"broken":  "not a property",
	"author":  {"properties":{"name":{"type":"keyword"},"home":{"properties":{"city":{"type":"keyword"}}}}},
	"notes":   {"type":"nested","properties":{"by":{"type":"keyword"},"reply":{"properties":{"by":{"type":"keyword"}}}}}
}`

// What a mapping's fields are to a grid: which are columns, and how the whole
// value of each is asked for.
func TestWhatAMappingsFieldsAre(t *testing.T) {
	got := mapping(t, sample)
	for name, want := range map[string]field{
		"id":               {exact: "id"},
		"title":            {exact: "title.keyword", analysed: true}, // the first keyword by name, whatever order it came in
		"body":             {analysed: true},
		"quick":            {analysed: true},
		"tag":              {exact: "tag"},
		"at":               {exact: "at"},
		"where":            {},
		"blob":             {},
		"labels":           {exact: "labels"},
		"empty":            {},
		"untyped":          {},
		"author.name":      {exact: "author.name"},
		"author.home.city": {exact: "author.home.city"},
		"notes.by":         {exact: "notes.by", nested: "notes"},
		"notes.reply.by":   {exact: "notes.reply.by", nested: "notes"},
	} {
		f, ok := got[name]
		if !ok {
			t.Errorf("%s is not a column", name)
			continue
		}
		if f.exact != want.exact || f.analysed != want.analysed || f.nested != want.nested {
			t.Errorf("%s: exact %q, analysed %v, nested %q; want %q, %v, %q",
				name, f.exact, f.analysed, f.nested, want.exact, want.analysed, want.nested)
		}
	}
	// An object with fields is its fields; a sub-field is the same data again;
	// and what is not a property is not a column.
	for _, name := range []string{"author", "author.home", "notes", "title.keyword", "title.raw", "body.en", "broken"} {
		if _, ok := got[name]; ok {
			t.Errorf("%s is a column", name)
		}
	}
	if len(got) != 15 {
		t.Errorf("%d columns", len(got))
	}
}

func TestWhatAMappedTypeIsToAGrid(t *testing.T) {
	for native, want := range map[string]model.TypeClass{
		"long": model.TypeInteger, "integer": model.TypeInteger, "short": model.TypeInteger,
		"byte": model.TypeInteger, "unsigned_long": model.TypeInteger,
		"double": model.TypeFloat, "float": model.TypeFloat, "half_float": model.TypeFloat, "scaled_float": model.TypeFloat,
		"boolean": model.TypeBool, "date": model.TypeTimestamp, "date_nanos": model.TypeTimestamp,
		"object": model.TypeJSON, "nested": model.TypeJSON, "flattened": model.TypeJSON,
		"geo_point": model.TypeJSON, "geo_shape": model.TypeJSON,
		"text": model.TypeString, "keyword": model.TypeString, "ip": model.TypeString,
		"something_new": model.TypeString, "": model.TypeUnknown,
	} {
		if got := typeOf(native); got.Class != want || got.Native != native || got.Length != -1 {
			t.Errorf("%q is %+v, want class %v", native, got, want)
		}
	}
}

// fieldsOf are a mapping's fields as a browse holds them.
func fieldsOf(t *testing.T, props string) []field {
	t.Helper()
	var out []field
	for _, f := range mapping(t, props) {
		out = append(out, f)
	}
	return out
}

func asJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// What each of the grid's filters becomes. What the engine then makes of it
// is the live suite's to say; this holds that the request is the one meant,
// including which name a field is asked by.
func TestWhatAFilterBecomes(t *testing.T) {
	mapped := fieldsOf(t, sample)
	when := time.Date(2026, 3, 4, 5, 6, 7, 800, time.FixedZone("x", 3600))
	for name, c := range map[string]struct {
		f    source.Filter
		want string
	}{
		"equal": {source.Filter{Column: "tag", Op: source.OpEqual, Values: []any{"a"}},
			`{"term":{"tag":"a"}}`},
		"equal, through the keyword": {source.Filter{Column: "title", Op: source.OpEqual, Values: []any{"a"}},
			`{"term":{"title.keyword":"a"}}`},
		"equal, as words": {source.Filter{Column: "body", Op: source.OpEqual, Values: []any{"a b"}},
			`{"match_phrase":{"body":"a b"}}`},
		"equal to nothing": {source.Filter{Column: "tag", Op: source.OpEqual, Values: []any{nil}},
			`{"bool":{"must_not":[{"exists":{"field":"tag"}}]}}`},
		"not equal": {source.Filter{Column: "tag", Op: source.OpNotEqual, Values: []any{"a"}},
			`{"bool":{"must_not":[{"term":{"tag":"a"}}]}}`},
		"not equal to nothing": {source.Filter{Column: "title", Op: source.OpNotEqual, Values: []any{nil}},
			`{"bool":{"must_not":[{"bool":{"must_not":[{"exists":{"field":"title"}}]}}]}}`},
		"less":             {source.Filter{Column: "id", Op: source.OpLess, Values: []any{int64(3)}}, `{"range":{"id":{"lt":3}}}`},
		"less or equal":    {source.Filter{Column: "id", Op: source.OpLessEqual, Values: []any{int64(3)}}, `{"range":{"id":{"lte":3}}}`},
		"greater":          {source.Filter{Column: "id", Op: source.OpGreater, Values: []any{int64(3)}}, `{"range":{"id":{"gt":3}}}`},
		"greater or equal": {source.Filter{Column: "id", Op: source.OpGreaterEqual, Values: []any{int64(3)}}, `{"range":{"id":{"gte":3}}}`},
		"a time, as the engine writes one": {source.Filter{Column: "at", Op: source.OpGreater, Values: []any{when}},
			`{"range":{"at":{"gt":"2026-03-04T04:06:07.0000008Z"}}}`},
		"an exact number, as its digits": {source.Filter{Column: "id", Op: source.OpEqual, Values: []any{model.Decimal("18446744073709551615")}},
			`{"term":{"id":18446744073709551615}}`},
		"bytes, as text": {source.Filter{Column: "tag", Op: source.OpEqual, Values: []any{[]byte("a")}},
			`{"term":{"tag":"a"}}`},
		"between": {source.Filter{Column: "id", Op: source.OpBetween, Values: []any{int64(1), int64(9)}},
			`{"range":{"id":{"gte":1,"lte":9}}}`},
		"one of": {source.Filter{Column: "title", Op: source.OpIn, Values: []any{"a", when}},
			`{"terms":{"title.keyword":["a","2026-03-04T04:06:07.0000008Z"]}}`},
		"none of": {source.Filter{Column: "tag", Op: source.OpNotIn, Values: []any{"a"}},
			`{"bool":{"must_not":[{"terms":{"tag":["a"]}}]}}`},
		"has none": {source.Filter{Column: "where", Op: source.OpIsNull},
			`{"bool":{"must_not":[{"exists":{"field":"where"}}]}}`},
		"has one": {source.Filter{Column: "title", Op: source.OpIsNotNull},
			`{"exists":{"field":"title"}}`},
		// Whether there is one is all that can be asked of a place, and both
		// ways of asking it can be.
		"has a place": {source.Filter{Column: "where", Op: source.OpIsNotNull},
			`{"exists":{"field":"where"}}`},
		"contains": {source.Filter{Column: "title", Op: source.OpContains, Values: []any{`50%*a?b\`}},
			`{"wildcard":{"title.keyword":{"case_insensitive":true,"value":"*50%\\*a\\?b\\\\*"}}}`},
		"contains, of something that is not text": {source.Filter{Column: "tag", Op: source.OpContains, Values: []any{42}},
			`{"wildcard":{"tag":{"case_insensitive":true,"value":"*42*"}}}`},
		"contains, as words": {source.Filter{Column: "body", Op: source.OpContains, Values: []any{"a b"}},
			`{"match_phrase":{"body":"a b"}}`},
		"like": {source.Filter{Column: "tag", Op: source.OpLike, Values: []any{"a%b_c"}},
			`{"wildcard":{"tag":{"value":"a*b?c"}}}`},
		"not like": {source.Filter{Column: "tag", Op: source.OpNotLike, Values: []any{"a%"}},
			`{"bool":{"must_not":[{"wildcard":{"tag":{"value":"a*"}}}]}}`},
		"a regular expression": {source.Filter{Column: "title", Op: source.OpRegex, Values: []any{"a.*"}},
			`{"regexp":{"title.keyword":{"value":"a.*"}}}`},
		"negated": {source.Filter{Column: "tag", Op: source.OpEqual, Values: []any{"a"}, Negate: true},
			`{"bool":{"must_not":[{"term":{"tag":"a"}}]}}`},
		"by identity": {source.Filter{Column: "_id", Op: source.OpIn, Values: []any{"a", "b"}},
			`{"terms":{"_id":["a","b"]}}`},
	} {
		got, err := filter(mapped, c.f)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if text := asJSON(t, got); text != c.want {
			t.Errorf("%s:\n got %s\nwant %s", name, text, c.want)
		}
	}
}

func TestAFilterThatCannotBeWrittenIsRefused(t *testing.T) {
	mapped := fieldsOf(t, sample)
	one := []any{"a"}
	for name, c := range map[string]struct {
		f    source.Filter
		says string
	}{
		"no field":                         {source.Filter{Op: source.OpEqual, Values: one}, "needs a field"},
		"no such field":                    {source.Filter{Column: "nowhere", Op: source.OpEqual, Values: one}, `"nowhere" is not a field`},
		"a sub-field by its name":          {source.Filter{Column: "title.keyword", Op: source.OpEqual, Values: one}, "not a field"},
		"the whole document":               {source.Filter{Column: "_source", Op: source.OpEqual, Values: one}, "whole document"},
		"nested":                           {source.Filter{Column: "notes.reply.by", Op: source.OpEqual, Values: one}, "nested"},
		"nested, even whether it is there": {source.Filter{Column: "notes.by", Op: source.OpIsNull}, "nested"},
		"a place equal":                    {source.Filter{Column: "where", Op: source.OpEqual, Values: one}, "holds a geo_point, which cannot be compared"},
		"an object contains":               {source.Filter{Column: "empty", Op: source.OpContains, Values: one}, "holds an object"},
		"an untyped field":                 {source.Filter{Column: "untyped", Op: source.OpEqual, Values: one}, "holds an object"},
		"bytes in a list":                  {source.Filter{Column: "blob", Op: source.OpIn, Values: one}, "holds a binary"},
		"words ordered":                    {source.Filter{Column: "body", Op: source.OpLess, Values: one}, "analysed text, which is held as its words: it has no order"},
		"words between":                    {source.Filter{Column: "body", Op: source.OpBetween, Values: []any{"a", "b"}}, "it has no order"},
		"words in a list":                  {source.Filter{Column: "body", Op: source.OpIn, Values: one}, "cannot be one of a list"},
		"words not in a list":              {source.Filter{Column: "body", Op: source.OpNotIn, Values: one}, "cannot be one of a list"},
		"a pattern across words":           {source.Filter{Column: "body", Op: source.OpLike, Values: one}, "a pattern has nothing to run across"},
		"not a pattern across words":       {source.Filter{Column: "body", Op: source.OpNotLike, Values: one}, "a pattern has nothing"},
		"an expression across words":       {source.Filter{Column: "body", Op: source.OpRegex, Values: one}, "an expression has nothing"},
		"ordered against nothing":          {source.Filter{Column: "id", Op: source.OpGreater, Values: []any{nil}}, "with nothing"},
		"an operator nobody knows":         {source.Filter{Column: "id", Op: "sounds like", Values: one}, `"sounds like" is not one`},
		"equal to two":                     {source.Filter{Column: "id", Op: source.OpEqual, Values: []any{1, 2}}, "takes 1 value(s), got 2"},
		"not equal to none":                {source.Filter{Column: "id", Op: source.OpNotEqual}, "takes 1 value(s), got 0"},
		"less than two":                    {source.Filter{Column: "id", Op: source.OpLess, Values: []any{1, 2}}, "takes 1 value(s), got 2"},
		"between one":                      {source.Filter{Column: "id", Op: source.OpBetween, Values: one}, "takes 2 value(s), got 1"},
		"in none":                          {source.Filter{Column: "id", Op: source.OpIn}, "at least one value"},
		"null of something":                {source.Filter{Column: "id", Op: source.OpIsNull, Values: one}, "takes 0 value(s), got 1"},
		"not null of something":            {source.Filter{Column: "id", Op: source.OpIsNotNull, Values: one}, "takes 0 value(s), got 1"},
		"contains none":                    {source.Filter{Column: "tag", Op: source.OpContains}, "takes 1 value(s), got 0"},
		"like none":                        {source.Filter{Column: "tag", Op: source.OpLike}, "takes 1 value(s), got 0"},
		"an expression of two":             {source.Filter{Column: "tag", Op: source.OpRegex, Values: []any{"a", "b"}}, "takes 1 value(s), got 2"},
	} {
		got, err := filter(mapped, c.f)
		if err == nil {
			t.Errorf("%s became %s", name, asJSON(t, got))
		} else if !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: %v, want it to mention %q", name, err, c.says)
		}
	}
}

// The condition a browse reads under: everything, or every filter at once —
// and never a condition typed in a language this has not claimed.
func TestTheConditionABrowseReadsUnder(t *testing.T) {
	mapped := fieldsOf(t, sample)
	q, err := query(mapped, source.BrowseOptions{Where: "  "})
	if err != nil || asJSON(t, q) != `{"match_all":{}}` {
		t.Errorf("no filters: %s, %v", asJSON(t, q), err)
	}
	q, err = query(mapped, source.BrowseOptions{Filters: []source.Filter{
		{Column: "tag", Op: source.OpEqual, Values: []any{"a"}}, {Column: "id", Op: source.OpIsNotNull}}})
	if err != nil || asJSON(t, q) != `{"bool":{"filter":[{"term":{"tag":"a"}},{"exists":{"field":"id"}}]}}` {
		t.Errorf("two filters: %s, %v", asJSON(t, q), err)
	}
	if _, err := query(mapped, source.BrowseOptions{Where: "tag:a"}); err == nil || !strings.Contains(err.Error(), "typed condition") {
		t.Errorf("a typed condition: %v", err)
	}
	if _, err := query(mapped, source.BrowseOptions{Filters: []source.Filter{
		{Column: "tag", Op: source.OpEqual, Values: []any{"a"}}, {Column: "nowhere", Op: source.OpIsNull}}}); err == nil {
		t.Error("a filter that cannot be written was left out of the condition")
	}
}

func TestAPatternIsWrittenTheEnginesWay(t *testing.T) {
	for like, want := range map[string]string{
		"abc": "abc", "a%": "a*", "%a%": "*a*", "a_c": "a?c",
		`50\%`: "50%", `a\_b`: "a_b", `a\\b`: `a\\b`, // a backslash makes the next character itself
		"a*b": `a\*b`, "a?b": `a\?b`, `a\*b`: `a\*b`, // and the engine's own wildcards mean themselves
		`a\`: `a\\`, "": "", "é%": "é*",
	} {
		if got := likeToWildcard(like); got != want {
			t.Errorf("LIKE %q is wildcard %q, want %q", like, got, want)
		}
	}
	for text, want := range map[string]string{"abc": "abc", "50%": "50%", "a_b": "a_b", "a*b?c": `a\*b\?c`, `a\b`: `a\\b`} {
		if got := escapeWildcard(text); got != want {
			t.Errorf("%q written to match itself is %q, want %q", text, got, want)
		}
	}
}

func TestTheOrderABrowseReadsIn(t *testing.T) {
	mapped := fieldsOf(t, sample)
	for name, c := range map[string]struct {
		sorts []source.Sort
		want  string
	}{
		"none":          {nil, `["_doc"]`},
		"up":            {[]source.Sort{{Column: "id"}}, `[{"id":{"missing":"_last","order":"asc"}},"_doc"]`},
		"down":          {[]source.Sort{{Column: "id", Descending: true}}, `[{"id":{"missing":"_last","order":"desc"}},"_doc"]`},
		"nothing first": {[]source.Sort{{Column: "id", NullsFirst: true}}, `[{"id":{"missing":"_first","order":"asc"}},"_doc"]`},
		"through the keyword, and in the order given": {[]source.Sort{{Column: "title", Descending: true}, {Column: "tag"}},
			`[{"title.keyword":{"missing":"_last","order":"desc"}},{"tag":{"missing":"_last","order":"asc"}},"_doc"]`},
	} {
		got, err := order(mapped, c.sorts)
		if err != nil || asJSON(t, got) != c.want {
			t.Errorf("%s:\n got %s, %v\nwant %s", name, asJSON(t, got), err, c.want)
		}
	}
	for col, says := range map[string]string{
		"body": "analysed text", "_id": "cannot be sorted by _id", "notes.by": "nested", "where": "holds a geo_point, which has no order",
		"nowhere": "not a field", "_source": "whole document", "": "needs a field",
	} {
		if _, err := order(mapped, []source.Sort{{Column: "id"}, {Column: col}}); err == nil || !strings.Contains(err.Error(), says) {
			t.Errorf("a sort by %q: %v, want a refusal mentioning %q", col, err, says)
		}
	}
}

func TestTheRequestABrowseMakes(t *testing.T) {
	mapped := fieldsOf(t, sample)
	body, err := searchBody(mapped, source.BrowseOptions{Offset: 40, Limit: 20})
	if err != nil || asJSON(t, body) != `{"from":40,"query":{"match_all":{}},"size":20,"sort":["_doc"],"track_total_hits":false}` {
		t.Errorf("a page: %s, %v", asJSON(t, body), err)
	}
	// Nobody saying how many is not everything (NFR-P11), and a page before
	// the first is the first.
	body, err = searchBody(mapped, source.BrowseOptions{Offset: -5})
	if err != nil || body["size"] != int64(defaultPage) || body["from"] != int64(0) {
		t.Errorf("with nothing said: %s, %v", asJSON(t, body), err)
	}
	if _, err := searchBody(mapped, source.BrowseOptions{Sorts: []source.Sort{{Column: "body"}}}); err == nil {
		t.Error("a sort that cannot be made was left out of the request")
	}
	if _, err := searchBody(mapped, source.BrowseOptions{Where: "x"}); err == nil {
		t.Error("a condition that cannot be made was left out of the request")
	}
}

func TestTheColumnsOfABrowse(t *testing.T) {
	mapped := []field{{name: "a.b", typ: typeOf("long")}, {name: "c", typ: typeOf("text")}}
	all, err := columns(mapped, nil)
	if err != nil || len(all) != 4 {
		t.Fatalf("%v, %v", all, err)
	}
	var names []string
	for _, c := range all {
		names = append(names, c.def.Name)
		// Nothing here is written to, so nothing here is offered for editing.
		if !c.def.ReadOnly {
			t.Errorf("%s can be edited", c.def.Name)
		}
	}
	// The identity first and the document last, with the mapping between.
	if !reflect.DeepEqual(names, []string{"_id", "a.b", "c", "_source"}) {
		t.Errorf("the columns are %v", names)
	}
	if !reflect.DeepEqual(all[1].path, []string{"a", "b"}) || all[1].def.Type.Class != model.TypeInteger ||
		all[0].def.Type.Class != model.TypeString || all[3].def.Type.Class != model.TypeJSON {
		t.Errorf("a.b is read at %v as %+v", all[1].path, all[1].def.Type)
	}
	some, err := columns(mapped, []string{"_source", "c", "_id"})
	if err != nil || len(some) != 3 || some[0].def.Name != "_source" || some[1].def.Name != "c" || some[2].def.Name != "_id" {
		t.Errorf("asked for three: %v, %v", some, err)
	}
	if _, err := columns(mapped, []string{"c", "d"}); err == nil || !strings.Contains(err.Error(), `"d" is not a field`) {
		t.Errorf("a column that is not there: %v", err)
	}
}

func TestANumberKeepsEveryDigit(t *testing.T) {
	integer, float, date := typeOf("long"), typeOf("double"), typeOf("date")
	at := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	for name, c := range map[string]struct {
		in   any
		typ  model.DataType
		want any
	}{
		"nothing":                        {nil, integer, nil},
		"a boolean":                      {true, typeOf("boolean"), true},
		"an integer":                     {json.Number("9007199254740993"), integer, int64(9007199254740993)}, // which a float64 cannot hold
		"a negative integer":             {json.Number("-7"), integer, int64(-7)},
		"an integer too long":            {json.Number("18446744073709551615"), integer, model.Decimal("18446744073709551615")},
		"a float":                        {json.Number("1.5"), float, 1.5},
		"a float written whole":          {json.Number("3"), float, 3.0},
		"an exponent":                    {json.Number("1e3"), integer, 1000.0},
		"a fraction in an integer field": {json.Number("2.5"), integer, 2.5},
		"a float too large":              {json.Number("1e999"), float, model.Decimal("1e999")},
		"a number nobody mapped":         {json.Number("12"), model.DataType{}, int64(12)},
		"text":                           {"abc", typeOf("keyword"), "abc"},
		"text that looks like a date, in a field that is not one": {"2026-03-04", typeOf("keyword"), "2026-03-04"},
		"a date with a zone":            {"2026-03-04T06:06:07+01:00", date, at},
		"a date with a fraction":        {"2026-03-04T05:06:07.5Z", date, at.Add(500 * time.Millisecond)},
		"a date with no zone":           {"2026-03-04T05:06:07", date, at},
		"a date alone":                  {"2026-03-04", date, time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC)},
		"a date as milliseconds":        {json.Number("1772600767000"), date, at},
		"a date written some other way": {"4th of March", date, "4th of March"},
		"several":                       {[]any{json.Number("1"), json.Number("2")}, integer, []any{int64(1), int64(2)}},
		"several dates":                 {[]any{"2026-03-04"}, date, []any{time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC)}},
		"an object, whose numbers are kept too": {map[string]any{"n": json.Number("9007199254740993"), "in": map[string]any{"f": json.Number("0.5")}},
			typeOf("object"), map[string]any{"n": int64(9007199254740993), "in": map[string]any{"f": 0.5}}},
		"an object's date-like text is text: nothing says it is a date": {map[string]any{"d": "2026-03-04"}, date, map[string]any{"d": "2026-03-04"}},
	} {
		if got := narrow(c.in, c.typ); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %#v (%T), want %#v (%T)", name, got, got, c.want, c.want)
		}
	}
}

func TestWhereInADocumentAFieldIs(t *testing.T) {
	doc, err := document(json.RawMessage(`{
		"a": 1, "b": {"c": {"d": "deep"}}, "e.f": "dotted", "g": {"h.i": "half"},
		"list": [{"n": 1}, {"n": 2}, {"m": 3}, 4], "nil": null,
		"both": {"x": null}, "both.x": "the longer key"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]any{
		"a": json.Number("1"), "b.c.d": "deep",
		"e.f": "dotted", "g.h.i": "half", // a dotted key is the same field as a nested one
		"list.n":  []any{json.Number("1"), json.Number("2")}, // every object's, and only theirs
		"list.zz": nil, "nil": nil, "missing": nil, "a.b": nil, "b.c.d.e": nil,
		"both.x": "the longer key",
	} {
		if got := at(doc, strings.Split(path, ".")); !reflect.DeepEqual(got, want) {
			t.Errorf("%s is %#v, want %#v", path, got, want)
		}
	}
	if got := at(doc, nil); !reflect.DeepEqual(got, any(doc)) {
		t.Error("no path is not the document")
	}
	// No source at all is no document rather than an empty one, and what is
	// not a document is said to be.
	for _, raw := range []string{"", "null"} {
		if d, err := document(json.RawMessage(raw)); d != nil || err != nil {
			t.Errorf("%q: %v, %v", raw, d, err)
		}
	}
	if _, err := document(json.RawMessage(`[1,2]`)); err == nil {
		t.Error("an array was read as a document")
	}
}

func TestAPageOfDocuments(t *testing.T) {
	cols, err := columns([]field{{name: "n", typ: typeOf("long")}, {name: "o.p", typ: typeOf("keyword")}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	st := &hitStream{cols: cols, hits: []hit{
		{ID: "a", Source: json.RawMessage(`{"n": 9007199254740993, "o": {"p": "x"}, "extra": 1}`)},
		{ID: "b"},
		{ID: "c", Source: json.RawMessage(`{broken`)},
	}}
	ctx := context.Background()
	if got := st.Columns(); len(got) != 4 || got[1].Name != "n" || got[2].Name != "o.p" {
		t.Fatalf("columns %v", got)
	}
	row, err := st.Next(ctx)
	want := model.Row{"a", int64(9007199254740993), "x", map[string]any{"n": int64(9007199254740993), "o": map[string]any{"p": "x"}, "extra": int64(1)}}
	if err != nil || !reflect.DeepEqual(row, want) {
		t.Errorf("the first: %#v, %v", row, err)
	}
	// A read that has been stopped says so, and has not moved on.
	stopped, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := st.Next(stopped); !errors.Is(err, context.Canceled) {
		t.Errorf("a stopped read: %v", err)
	}
	// A document with no source is its identity and nothing else.
	row, err = st.Next(ctx)
	if err != nil || !reflect.DeepEqual(row, model.Row{"b", nil, nil, nil}) {
		t.Errorf("the second: %#v, %v", row, err)
	}
	if _, err := st.Next(ctx); err == nil || !strings.Contains(err.Error(), "could not be read") {
		t.Errorf("a document that is not one: %v", err)
	}
	if _, err := st.Next(ctx); !errors.Is(err, io.EOF) {
		t.Errorf("after the last: %v", err)
	}
	for range 2 {
		if err := st.Close(); err != nil {
			t.Errorf("closing, and closing again: %v", err)
		}
	}
}

// Only a refusal about the result window is put in other words; anything else
// is what it was.
func TestOnlyDeepPagingIsReworded(t *testing.T) {
	other := said(400, []byte(`{"error":{"type":"parsing_exception","reason":"bad query"}}`))
	if got := deepPaging(other, 0, 10); got.Error() != other.Error() {
		t.Errorf("another refusal became %v", got)
	}
	plain := errors.New("Result window is too large")
	if got := deepPaging(plain, 0, 10); got.Error() != plain.Error() {
		t.Errorf("an error that is not the engine's became %v", got)
	}
	deep := said(400, []byte(`{"error":{"type":"search_phase_execution_exception","reason":"all shards failed","caused_by":{"type":"illegal_argument_exception","reason":"Result window is too large, from + size must be less than or equal to: [10000] but was [10010]"}}}`))
	got := deepPaging(deep, 10000, 10)
	if got.Error() == deep.Error() || !errors.Is(got, deep) || !strings.Contains(got.Error(), "documents 10001 to 10010") {
		t.Errorf("past the window: %v", got)
	}
}

// How many documents hold each field is asked a few hundred fields at a time,
// and a field inside a nested object is asked for where it is.
func TestPresenceIsAskedInBatches(t *testing.T) {
	var asked []int
	cfg := served(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			_, _ = io.WriteString(w, hello(""))
			return
		}
		var body struct {
			Size  *int                       `json:"size"`
			Total bool                       `json:"track_total_hits"`
			Aggs  map[string]json.RawMessage `json:"aggs"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		// Posted: a question with a body is one a proxy may strip the body
		// from when it is asked any other way.
		if r.Method != http.MethodPost || r.URL.Path != "/logs/_search" || body.Size == nil || *body.Size != 0 || !body.Total {
			t.Errorf("asked %s %s for %+v", r.Method, r.URL.Path, body)
		}
		asked = append(asked, len(body.Aggs))
		counts := map[string]any{}
		for name := range body.Aggs {
			n, _ := strconv.Atoi(strings.TrimPrefix(name, "f"))
			counts[name] = map[string]any{"doc_count": n * 2}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"hits": map[string]any{"total": map[string]any{"value": 1000}}, "aggregations": counts})
	})
	src, err := Driver{}.Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	s := src.(*esSource)
	fields := make([]field, 450)
	for i := range fields {
		fields[i] = field{name: "f" + strconv.Itoa(i)}
	}
	total, holding, err := s.presence(context.Background(), "logs", fields)
	if err != nil || total != 1000 || len(holding) != 450 {
		t.Fatalf("%d, %d fields, %v", total, len(holding), err)
	}
	if !reflect.DeepEqual(asked, []int{200, 200, 50}) {
		t.Errorf("asked in batches of %v", asked)
	}
	for i, n := range holding {
		if n != int64(i*2) {
			t.Fatalf("field %d is held by %d", i, n)
		}
	}
	// No fields is still asked how many documents there are, once.
	asked = nil
	if total, holding, err := s.presence(context.Background(), "logs", nil); err != nil || total != 1000 || len(holding) != 0 || !reflect.DeepEqual(asked, []int{0}) {
		t.Errorf("with no fields: %d, %v, %v, asked %v", total, holding, err, asked)
	}
	// Exactly a batch is one request and not two.
	asked = nil
	if _, _, err := s.presence(context.Background(), "logs", fields[:200]); err != nil || !reflect.DeepEqual(asked, []int{200}) {
		t.Errorf("exactly a batch: asked %v, %v", asked, err)
	}

	if got := asJSON(t, holds(field{name: "a.b"})); got != `{"exists":{"field":"a.b"}}` {
		t.Errorf("an ordinary field: %s", got)
	}
	if got := asJSON(t, holds(field{name: "notes.by", nested: "notes"})); got != `{"nested":{"path":"notes","query":{"exists":{"field":"notes.by"}}}}` {
		t.Errorf("a nested field: %s", got)
	}
}

// What the source claims is what it does: it reads, filters, sorts and counts,
// and writes nothing.
func TestWhatItClaims(t *testing.T) {
	caps := (&esSource{}).Capabilities()
	if caps.Paradigm != model.ParadigmDocument || !caps.Data.ServerFilter || !caps.Data.ServerSort || !caps.Data.ExactCount {
		t.Errorf("it claims %+v", caps.Data)
	}
	if caps.Data.Insert || caps.Data.Update || caps.Data.Delete || caps.Data.BulkLoad || caps.Query.Supported || caps.Schema.DDL {
		t.Errorf("it claims to change something: %+v", caps)
	}
	if !caps.Supports(model.KindCollection) || !caps.Supports(model.KindFolder) || caps.Supports(model.KindTable) {
		t.Errorf("it holds %v", caps.Objects)
	}
	var src any = &esSource{}
	if _, ok := src.(source.Writer); ok {
		t.Error("it implements Writer")
	}
	if _, ok := src.(source.Dialect); ok {
		t.Error("it has a query language")
	}
}
