package app

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// A table's indexes, designed beside its columns and its keys (FR-6.3,
// ADR-0114).
//
// An index is not a constraint, but it is edited the same way and for the
// same reasons: it is made of the table's columns, the engine knows it by
// its name, and one changed in place is one change rather than a drop and a
// rebuild — which for an index is the difference between a moment and an
// afternoon.
//
// This is the designer's half of indexes. The other half, in indexes.go, is
// making and unmaking a document store's indexes against a live source, and
// the two do not meet: one edits a table on paper, the other sends.

// IndexChange is one difference in a table's indexes.
type IndexChange struct {
	// Change is what happened, in the same vocabulary as a column's.
	Change ColumnChangeKind

	// Name is the index's name as the engine knows it, or "" where the
	// engine named it.
	Name string

	Was model.Index
	Now model.Index
}

// Indexes are the table's indexes as they now stand.
func (d *Design) Indexes() []model.Index { return copyIndexes(d.to.Indexes) }

// AddIndex adds an index.
func (d *Design) AddIndex(idx model.Index) error {
	clean, err := d.checkIndex(idx)
	if err != nil {
		return err
	}
	if err := d.freeName(clean.Name); err != nil {
		return err
	}
	d.to.Indexes = append(d.to.Indexes, clean)
	return nil
}

// ChangeIndex replaces the index called name.
//
// In place, for the reason a constraint is: one that fails its checks must
// leave the one that was there, and an index is expensive enough that
// dropping one by accident is felt.
func (d *Design) ChangeIndex(name string, idx model.Index) error {
	at := d.indexNamed(name)
	if at < 0 {
		return fmt.Errorf("this table has no index called %q", name)
	}
	clean, err := d.checkIndex(idx)
	if err != nil {
		return err
	}
	if !strings.EqualFold(clean.Name, name) {
		if err := d.freeName(clean.Name); err != nil {
			return err
		}
	}
	d.to.Indexes[at] = clean
	return nil
}

// DropIndex removes the index called name.
func (d *Design) DropIndex(name string) error {
	at := d.indexNamed(name)
	if at < 0 {
		return fmt.Errorf("this table has no index called %q", name)
	}
	d.to.Indexes = slices.Delete(d.to.Indexes, at, at+1)
	return nil
}

// IndexChanges are the differences in a table's indexes, compared the way
// its constraints are: by name where there is one, and by what they say
// where the engine named them.
func (d *Design) IndexChanges() []IndexChange {
	changes := byName(ConstraintUnique, d.from.Indexes, d.to.Indexes,
		func(i model.Index) string { return i.Name }, sameIndex)
	out := make([]IndexChange, 0, len(changes))
	for _, c := range changes {
		got := IndexChange{Change: c.Change, Name: c.Name}
		if c.Was != nil {
			got.Was = c.Was.(model.Index)
		}
		if c.Now != nil {
			got.Now = c.Now.(model.Index)
		}
		out = append(out, got)
	}
	return out
}

// checkIndex is what can be checked without a server.
//
// An expression is not read, for the reason a check constraint's is not: it
// is the engine's own language. A column named plainly must be one the table
// has, because that is a mistake this can catch and the server would only
// catch later.
func (d *Design) checkIndex(idx model.Index) (model.Index, error) {
	if len(idx.Columns) == 0 {
		return model.Index{}, errors.New("an index needs at least one column")
	}
	cols := make([]model.IndexColumn, 0, len(idx.Columns))
	seen := make([]string, 0, len(idx.Columns))
	for _, c := range idx.Columns {
		if expr := strings.TrimSpace(c.Expression); expr != "" {
			cols = append(cols, model.IndexColumn{Expression: expr, Descending: c.Descending})
			continue
		}
		at := d.index(strings.TrimSpace(c.Name))
		if at < 0 {
			return model.Index{}, fmt.Errorf("an index names %s, which this table has no column called", c.Name)
		}
		name := d.to.Columns[at].Name
		if slices.Contains(seen, name) {
			return model.Index{}, fmt.Errorf("an index names %s twice", name)
		}
		seen = append(seen, name)
		cols = append(cols, model.IndexColumn{Name: name, Descending: c.Descending})
	}

	include := make([]string, 0, len(idx.Include))
	for _, c := range idx.Include {
		at := d.index(strings.TrimSpace(c))
		if at < 0 {
			return model.Index{}, fmt.Errorf("an index includes %s, which this table has no column called", c)
		}
		name := d.to.Columns[at].Name
		if slices.Contains(seen, name) {
			return model.Index{}, fmt.Errorf("%s is both indexed and included, and it can only be one", name)
		}
		include = append(include, name)
	}

	idx.Name = strings.TrimSpace(idx.Name)
	idx.Method = strings.TrimSpace(idx.Method)
	idx.Predicate = strings.TrimSpace(idx.Predicate)
	idx.Columns, idx.Include = cols, include
	if len(include) == 0 {
		idx.Include = nil
	}
	return idx, nil
}

func (d *Design) indexNamed(name string) int {
	return slices.IndexFunc(d.to.Indexes, func(i model.Index) bool {
		return strings.EqualFold(i.Name, name)
	})
}

// sameIndex reports whether two indexes say the same thing. Attrs are not
// compared: they are what an engine said about an index it already has,
// rather than anything somebody typed.
func sameIndex(a, b model.Index) bool {
	return a.Unique == b.Unique && a.Method == b.Method && a.Predicate == b.Predicate &&
		slices.Equal(a.Include, b.Include) &&
		slices.EqualFunc(a.Columns, b.Columns, func(x, y model.IndexColumn) bool {
			return x.Name == y.Name && x.Expression == y.Expression && x.Descending == y.Descending
		})
}

func copyIndexes(in []model.Index) []model.Index {
	out := make([]model.Index, len(in))
	for i, idx := range in {
		idx.Columns = slices.Clone(in[i].Columns)
		idx.Include = slices.Clone(in[i].Include)
		out[i] = idx
	}
	return out
}

// indexed reports whether an index names this column, which is what stops it
// being dropped out from under one.
//
// An index over an expression is not read, for the reason checkIndex gives.
func (d *Design) indexed(name string) (string, bool) {
	for _, idx := range d.to.Indexes {
		for _, c := range idx.Columns {
			if c.Expression == "" && strings.EqualFold(c.Name, name) {
				return describeConstraint("the index", idx.Name), true
			}
		}
		if slices.ContainsFunc(idx.Include, eq(name)) {
			return describeConstraint("the index", idx.Name), true
		}
	}
	return "", false
}
