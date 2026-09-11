package localdb

import (
	"context"
	"encoding/json"
	"fmt"
)

// Session is the window as it was left: its size, the sidebar's width and
// the tabs in order, so that the next start puts it back (FR-15.2, NFR-R3).
// Unsaved query text is not here but in scratch buffers, which are written
// as it is typed.
type Session struct {
	Width, Height float32
	Sidebar       float64 // the sidebar's share of the window's width
	Tabs          []SessionTab
	Active        int // index into Tabs; -1 when no tab is selected
	// Expanded is the explorer's open branches, by tree ID.
	Expanded []string `json:",omitempty"`
}

// Kinds of session tab.
const (
	SessionObject    = "object"
	SessionQuery     = "query"
	SessionStructure = "structure"
)

// SessionTab is one tab. An object tab names its object and how its rows
// were being viewed; a query tab names its scratch buffer and saved query.
type SessionTab struct {
	Kind         string
	ConnectionID string
	Pinned       bool `json:",omitempty"`

	RefKind string   `json:",omitempty"`
	RefPath []string `json:",omitempty"`
	Label   string   `json:",omitempty"`
	// Filters maps a column's name to its filter text, and Sorts are by
	// name too, so that a column added or dropped since cannot move either
	// onto another column.
	Filters map[string]string `json:",omitempty"`
	Sorts   []SessionSort     `json:",omitempty"`
	Where   string            `json:",omitempty"`

	// Columns are the table's columns as shown, left to right, each with
	// the width a person gave it, if any. Hidden names the hidden ones, and
	// Frozen is how many stay in view as the rest scroll. All are empty
	// while the table is laid out as it first opened.
	Columns []SessionColumn `json:",omitempty"`
	Hidden  []string        `json:",omitempty"`
	Frozen  int             `json:",omitempty"`

	ScratchID string `json:",omitempty"`
	SavedID   string `json:",omitempty"`
}

// SessionColumn is one column as shown. A zero Width is the column's own.
type SessionColumn struct {
	Name  string
	Width float32 `json:",omitempty"`
}

// SessionSort is one column of a tab's sort.
type SessionSort struct {
	Column     string
	Descending bool `json:",omitempty"`
}

const sessionKey = "session"

// PutSession stores the session, replacing the last one.
func (d *DB) PutSession(ctx context.Context, s Session) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return d.Put(ctx, sessionKey, b)
}

// Session returns the session last stored, and whether there is one.
func (d *DB) Session(ctx context.Context) (Session, bool, error) {
	b, ok, err := d.Get(ctx, sessionKey)
	if !ok || err != nil {
		return Session{}, false, err
	}
	var s Session
	if err := json.Unmarshal(b, &s); err != nil {
		return Session{}, false, fmt.Errorf("the saved session cannot be read: %w", err)
	}
	return s, true, nil
}
