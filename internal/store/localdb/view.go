package localdb

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
)

// A named view is a table's rows as somebody arranged them — its filters,
// its sort, the columns it shows and how wide they are — kept under a name
// so that the arrangement can be had again (FR-3.16).
//
// It is a session tab's arrangement with a name on it, and deliberately so.
// What a session restores when the application starts and what a view
// restores when somebody picks it are the same thing, so they are the same
// thing here: one shape to keep in step, and the code that puts a tab back
// does both.

// View is a saved arrangement of one object's rows.
type View struct {
	ID   string
	Name string
	// Tab is the arrangement, and names the object it is of.
	Tab   SessionTab
	Saved time.Time
}

// Views live in the key-value table under this prefix, grouped by the
// object they are of so that a table's own can be read without reading
// everybody's.
const viewPrefix = "view/"

// objectKey names the object a view is of. The separator is one no name
// holds, so that two different objects cannot share a key.
func objectKey(connID, refKind string, refPath []string) string {
	return strings.Join(append([]string{connID, refKind}, refPath...), "\x1f")
}

// viewKey is where one view is kept.
func viewKey(v View) string {
	return viewPrefix + objectKey(v.Tab.ConnectionID, v.Tab.RefKind, v.Tab.RefPath) + "\x1e" + v.ID
}

// PutView stores a view, replacing any with its ID.
func (d *DB) PutView(ctx context.Context, v View) error {
	switch {
	case v.ID == "":
		return errors.New("a view needs an ID")
	case strings.TrimSpace(v.Name) == "":
		return errors.New("a view needs a name")
	case v.Tab.ConnectionID == "" || len(v.Tab.RefPath) == 0:
		return errors.New("a view needs an object to be of")
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return d.Put(ctx, viewKey(v), b)
}

// Views returns the views saved for one object, by name.
//
// A view that cannot be read is left where it is and named in the error;
// the rest are still returned, because one unreadable view is not a reason
// to have none.
func (d *DB) Views(ctx context.Context, connID, refKind string, refPath []string) ([]View, error) {
	from := viewPrefix + objectKey(connID, refKind, refPath) + "\x1e"
	rows, err := d.db.QueryContext(ctx,
		"SELECT key, value FROM kv WHERE key >= ? AND key < ?", from, from+"\x7f")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var (
		out []View
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
		var v View
		if err := json.Unmarshal(b, &v); err != nil {
			bad = append(bad, errors.New(key+": "+err.Error()))
			continue
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, errors.Join(bad...)
}

// DeleteView forgets one.
func (d *DB) DeleteView(ctx context.Context, v View) error {
	return d.Delete(ctx, viewKey(v))
}
