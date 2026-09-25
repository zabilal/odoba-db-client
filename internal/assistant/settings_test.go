package assistant

import (
	"errors"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/store"
)

// The settings and a connection's opt-in, read as a provider and a consent
// (FR-14.4, FR-14.5).

// A settings file with no assistant section has no assistant. That is what "off
// by default" is in a file: the absence of a section, rather than a flag
// somebody has to remember to write.
func TestNoSectionIsNoAssistant(t *testing.T) {
	if _, ok := ProviderFor(nil); ok {
		t.Error("it found a provider in nothing")
	}
	c := ConsentFor(nil, nil, false)
	if err := c.Allow(false); !errors.Is(err, ErrOff) {
		t.Errorf("it said %v", err)
	}
	if Ready(nil, nil) {
		t.Error("it is ready with nothing configured")
	}
}

func TestTheProviderTheSettingsDescribe(t *testing.T) {
	p, ok := ProviderFor(&store.Assistant{Provider: "openai", Model: "a-model"})
	if !ok {
		t.Fatal("it found no provider")
	}
	if p.Name != "OpenAI" || p.Wire != WireOpenAI || p.Model != "a-model" {
		t.Errorf("it found %+v", p)
	}
	// Its own address unless one was given.
	if p.Endpoint != "https://api.openai.com" {
		t.Errorf("it would ask at %q", p.Endpoint)
	}
	// A gateway of one's own stands in front of the provider whose wire it
	// speaks, which is how an organisation that allows this at all does it.
	p, _ = ProviderFor(&store.Assistant{Provider: "openai", Model: "m",
		Endpoint: "https://ours.example.com", Header: "X-Our-Key"})
	if p.Endpoint != "https://ours.example.com" || p.Header != "X-Our-Key" {
		t.Errorf("it found %+v", p)
	}
	// A provider nobody has is no provider, rather than a guess.
	if _, ok := ProviderFor(&store.Assistant{Provider: "a-model-of-my-uncle's", Model: "m"}); ok {
		t.Error("it found a provider nobody has")
	}
	if _, ok := ProviderFor(&store.Assistant{Model: "m"}); ok {
		t.Error("it found a provider with no name")
	}
	// Whitespace is not a model, and a provider with no model is not ready.
	p, _ = ProviderFor(&store.Assistant{Provider: "openai", Model: "   "})
	if p.Model != "" {
		t.Errorf("it carries %q as a model", p.Model)
	}
	if p.Valid() == nil {
		t.Error("a provider with no model chosen is ready to use")
	}
}

func TestWhatMayBeSent(t *testing.T) {
	on := &store.Assistant{Enabled: true, Provider: "openai", Model: "m"}
	for name, c := range map[string]struct {
		settings   *store.Assistant
		connection *store.SavedConnection
		confirmed  bool
		want       error
		data       bool
	}{
		"the application is off": {&store.Assistant{Provider: "openai", Model: "m"},
			&store.SavedConnection{Assistant: &store.ConnectionAssistant{Enabled: true}}, false, ErrOff, false},
		"the connection has agreed nothing": {on, &store.SavedConnection{}, false, ErrConnectionOff, false},
		"the connection has opted in":       {on, optedIn(false), false, nil, false},
		"and its data too":                  {on, optedIn(true), false, nil, true},
		// A connection that has not opted in has not opted in to anything: the
		// data switch is meaningless without the first one.
		"data without the opt-in": {on,
			&store.SavedConnection{Assistant: &store.ConnectionAssistant{Data: true}}, false,
			ErrConnectionOff, false},
		"a production connection":            {on, production(true), false, nil, false},
		"a production connection, consented": {on, production(true), true, nil, true},
	} {
		t.Run(name, func(t *testing.T) {
			got := ConsentFor(c.settings, c.connection, c.confirmed)
			err := got.Allow(false)
			if c.want == nil {
				if err != nil {
					t.Errorf("asking about the schema said %v", err)
				}
			} else if !errors.Is(err, c.want) {
				t.Errorf("asking about the schema said %v, want %v", err, c.want)
			}
			if got.AllowsData() != c.data {
				t.Errorf("data allowed %v, want %v", got.AllowsData(), c.data)
			}
		})
	}
}

func optedIn(data bool) *store.SavedConnection {
	return &store.SavedConnection{Assistant: &store.ConnectionAssistant{Enabled: true, Data: data}}
}

func production(data bool) *store.SavedConnection {
	c := optedIn(data)
	c.Environment = "production"
	return c
}

// A production connection is recognised however it is spelt in the settings:
// the environment is text somebody typed or chose, and "Production" and
// "production" are the same place.
func TestAProductionConnectionIsRecognised(t *testing.T) {
	on := &store.Assistant{Enabled: true, Provider: "openai", Model: "m"}
	for _, env := range []string{"production", "Production", "PRODUCTION"} {
		c := optedIn(true)
		c.Environment = env
		if !ConsentFor(on, c, false).Production {
			t.Errorf("%q is not read as production", env)
		}
		if ConsentFor(on, c, false).AllowsData() {
			t.Errorf("%q sends data with no consent for this session", env)
		}
	}
	for _, env := range []string{"", "staging", "dev", "prod-ish"} {
		c := optedIn(true)
		c.Environment = env
		if ConsentFor(on, c, false).Production {
			t.Errorf("%q is read as production", env)
		}
	}
}

// Consent for a session is not in the settings and cannot be: there is no field
// for it, so nothing can write one.
func TestSessionConsentIsNotWritten(t *testing.T) {
	c := production(true)
	if ConsentFor(&store.Assistant{Enabled: true}, c, true).Confirmed != true {
		t.Error("consent given for this session was not carried")
	}
	// The connection's own settings hold two switches and no third: a
	// confirmation in a file is a confirmation nobody gave.
	if c.Assistant.Enabled != true || c.Assistant.Data != true {
		t.Fatalf("the connection holds %+v", c.Assistant)
	}
	if ConsentFor(&store.Assistant{Enabled: true}, c, false).Confirmed {
		t.Error("a fresh session starts consented")
	}
}

// Ready is what the window asks before offering the assistant at all: a command
// offered where it cannot work is worse than one that is not there.
func TestWhenTheAssistantIsReady(t *testing.T) {
	whole := &store.Assistant{Enabled: true, Provider: "openai", Model: "m"}
	if !Ready(whole, optedIn(false)) {
		t.Error("it is not ready with everything set")
	}
	for name, c := range map[string]struct {
		settings   *store.Assistant
		connection *store.SavedConnection
	}{
		"off":           {&store.Assistant{Provider: "openai", Model: "m"}, optedIn(false)},
		"no provider":   {&store.Assistant{Enabled: true, Model: "m"}, optedIn(false)},
		"no model":      {&store.Assistant{Enabled: true, Provider: "openai"}, optedIn(false)},
		"not opted in":  {whole, &store.SavedConnection{}},
		"no connection": {whole, nil},
	} {
		t.Run(name, func(t *testing.T) {
			if Ready(c.settings, c.connection) {
				t.Error("it is ready")
			}
		})
	}
	// A production connection that has not consented this session is still
	// ready: its schema can be asked about, which is what Ready is about.
	if !Ready(whole, production(false)) {
		t.Error("a production connection cannot be asked about its schema")
	}
}
