package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Where a question is asked (FR-14.5).
//
// Two wire formats cover every provider worth having: the OpenAI-compatible
// chat-completions call, which OpenAI, Ollama, llama.cpp, LM Studio, vLLM and
// most of what people run themselves speak; and Anthropic's messages call,
// which is its own. So a provider is a kind, an address and a model, and a
// local endpoint is not a special case — it is the OpenAI-compatible kind
// pointed at a URL somebody gives, which is why running a model on one's own
// machine needs no code here at all.
//
// The key is never in this struct. It is fetched from the keychain when a
// request is made, as a driver's password is (FR-1.5, NFR-S1), so that nothing
// holding a provider's settings is holding a credential.

// Wire is a request format. A provider's wire is not the same thing as what a
// request asks for (Kind), and calling both a kind would confuse the two in
// every signature they meet in.
type Wire string

const (
	// WireOpenAI is POST /v1/chat/completions with an OpenAI-shaped body. What
	// OpenAI speaks, and what almost everything self-hosted speaks in order to
	// be usable with OpenAI's own clients.
	WireOpenAI Wire = "openai"

	// WireAnthropic is POST /v1/messages, which is Anthropic's own.
	WireAnthropic Wire = "anthropic"
)

var wires = []Wire{WireOpenAI, WireAnthropic}

// Provider is where to ask, and which model to ask.
type Provider struct {
	// ID is how it is written in the settings.
	ID string

	// Name is what the window shows beside an answer, with the model
	// (FR-14.5): a person reading a generated statement has to be able to see
	// what produced it.
	Name string

	Wire Wire

	// Endpoint is the base URL. Required for every provider, including the
	// hosted ones: a provider whose address is compiled in cannot be pointed
	// at a proxy, and every organisation that allows this at all has one.
	Endpoint string

	// Model is the model's name as that provider spells it.
	Model string

	// Secret names the keychain entry holding the key, or is empty for an
	// endpoint that wants none — which is what a model on one's own machine
	// usually is.
	Secret string

	// Header is the name of the header the key goes in, for an endpoint that
	// differs. Empty is the kind's own.
	Header string
}

// Builtin are the providers offered without anybody configuring one, which is
// two hosted services and the shape of a local one. Each still needs a model
// and a key: nothing here guesses either.
func Builtin() []Provider {
	return []Provider{
		{ID: "openai", Name: "OpenAI", Wire: WireOpenAI,
			Endpoint: "https://api.openai.com", Secret: "assistant.openai"},
		{ID: "anthropic", Name: "Anthropic", Wire: WireAnthropic,
			Endpoint: "https://api.anthropic.com", Secret: "assistant.anthropic"},
		// A model on this machine, or on one of yours. The endpoint is what
		// somebody running Ollama, llama.cpp, LM Studio or vLLM already has,
		// and most of them want no key at all.
		{ID: "local", Name: "A model of your own", Wire: WireOpenAI,
			Endpoint: "http://localhost:11434", Secret: ""},
	}
}

// Valid says what is wrong with a provider, or nothing.
func (p Provider) Valid() error {
	switch {
	case strings.TrimSpace(p.ID) == "":
		return errors.New("a provider needs a name of its own")
	case !contains(wires, p.Wire):
		return fmt.Errorf("%q is not a way of talking to a model", p.Wire)
	case strings.TrimSpace(p.Endpoint) == "":
		return errors.New("a provider needs an address")
	case strings.TrimSpace(p.Model) == "":
		// Said rather than defaulted: a model nobody chose is a bill nobody
		// expected and an answer nobody can account for (FR-14.5).
		return errors.New("choose which model to ask")
	}
	u, err := url.Parse(strings.TrimSpace(p.Endpoint))
	if err != nil {
		return fmt.Errorf("that address cannot be read: %w", err)
	}
	switch u.Scheme {
	case "https":
	case "http":
		// Plain text is allowed only to this machine, which is what a model of
		// one's own is. Anywhere else, a prompt carrying a schema — and
		// possibly rows — would cross the network in the clear (NFR-S3).
		if !isLocal(u.Hostname()) {
			return errors.New("http:// is allowed only for a model on this machine; use https://")
		}
	default:
		return fmt.Errorf("%s:// is not an address a model is asked at", u.Scheme)
	}
	return nil
}

// isLocal reports whether a host is this machine. The names and addresses the
// loopback answers to, and nothing that merely looks private: a model on
// another machine on the network is still a prompt crossing a network.
func isLocal(host string) bool {
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1", "[::1]", "0.0.0.0":
		return true
	}
	return strings.HasSuffix(strings.ToLower(host), ".localhost")
}

// Answer is what came back, with what produced it.
type Answer struct {
	// Text is the model's answer, whole.
	Text string

	// Provider and Model are what answered, so the window can always say
	// (FR-14.5).
	Provider string
	Model    string

	// Took is how long it took, which is the other half of deciding whether to
	// ask again.
	Took time.Duration
}

// Secrets resolves the key a provider needs, as a driver's password is
// resolved: a function rather than a value, so that nothing holding a
// provider's settings holds a credential (FR-1.5, NFR-S1).
type Secrets func(name string) (string, error)

// Client asks a model something.
type Client struct {
	// HTTP is the client used. Zero uses one of this package's own, which
	// refuses redirects: a request carrying a schema must not be sent
	// somewhere else on a server's say-so.
	HTTP *http.Client

	// Secret resolves a provider's key. Nil means none is available, which is
	// fine for an endpoint that wants none and a refusal for one that does.
	Secret Secrets
}

// httpClient is the default. Redirects are refused for the reason
// internal/cloud refuses them: what is in flight is not for anywhere else.
var httpClient = &http.Client{
	Timeout: 2 * time.Minute,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("refused to follow a redirect while asking a model")
	},
}

// maxAnswer bounds what a provider can make this read. An answer is prose and
// a statement; a megabyte is far more than either.
const maxAnswer = 1 << 20

// Ask puts one question to one model.
//
// Consent is checked first and before anything is sent, and the request says
// for itself whether it carries data: a caller cannot be trusted to say, and a
// caller that got it wrong would send data under a consent that did not cover
// it (FR-14.4).
func (c *Client) Ask(ctx context.Context, p Provider, consent Consent, r Request) (*Answer, error) {
	if err := r.Valid(); err != nil {
		return nil, err
	}
	if err := consent.Allow(r.SendsData()); err != nil {
		return nil, err
	}
	if err := p.Valid(); err != nil {
		return nil, err
	}
	key := ""
	if p.Secret != "" {
		if c.Secret == nil {
			return nil, fmt.Errorf("%s needs a key and there is nowhere to read one from", p.Name)
		}
		var err error
		if key, err = c.Secret(p.Secret); err != nil {
			return nil, fmt.Errorf("%s's key could not be read: %w", p.Name, err)
		}
		if strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("%s has no key set", p.Name)
		}
	}
	body, err := c.body(p, r)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointFor(p), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	setKey(req, p, key)

	start := time.Now()
	client := c.HTTP
	if client == nil {
		client = httpClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s could not be reached: %w", p.Name, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswer))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		// The body of a refusal says which model was wrong, or which quota,
		// and is worth more than the status alone.
		return nil, fmt.Errorf("%s said %s: %s", p.Name, resp.Status, firstLine(string(raw)))
	}
	text, err := readAnswer(p.Wire, raw)
	if err != nil {
		return nil, fmt.Errorf("%s answered in a way this could not read: %w", p.Name, err)
	}
	return &Answer{Text: text, Provider: p.Name, Model: p.Model, Took: time.Since(start)}, nil
}

// endpointFor is where a kind is asked.
func endpointFor(p Provider) string {
	base := strings.TrimRight(strings.TrimSpace(p.Endpoint), "/")
	switch p.Wire {
	case WireAnthropic:
		return base + "/v1/messages"
	}
	return base + "/v1/chat/completions"
}

// setKey puts the key where the kind wants it. A provider may name a header of
// its own, which is what an endpoint behind somebody's own gateway needs.
func setKey(req *http.Request, p Provider, key string) {
	if key == "" {
		return
	}
	if p.Header != "" {
		req.Header.Set(p.Header, key)
		return
	}
	switch p.Wire {
	case WireAnthropic:
		req.Header.Set("x-api-key", key)
		req.Header.Set("anthropic-version", anthropicVersion)
	default:
		req.Header.Set("Authorization", "Bearer "+key)
	}
}

// anthropicVersion is the API version the messages call requires. Pinned
// rather than latest, because a version nobody chose is a shape nobody tested.
const anthropicVersion = "2023-06-01"

// maxOutput bounds an answer at the provider. A statement and its explanation
// is a page; asking for more is paying for a model to keep going.
const maxOutput = 2000

// body is the request a kind takes.
func (c *Client) body(p Provider, r Request) ([]byte, error) {
	system, user := Prompt(r)
	switch p.Wire {
	case WireAnthropic:
		return json.Marshal(map[string]any{
			"model":      p.Model,
			"max_tokens": maxOutput,
			"system":     system,
			"messages": []map[string]string{
				{"role": "user", "content": user},
			},
		})
	}
	return json.Marshal(map[string]any{
		"model":      p.Model,
		"max_tokens": maxOutput,
		// Zero, because what is wanted here is the same answer to the same
		// question: a statement that differed between two askings would be
		// impossible to review.
		"temperature": 0,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
	})
}

// readAnswer pulls the text out of what a kind sends back.
func readAnswer(wire Wire, raw []byte) (string, error) {
	switch wire {
	case WireAnthropic:
		var out struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			return "", err
		}
		var b strings.Builder
		for _, part := range out.Content {
			if part.Type == "text" {
				b.WriteString(part.Text)
			}
		}
		if b.Len() == 0 {
			return "", errors.New("it sent no text")
		}
		return b.String(), nil
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	if len(out.Choices) == 0 || out.Choices[0].Message.Content == "" {
		return "", errors.New("it sent no text")
	}
	return out.Choices[0].Message.Content, nil
}

// firstLine is enough of a refusal to read, which is all an error line has room
// for. The rest is in the provider's own logs.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	const longest = 300
	if len(s) > longest {
		return s[:longest] + "…"
	}
	return s
}
