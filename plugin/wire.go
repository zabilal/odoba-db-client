package plugin

import "encoding/json"

// The conversation itself: one JSON object a line, in both directions.
//
// Lines rather than anything framed, because a line is what every language has
// a reader for, and a plugin that can be written in twenty lines of Python is a
// plugin somebody will write. The cost is that nothing binary goes over it
// without being encoded, which is the right trade for a protocol whose job is
// to be easy to implement correctly.

// Request is what the host asks.
type Request struct {
	// ID is the host's own number for this request. Answers carry it back, so
	// several can be in flight at once.
	ID int64 `json:"id"`
	// Op is what to do: hello, open, close, ping, info, root, children,
	// describe, browse, query or cancel.
	Op string `json:"op"`
	// Handle names the connection this is about, as open answered it.
	Handle string `json:"handle,omitempty"`

	Config *Config        `json:"config,omitempty"`
	Ref    *Ref           `json:"ref,omitempty"`
	Browse *BrowseOptions `json:"browse,omitempty"`
	// Of is the request cancel is about.
	Of int64 `json:"of,omitempty"`
}

// The operations. They are strings on the wire so that a plugin written in
// another language reads as what it does.
const (
	OpHello    = "hello"
	OpOpen     = "open"
	OpClose    = "close"
	OpPing     = "ping"
	OpInfo     = "info"
	OpRoot     = "root"
	OpChildren = "children"
	OpDescribe = "describe"
	OpBrowse   = "browse"
	OpCancel   = "cancel"
)

// Response is what the plugin answers. Several may carry the same ID: a browse
// answers columns, then a row line for each row, then done.
type Response struct {
	ID int64 `json:"id"`

	// Error says what went wrong. A response with an error carries nothing
	// else and ends the request.
	Error string `json:"error,omitempty"`
	// Kind classifies a failure to open, so that the application can say
	// something useful: config, auth, network, tls, nodatabase or refused.
	Kind string `json:"kind,omitempty"`

	Hello   *Hello   `json:"hello,omitempty"`
	Handle  string   `json:"handle,omitempty"`
	Info    *Info    `json:"info,omitempty"`
	Nodes   []Node   `json:"nodes,omitempty"`
	Object  *Object  `json:"object,omitempty"`
	Columns []Column `json:"columns,omitempty"`
	// Row is one row of a stream. Values are JSON: a number, a string, a
	// boolean, null, or an object or array for a document.
	Row []json.RawMessage `json:"row,omitempty"`
	// Done ends a request. Every request ends with either Done or Error, and
	// the host waits for one of them.
	Done bool `json:"done,omitempty"`
	// OK ends a request that answers nothing else.
	OK bool `json:"ok,omitempty"`
}

// The kinds of failure a plugin can name, matching what the application's own
// drivers report so that a plugin's refusal reads like everything else's.
const (
	FailConfig     = "config"
	FailAuth       = "auth"
	FailNetwork    = "network"
	FailTLS        = "tls"
	FailNoDatabase = "nodatabase"
	FailRefused    = "refused"
)
