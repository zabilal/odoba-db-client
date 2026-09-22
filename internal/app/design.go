package app

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// Designing a table's columns (FR-6.1).
//
// A design is the table as it is and the table as somebody wants it, held
// side by side. Nothing here renders DDL or runs anything: what the change
// is rendered as, and the rule that it is seen before it runs, is FR-6.4 and
// belongs with the preview. Keeping the two apart is what lets the editor be
// exercised without a server and the DDL be exercised without a window.
//
// The pair matters more than a list of edits would. A person changing a
// column's type and changing it back has made no change, and a design that
// remembered the two steps would offer to run something for nothing. Asked
// what changed, this compares.

// Design is a table being edited: where it started, and where it is now.
type Design struct {
	// Ref is the table being designed.
	Ref model.ObjectRef

	from *model.Table
	to   *model.Table

	// origin says, for each column as it now stands, which of the original
	// columns it came from, or -1 where it is new.
	//
	// Position cannot answer that. Drop a column in the middle and every
	// column after it moves up one; matched by position, each would read as
	// the column before it renamed — which is how a rename that carries its
	// data becomes a drop and an add that does not.
	origin []int
}

// NewDesign begins designing an existing table. The table is copied, so that
// editing a design never changes what was read from the server.
func NewDesign(ref model.ObjectRef, t *model.Table) *Design {
	if t == nil {
		t = &model.Table{}
	}
	d := &Design{Ref: ref, from: copyTable(t), to: copyTable(t)}
	d.origin = make([]int, len(d.to.Columns))
	for i := range d.origin {
		d.origin[i] = i
	}
	return d
}

// Columns are the columns as they now stand.
func (d *Design) Columns() []model.Column { return copyColumns(d.to.Columns) }

// Original is the table as it was read, for whatever renders the change.
func (d *Design) Original() *model.Table { return copyTable(d.from) }

// Table is the table as it now stands.
func (d *Design) Table() *model.Table { return copyTable(d.to) }

// AddColumn adds a column at the end.
//
// A new column is nullable whatever it was asked to be, where the table has
// rows: a NOT NULL column added to rows that exist needs a value for every
// one of them, and this has none to give. That is a refusal rather than a
// silent change — somebody who wants it not null can say so once it has
// values.
func (d *Design) AddColumn(c model.Column) error {
	name := strings.TrimSpace(c.Name)
	if name == "" {
		return errors.New("a column needs a name")
	}
	if d.index(name) >= 0 {
		return fmt.Errorf("this table already has a column called %q", name)
	}
	if c.Type.Class == model.TypeUnknown && strings.TrimSpace(c.Type.Native) == "" {
		return fmt.Errorf("%s needs a type", name)
	}
	c.Name = name
	c.Position = len(d.to.Columns)
	if !c.Type.Nullable && !c.HasDefault && d.hasRows() {
		return fmt.Errorf("%s cannot be added as NOT NULL to a table that already has rows, "+
			"because every row would need a value for it: add it nullable, or give it a default", name)
	}
	d.to.Columns = append(d.to.Columns, c)
	d.origin = append(d.origin, -1)
	return nil
}

// ChangeColumn replaces the column called name.
//
// Renaming is done here too, by giving the column a different name: a rename
// and a retype are one act to whoever is typing, and telling them apart is
// the business of whatever renders the change.
func (d *Design) ChangeColumn(name string, to model.Column) error {
	at := d.index(name)
	if at < 0 {
		return fmt.Errorf("this table has no column called %q", name)
	}
	newName := strings.TrimSpace(to.Name)
	if newName == "" {
		return errors.New("a column needs a name")
	}
	if other := d.index(newName); other >= 0 && other != at {
		return fmt.Errorf("this table already has a column called %q", newName)
	}
	if to.Type.Class == model.TypeUnknown && strings.TrimSpace(to.Type.Native) == "" {
		return fmt.Errorf("%s needs a type", newName)
	}
	to.Name, to.Position = newName, at
	d.to.Columns[at] = to
	return nil
}

// DropColumn removes the column called name.
//
// A column the primary key is made of is refused: dropping it would take the
// key with it, which is a larger change than the one being asked for and is
// T3.2's to offer.
func (d *Design) DropColumn(name string) error {
	at := d.index(name)
	if at < 0 {
		return fmt.Errorf("this table has no column called %q", name)
	}
	if what, held := d.keyed(d.to.Columns[at].Name); held {
		return fmt.Errorf("%s is part of %s, so dropping it would drop that too; change it first",
			name, what)
	}
	d.to.Columns = slices.Delete(d.to.Columns, at, at+1)
	d.origin = slices.Delete(d.origin, at, at+1)
	for i := range d.to.Columns {
		d.to.Columns[i].Position = i
	}
	return nil
}

// Revert puts a column back as it was read, and answers whether there was
// anything to put back. A column that was added is removed by it.
func (d *Design) Revert(name string) bool {
	at := d.index(name)
	if at < 0 {
		return false
	}
	was := d.origin[at]
	if was < 0 {
		// It was never read: putting it back means taking it away. The
		// primary key cannot object, because a column added since is not in
		// one.
		d.to.Columns = slices.Delete(d.to.Columns, at, at+1)
		d.origin = slices.Delete(d.origin, at, at+1)
		for i := range d.to.Columns {
			d.to.Columns[i].Position = i
		}
		return true
	}
	if sameColumn(d.to.Columns[at], d.from.Columns[was]) {
		return false
	}
	d.to.Columns[at] = copyColumn(d.from.Columns[was])
	d.to.Columns[at].Position = at
	return true
}

// RevertAll puts every column back as it was read.
func (d *Design) RevertAll() {
	d.to = copyTable(d.from)
	d.origin = make([]int, len(d.to.Columns))
	for i := range d.origin {
		d.origin[i] = i
	}
}

// Changed reports whether anything is different from what was read: a
// column, or a key or constraint on them.
func (d *Design) Changed() bool {
	return len(d.Changes()) > 0 || len(d.ConstraintChanges()) > 0
}

// ChangeKind says what happened to one column.
type ColumnChangeKind uint8

const (
	ColumnAdded ColumnChangeKind = iota
	ColumnDropped
	ColumnAltered
)

var columnChangeNames = map[ColumnChangeKind]string{
	ColumnAdded:   "added",
	ColumnDropped: "dropped",
	ColumnAltered: "changed",
}

func (k ColumnChangeKind) String() string { return columnChangeNames[k] }

// ColumnChange is one difference between the table as it was and as it is.
type ColumnChange struct {
	Kind ColumnChangeKind

	// Name is the column's name as it was, for a change or a drop, and as it
	// will be for an addition.
	Name string

	// From and To are the column before and after. From is zero for an
	// addition and To is zero for a drop.
	From model.Column
	To   model.Column
}

// Renamed reports a change that gives the column a different name.
func (c ColumnChange) Renamed() bool {
	return c.Kind == ColumnAltered && !strings.EqualFold(c.From.Name, c.To.Name)
}

// Changes are the differences between the table as it was read and as it now
// stands, in the order somebody would read them: what went, what changed,
// what arrived.
//
// A column edited back to what it was is not a change. That is the point of
// holding two tables rather than a list of edits.
func (d *Design) Changes() []ColumnChange {
	var out []ColumnChange
	// Gone: an original column that nothing now comes from.
	for i, was := range d.from.Columns {
		if !slices.Contains(d.origin, i) {
			out = append(out, ColumnChange{Kind: ColumnDropped, Name: was.Name, From: was})
		}
	}
	// Changed, and arrived, in the order they now stand.
	for i, now := range d.to.Columns {
		from := d.origin[i]
		if from < 0 {
			out = append(out, ColumnChange{Kind: ColumnAdded, Name: now.Name, To: now})
			continue
		}
		if was := d.from.Columns[from]; !sameColumn(was, now) {
			out = append(out, ColumnChange{Kind: ColumnAltered, Name: was.Name, From: was, To: now})
		}
	}
	return out
}

func (d *Design) index(name string) int {
	for i := range d.to.Columns {
		if strings.EqualFold(d.to.Columns[i].Name, name) {
			return i
		}
	}
	return -1
}

// hasRows reports whether the table is known to hold any. An estimate of -1
// is not known, and an unknown table is treated as holding rows: refusing a
// NOT NULL column that would have been fine is a question somebody can
// answer, and adding one that fails on the server is an error they cannot.
func (d *Design) hasRows() bool { return d.from.RowsEstimate != 0 }

// sameColumn reports whether two columns say the same thing. Position is not
// part of it: a column that moved because one before it went is not changed.
func sameColumn(a, b model.Column) bool {
	return a.Name == b.Name &&
		sameType(a.Type, b.Type) &&
		a.Default == b.Default && a.HasDefault == b.HasDefault &&
		a.Identity == b.Identity && a.AutoIncrement == b.AutoIncrement &&
		a.Generated == b.Generated && a.Comment == b.Comment
}

// sameType compares what a person can edit. Class and Native are both here
// because a type can be changed by name without its class moving — varchar
// to text is a real change and both are strings.
func sameType(a, b model.DataType) bool {
	return a.Class == b.Class && a.Native == b.Native && a.Nullable == b.Nullable &&
		a.Length == b.Length && a.Precision == b.Precision && a.Scale == b.Scale &&
		a.TimeZone == b.TimeZone
}

func copyTable(t *model.Table) *model.Table {
	out := *t
	out.Columns = copyColumns(t.Columns)
	if t.PrimaryKey != nil {
		pk := *t.PrimaryKey
		pk.Columns = slices.Clone(t.PrimaryKey.Columns)
		out.PrimaryKey = &pk
	}
	// Every constraint is copied too, and its columns with it. A slice
	// shared with the table that was read is a design that edits it, which
	// is the defect the columns already had a test for.
	out.Uniques = make([]model.UniqueConstraint, len(t.Uniques))
	for i, u := range t.Uniques {
		u.Columns = slices.Clone(u.Columns)
		out.Uniques[i] = u
	}
	out.ForeignKeys = make([]model.ForeignKey, len(t.ForeignKeys))
	for i, f := range t.ForeignKeys {
		f.Columns, f.RefColumns = slices.Clone(f.Columns), slices.Clone(f.RefColumns)
		out.ForeignKeys[i] = f
	}
	out.Checks = slices.Clone(t.Checks)
	return &out
}

func copyColumns(cols []model.Column) []model.Column {
	out := make([]model.Column, len(cols))
	for i, c := range cols {
		out[i] = copyColumn(c)
	}
	return out
}

func copyColumn(c model.Column) model.Column {
	if c.Attrs != nil {
		attrs := make(map[string]string, len(c.Attrs))
		for k, v := range c.Attrs {
			attrs[k] = v
		}
		c.Attrs = attrs
	}
	if c.Type.Element != nil {
		el := *c.Type.Element
		c.Type.Element = &el
	}
	return c
}
