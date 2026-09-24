package localdb

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
)

// A workspace is a piece of work rather than a place: the connections it
// is done over, the tabs it was left open at, and the saved queries that
// belong to it (FR-15.9).
//
// It is not a second copy of anything. The connections are the ones
// already saved, named here by their IDs; the tabs are a session, the same
// shape the window already keeps; and a saved query belongs to a workspace
// because its connection does. Nothing is duplicated, so nothing can
// disagree.

// Workspace is a named grouping.
type Workspace struct {
	ID   string
	Name string
	// Connections are the connections it is over, by ID. None means every
	// connection, which is what a workspace nobody has narrowed shows.
	Connections []string
	// Tabs is the window as this workspace was left. It is a session
	// because that is what a window is.
	Tabs Session
	Used time.Time
}

// Workspaces live in the key-value table under this prefix.
const workspacePrefix, workspaceEnd = "workspace/", "workspace0"

// Holds reports whether a connection belongs to this workspace. One over
// no connections in particular holds them all.
func (w Workspace) Holds(connID string) bool {
	if len(w.Connections) == 0 {
		return true
	}
	for _, id := range w.Connections {
		if id == connID {
			return true
		}
	}
	return false
}

// PutWorkspace stores a workspace, replacing any with its ID.
func (d *DB) PutWorkspace(ctx context.Context, w Workspace) error {
	switch {
	case w.ID == "":
		return errors.New("a workspace needs an ID")
	case strings.TrimSpace(w.Name) == "":
		return errors.New("a workspace needs a name")
	}
	b, err := json.Marshal(w)
	if err != nil {
		return err
	}
	return d.Put(ctx, workspacePrefix+w.ID, b)
}

// Workspaces returns every workspace, by name.
//
// One that cannot be read is left where it is and named in the error; the
// rest are still returned, because one unreadable workspace is not a
// reason to have none.
func (d *DB) Workspaces(ctx context.Context) ([]Workspace, error) {
	rows, err := d.db.QueryContext(ctx,
		"SELECT key, value FROM kv WHERE key >= ? AND key < ?", workspacePrefix, workspaceEnd)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var (
		out []Workspace
		bad []error
	)
	for rows.Next() {
		var (
			key string
			b   []byte
		)
		if err := rows.Scan(&key, &b); err != nil {
			return nil, err
		}
		var w Workspace
		if err := json.Unmarshal(b, &w); err != nil {
			bad = append(bad, errors.New(key+": "+err.Error()))
			continue
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, errors.Join(bad...)
}

// DeleteWorkspace forgets one. What it grouped is left where it is: a
// workspace holds nothing of its own, so forgetting one loses no
// connection, no query and no saved view.
func (d *DB) DeleteWorkspace(ctx context.Context, id string) error {
	return d.Delete(ctx, workspacePrefix+id)
}
