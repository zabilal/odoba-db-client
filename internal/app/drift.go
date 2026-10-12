package app

import (
	"context"
	"errors"
	"io"
	"math"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Whether a row changed underneath an edit (FR-4.12).
//
// An edit is made to a row as it was read, which may be minutes ago. The
// statement that writes it finds the row by its key and sets the columns
// that were changed, so somebody else's change to another column is not
// written over (Pending.Changeset) — but their change to the same column is,
// and a deletion is written to a row that may no longer be the row it was.
// So before the changes are written, each row they touch is read again by
// its key and held against what was read: the check is optimistic, in that
// nothing is locked while a person thinks.
//
// It warns; it does not prevent. Between this reading and the writing there
// is still a moment in which a row can change, and closing that would take
// the row's old values in every statement's condition, in every driver.

// Drifted is a row that is not as it was read.
type Drifted struct {
	Kind source.ChangeKind // what was to be done to it
	Key  []any
	// Gone is a row that is no longer there to be changed.
	Gone bool
	// Columns are the columns that hold something else now, where it is
	// still there.
	Columns []DriftedColumn
}

// DriftedColumn is one column of a row that holds something else now.
type DriftedColumn struct {
	Name     string
	Was, Now any
	// Edited reports whether the pending change writes this column: a change
	// about to be written over, rather than one beside it.
	Edited bool
}

// DriftLimit is the most rows a check reads again. Each is a read by its key,
// and a commit of thousands of rows should not become thousands of reads
// before it starts; what is past the limit is counted and said.
const DriftLimit = 200

// Drift reads again each row the pending changes update or delete, and
// returns those that are not as they were read, in the order they were
// changed, and how many were past the limit and not read. read is given the
// conditions that find one row — its key — and returns what matches, with the
// columns in the order the pending rows have them.
func (p *Pending) Drift(ctx context.Context, read func(ctx context.Context, key []source.Filter) ([]model.Row, error)) ([]Drifted, int, error) {
	var out []Drifted
	unchecked := 0
	for n, k := range p.order {
		if n >= DriftLimit {
			unchecked = len(p.order) - n
			break
		}
		c := p.rows[k]
		filters := make([]source.Filter, len(c.key))
		for i, v := range c.key {
			name := p.cols[p.keyAt[i]].SourceName()
			if v == nil {
				filters[i] = source.Filter{Column: name, Op: source.OpIsNull}
				continue
			}
			filters[i] = source.Filter{Column: name, Op: source.OpEqual, Values: []any{v}}
		}
		rows, err := read(ctx, filters)
		if err != nil {
			return nil, 0, err
		}
		d := Drifted{Kind: c.kind, Key: c.key}
		switch len(rows) {
		case 0:
			d.Gone = true
		case 1:
			for i, col := range p.cols {
				if i >= len(rows[0]) || i >= len(c.orig) || sameCell(c.orig[i], rows[0][i]) {
					continue
				}
				_, edited := c.values[i]
				d.Columns = append(d.Columns, DriftedColumn{Name: col.Name, Was: c.orig[i], Now: rows[0][i], Edited: edited})
			}
		default:
			// More than one row has the key: it is not a key, and which of
			// them was read cannot be said. Nothing is claimed about it.
			continue
		}
		if d.Gone || len(d.Columns) > 0 {
			out = append(out, d)
		}
	}
	return out, unchecked, nil
}

// sameCell is sameValue, with not-a-number the same as itself: a cell that
// held NaN and holds NaN has not changed.
func sameCell(a, b any) bool {
	if x, ok := a.(float64); ok && math.IsNaN(x) {
		y, ok := b.(float64)
		return ok && math.IsNaN(y)
	}
	return sameValue(a, b)
}

// ReadByKey is how a table's rows are read again for Drift: the rows the
// conditions find, with the columns this browse reads, whatever filter and
// order it has of its own. Two are asked for, so that a key that is not one
// shows as two.
func (b *BrowseSource) ReadByKey(ctx context.Context, key []source.Filter) (_ []model.Row, err error) {
	defer panics.Recover(&err, "reading a row again")
	rs, err := b.src.Browse(ctx, b.ref, source.BrowseOptions{Columns: b.opt.Columns, Filters: key, Limit: 2})
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []model.Row
	for len(out) < 2 {
		row, err := rs.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, nil
}
