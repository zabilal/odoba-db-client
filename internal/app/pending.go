package app

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Pending changes (FR-4.3, ADR-0027). Edits to a table's rows gather here,
// locally, and nothing reaches the server until they are committed: a cell
// changed, a row deleted, a row added. Each row read is known by the values
// of its identity, not by where it sits in the grid, so an edit stays with
// its row when a sort or a filter moves it, and a row read again finds its
// edits. Only real changes count: a cell set back to what it was is no
// change, and a row with none left is unchanged.

// ErrNoRowIdentity is a table whose rows cannot be told apart, and so cannot
// be edited: a change could not say which row it is to (FR-4.7).
var ErrNoRowIdentity = errors.New("these rows have no key to tell them apart, so they cannot be edited")

// Pending is one table's changes not yet written.
type Pending struct {
	id    model.RowIdentity
	cols  []model.ColumnDef
	keyAt []int // where each identity column is among cols

	rows  map[string]*change // changes to rows read, by their key
	order []string           // those keys, in the order first changed
	added []*change          // new rows, in the order added
}

// change is one row's pending change. values holds the columns changed, or
// for a new row those given, by their place among the columns.
type change struct {
	kind   source.ChangeKind
	key    []any
	orig   model.Row // the row as read; nil for a new one
	values map[int]any
}

// NewPending starts the pending changes of rows with these columns and this
// identity. It refuses rows it could not address: an identity that edits
// nothing, or whose columns are not among the rows'.
func NewPending(cols []model.ColumnDef, id model.RowIdentity) (*Pending, error) {
	if !id.Editable() {
		return nil, ErrNoRowIdentity
	}
	p := &Pending{id: id, cols: cols, rows: map[string]*change{}}
	for _, name := range id.Columns {
		at := -1
		for i, c := range cols {
			if c.Name == name {
				at = i
			}
		}
		if at < 0 {
			return nil, fmt.Errorf("%w: the key's column %q is not among them", ErrNoRowIdentity, name)
		}
		p.keyAt = append(p.keyAt, at)
	}
	return p, nil
}

// key is a row's identity: its values, and a string that is the same for
// every read of the same row.
func (p *Pending) key(row model.Row) (string, []any) {
	vals := make([]any, len(p.keyAt))
	for i, at := range p.keyAt {
		if at < len(row) {
			vals[i] = row[at]
		}
	}
	return keyString(vals), vals
}

// keyString is a key's values, with their types, as one string.
func keyString(vals []any) string {
	var b strings.Builder
	for _, v := range vals {
		fmt.Fprintf(&b, "%T\x1f%v\x1e", v, v)
	}
	return b.String()
}

// Set changes one cell of a row read. Set back to what the row holds, the
// cell is no longer changed. A deleted row is not edited: revert it first.
func (p *Pending) Set(row model.Row, col int, v any) error {
	if col < 0 || col >= len(p.cols) {
		return fmt.Errorf("app: no column %d", col)
	}
	k, key := p.key(row)
	c := p.rows[k]
	if c != nil && c.kind == source.ChangeDelete {
		return errors.New("the row is to be deleted; revert it before changing it")
	}
	if col < len(row) && sameValue(row[col], v) {
		p.RevertCell(row, col)
		return nil
	}
	if c == nil {
		c = &change{kind: source.ChangeUpdate, key: key, orig: row, values: map[int]any{}}
		p.rows[k] = c
		p.order = append(p.order, k)
	}
	c.values[col] = v
	return nil
}

// Delete marks a row read to be deleted. Any edits to it go: the row is
// going.
func (p *Pending) Delete(row model.Row) {
	k, key := p.key(row)
	c := p.rows[k]
	if c == nil {
		c = &change{key: key, orig: row}
		p.rows[k] = c
		p.order = append(p.order, k)
	}
	c.kind, c.values = source.ChangeDelete, nil
}

// RevertCell undoes one cell's change. A row left with no change is
// unchanged.
func (p *Pending) RevertCell(row model.Row, col int) {
	k, _ := p.key(row)
	c := p.rows[k]
	if c == nil || c.kind != source.ChangeUpdate {
		return
	}
	delete(c.values, col)
	if len(c.values) == 0 {
		p.forget(k)
	}
}

// RevertRow undoes every change to a row read, a deletion included.
func (p *Pending) RevertRow(row model.Row) {
	k, _ := p.key(row)
	p.forget(k)
}

func (p *Pending) forget(k string) {
	if _, ok := p.rows[k]; !ok {
		return
	}
	delete(p.rows, k)
	for i, o := range p.order {
		if o == k {
			p.order = append(p.order[:i:i], p.order[i+1:]...)
			break
		}
	}
}

// RevertAll undoes every change, the new rows included.
func (p *Pending) RevertAll() {
	p.rows, p.order, p.added = map[string]*change{}, nil, nil
}

// State is how a row read stands. The grid asks it of every cell it draws,
// so with nothing changed it answers without reading the row's key.
func (p *Pending) State(row model.Row) model.RowState {
	if len(p.rows) == 0 {
		return model.RowUnchanged
	}
	k, _ := p.key(row)
	switch c := p.rows[k]; {
	case c == nil:
		return model.RowUnchanged
	case c.kind == source.ChangeDelete:
		return model.RowDeleted
	}
	return model.RowModified
}

// Value is a cell's pending value, if it has one.
func (p *Pending) Value(row model.Row, col int) (any, bool) {
	if len(p.rows) == 0 {
		return nil, false
	}
	k, _ := p.key(row)
	c := p.rows[k]
	if c == nil {
		return nil, false
	}
	v, ok := c.values[col]
	return v, ok
}

// Add starts a new row, with no column given, and says which new row it is.
func (p *Pending) Add() int {
	p.added = append(p.added, &change{kind: source.ChangeInsert, values: map[int]any{}})
	return len(p.added) - 1
}

// SetAdded gives a column of a new row a value.
func (p *Pending) SetAdded(i, col int, v any) error {
	if i < 0 || i >= len(p.added) {
		return fmt.Errorf("app: no new row %d", i)
	}
	if col < 0 || col >= len(p.cols) {
		return fmt.Errorf("app: no column %d", col)
	}
	p.added[i].values[col] = v
	return nil
}

// UnsetAdded takes a new row's column back to not given, so the server
// gives it its default.
func (p *Pending) UnsetAdded(i, col int) {
	if i >= 0 && i < len(p.added) {
		delete(p.added[i].values, col)
	}
}

// RemoveAdded drops a new row, which nothing had written.
func (p *Pending) RemoveAdded(i int) {
	if i >= 0 && i < len(p.added) {
		p.added = append(p.added[:i:i], p.added[i+1:]...)
	}
}

// Duplicate adds a new row with a row's values, its pending edits
// included, but not its key, which the server gives or is typed: a copy with
// the same key could not be inserted. A column the row was not given stays
// not given. It says which new row it is.
func (p *Pending) Duplicate(row model.Row) int {
	i := p.Add()
	for col := range p.cols {
		if col >= len(row) || slices.Contains(p.keyAt, col) {
			continue
		}
		v := row[col]
		if nv, ok := p.Value(row, col); ok {
			v = nv
		}
		if _, given := v.(model.Default); !given {
			p.added[i].values[col] = v
		}
	}
	return i
}

// Added is the new rows as rows, a column not given model.Default.
func (p *Pending) Added() []model.Row {
	out := make([]model.Row, len(p.added))
	for i, c := range p.added {
		row := make(model.Row, len(p.cols))
		for col := range row {
			row[col] = model.Default{}
		}
		for col, v := range c.values {
			row[col] = v
		}
		out[i] = row
	}
	return out
}

// Len is how many rows are changed, added or deleted.
func (p *Pending) Len() int { return len(p.order) + len(p.added) }

// Change says which row the changeset's i'th change is to: a row read, by
// its key, or a new row, by its place among the new rows.
func (p *Pending) Change(i int) (key []any, added int, ok bool) {
	switch {
	case i >= 0 && i < len(p.order):
		return p.rows[p.order[i]].key, -1, true
	case i >= len(p.order) && i < p.Len():
		return nil, i - len(p.order), true
	}
	return nil, -1, false
}

// IsRow reports whether a row read has a key, as Change gives it.
func (p *Pending) IsRow(row model.Row, key []any) bool {
	k, _ := p.key(row)
	return k == keyString(key)
}

// Changeset is the changes as the source's writer takes them: each row read
// by the key it had, an update with only the columns changed, so an edit to
// one cell does not write over a change another made to another; deletions
// and updates in the order first made, then the new rows in the order added.
func (p *Pending) Changeset(confirmed bool) source.Changeset {
	cs := source.Changeset{Target: p.id.Target, Identity: p.id, Confirmed: confirmed}
	for _, k := range p.order {
		c := p.rows[k]
		cs.Changes = append(cs.Changes, source.RowChange{Kind: c.kind, Key: c.key, Values: p.named(c.values)})
	}
	for _, c := range p.added {
		cs.Changes = append(cs.Changes, source.RowChange{Kind: source.ChangeInsert, Values: p.named(c.values)})
	}
	return cs
}

// named is values by column name.
func (p *Pending) named(values map[int]any) map[string]any {
	out := make(map[string]any, len(values))
	for col, v := range values {
		out[p.cols[col].Name] = v
	}
	return out
}

// sameValue reports whether two cell values are the same value: bytes by
// their bytes, times as instants, anything else as equal.
func sameValue(a, b any) bool {
	switch x := a.(type) {
	case []byte:
		y, ok := b.([]byte)
		return ok && bytes.Equal(x, y)
	case time.Time:
		y, ok := b.(time.Time)
		return ok && x.Equal(y)
	}
	return reflect.DeepEqual(a, b)
}
