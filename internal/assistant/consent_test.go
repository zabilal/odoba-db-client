package assistant

import (
	"errors"
	"strings"
	"testing"
)

// What the assistant may see, and what it may not (FR-14.3, FR-14.4).
//
// This is the part of the assistant with nothing to do with a model: the
// question is whether a request may be made at all, and it is answered here so
// that no window and no provider can be the thing that decides.

// A Consent nobody filled in allows nothing. That is the whole of "off by
// default", and it is a property of the zero value rather than of a setting
// somebody has to remember to write.
func TestNothingIsAllowedByDefault(t *testing.T) {
	var none Consent
	if err := none.Allow(false); !errors.Is(err, ErrOff) {
		t.Errorf("asking about the schema said %v", err)
	}
	if err := none.Allow(true); !errors.Is(err, ErrOff) {
		t.Errorf("sending data said %v", err)
	}
	if none.AllowsData() {
		t.Error("it allows data")
	}
}

// The switches are asked in order, so that somebody who has not turned the
// assistant on is told that rather than told about a data toggle they have
// never seen.
func TestWhichSwitchIsTheOneThatIsOff(t *testing.T) {
	for name, c := range map[string]struct {
		consent Consent
		data    bool
		want    error
	}{
		"the application is off": {Consent{}, false, ErrOff},
		"the application is off, even for data": {
			Consent{Connection: true, Data: true}, true, ErrOff},
		"the connection is not opted in": {Consent{Enabled: true}, false, ErrConnectionOff},
		"the connection is not opted in, even for data": {
			Consent{Enabled: true, Data: true}, true, ErrConnectionOff},
		"the schema is allowed": {Consent{Enabled: true, Connection: true}, false, nil},
		"data is off":           {Consent{Enabled: true, Connection: true}, true, ErrDataOff},
		"data is on":            {Consent{Enabled: true, Connection: true, Data: true}, true, nil},
		"production data needs consent this session": {
			Consent{Enabled: true, Connection: true, Data: true, Production: true}, true,
			ErrProductionUnconfirmed},
		"production data, consented": {
			Consent{Enabled: true, Connection: true, Data: true, Production: true, Confirmed: true},
			true, nil},
		// A production connection's schema is not its data, and asking about
		// one is not asking about the other.
		"production schema needs no consent": {
			Consent{Enabled: true, Connection: true, Production: true}, false, nil},
	} {
		t.Run(name, func(t *testing.T) {
			err := c.consent.Allow(c.data)
			if c.want == nil {
				if err != nil {
					t.Errorf("it said %v", err)
				}
				return
			}
			if !errors.Is(err, c.want) {
				t.Errorf("it said %v, want %v", err, c.want)
			}
		})
	}
}

// Consent for a session is not written anywhere, and is asked again next time:
// a confirmation remembered is a confirmation nobody gave.
func TestConsentForASessionIsForOneSession(t *testing.T) {
	c := Consent{Enabled: true, Connection: true, Data: true, Production: true, Confirmed: true}
	if !c.AllowsData() {
		t.Fatal("consented, and it refuses")
	}
	// A fresh session has it off, because nothing puts it back: the field is
	// not in the settings and there is nothing here that could write it.
	c.Confirmed = false
	if c.AllowsData() {
		t.Error("it allows production data with no consent for this session")
	}
}

// What the assistant may see is said in one line, for the window to show beside
// the provider and the model: somebody about to ask a question should be able to
// read what leaves the machine without opening Settings.
func TestWhatItMaySeeIsSaidInALine(t *testing.T) {
	for name, c := range map[string]struct {
		consent Consent
		says    string
	}{
		"off":             {Consent{}, "off"},
		"off here":        {Consent{Enabled: true}, "off for this connection"},
		"the schema only": {Consent{Enabled: true, Connection: true}, "not the data"},
		"the data too":    {Consent{Enabled: true, Connection: true, Data: true}, "rows you send"},
		"production, unconsented": {
			Consent{Enabled: true, Connection: true, Data: true, Production: true},
			"consent each session"},
	} {
		t.Run(name, func(t *testing.T) {
			got := c.consent.Describe()
			if !strings.Contains(got, c.says) {
				t.Errorf("it says %q, which does not mention %q", got, c.says)
			}
		})
	}
}

// A request says for itself whether it carries data. The caller does not get to
// say: a caller that got it wrong would send data under a consent that did not
// cover it.
func TestARequestSaysWhetherItCarriesData(t *testing.T) {
	names := Request{Kind: KindQuery, Question: "how many people",
		Grounding: Grounding{Tables: []Table{{Name: "people"}}}}
	if names.SendsData() {
		t.Error("a schema reads as data")
	}
	withRows := names
	withRows.Grounding.Rows = []Sample{{Table: "people", Rows: [][]string{{"1"}}}}
	if !withRows.SendsData() {
		t.Error("rows read as no data")
	}
	// An empty sample is not data: nothing in it came from a row.
	empty := names
	empty.Grounding.Rows = nil
	if empty.SendsData() {
		t.Error("no rows read as data")
	}
}

func TestARequestThatCannotBeAskedIsRefused(t *testing.T) {
	for name, c := range map[string]struct {
		request Request
		says    string
	}{
		"nothing to ask": {Request{Kind: KindQuery}, "no question"},
		"only spaces":    {Request{Kind: KindQuery, Question: "   "}, "no question"},
		// Asking what a statement does with no statement is not the same
		// mistake as asking a question with no question, and says so: somebody
		// told there is no question when they pressed Explain would go looking
		// for a question box.
		"nothing to explain": {Request{Kind: KindExplain}, "nothing to explain"},
		"nothing to plan":    {Request{Kind: KindExplainPlan}, "nothing to explain"},
		"a kind nobody has":  {Request{Kind: Kind("sing"), Question: "a song"}, "not something"},
		"no kind at all":     {Request{Question: "a question"}, "not something"},
	} {
		t.Run(name, func(t *testing.T) {
			err := c.request.Valid()
			if err == nil {
				t.Fatal("it was accepted")
			}
			if !strings.Contains(err.Error(), c.says) {
				t.Errorf("it says %q, which does not mention %q", err, c.says)
			}
		})
	}
	for _, k := range kinds {
		if err := (Request{Kind: k, Question: "SELECT 1"}).Valid(); err != nil {
			t.Errorf("%s: %v", k, err)
		}
	}
}
