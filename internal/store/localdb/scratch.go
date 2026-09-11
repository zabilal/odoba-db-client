package localdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
)

// Scratch is a query tab's text that is saved nowhere else. It is kept as it
// is typed, so that a crash does not lose it, and its tab reopens at the next
// start (NFR-R2, NFR-R3).
type Scratch struct {
	ID           string
	ConnectionID string
	// SavedID is the saved query the text is an edit of; empty if none.
	SavedID string
	Body    string
	// Opened is when the tab first opened. Tabs reopen in this order.
	Opened time.Time
}

// Scratch buffers live in the key-value table under this prefix. The next
// key after every one of them is the prefix with its '/' incremented.
const scratchPrefix, scratchEnd = "scratch/", "scratch0"

// PutScratch stores a scratch buffer, replacing any with its ID.
func (d *DB) PutScratch(ctx context.Context, sc Scratch) error {
	if sc.ID == "" {
		return errors.New("a scratch buffer needs an ID")
	}
	b, err := json.Marshal(sc)
	if err != nil {
		return err
	}
	return d.Put(ctx, scratchPrefix+sc.ID, b)
}

// Scratches returns every scratch buffer, oldest tab first. A buffer that
// cannot be read is left where it is and named in the error; the rest are
// still returned.
func (d *DB) Scratches(ctx context.Context) ([]Scratch, error) {
	rows, err := d.db.QueryContext(ctx,
		"SELECT key, value FROM kv WHERE key >= ? AND key < ?", scratchPrefix, scratchEnd)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var (
		out []Scratch
		bad []error
	)
	for rows.Next() {
		var (
			key string
			v   []byte
		)
		if err := rows.Scan(&key, &v); err != nil {
			return nil, err
		}
		var sc Scratch
		if err := json.Unmarshal(v, &sc); err != nil {
			bad = append(bad, fmt.Errorf("%s: %w", key, err))
			continue
		}
		out = append(out, sc)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Opened.Before(out[j].Opened) })
	return out, errors.Join(bad...)
}

// DeleteScratch removes a scratch buffer. Removing one that is not there is
// not an error.
func (d *DB) DeleteScratch(ctx context.Context, id string) error {
	return d.Delete(ctx, scratchPrefix+id)
}
