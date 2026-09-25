package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// Asking a model something (FR-14.5).
//
// Against a server in this process, which is how internal/cloud's token
// endpoints are proved and for the same reason: no account, no network, and a
// test that says exactly what went over the wire. What it cannot prove is any
// real provider's behaviour, and that is said rather than glossed.

// stub is a model that answers, and records what it was asked.
type stub struct {
	*httptest.Server
	wire Wire

	path    string
	headers http.Header
	body    map[string]any
	raw     string

	status int
	answer string
	// send, where it is set, is the body sent instead of a well-formed answer.
	send string
}

func newStub(t *testing.T, wire Wire) *stub {
	t.Helper()
	s := &stub{wire: wire, status: http.StatusOK, answer: "SELECT 1"}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.path = r.URL.Path
		s.headers = r.Header.Clone()
		raw, _ := io.ReadAll(r.Body)
		s.raw = string(raw)
		json.Unmarshal(raw, &s.body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(s.status)
		if s.send != "" {
			io.WriteString(w, s.send)
			return
		}
		switch s.wire {
		case WireAnthropic:
			json.NewEncoder(w).Encode(map[string]any{
				"content": []map[string]string{{"type": "text", "text": s.answer}}})
		default:
			json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{"message": map[string]string{"content": s.answer}}}})
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *stub) provider() Provider {
	return Provider{ID: "stub", Name: "A stub", Wire: s.wire,
		Endpoint: s.URL, Model: "a-model", Secret: "assistant.stub"}
}

func allowed() Consent { return Consent{Enabled: true, Connection: true} }

func keyed(key string) *Client {
	return &Client{Secret: func(string) (string, error) { return key, nil }}
}

func ask(t *testing.T, c *Client, p Provider, consent Consent, r Request) (*Answer, error) {
	t.Helper()
	return c.Ask(context.Background(), p, consent, r)
}

func query() Request {
	return Request{Kind: KindQuery, Question: "how many people are there",
		Grounding: Grounding{Product: "PostgreSQL", Dialect: "postgresql",
			Tables: []Table{{Name: "public.people", Columns: []Column{{Name: "id", Type: "integer"}}}}}}
}

// Both wire formats, asked and read.
func TestBothWaysOfAskingAModel(t *testing.T) {
	for name, c := range map[string]struct {
		wire Wire
		path string
		key  string
	}{
		"OpenAI-compatible": {WireOpenAI, "/v1/chat/completions", "Authorization"},
		"Anthropic":         {WireAnthropic, "/v1/messages", "x-api-key"},
	} {
		t.Run(name, func(t *testing.T) {
			s := newStub(t, c.wire)
			s.answer = "SELECT count(*) FROM public.people"
			got, err := ask(t, keyed("a-key"), s.provider(), allowed(), query())
			if err != nil {
				t.Fatal(err)
			}
			if s.path != c.path {
				t.Errorf("it asked at %q, want %q", s.path, c.path)
			}
			if got.Text != s.answer {
				t.Errorf("it read %q", got.Text)
			}
			// What answered is carried with the answer, so the window can
			// always say (FR-14.5).
			if got.Provider != "A stub" || got.Model != "a-model" {
				t.Errorf("it says %q / %q", got.Provider, got.Model)
			}
			if got.Took <= 0 {
				t.Error("it took no time at all")
			}
			if !strings.Contains(got.Summary(), "a-model") {
				t.Errorf("its summary is %q", got.Summary())
			}
			// The model asked for is the one configured: a model nobody chose
			// is a bill nobody expected.
			if s.body["model"] != "a-model" {
				t.Errorf("it asked %v", s.body["model"])
			}
		})
	}
}

// The key goes where each format wants it, and nowhere else. A key in the wrong
// header is a key sent to a server that will log it as a header it does not
// know.
func TestWhereTheKeyGoes(t *testing.T) {
	open := newStub(t, WireOpenAI)
	if _, err := ask(t, keyed("sk-secret"), open.provider(), allowed(), query()); err != nil {
		t.Fatal(err)
	}
	if got := open.headers.Get("Authorization"); got != "Bearer sk-secret" {
		t.Errorf("it sent %q", got)
	}
	if open.headers.Get("x-api-key") != "" {
		t.Error("it sent the key twice, in two headers")
	}

	ant := newStub(t, WireAnthropic)
	if _, err := ask(t, keyed("sk-secret"), ant.provider(), allowed(), query()); err != nil {
		t.Fatal(err)
	}
	if got := ant.headers.Get("x-api-key"); got != "sk-secret" {
		t.Errorf("it sent %q", got)
	}
	if ant.headers.Get("Authorization") != "" {
		t.Error("it sent the key twice, in two headers")
	}
	// The version is pinned rather than latest: a version nobody chose is a
	// shape nobody tested.
	if got := ant.headers.Get("anthropic-version"); got != anthropicVersion {
		t.Errorf("it asked for version %q", got)
	}
}

// A gateway of somebody's own takes the key in a header of its own, which is
// what an organisation that allows this at all has.
func TestAKeyInAHeaderOfItsOwn(t *testing.T) {
	s := newStub(t, WireOpenAI)
	p := s.provider()
	p.Header = "X-Our-Gateway-Key"
	if _, err := ask(t, keyed("k"), p, allowed(), query()); err != nil {
		t.Fatal(err)
	}
	if got := s.headers.Get("X-Our-Gateway-Key"); got != "k" {
		t.Errorf("it sent %q", got)
	}
	if s.headers.Get("Authorization") != "" {
		t.Error("it also sent the key the usual way")
	}
}

// An endpoint that wants no key is asked without one, which is what a model on
// one's own machine usually is.
func TestAnEndpointThatWantsNoKey(t *testing.T) {
	s := newStub(t, WireOpenAI)
	p := s.provider()
	p.Secret = ""
	// No Secrets function at all, which is what an application with no keychain
	// entry for this provider has.
	if _, err := ask(t, &Client{}, p, allowed(), query()); err != nil {
		t.Fatal(err)
	}
	if s.headers.Get("Authorization") != "" {
		t.Errorf("it sent %q", s.headers.Get("Authorization"))
	}
}

// A provider that needs a key and cannot get one is refused before anything is
// sent: a request without it would be a round trip to be told what this knew.
func TestAKeyThatCannotBeHadIsRefusedBeforeAsking(t *testing.T) {
	s := newStub(t, WireOpenAI)
	for name, c := range map[string]*Client{
		"nowhere to read one from": {},
		"nothing there":            {Secret: func(string) (string, error) { return "", errors.New("no such entry") }},
		"an empty one":             keyed("   "),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ask(t, c, s.provider(), allowed(), query())
			if err == nil {
				t.Fatal("it asked anyway")
			}
			if s.path != "" {
				t.Errorf("it reached %q", s.path)
			}
			// A key that could not be read and a key that is not set are
			// different things to go and fix, and say so.
			if name == "nothing there" && !strings.Contains(err.Error(), "could not be read") {
				t.Errorf("it says %q", err)
			}
			if name == "an empty one" && !strings.Contains(err.Error(), "no key set") {
				t.Errorf("it says %q", err)
			}
		})
	}
}

// Nothing is sent without consent, and nothing is sent before consent is
// checked: a refusal after the request would be a refusal of the answer.
func TestNothingIsSentWithoutConsent(t *testing.T) {
	s := newStub(t, WireOpenAI)
	for name, consent := range map[string]Consent{
		"off":      {},
		"off here": {Enabled: true},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ask(t, keyed("k"), s.provider(), consent, query()); err == nil {
				t.Fatal("it asked anyway")
			}
			if s.path != "" {
				t.Errorf("it reached %q", s.path)
			}
		})
	}
	// And a request carrying rows is refused where only the schema is allowed,
	// which is the case the data toggle exists for.
	withRows := query()
	withRows.Grounding.Rows = []Sample{{Table: "people", Columns: []string{"id"}, Rows: [][]string{{"1"}}}}
	if _, err := ask(t, keyed("k"), s.provider(), allowed(), withRows); !errors.Is(err, ErrDataOff) {
		t.Errorf("it said %v", err)
	}
	if s.path != "" {
		t.Errorf("it reached %q", s.path)
	}
}

// The schema goes with the question, because a model that is not told it invents
// table names.
func TestTheSchemaGoesWithTheQuestion(t *testing.T) {
	s := newStub(t, WireOpenAI)
	if _, err := ask(t, keyed("k"), s.provider(), allowed(), query()); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"public.people", "id integer", "PostgreSQL", "how many people"} {
		if !strings.Contains(s.raw, want) {
			t.Errorf("what was sent does not mention %q:\n%s", want, s.raw)
		}
	}
	// The same answer to the same question, which is what makes a generated
	// statement reviewable.
	if got := s.body["temperature"]; got != float64(0) {
		t.Errorf("it asked at temperature %v", got)
	}
}

// A provider that cannot be asked is refused, and the refusal says which part is
// wrong.
func TestAProviderThatCannotBeAsked(t *testing.T) {
	good := Provider{ID: "p", Name: "P", Wire: WireOpenAI,
		Endpoint: "https://example.invalid", Model: "m"}
	if err := good.Valid(); err != nil {
		t.Fatalf("a whole provider: %v", err)
	}
	for name, c := range map[string]struct {
		change func(*Provider)
		says   string
	}{
		"no name of its own":         {func(p *Provider) { p.ID = "" }, "name of its own"},
		"no address":                 {func(p *Provider) { p.Endpoint = "" }, "needs an address"},
		"no model":                   {func(p *Provider) { p.Model = "" }, "which model"},
		"a way nobody has":           {func(p *Provider) { p.Wire = "smoke signals" }, "not a way"},
		"an address that is not one": {func(p *Provider) { p.Endpoint = "ht tp://x" }, "cannot be read"},
		"another scheme":             {func(p *Provider) { p.Endpoint = "ftp://x" }, "not an address"},
		// A prompt carrying a schema, and possibly rows, must not cross a
		// network in the clear.
		"plain text to somewhere else": {func(p *Provider) { p.Endpoint = "http://api.example.com" },
			"only for a model on this machine"},
	} {
		t.Run(name, func(t *testing.T) {
			p := good
			c.change(&p)
			err := p.Valid()
			if err == nil {
				t.Fatal("it was accepted")
			}
			if !strings.Contains(err.Error(), c.says) {
				t.Errorf("it says %q, which does not mention %q", err, c.says)
			}
		})
	}
}

// An address with a slash on the end is the same address: a path built from one
// would have two, and a server that answered /v1//v1 would be a surprise.
func TestATrailingSlashIsTheSameAddress(t *testing.T) {
	s := newStub(t, WireOpenAI)
	p := s.provider()
	p.Endpoint = s.URL + "/"
	if _, err := ask(t, keyed("k"), p, allowed(), query()); err != nil {
		t.Fatal(err)
	}
	if s.path != "/v1/chat/completions" {
		t.Errorf("it asked at %q", s.path)
	}
}

// A request that cannot be asked is refused before anything is sent, not after:
// a round trip to be told what this already knew is a round trip somebody paid
// for.
func TestAnInvalidRequestIsRefusedBeforeAsking(t *testing.T) {
	s := newStub(t, WireOpenAI)
	if _, err := ask(t, keyed("k"), s.provider(), allowed(), Request{Kind: KindQuery}); err == nil {
		t.Fatal("it asked anyway")
	}
	if s.path != "" {
		t.Errorf("it reached %q", s.path)
	}
}

// An answer is bounded: a provider that sent gigabytes would otherwise be the
// thing that decided how much memory this uses.
func TestAnAnswerIsBounded(t *testing.T) {
	huge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"content":"`)
		for i := 0; i < (maxAnswer/1024)+64; i++ {
			io.WriteString(w, strings.Repeat("x", 1024))
		}
		io.WriteString(w, `"}}]}`)
	}))
	defer huge.Close()
	p := Provider{ID: "p", Name: "A stub", Wire: WireOpenAI, Endpoint: huge.URL, Model: "m"}
	// Cut off, so what comes back is not JSON any more — which is the honest
	// outcome: an answer that did not arrive whole is not an answer.
	_, err := (&Client{}).Ask(context.Background(), p, allowed(), query())
	if err == nil {
		t.Fatal("it read an answer of any size at all")
	}
	if !strings.Contains(err.Error(), "could not read") {
		t.Errorf("it says %q", err)
	}
}

// Plain text is allowed to this machine, which is what a model of one's own is.
func TestPlainTextIsAllowedToThisMachine(t *testing.T) {
	for _, host := range []string{"localhost", "127.0.0.1", "[::1]", "ollama.localhost"} {
		p := Provider{ID: "p", Wire: WireOpenAI, Model: "m", Endpoint: "http://" + host + ":11434"}
		if err := p.Valid(); err != nil {
			t.Errorf("%s: %v", host, err)
		}
	}
	// And not to somewhere that merely looks private: a model on another
	// machine on the network is still a prompt crossing a network.
	for _, host := range []string{"10.0.0.5", "192.168.1.9", "my-nas.local", "localhost.example.com"} {
		p := Provider{ID: "p", Wire: WireOpenAI, Model: "m", Endpoint: "http://" + host + ":11434"}
		if err := p.Valid(); err == nil {
			t.Errorf("%s was accepted over plain text", host)
		}
	}
}

// A provider is refused after consent, so that somebody who has not turned the
// assistant on is told that rather than told their endpoint is wrong.
func TestConsentIsAskedBeforeTheProvider(t *testing.T) {
	broken := Provider{ID: "", Wire: "nonsense"}
	if _, err := ask(t, keyed("k"), broken, Consent{}, query()); !errors.Is(err, ErrOff) {
		t.Errorf("it said %v", err)
	}
}

// What a provider says went wrong is passed on: "which model was wrong" is in
// the body, and the status alone says nothing anybody can act on.
func TestWhatAProviderSaysWentWrongIsPassedOn(t *testing.T) {
	s := newStub(t, WireOpenAI)
	s.status = http.StatusNotFound
	s.send = `{"error":{"message":"the model a-model does not exist"}}`
	_, err := ask(t, keyed("k"), s.provider(), allowed(), query())
	if err == nil {
		t.Fatal("it read an answer out of a refusal")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("it says %q", err)
	}
	if !strings.Contains(err.Error(), "A stub") {
		t.Errorf("it does not say which provider: %q", err)
	}
}

// An answer this cannot read is said to be one, rather than passed on as an
// empty answer.
func TestAnAnswerThatCannotBeRead(t *testing.T) {
	for name, c := range map[string]struct {
		wire Wire
		send string
	}{
		"not JSON at all":        {WireOpenAI, "not json"},
		"no choices":             {WireOpenAI, `{"choices":[]}`},
		"an empty message":       {WireOpenAI, `{"choices":[{"message":{"content":""}}]}`},
		"no content":             {WireAnthropic, `{"content":[]}`},
		"no text in the content": {WireAnthropic, `{"content":[{"type":"thinking"}]}`},
		// A part that is not text is not the answer, whatever it holds: a
		// model's own working is not what a person asked for.
		"thinking, with words in it": {WireAnthropic,
			`{"content":[{"type":"thinking","text":"let me think about that"}]}`},
	} {
		t.Run(name, func(t *testing.T) {
			s := newStub(t, c.wire)
			s.send = c.send
			if _, err := ask(t, keyed("k"), s.provider(), allowed(), query()); err == nil {
				t.Fatal("it read something")
			}
		})
	}
}

// A provider that cannot be reached says so, naming itself: an address that is
// wrong and a service that is down read the same way otherwise.
func TestAProviderThatCannotBeReached(t *testing.T) {
	p := Provider{ID: "p", Name: "A stub", Wire: WireOpenAI,
		Endpoint: "http://127.0.0.1:1", Model: "m"}
	_, err := ask(t, &Client{}, p, allowed(), query())
	if err == nil {
		t.Fatal("something answered on port 1")
	}
	if !strings.Contains(err.Error(), "A stub could not be reached") {
		t.Errorf("it says %q", err)
	}
}

// A question let go of is let go of: nothing waits on a model after the person
// stopped caring.
func TestAQuestionLetGoOf(t *testing.T) {
	// The handler waits to be released rather than on the request's own
	// context: a server does not notice a client abandoning a request until it
	// reads or writes, so a handler waiting on that would still be waiting when
	// Close came to wait for it, and the test would hang rather than fail.
	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer func() {
		close(release)
		slow.Close()
	}()
	p := Provider{ID: "p", Name: "A stub", Wire: WireOpenAI, Endpoint: slow.URL, Model: "m"}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := (&Client{}).Ask(ctx, p, allowed(), query()); err == nil {
		t.Fatal("it waited and answered")
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("it took %v to give up", took)
	}
}

// A redirect is refused: what is in flight carries a schema and is not for
// anywhere else.
func TestARedirectIsRefused(t *testing.T) {
	var elsewhere *httptest.Server
	elsewhere = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the prompt was sent to the place it was redirected to")
		w.WriteHeader(http.StatusOK)
	}))
	defer elsewhere.Close()
	away := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/v1/chat/completions", http.StatusTemporaryRedirect)
	}))
	defer away.Close()
	p := Provider{ID: "p", Name: "A stub", Wire: WireOpenAI, Endpoint: away.URL, Model: "m"}
	if _, err := (&Client{}).Ask(context.Background(), p, allowed(), query()); err == nil {
		t.Error("it followed a redirect")
	}
}

// The providers offered without anybody configuring one: two hosted, and the
// shape of a local one. Each still needs a model, which is why none of them is
// valid until somebody chooses.
func TestTheProvidersOfferedToBeginWith(t *testing.T) {
	got := Builtin()
	if len(got) < 3 {
		t.Fatalf("it offers %+v", got)
	}
	local := 0
	for _, p := range got {
		if err := p.Valid(); err == nil {
			t.Errorf("%s is ready to use with no model chosen", p.ID)
		}
		p.Model = "m"
		if err := p.Valid(); err != nil {
			t.Errorf("%s with a model chosen: %v", p.ID, err)
		}
		if p.Secret == "" {
			local++
		}
	}
	// One of them wants no key, because a model on one's own machine usually
	// does not: a provider list where every entry demanded one would make
	// running your own look unsupported.
	if local != 1 {
		t.Errorf("%d of them want no key", local)
	}
}

// The grounding a schema gives, bounded and saying what it left out.
func TestTheGroundingASchemaGives(t *testing.T) {
	db := &model.Database{Name: "db", Schemas: []model.Schema{{Name: "public", Tables: []model.Table{
		{Name: "people", Columns: []model.Column{
			{Name: "id", Type: model.DataType{Native: "integer"}},
			{Name: "name", Type: model.DataType{Native: "text"}}},
			PrimaryKey: &model.PrimaryKey{Columns: []string{"id"}}},
		{Name: "orders", Columns: []model.Column{{Name: "id", Type: model.DataType{Native: "integer"}}}},
	}}}}
	g := FromSchema(db, "PostgreSQL", "postgresql", nil)
	if len(g.Tables) != 2 || g.Left != 0 {
		t.Fatalf("it describes %+v, leaving %d", g.Tables, g.Left)
	}
	// One is one, in English: "1 other table" and not "1 other tables".
	if got := plural(1, "other table"); got != "1 other table" {
		t.Errorf("one reads as %q", got)
	}
	if got := plural(2, "other table"); got != "2 other tables" {
		t.Errorf("two read as %q", got)
	}
	said := g.Describe()
	for _, want := range []string{"PostgreSQL", "public.people (id integer, name text)", "keyed by id", "public.orders"} {
		if !strings.Contains(said, want) {
			t.Errorf("it reads\n%s\nwhich lacks %q", said, want)
		}
	}
	// Narrowed to the tables a question is about, which is how a question about
	// two tables is asked without sending a hundred — and what is left out is
	// counted, because a model told about two tables may answer as though there
	// were only two.
	one := FromSchema(db, "PostgreSQL", "postgresql", []string{"people"})
	if len(one.Tables) != 1 || one.Left != 1 {
		t.Fatalf("narrowed, it describes %+v, leaving %d", one.Tables, one.Left)
	}
	if !strings.Contains(one.Describe(), "1 other table") {
		t.Errorf("it reads\n%s", one.Describe())
	}
	// A schema qualified name matches too, since that is how a designer or a
	// tree would name one.
	if got := FromSchema(db, "P", "p", []string{"public.orders"}); len(got.Tables) != 1 {
		t.Errorf("a qualified name matched %+v", got.Tables)
	}
	if got := FromSchema(nil, "P", "p", nil); len(got.Tables) != 0 {
		t.Errorf("no database described %+v", got.Tables)
	}
}

// A schema too large for a prompt is cut, and says so: one quietly cut would
// produce a statement about the half the model happened to see.
func TestALargeSchemaIsCutAndSaysSo(t *testing.T) {
	var tables []model.Table
	for i := 0; i < maxTables+5; i++ {
		tables = append(tables, model.Table{Name: "t" + string(rune('a'+i%26)) + string(rune('0'+i/26))})
	}
	wide := model.Table{Name: "wide"}
	for i := 0; i < maxColumns+3; i++ {
		wide.Columns = append(wide.Columns, model.Column{Name: "c" + string(rune('a'+i%26))})
	}
	db := &model.Database{Schemas: []model.Schema{{Tables: append([]model.Table{wide}, tables...)}}}
	g := FromSchema(db, "P", "p", nil)
	if len(g.Tables) != maxTables {
		t.Errorf("it describes %d tables", len(g.Tables))
	}
	// The wide table is one of them, so what is over the bound is the five
	// extras plus it.
	if g.Left != 6 {
		t.Errorf("it says %d were left out", g.Left)
	}
	if len(g.Tables[0].Columns) != maxColumns || g.Tables[0].Left != 3 {
		t.Errorf("the wide table has %d columns and says %d were left out",
			len(g.Tables[0].Columns), g.Tables[0].Left)
	}
	said := g.Describe()
	if !strings.Contains(said, "6 other tables") || !strings.Contains(said, "3 other columns") {
		t.Errorf("it reads\n%s", said)
	}
}

// A sample is a few rows as text, bounded, and bytes are said to be bytes
// rather than shown as text.
func TestASampleOfRows(t *testing.T) {
	cols := []model.ColumnDef{{Name: "id"}, {Name: "name"}, {Name: "pic"}, {Name: "note"}}
	var rows []model.Row
	for i := 0; i < maxRows+10; i++ {
		rows = append(rows, model.Row{int64(i), "person", []byte{1, 2, 3}, nil})
	}
	s := SampleOf("people", cols, rows)
	if len(s.Rows) > maxRows {
		t.Errorf("it sampled %d rows", len(s.Rows))
	}
	if s.Rows[0][2] != "<3 bytes>" {
		t.Errorf("the bytes read as %q", s.Rows[0][2])
	}
	if s.Rows[0][3] != "NULL" {
		t.Errorf("nothing reads as %q", s.Rows[0][3])
	}
	said := s.describe()
	if !strings.Contains(said, "Some rows of people") || !strings.Contains(said, "id | name") {
		t.Errorf("it reads\n%s", said)
	}
	// A very long value is cut: a prompt full of one blob is a prompt about
	// nothing else.
	long := SampleOf("t", []model.ColumnDef{{Name: "body"}},
		[]model.Row{{strings.Repeat("x", 500)}})
	if len(long.Rows[0][0]) > 200 {
		t.Errorf("a long value went in whole: %d characters", len(long.Rows[0][0]))
	}
	if !strings.HasSuffix(long.Rows[0][0], "…") {
		t.Error("a cut value does not say it was cut")
	}
	// No rows is no sample, which is what makes a request carry no data — and
	// nothing to say about it either, rather than a heading over nothing.
	empty := SampleOf("t", cols, nil)
	if len(empty.Rows) != 0 {
		t.Errorf("no rows sampled %+v", empty.Rows)
	}
	if got := empty.describe(); got != "" {
		t.Errorf("an empty sample reads %q", got)
	}
}
