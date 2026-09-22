package localdb

import (
	"context"
	"encoding/json"
)

// Where somebody put the boxes on a diagram (FR-8.2).
//
// Only the nodes somebody moved are kept. Keeping every position would mean
// a diagram never laid itself out again: a table added to the schema would
// have no position and would land wherever nothing else did, while the rest
// stayed exactly as they were read from a file weeks ago. Keeping only what
// was moved means the diagram is laid out afresh each time and the hand
// arrangement is put back on top of it.

// NodePlace is where one node was put.
type NodePlace struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// DiagramLayout is a diagram as somebody left it.
type DiagramLayout struct {
	// Moved holds the nodes somebody dragged, by their ID.
	Moved map[string]NodePlace `json:"moved,omitempty"`

	// Pan and Zoom are where the view was left, so that reopening a diagram
	// shows the part of it somebody was looking at.
	Pan  NodePlace `json:"pan"`
	Zoom float64   `json:"zoom,omitempty"`
}

func layoutKey(connID, diagram string) string { return "layout:" + connID + ":" + diagram }

// Layout reads a diagram's arrangement, and whether one was kept.
func (d *DB) Layout(ctx context.Context, connID, diagram string) (DiagramLayout, bool, error) {
	b, ok, err := d.Get(ctx, layoutKey(connID, diagram))
	if err != nil || !ok {
		return DiagramLayout{}, false, err
	}
	var l DiagramLayout
	if err := json.Unmarshal(b, &l); err != nil {
		// A layout that cannot be read is a layout nobody has: it is where
		// boxes were put, and losing it costs a rearrangement rather than
		// anything a person cannot do again.
		return DiagramLayout{}, false, nil
	}
	return l, true, nil
}

// PutLayout keeps a diagram's arrangement.
func (d *DB) PutLayout(ctx context.Context, connID, diagram string, l DiagramLayout) error {
	b, err := json.Marshal(l)
	if err != nil {
		return err
	}
	return d.Put(ctx, layoutKey(connID, diagram), b)
}

// ForgetLayout drops a diagram's arrangement, for laying it out afresh.
func (d *DB) ForgetLayout(ctx context.Context, connID, diagram string) error {
	return d.Delete(ctx, layoutKey(connID, diagram))
}
