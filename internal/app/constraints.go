package app

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// A table's keys and constraints, designed on paper beside its columns
// (FR-6.2, ADR-0114).
//
// They go on the same design for the reason a table is one thing: a key is
// made of columns, a column cannot be dropped out from under a key, and
// somebody renaming a column expects the key that names it to follow. Held
// apart, the two would have to be reconciled at the end, which is where the
// mistakes live.

// ConstraintKind says which sort of constraint changed.
type ConstraintKind uint8

const (
	ConstraintPrimaryKey ConstraintKind = iota
	ConstraintUnique
	ConstraintForeignKey
	ConstraintCheck
)

var constraintKindNames = map[ConstraintKind]string{
	ConstraintPrimaryKey: "primary key",
	ConstraintUnique:     "unique constraint",
	ConstraintForeignKey: "foreign key",
	ConstraintCheck:      "check constraint",
}

func (k ConstraintKind) String() string { return constraintKindNames[k] }

// ConstraintChange is one difference in a table's keys and constraints.
type ConstraintChange struct {
	Kind ConstraintKind

	// Change is what happened, reusing the column vocabulary: a constraint
	// is added, dropped, or altered.
	Change ColumnChangeKind

	// Name is the constraint's name, or "" where the engine names it.
	Name string

	// Was and Now are the constraint before and after, as the concrete type
	// for Kind: *model.PrimaryKey, model.UniqueConstraint, model.ForeignKey
	// or model.CheckConstraint. The one that does not apply is nil.
	Was any
	Now any
}

// SetPrimaryKey replaces the table's primary key.
//
// Its columns must be columns the table has as it now stands, which is what
// makes renaming a column and then keying on its new name work, and keying
// on a name nobody has a refusal rather than a statement the server rejects.
func (d *Design) SetPrimaryKey(name string, columns []string) error {
	cols, err := d.knownColumns(columns, "a primary key")
	if err != nil {
		return err
	}
	for _, c := range cols {
		if at := d.index(c); at >= 0 && d.to.Columns[at].Type.Nullable {
			return fmt.Errorf("%s is nullable, and a primary key's columns cannot be: "+
				"clear its Nullable box first", c)
		}
	}
	d.to.PrimaryKey = &model.PrimaryKey{Name: strings.TrimSpace(name), Columns: cols}
	return nil
}

// DropPrimaryKey removes the table's primary key, and answers whether there
// was one.
func (d *Design) DropPrimaryKey() bool {
	if d.to.PrimaryKey == nil {
		return false
	}
	d.to.PrimaryKey = nil
	return true
}

// AddUnique adds a uniqueness constraint.
func (d *Design) AddUnique(u model.UniqueConstraint) error {
	cols, err := d.knownColumns(u.Columns, "a unique constraint")
	if err != nil {
		return err
	}
	u.Name = strings.TrimSpace(u.Name)
	if err := d.freeName(u.Name); err != nil {
		return err
	}
	u.Columns = cols
	d.to.Uniques = append(d.to.Uniques, u)
	return nil
}

// DropUnique removes the uniqueness constraint called name.
func (d *Design) DropUnique(name string) error {
	at := slices.IndexFunc(d.to.Uniques, func(u model.UniqueConstraint) bool {
		return strings.EqualFold(u.Name, name)
	})
	if at < 0 {
		return fmt.Errorf("this table has no unique constraint called %q", name)
	}
	d.to.Uniques = slices.Delete(d.to.Uniques, at, at+1)
	return nil
}

// AddForeignKey adds a referential constraint.
//
// The table it refers to is not checked against the server: a design is
// edited without one, and a key to a table that is not there is a refusal
// the server gives with an error naming exactly what is missing. What is
// checked is what can be: the columns are this table's, there are as many of
// them as there are columns referred to, and the actions are actions.
func (d *Design) AddForeignKey(fk model.ForeignKey) error {
	cols, err := d.knownColumns(fk.Columns, "a foreign key")
	if err != nil {
		return err
	}
	fk.Name = strings.TrimSpace(fk.Name)
	if err := d.freeName(fk.Name); err != nil {
		return err
	}
	if strings.TrimSpace(fk.RefTable) == "" {
		return errors.New("a foreign key needs a table to refer to")
	}
	if len(fk.RefColumns) != len(cols) {
		return fmt.Errorf("this key has %d columns and refers to %d, and those must match",
			len(cols), len(fk.RefColumns))
	}
	for _, a := range []model.ReferentialAction{fk.OnDelete, fk.OnUpdate} {
		if err := knownAction(a); err != nil {
			return err
		}
	}
	fk.Columns = cols
	d.to.ForeignKeys = append(d.to.ForeignKeys, fk)
	return nil
}

// DropForeignKey removes the foreign key called name.
func (d *Design) DropForeignKey(name string) error {
	at := slices.IndexFunc(d.to.ForeignKeys, func(fk model.ForeignKey) bool {
		return strings.EqualFold(fk.Name, name)
	})
	if at < 0 {
		return fmt.Errorf("this table has no foreign key called %q", name)
	}
	d.to.ForeignKeys = slices.Delete(d.to.ForeignKeys, at, at+1)
	return nil
}

// AddCheck adds a row predicate.
//
// The expression is not read. A check is the engine's own language — its
// functions, its operators, its casts — and a parser here would be seven
// parsers, each of them wrong somewhere. What it does check is that there is
// one: an empty check is a statement that cannot run.
func (d *Design) AddCheck(c model.CheckConstraint) error {
	c.Name, c.Expression = strings.TrimSpace(c.Name), strings.TrimSpace(c.Expression)
	if c.Expression == "" {
		return errors.New("a check constraint needs an expression")
	}
	if err := d.freeName(c.Name); err != nil {
		return err
	}
	d.to.Checks = append(d.to.Checks, c)
	return nil
}

// DropCheck removes the check called name.
func (d *Design) DropCheck(name string) error {
	at := slices.IndexFunc(d.to.Checks, func(c model.CheckConstraint) bool {
		return strings.EqualFold(c.Name, name)
	})
	if at < 0 {
		return fmt.Errorf("this table has no check constraint called %q", name)
	}
	d.to.Checks = slices.Delete(d.to.Checks, at, at+1)
	return nil
}

// ChangeUnique replaces the uniqueness constraint called name.
//
// Replacing in place rather than dropping and adding matters: a constraint
// that fails its checks must leave the one that was there, and a drop
// followed by a failed add would leave the table without it.
func (d *Design) ChangeUnique(name string, u model.UniqueConstraint) error {
	at := slices.IndexFunc(d.to.Uniques, func(x model.UniqueConstraint) bool {
		return strings.EqualFold(x.Name, name)
	})
	if at < 0 {
		return fmt.Errorf("this table has no unique constraint called %q", name)
	}
	cols, err := d.knownColumns(u.Columns, "a unique constraint")
	if err != nil {
		return err
	}
	u.Name = strings.TrimSpace(u.Name)
	if !strings.EqualFold(u.Name, name) {
		if err := d.freeName(u.Name); err != nil {
			return err
		}
	}
	u.Columns = cols
	d.to.Uniques[at] = u
	return nil
}

// ChangeForeignKey replaces the foreign key called name.
func (d *Design) ChangeForeignKey(name string, fk model.ForeignKey) error {
	at := slices.IndexFunc(d.to.ForeignKeys, func(x model.ForeignKey) bool {
		return strings.EqualFold(x.Name, name)
	})
	if at < 0 {
		return fmt.Errorf("this table has no foreign key called %q", name)
	}
	cols, err := d.knownColumns(fk.Columns, "a foreign key")
	if err != nil {
		return err
	}
	fk.Name = strings.TrimSpace(fk.Name)
	if !strings.EqualFold(fk.Name, name) {
		if err := d.freeName(fk.Name); err != nil {
			return err
		}
	}
	if strings.TrimSpace(fk.RefTable) == "" {
		return errors.New("a foreign key needs a table to refer to")
	}
	if len(fk.RefColumns) != len(cols) {
		return fmt.Errorf("this key has %d columns and refers to %d, and those must match",
			len(cols), len(fk.RefColumns))
	}
	for _, a := range []model.ReferentialAction{fk.OnDelete, fk.OnUpdate} {
		if err := knownAction(a); err != nil {
			return err
		}
	}
	fk.Columns = cols
	d.to.ForeignKeys[at] = fk
	return nil
}

// ChangeCheck replaces the check constraint called name.
func (d *Design) ChangeCheck(name string, c model.CheckConstraint) error {
	at := slices.IndexFunc(d.to.Checks, func(x model.CheckConstraint) bool {
		return strings.EqualFold(x.Name, name)
	})
	if at < 0 {
		return fmt.Errorf("this table has no check constraint called %q", name)
	}
	c.Name, c.Expression = strings.TrimSpace(c.Name), strings.TrimSpace(c.Expression)
	if c.Expression == "" {
		return errors.New("a check constraint needs an expression")
	}
	if !strings.EqualFold(c.Name, name) {
		if err := d.freeName(c.Name); err != nil {
			return err
		}
	}
	d.to.Checks[at] = c
	return nil
}

// Constraints are the table's keys and constraints as they now stand.
func (d *Design) Constraints() (*model.PrimaryKey, []model.UniqueConstraint, []model.ForeignKey, []model.CheckConstraint) {
	t := copyTable(d.to)
	return t.PrimaryKey, t.Uniques, t.ForeignKeys, t.Checks
}

// ConstraintChanges are the differences in a table's keys and constraints,
// compared the way its columns are: what it was read as, against what it now
// says.
func (d *Design) ConstraintChanges() []ConstraintChange {
	var out []ConstraintChange

	if was, now := d.from.PrimaryKey, d.to.PrimaryKey; !samePrimaryKey(was, now) {
		switch {
		case was == nil:
			out = append(out, ConstraintChange{Kind: ConstraintPrimaryKey, Change: ColumnAdded,
				Name: now.Name, Now: now})
		case now == nil:
			out = append(out, ConstraintChange{Kind: ConstraintPrimaryKey, Change: ColumnDropped,
				Name: was.Name, Was: was})
		default:
			out = append(out, ConstraintChange{Kind: ConstraintPrimaryKey, Change: ColumnAltered,
				Name: was.Name, Was: was, Now: now})
		}
	}

	out = append(out, byName(ConstraintUnique, d.from.Uniques, d.to.Uniques,
		func(u model.UniqueConstraint) string { return u.Name },
		func(a, b model.UniqueConstraint) bool { return slices.Equal(a.Columns, b.Columns) })...)

	out = append(out, byName(ConstraintForeignKey, d.from.ForeignKeys, d.to.ForeignKeys,
		func(f model.ForeignKey) string { return f.Name }, sameForeignKey)...)

	out = append(out, byName(ConstraintCheck, d.from.Checks, d.to.Checks,
		func(c model.CheckConstraint) string { return c.Name },
		func(a, b model.CheckConstraint) bool { return a.Expression == b.Expression })...)

	return out
}

// byName compares two lists of named constraints. A constraint is followed
// by its name, because that is what the engine knows it as and what a
// statement dropping it will say; a constraint with no name is one the
// engine named, and is compared by what it says instead.
func byName[T any](kind ConstraintKind, was, now []T, name func(T) string, same func(a, b T) bool) []ConstraintChange {
	var out []ConstraintChange
	matched := make([]bool, len(now))

	for _, w := range was {
		at := -1
		for i, n := range now {
			if matched[i] {
				continue
			}
			if named := name(w); named != "" && strings.EqualFold(named, name(n)) {
				at = i
				break
			}
			if name(w) == "" && name(n) == "" && same(w, n) {
				at = i
				break
			}
		}
		if at < 0 {
			out = append(out, ConstraintChange{Kind: kind, Change: ColumnDropped, Name: name(w), Was: w})
			continue
		}
		matched[at] = true
		if !same(w, now[at]) {
			out = append(out, ConstraintChange{Kind: kind, Change: ColumnAltered,
				Name: name(w), Was: w, Now: now[at]})
		}
	}
	for i, n := range now {
		if !matched[i] {
			out = append(out, ConstraintChange{Kind: kind, Change: ColumnAdded, Name: name(n), Now: n})
		}
	}
	return out
}

// knownColumns checks that every name is a column this table has as it now
// stands, and answers them as the table spells them.
func knownColumnsErr(what string) error { return fmt.Errorf("%s needs at least one column", what) }

func (d *Design) knownColumns(names []string, what string) ([]string, error) {
	if len(names) == 0 {
		return nil, knownColumnsErr(what)
	}
	out := make([]string, 0, len(names))
	for _, n := range names {
		at := d.index(strings.TrimSpace(n))
		if at < 0 {
			return nil, fmt.Errorf("%s names %s, which this table has no column called", what, n)
		}
		if slices.Contains(out, d.to.Columns[at].Name) {
			return nil, fmt.Errorf("%s names %s twice", what, d.to.Columns[at].Name)
		}
		out = append(out, d.to.Columns[at].Name)
	}
	return out, nil
}

// freeName refuses a name another constraint already has. An empty name is
// always free: it means the engine will choose one, and it will choose a
// different one each time.
func (d *Design) freeName(name string) error {
	if name == "" {
		return nil
	}
	taken := func(n string) bool { return strings.EqualFold(n, name) }
	switch {
	case d.to.PrimaryKey != nil && taken(d.to.PrimaryKey.Name),
		slices.ContainsFunc(d.to.Indexes, func(i model.Index) bool { return taken(i.Name) }),
		slices.ContainsFunc(d.to.Uniques, func(u model.UniqueConstraint) bool { return taken(u.Name) }),
		slices.ContainsFunc(d.to.ForeignKeys, func(f model.ForeignKey) bool { return taken(f.Name) }),
		slices.ContainsFunc(d.to.Checks, func(c model.CheckConstraint) bool { return taken(c.Name) }):
		return fmt.Errorf("this table already has a constraint called %q", name)
	}
	return nil
}

// knownAction refuses anything that is not one of the five the standard
// names, empty meaning the engine's own default.
func knownAction(a model.ReferentialAction) error {
	switch a {
	case "", model.ActionNoAction, model.ActionRestrict, model.ActionCascade,
		model.ActionSetNull, model.ActionSetDefault:
		return nil
	}
	return fmt.Errorf("%q is not something a foreign key can do: it is NO ACTION, RESTRICT, CASCADE, "+
		"SET NULL or SET DEFAULT", a)
}

func samePrimaryKey(a, b *model.PrimaryKey) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	}
	return a.Name == b.Name && slices.Equal(a.Columns, b.Columns)
}

func sameForeignKey(a, b model.ForeignKey) bool {
	return slices.Equal(a.Columns, b.Columns) &&
		a.RefSchema == b.RefSchema && a.RefTable == b.RefTable &&
		slices.Equal(a.RefColumns, b.RefColumns) &&
		a.OnDelete == b.OnDelete && a.OnUpdate == b.OnUpdate
}

// keyed reports whether any key or constraint names this column, which is
// what stops it being dropped out from under one.
//
// A check's expression is not read, for the reason AddCheck gives. A check
// that names a dropped column is the server's to refuse, with its own
// message about its own expression.
func (d *Design) held(name string) (string, bool) {
	if what, ok := d.keyed(name); ok {
		return what, true
	}
	return d.indexed(name)
}

func (d *Design) keyed(name string) (string, bool) {
	if d.to.PrimaryKey != nil && slices.ContainsFunc(d.to.PrimaryKey.Columns, eq(name)) {
		return "this table's primary key", true
	}
	for _, u := range d.to.Uniques {
		if slices.ContainsFunc(u.Columns, eq(name)) {
			return describeConstraint("the unique constraint", u.Name), true
		}
	}
	for _, f := range d.to.ForeignKeys {
		if slices.ContainsFunc(f.Columns, eq(name)) {
			return describeConstraint("the foreign key", f.Name), true
		}
	}
	return "", false
}

func eq(name string) func(string) bool {
	return func(s string) bool { return strings.EqualFold(s, name) }
}

func describeConstraint(what, name string) string {
	if name == "" {
		return what + " on it"
	}
	return what + " " + name
}
