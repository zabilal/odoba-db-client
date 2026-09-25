package assistant

import (
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/store"
)

// The settings and a connection's opt-in, read as a provider and a consent
// (FR-14.4, FR-14.5).
//
// This is the one place the two halves meet: what somebody configured, and what
// they agreed to. It is here rather than in the window because the window must
// not be the thing that decides — a Consent built in a dialog would be a Consent
// a dialog could get wrong.

// ProviderFor is the provider the settings describe, and whether there is one.
//
// A settings file with no assistant section has no provider, which is how the
// application ships: "off by default" is the absence of a section rather than a
// flag somebody has to remember to write (FR-14.4).
func ProviderFor(s *store.Assistant) (Provider, bool) {
	if s == nil || strings.TrimSpace(s.Provider) == "" {
		return Provider{}, false
	}
	for _, p := range Builtin() {
		if !strings.EqualFold(p.ID, s.Provider) {
			continue
		}
		p.Model = strings.TrimSpace(s.Model)
		if e := strings.TrimSpace(s.Endpoint); e != "" {
			// A gateway of one's own, or a model on one's own machine, stands
			// in front of the provider whose wire it speaks.
			p.Endpoint = e
		}
		if h := strings.TrimSpace(s.Header); h != "" {
			p.Header = h
		}
		return p, true
	}
	return Provider{}, false
}

// ConsentFor is what may be sent, from the application's settings, the
// connection's own, and whether consent has been given for this session.
//
// The session's consent is a parameter rather than a field of either, because
// it is not written anywhere: it is given for one act and asked again next time
// (FR-14.4).
func ConsentFor(s *store.Assistant, c *store.SavedConnection, confirmed bool) Consent {
	out := Consent{Confirmed: confirmed}
	if s != nil {
		out.Enabled = s.Enabled
	}
	if c == nil {
		return out
	}
	out.Production = strings.EqualFold(c.Environment, "production")
	if c.Assistant != nil {
		out.Connection = c.Assistant.Enabled
		// The second switch as it stands. It is meaningless without the first,
		// and nothing has to be done about that here: Allow and Describe both
		// answer for the opt-in before they look at the data, so a connection
		// that has not opted in has not opted in to anything.
		out.Data = c.Assistant.Data
	}
	return out
}

// Ready reports whether the assistant can be asked about this connection at
// all, which is what the window asks before offering it (REQ-DB-2): a command
// offered where it cannot work is worse than one that is not there.
func Ready(s *store.Assistant, c *store.SavedConnection) bool {
	if ConsentFor(s, c, false).Allow(false) != nil {
		return false
	}
	p, ok := ProviderFor(s)
	return ok && p.Valid() == nil
}
