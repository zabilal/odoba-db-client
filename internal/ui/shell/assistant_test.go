package shell

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/assistant"
	"github.com/ikigai-db/ikigai-db/internal/store"
	"github.com/ikigai-db/ikigai-db/internal/ui/editor"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer/view"
)

// The assistant, in the window (FR-14.1, FR-14.2, FR-14.4, FR-14.5, FR-14.6).
//
// Against a model in this process, so that what leaves the window can be read:
// a test that says "it asked" and not what it asked would be no use about a
// feature whose whole difficulty is what it sends.

// model is a language model that answers, and records what it was asked.
//
// asked is the prompt as the model reads it rather than the request body, so
// that a test asserting what was sent asserts the words and not JSON's escapes:
// a body checked for "id > 1" would fail on the "id \u003e 1" that encoding it
// produces, and would be reading the transport rather than the prompt.
type modelStub struct {
	*httptest.Server
	asked  string
	answer string
}

func newModel(t *testing.T) *modelStub {
	t.Helper()
	m := &modelStub{answer: "SELECT count(*) FROM main.items"}
	m.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		m.asked = promptIn(raw)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": m.answer}}}})
	}))
	t.Cleanup(m.Close)
	return m
}

// promptIn is everything a request says to the model: the system prompt, where
// the wire has one of its own, and every message.
func promptIn(raw []byte) string {
	var sent struct {
		System   string `json:"system"`
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &sent); err != nil {
		return string(raw)
	}
	parts := []string{sent.System}
	for _, msg := range sent.Messages {
		parts = append(parts, msg.Content)
	}
	return strings.Join(parts, "\n")
}

// withAssistant is a fixture whose assistant is set up and whose one connection
// has opted in, which is the state every question below is asked from.
func withAssistant(t *testing.T, data bool) (*fixture, *modelStub, store.SavedConnection) {
	t.Helper()
	fx := newFixture(t)
	m := newModel(t)
	c := fx.create(t, "db1", nil)
	c.Assistant = &store.ConnectionAssistant{Enabled: true, Data: data}
	if err := fx.conns.Update(c, app.SecretEdit{}); err != nil {
		t.Fatal(err)
	}
	if err := fx.s.d.Settings.Update(func(st *store.Settings) error {
		st.Assistant = &store.Assistant{Enabled: true, Provider: "local",
			Model: "a-model", Endpoint: m.URL}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	fx.s.sync()
	return fx, m, c
}

// selectConnection puts the explorer on a connection, which is what a question
// with no tab open is about.
func selectConnection(t *testing.T, fx *fixture, id string) {
	t.Helper()
	fx.s.Explorer.Refresh("")
	loaded(t, fx, "")
	fx.s.Explorer.Tree.Select(view.ConnectionID(id))
	fx.s.sync()
}

// The assistant is not offered until it is set up and the connection has opted
// in: a command offered where it cannot work is worse than one that is not
// there (REQ-DB-2, FR-14.4).
func TestTheAssistantIsNotOfferedUntilItIsAllowed(t *testing.T) {
	fx := newFixture(t)
	c := fx.create(t, "db1", nil)
	selectConnection(t, fx, c.ID)
	if fx.s.canAskAssistant() {
		t.Error("it is offered with nothing set up")
	}

	// Set up, but the connection has agreed nothing.
	m := newModel(t)
	if err := fx.s.d.Settings.Update(func(st *store.Settings) error {
		st.Assistant = &store.Assistant{Enabled: true, Provider: "local", Model: "m", Endpoint: m.URL}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	fx.s.sync()
	if fx.s.canAskAssistant() {
		t.Error("it is offered on a connection that has not opted in")
	}

	// Opted in.
	c.Assistant = &store.ConnectionAssistant{Enabled: true}
	if err := fx.conns.Update(c, app.SecretEdit{}); err != nil {
		t.Fatal(err)
	}
	selectConnection(t, fx, c.ID)
	if !fx.s.canAskAssistant() {
		t.Error("it is not offered on a connection that has opted in")
	}
	if fx.s.menuItems[cmdAssistantAsk].Disabled {
		t.Error("the menu item is disabled where the command is offered")
	}

	// Turned off again at the application, which is one switch over everything.
	if err := fx.s.d.Settings.Update(func(st *store.Settings) error {
		st.Assistant.Enabled = false
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	fx.s.sync()
	if fx.s.canAskAssistant() {
		t.Error("it is offered with the application's switch off")
	}
}

// A question puts the statement in a query tab, unrun. That is FR-14.6, and it
// is why this calls what writes a script rather than anything that executes.
func TestAQuestionLandsInTheEditorUnrun(t *testing.T) {
	fx, m, c := withAssistant(t, false)
	selectConnection(t, fx, c.ID)
	before := len(fx.s.open)
	fx.s.runAsk(&c, providerOf(t, fx), assistant.KindQuery, "how many items", false)
	pump(t, fx.q, func() bool { return len(fx.s.open) > before })

	opened := fx.s.open[len(fx.s.open)-1]
	if opened.query == nil {
		t.Fatal("it opened something that is not a query")
	}
	text := opened.query.editor.Document().Text()
	if !strings.Contains(text, "SELECT count(*) FROM main.items") {
		t.Errorf("the editor reads\n%s", text)
	}
	// What produced it, and that it has not run: somebody reading a generated
	// statement has to be able to see both (FR-14.5, FR-14.6).
	if !strings.Contains(text, "a-model") {
		t.Errorf("it does not say what answered:\n%s", text)
	}
	if !strings.Contains(text, "Nothing here has run") {
		t.Errorf("it does not say it has not run:\n%s", text)
	}
	// And it has not run: no result, and nothing running. This is the whole of
	// T5.8 -- a generated statement is reviewed before it is executed, so the
	// thing to assert is the absence of an execution (FR-14.6).
	if len(opened.query.sets) != 0 || opened.query.executing {
		t.Errorf("it ran: %d results, executing=%v", len(opened.query.sets), opened.query.executing)
	}
	// And the schema went with the question, because a model that is not told it
	// invents table names (FR-14.1).
	if !strings.Contains(m.asked, "items") {
		t.Errorf("what was asked does not carry the schema:\n%s", m.asked)
	}
	if !strings.Contains(m.asked, "how many items") {
		t.Errorf("what was asked does not carry the question:\n%s", m.asked)
	}
}

func providerOf(t *testing.T, fx *fixture) assistant.Provider {
	t.Helper()
	p, ok := assistant.ProviderFor(fx.s.assistantSettings())
	if !ok {
		t.Fatal("no provider is set up")
	}
	return p
}

// An answer with no statement in it is an answer, and is shown as the words it
// is rather than put in an editor.
func TestAnAnswerInWordsIsShownAsWords(t *testing.T) {
	fx, m, c := withAssistant(t, false)
	m.answer = "There is no table of orders in this schema, so I cannot count them."
	before := len(fx.s.open)
	fx.s.runAsk(&c, providerOf(t, fx), assistant.KindQuery, "how many orders", false)
	// On anything happening, rather than on the dialog: waiting for the dialog
	// would report an answer put in an editor as a timeout, and a timeout says
	// nothing about what went wrong.
	pump(t, fx.q, func() bool {
		return fx.s.win.Canvas().Overlays().Top() != nil || len(fx.s.open) != before
	})
	if len(fx.s.open) != before {
		t.Fatal("it opened an editor for an answer with no statement in it")
	}
	if fx.s.win.Canvas().Overlays().Top() == nil {
		t.Fatal("it showed nothing")
	}
	if !strings.Contains(fx.s.status.Text, "answered") {
		t.Errorf("the status line says %q", fx.s.status.Text)
	}
}

// Rows are sent only where the connection allows them, and only where the
// question asked for them: reading somebody's data to be refused would be
// reading it for nothing (FR-14.3).
func TestRowsAreSentOnlyWhereTheyAreAllowedAndAskedFor(t *testing.T) {
	for name, c := range map[string]struct {
		data, asked bool
		sends       bool
	}{
		"allowed and asked for":  {true, true, true},
		"allowed, not asked":     {true, false, false},
		"asked for, not allowed": {false, true, false},
		"neither":                {false, false, false},
	} {
		t.Run(name, func(t *testing.T) {
			fx, m, conn := withAssistant(t, c.data)
			fx.s.runAsk(&conn, providerOf(t, fx), assistant.KindQuery, "how many", c.asked)
			pump(t, fx.q, func() bool { return m.asked != "" || fx.s.errors.shown() })
			if c.sends {
				if !strings.Contains(m.asked, "Some rows of") {
					t.Errorf("no rows were sent:\n%s", m.asked)
				}
				return
			}
			if strings.Contains(m.asked, "Some rows of") {
				t.Errorf("rows were sent:\n%s", m.asked)
			}
			// Not asked for is not a refusal: the schema still goes, and the
			// question is still answered.
			if !c.asked || c.data {
				if !strings.Contains(m.asked, "items") {
					t.Errorf("the schema was not sent either:\n%s", m.asked)
				}
			}
		})
	}
}

// Explaining is offered only where there is a statement to explain, and
// explains what Run would run.
func TestExplainingAStatement(t *testing.T) {
	// Rows are allowed here so that the explaining not sending them is a
	// choice rather than a refusal: a question about what a statement does is
	// answered from the statement (FR-14.2).
	fx, m, c := withAssistant(t, true)
	if fx.s.canExplainWithAssistant() {
		t.Error("it is offered with no editor open")
	}
	tb := fx.s.OpenQuery(c.ID)
	if tb == nil {
		t.Fatal("no query tab opened")
	}
	fx.s.sync()
	if fx.s.canExplainWithAssistant() {
		t.Error("it is offered with an empty editor")
	}
	tb.query.editor.Document().SetText("SELECT * FROM items WHERE id > 1")
	fx.s.sync()
	if !fx.s.canExplainWithAssistant() {
		t.Fatal("it is not offered with a statement in the editor")
	}
	m.answer = "It reads every column of items for the rows whose id is over one."
	fx.s.explainWithAssistant()
	pump(t, fx.q, func() bool { return m.asked != "" })
	if !strings.Contains(m.asked, "Explain this statement") {
		t.Errorf("it asked\n%s", m.asked)
	}
	if !strings.Contains(m.asked, "id > 1") {
		t.Errorf("it did not send the statement:\n%s", m.asked)
	}
	if strings.Contains(m.asked, "Some rows of") {
		t.Errorf("explaining sent rows:\n%s", m.asked)
	}
}

// Explaining is about what Run would run, which is the selection where there is
// one: somebody who has picked out one statement of five is asking about that
// one.
func TestExplainingExplainsTheSelection(t *testing.T) {
	fx, m, c := withAssistant(t, false)
	tb := fx.s.OpenQuery(c.ID)
	if tb == nil {
		t.Fatal("no query tab opened")
	}
	doc := tb.query.editor.Document()
	doc.SetText("DELETE FROM items;\nSELECT count(*) FROM items")
	doc.SetCaret(editor.Pos{Line: 1}, false)
	doc.SetCaret(editor.Pos{Line: 1, Col: 26}, true)
	fx.s.sync()
	fx.s.explainWithAssistant()
	pump(t, fx.q, func() bool { return m.asked != "" })
	if !strings.Contains(m.asked, "SELECT count(*) FROM items") {
		t.Errorf("it did not send the selection:\n%s", m.asked)
	}
	if strings.Contains(m.asked, "DELETE FROM items") {
		t.Errorf("it sent what was not selected:\n%s", m.asked)
	}
}

// Explaining goes away with the assistant, statement or no statement: the
// switch is over everything (FR-14.4).
func TestExplainingGoesAwayWithTheAssistant(t *testing.T) {
	fx, _, c := withAssistant(t, false)
	tb := fx.s.OpenQuery(c.ID)
	if tb == nil {
		t.Fatal("no query tab opened")
	}
	tb.query.editor.Document().SetText("SELECT 1")
	fx.s.sync()
	if !fx.s.canExplainWithAssistant() {
		t.Fatal("it is not offered with a statement and the assistant on")
	}
	if err := fx.s.d.Settings.Update(func(st *store.Settings) error {
		st.Assistant.Enabled = false
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	fx.s.sync()
	if fx.s.canExplainWithAssistant() {
		t.Error("it is offered with the assistant off")
	}
}

// The question box says what may be sent and offers rows only where they are
// allowed: a tick that did nothing would read as one that did (FR-14.5).
func TestTheQuestionBoxSaysWhatItMaySee(t *testing.T) {
	for name, allowed := range map[string]bool{"rows allowed": true, "schema only": false} {
		t.Run(name, func(t *testing.T) {
			fx, m, c := withAssistant(t, allowed)
			selectConnection(t, fx, c.ID)
			fx.s.askAssistant()
			top := fx.s.win.Canvas().Overlays().Top()
			if top == nil {
				t.Fatal("it asked nothing")
			}
			rows := findCheck(top, "Send a few rows as well as the schema")
			if rows == nil {
				t.Fatal("there is no rows switch")
			}
			if rows.Disabled() == allowed {
				t.Errorf("the rows switch is disabled=%v where rows allowed=%v",
					rows.Disabled(), allowed)
			}
			said := saidIn(top)
			for _, want := range []string{"A model of your own", "a-model"} {
				if !strings.Contains(said, want) {
					t.Errorf("it does not say %q:\n%s", want, said)
				}
			}
			if allowed && !strings.Contains(said, "rows you send") {
				t.Errorf("it does not say rows may go:\n%s", said)
			}
			if !allowed && !strings.Contains(said, "not the data") {
				t.Errorf("it does not say the data stays:\n%s", said)
			}
			// Asked without ticking the box, no rows go — including where they
			// would have been allowed.
			entriesIn(top)[0].SetText("how many items")
			test.Tap(findButton(top, "Ask"))
			pump(t, fx.q, func() bool { return m.asked != "" || fx.s.errors.shown() })
			if strings.Contains(m.asked, "Some rows of") {
				t.Errorf("rows went with a question that did not ask for them:\n%s", m.asked)
			}
			if !strings.Contains(m.asked, "how many items") {
				t.Errorf("the question did not go:\n%s", m.asked)
			}
		})
	}
}

// saidIn is every word a dialog shows, however deeply: what may be seen is said
// in a form item of its own, and a test that reached for it by position would
// pass while it said the wrong thing.
func saidIn(o fyne.CanvasObject) string {
	var out []string
	var walk func(fyne.CanvasObject)
	walk = func(o fyne.CanvasObject) {
		switch v := o.(type) {
		case nil:
			return
		case *widget.Label:
			out = append(out, v.Text)
		case *widget.PopUp:
			walk(v.Content)
		case *fyne.Container:
			for _, c := range v.Objects {
				walk(c)
			}
		case fyne.Widget:
			for _, c := range test.WidgetRenderer(v).Objects() {
				walk(c)
			}
		}
	}
	walk(o)
	return strings.Join(out, " ")
}

// Asking about a connection that has agreed nothing is refused, and says which
// switch is off. The menu item is disabled too, but a refusal that depended on a
// menu being right would be no refusal at all (FR-14.4).
func TestAskingIsRefusedWhereNothingHasOptedIn(t *testing.T) {
	fx, _, c := withAssistant(t, false)
	if err := fx.conns.Update(store.SavedConnection{
		ID: c.ID, Name: c.Name, Driver: c.Driver, Params: c.Params}, app.SecretEdit{}); err != nil {
		t.Fatal(err)
	}
	selectConnection(t, fx, c.ID)
	fx.s.askAssistant()
	if fx.s.win.Canvas().Overlays().Top() != nil {
		t.Error("it asked for a question anyway")
	}
	if !fx.s.errors.shown() {
		t.Fatal("it said nothing")
	}
	if !strings.Contains(fx.s.errors.message.Text, "connection") {
		t.Errorf("it says %q, which does not say which switch is off", fx.s.errors.message.Text)
	}
}

// Setting it up writes the settings and keeps the key out of them.
func TestSettingTheAssistantUp(t *testing.T) {
	fx := newFixture(t)
	m := newModel(t)
	fx.s.setUpAssistant()
	top := fx.s.win.Canvas().Overlays().Top()
	if top == nil {
		t.Fatal("it asked nothing")
	}
	picks := selectsIn(top)
	if len(picks) == 0 {
		t.Fatal("there is no provider to choose")
	}
	picks[0].SetSelected("A model of your own")
	entries := entriesIn(top)
	if len(entries) < 3 {
		t.Fatalf("it offered %d fields", len(entries))
	}
	entries[0].SetText("a-model") // the model
	entries[1].SetText(m.URL)     // the address
	findCheck(top, "Turn the assistant on").SetChecked(true)
	test.Tap(findButton(top, "Save"))

	got := fx.s.d.Settings.Get().Assistant
	if got == nil {
		t.Fatal("nothing was saved")
	}
	if !got.Enabled || got.Provider != "local" || got.Model != "a-model" || got.Endpoint != m.URL {
		t.Errorf("it saved %+v", got)
	}
	// The settings file holds no key, and there must never be one in it
	// (NFR-S1).
	raw, err := json.Marshal(fx.s.d.Settings.Get())
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"\"key\"", "sk-"} {
		if strings.Contains(string(raw), forbidden) {
			t.Errorf("the settings carry %q:\n%s", forbidden, raw)
		}
	}
	if !strings.Contains(fx.s.status.Text, "on") {
		t.Errorf("the status line says %q", fx.s.status.Text)
	}
}

// A key typed into the dialog goes to the keychain, and is read back from there:
// a key anywhere else would be a credential in a file (FR-1.5, NFR-S1).
func TestTheAssistantKeyGoesToTheKeychain(t *testing.T) {
	fx := newFixture(t)
	fx.s.setUpAssistant()
	top := fx.s.win.Canvas().Overlays().Top()
	selectsIn(top)[0].SetSelected("OpenAI")
	entries := entriesIn(top)
	entries[0].SetText("gpt-4o-mini") // the model
	entries[2].SetText("sk-not-a-real-key")
	findCheck(top, "Turn the assistant on").SetChecked(true)
	test.Tap(findButton(top, "Save"))
	if fx.s.errors.shown() {
		t.Fatalf("it said %q", fx.s.errors.message.Text)
	}
	got, err := fx.s.assistantSecrets()("assistant.openai")
	if err != nil {
		t.Fatalf("the key cannot be read back: %v", err)
	}
	if got != "sk-not-a-real-key" {
		t.Errorf("the keychain holds %q", got)
	}
	// And the settings hold the provider, with nothing of the key in them.
	raw, err := json.Marshal(fx.s.d.Settings.Get())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "sk-not-a-real-key") {
		t.Errorf("the settings carry the key:\n%s", raw)
	}
}

// Turning it on with nothing to ask is refused where somebody can see it, rather
// than saved and failing at the first question.
func TestTurningItOnWithNothingToAskIsRefused(t *testing.T) {
	fx := newFixture(t)
	fx.s.setUpAssistant()
	top := fx.s.win.Canvas().Overlays().Top()
	selectsIn(top)[0].SetSelected("A model of your own")
	findCheck(top, "Turn the assistant on").SetChecked(true)
	// No model chosen.
	test.Tap(findButton(top, "Save"))
	if fx.s.d.Settings.Get().Assistant != nil {
		t.Errorf("it saved %+v", fx.s.d.Settings.Get().Assistant)
	}
	if !fx.s.errors.shown() {
		t.Error("it said nothing")
	}
	if !strings.Contains(fx.s.errors.message.Text, "which model") {
		t.Errorf("it says %q", fx.s.errors.message.Text)
	}
}

// A provider can be set up and left off, which is worth keeping: turning it on
// again should not mean setting it up again.
func TestItCanBeSetUpAndLeftOff(t *testing.T) {
	fx := newFixture(t)
	fx.s.setUpAssistant()
	top := fx.s.win.Canvas().Overlays().Top()
	selectsIn(top)[0].SetSelected("A model of your own")
	entriesIn(top)[0].SetText("a-model")
	test.Tap(findButton(top, "Save"))
	got := fx.s.d.Settings.Get().Assistant
	if got == nil || got.Model != "a-model" {
		t.Fatalf("it saved %+v", got)
	}
	if got.Enabled {
		t.Error("it turned itself on")
	}
	if !strings.Contains(fx.s.status.Text, "off") {
		t.Errorf("the status line says %q", fx.s.status.Text)
	}
}

// A plain-http address is allowed only to this machine. Anywhere else, a schema
// -- and possibly rows -- would cross the network in the clear (NFR-S3).
func TestAPlainAddressElsewhereIsRefused(t *testing.T) {
	fx := newFixture(t)
	fx.s.setUpAssistant()
	top := fx.s.win.Canvas().Overlays().Top()
	selectsIn(top)[0].SetSelected("A model of your own")
	entries := entriesIn(top)
	entries[0].SetText("a-model")
	entries[1].SetText("http://models.example.com:11434")
	findCheck(top, "Turn the assistant on").SetChecked(true)
	test.Tap(findButton(top, "Save"))
	if got := fx.s.d.Settings.Get().Assistant; got != nil {
		t.Errorf("it saved %+v", got)
	}
	if !fx.s.errors.shown() {
		t.Fatal("it said nothing")
	}
	if !strings.Contains(fx.s.errors.message.Text, "this machine") {
		t.Errorf("it says %q", fx.s.errors.message.Text)
	}
}

// The key is not shown back, so saving the settings again without retyping it
// must not lose it: somebody changing which model to ask has not asked for their
// key to be forgotten.
func TestTheKeySurvivesTheSettingsBeingSavedAgain(t *testing.T) {
	fx := newFixture(t)
	fx.s.setUpAssistant()
	top := fx.s.win.Canvas().Overlays().Top()
	selectsIn(top)[0].SetSelected("OpenAI")
	entries := entriesIn(top)
	entries[0].SetText("gpt-4o-mini")
	entries[2].SetText("sk-not-a-real-key")
	test.Tap(findButton(top, "Save"))

	fx.s.setUpAssistant()
	top = fx.s.win.Canvas().Overlays().Top()
	entries = entriesIn(top)
	if entries[2].Text != "" {
		t.Error("the key is shown back")
	}
	entries[0].SetText("gpt-4o")
	test.Tap(findButton(top, "Save"))
	if got := fx.s.d.Settings.Get().Assistant; got == nil || got.Model != "gpt-4o" {
		t.Fatalf("it saved %+v", got)
	}
	got, err := fx.s.assistantSecrets()("assistant.openai")
	if err != nil {
		t.Fatalf("the key has gone: %v", err)
	}
	if got != "sk-not-a-real-key" {
		t.Errorf("the keychain holds %q", got)
	}
}

// A connection's opt-in is two switches, and the second follows the first: a
// connection nobody opted in has not opted in to anything.
func TestAConnectionsOptIn(t *testing.T) {
	fx := newFixture(t)
	c := fx.create(t, "db1", nil)
	f := fx.s.showConnectionForm(c.ID)
	if f == nil {
		t.Fatal("the form did not open")
	}
	if f.aiSchema.Checked || !f.aiData.Disabled() {
		t.Error("a connection starts opted in, or its rows switch starts usable")
	}
	f.aiSchema.SetChecked(true)
	if f.aiData.Disabled() {
		t.Error("the rows switch is still off once the connection has opted in")
	}
	f.aiData.SetChecked(true)
	if got := f.assistantOptIn(); got == nil || !got.Enabled || !got.Data {
		t.Errorf("it would save %+v", got)
	}
	// Turning the first off turns the second off with it, rather than leaving a
	// tick nobody can see the meaning of.
	f.aiSchema.SetChecked(false)
	if f.aiData.Checked || !f.aiData.Disabled() {
		t.Error("the rows switch survived the opt-in being taken away")
	}
	if got := f.assistantOptIn(); got != nil {
		t.Errorf("it would save %+v for a connection that has agreed nothing", got)
	}
}

// An opt-in is written and read back, so that it survives a restart — and taking
// it away leaves no section at all, which is how "off by default" is written in a
// file (FR-14.4).
func TestAnOptInSurvives(t *testing.T) {
	fx := newFixture(t)
	c := fx.create(t, "db1", nil)
	if c.Assistant != nil {
		t.Errorf("a new connection carries %+v", c.Assistant)
	}
	// Through the form, which is the only way somebody gives one.
	f := fx.s.showConnectionForm(c.ID)
	if f == nil {
		t.Fatal("the form did not open")
	}
	f.aiSchema.SetChecked(true)
	f.aiData.SetChecked(true)
	test.Tap(f.saveBtn)
	got, ok := fx.conns.Get(c.ID)
	if !ok {
		t.Fatal("the connection has gone")
	}
	if got.Assistant == nil || !got.Assistant.Enabled || !got.Assistant.Data {
		t.Fatalf("it reads back as %+v", got.Assistant)
	}
	// And the form shows it again, so that somebody opening a connection they
	// opted in a month ago can see that they did.
	f = fx.s.showConnectionForm(c.ID)
	if !f.aiSchema.Checked || !f.aiData.Checked {
		t.Errorf("the form shows schema=%v rows=%v", f.aiSchema.Checked, f.aiData.Checked)
	}
	// Taken away, and there is nothing left behind.
	f.aiSchema.SetChecked(false)
	test.Tap(f.saveBtn)
	if got, _ = fx.conns.Get(c.ID); got.Assistant != nil {
		t.Errorf("it still carries %+v", got.Assistant)
	}
}

// A file that says rows without the opt-in shows neither switch: a tick nobody
// can act on reads as one that means something, and settings files are
// hand-edited (FR-14.4).
func TestRowsWithoutTheOptInShowNeitherSwitch(t *testing.T) {
	fx := newFixture(t)
	c := fx.create(t, "db1", nil)
	c.Assistant = &store.ConnectionAssistant{Data: true}
	if err := fx.conns.Update(c, app.SecretEdit{}); err != nil {
		t.Fatal(err)
	}
	f := fx.s.showConnectionForm(c.ID)
	if f == nil {
		t.Fatal("the form did not open")
	}
	if f.aiSchema.Checked || f.aiData.Checked {
		t.Errorf("the form shows schema=%v rows=%v", f.aiSchema.Checked, f.aiData.Checked)
	}
	if !f.aiData.Disabled() {
		t.Error("the rows switch is usable on a connection that has not opted in")
	}
}

// A window with no settings file has nowhere to keep a provider, so the
// assistant is not offered and nothing reaches for a file that is not there.
// Deps.Settings is documented as nil-able and the end-to-end harness is such a
// window, which is where this came from: sync asks every command whether it is
// enabled, so one reach for a missing file panics the whole window.
func TestWithNoSettingsFileTheAssistantIsNotOffered(t *testing.T) {
	fx := newFixture(t)
	c := fx.create(t, "db1", nil)
	d := fx.deps
	d.Settings = nil
	s := New(fx.app, d)
	t.Cleanup(s.shutdown)
	s.Explorer.Refresh("")
	s.Explorer.Tree.Select(view.ConnectionID(c.ID))
	s.sync()
	if s.canSetUpAssistant() {
		t.Error("it offers to configure a provider it could not save")
	}
	if s.canAskAssistant() {
		t.Error("it offers to ask")
	}
	if s.canExplainWithAssistant() {
		t.Error("it offers to explain")
	}
}

// A selection can outlive the connection it names -- deleted in another window,
// or from the file. Asked about one of those, it says to open a connection
// rather than naming nothing at all.
func TestAskingAboutAConnectionThatHasGone(t *testing.T) {
	fx, _, c := withAssistant(t, false)
	selectConnection(t, fx, c.ID)
	if !fx.s.canAskAssistant() {
		t.Fatal("it is not offered before the connection goes")
	}
	if err := fx.conns.Delete(c.ID); err != nil {
		t.Fatal(err)
	}
	if fx.s.canAskAssistant() {
		t.Error("it is offered for a connection that has gone")
	}
	fx.s.askAssistant()
	if fx.s.win.Canvas().Overlays().Top() != nil {
		t.Error("it asked for a question about a connection that has gone")
	}
	if !fx.s.errors.shown() {
		t.Fatal("it said nothing")
	}
	if !strings.Contains(fx.s.errors.message.Text, "Open a connection") {
		t.Errorf("it says %q", fx.s.errors.message.Text)
	}
}

// Setting the assistant up needs somewhere to put the key, so it is not offered
// with the vault shut: a dialog that asked for a key it could not save would
// lose it (FR-1.5).
func TestSettingUpNeedsAnOpenVault(t *testing.T) {
	fx := newFixture(t)
	if !fx.s.canSetUpAssistant() {
		t.Fatal("it is not offered with the vault open")
	}
	if err := fx.conns.LockVault("correct horse"); err != nil {
		t.Fatal(err)
	}
	fx.conns.CloseVault()
	if fx.s.canSetUpAssistant() {
		t.Error("it is offered with the vault shut")
	}
}

// "Off by default" is the absence of a section rather than a flag somebody has
// to remember to write, and that is a property of the file (FR-14.4).
func TestAFileWithNoAssistantHasNoSection(t *testing.T) {
	fx := newFixture(t)
	fx.create(t, "db1", nil)
	raw, err := json.Marshal(fx.s.d.Settings.Get())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "assistant") {
		t.Errorf("a fresh settings file says:\n%s", raw)
	}
}
