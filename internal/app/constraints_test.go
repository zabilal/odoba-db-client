package app

import (
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// keyed is the fixture table with a unique, a foreign key and a check on it
// as well as its primary key.
func keyed() *Design {
	d := designed(0)
	t := d.Table()
	t.Uniques = []model.UniqueConstraint{{Name: "people_name_key", Columns: []string{"name"}}}
	t.ForeignKeys = []model.ForeignKey{{Name: "people_note_fk", Columns: []string{"note"},
		RefSchema: "public", RefTable: "notes", RefColumns: []string{"id"},
		OnDelete: model.ActionCascade}}
	t.Checks = []model.CheckConstraint{{Name: "people_name_len", Expression: "length(name) > 0"}}
	return NewDesign(d.Ref, t)
}

func TestAKeyIsChangedAndSaidSo(t *testing.T) {
	d := keyed()
	if d.Changed() {
		t.Fatal("a design begins changed")
	}
	// A key's columns cannot be nullable, so widening the key means saying
	// so about the column first. That is two acts, and the second is
	// refused until the first has happened.
	if err := d.SetPrimaryKey("people_pkey", []string{"id", "name"}); err == nil {
		t.Fatal("a nullable column was keyed on")
	}
	notNull := d.Columns()[1]
	notNull.Type.Nullable = false
	if err := d.ChangeColumn("name", notNull); err != nil {
		t.Fatal(err)
	}
	if err := d.SetPrimaryKey("people_pkey", []string{"id", "name"}); err != nil {
		t.Fatalf("keying on two columns: %v", err)
	}
	changes := d.ConstraintChanges()
	if len(changes) != 1 || changes[0].Kind != ConstraintPrimaryKey || changes[0].Change != ColumnAltered {
		t.Fatalf("it reads %+v", changes)
	}
	if !d.Changed() {
		t.Error("a design with a changed key says nothing changed")
	}

	// And dropping it is a change of its own.
	d2 := keyed()
	if !d2.DropPrimaryKey() {
		t.Error("dropping a key that was there did nothing")
	}
	if got := d2.ConstraintChanges(); len(got) != 1 || got[0].Change != ColumnDropped {
		t.Errorf("dropping a key reads as %+v", got)
	}
	if d2.DropPrimaryKey() {
		t.Error("dropping a key twice claimed to do something")
	}
}

// A key names columns as the table now spells them, so renaming a column and
// then keying on its new name works, and keying on a name nobody has is a
// refusal here rather than an error from the server.
func TestAKeyFollowsTheColumnsTheTableNowHas(t *testing.T) {
	d := keyed()
	renamed := d.Columns()[0]
	renamed.Name = "identifier"
	if err := d.ChangeColumn("id", renamed); err != nil {
		t.Fatal(err)
	}
	if err := d.SetPrimaryKey("people_pkey", []string{"identifier"}); err != nil {
		t.Fatalf("keying on the new name: %v", err)
	}
	err := d.SetPrimaryKey("people_pkey", []string{"id"})
	if err == nil || !strings.Contains(err.Error(), "no column called") {
		t.Errorf("keying on the old name said %v", err)
	}
}

func TestAddingAndDroppingTheOtherThree(t *testing.T) {
	d := keyed()
	if err := d.AddUnique(model.UniqueConstraint{Name: "people_note_key", Columns: []string{"note"}}); err != nil {
		t.Fatal(err)
	}
	if err := d.AddCheck(model.CheckConstraint{Name: "people_note_len", Expression: "length(note) < 100"}); err != nil {
		t.Fatal(err)
	}
	if err := d.AddForeignKey(model.ForeignKey{Name: "people_name_fk", Columns: []string{"name"},
		RefTable: "names", RefColumns: []string{"name"}, OnUpdate: model.ActionRestrict}); err != nil {
		t.Fatal(err)
	}
	if got := len(d.ConstraintChanges()); got != 3 {
		t.Fatalf("three constraints added read as %d changes", got)
	}
	for _, c := range d.ConstraintChanges() {
		if c.Change != ColumnAdded {
			t.Errorf("%s reads as %s", c.Name, c.Change)
		}
	}

	// And dropping the ones that were read.
	if err := d.DropUnique("people_name_key"); err != nil {
		t.Fatal(err)
	}
	if err := d.DropForeignKey("people_note_fk"); err != nil {
		t.Fatal(err)
	}
	if err := d.DropCheck("people_name_len"); err != nil {
		t.Fatal(err)
	}
	dropped := 0
	for _, c := range d.ConstraintChanges() {
		if c.Change == ColumnDropped {
			dropped++
		}
	}
	if dropped != 3 {
		t.Errorf("three constraints dropped read as %d drops", dropped)
	}
}

// A constraint is followed by its name, which is what the engine knows it as
// and what a statement dropping it will say. Changed in place, it is one
// change and not a drop and an add.
func TestAConstraintChangedInPlaceIsOneChange(t *testing.T) {
	d := keyed()
	if err := d.DropForeignKey("people_note_fk"); err != nil {
		t.Fatal(err)
	}
	if err := d.AddForeignKey(model.ForeignKey{Name: "people_note_fk", Columns: []string{"note"},
		RefSchema: "public", RefTable: "notes", RefColumns: []string{"id"},
		OnDelete: model.ActionSetNull}); err != nil {
		t.Fatal(err)
	}
	changes := d.ConstraintChanges()
	if len(changes) != 1 || changes[0].Change != ColumnAltered {
		t.Fatalf("it reads %+v", changes)
	}
	was, now := changes[0].Was.(model.ForeignKey), changes[0].Now.(model.ForeignKey)
	if was.OnDelete != model.ActionCascade || now.OnDelete != model.ActionSetNull {
		t.Errorf("it went from %q to %q", was.OnDelete, now.OnDelete)
	}
}

func TestAConstraintPutBackIsNotAChange(t *testing.T) {
	d := keyed()
	if err := d.DropUnique("people_name_key"); err != nil {
		t.Fatal(err)
	}
	if !d.Changed() {
		t.Fatal("dropping a constraint changed nothing")
	}
	if err := d.AddUnique(model.UniqueConstraint{Name: "people_name_key", Columns: []string{"name"}}); err != nil {
		t.Fatal(err)
	}
	if d.Changed() {
		t.Errorf("a constraint dropped and put back reads as %+v", d.ConstraintChanges())
	}
	d.RevertAll()
	if d.Changed() {
		t.Errorf("after reverting: %+v", d.ConstraintChanges())
	}
}

// A design must not edit the table it was given, constraints included.
func TestEditingConstraintsLeavesWhatWasReadAlone(t *testing.T) {
	read := &model.Table{
		Columns:     []model.Column{text("name")},
		Uniques:     []model.UniqueConstraint{{Name: "u", Columns: []string{"name"}}},
		ForeignKeys: []model.ForeignKey{{Name: "f", Columns: []string{"name"}, RefTable: "t", RefColumns: []string{"x"}}},
		Checks:      []model.CheckConstraint{{Name: "c", Expression: "true"}},
	}
	d := NewDesign(model.NewRef(model.KindTable, "public", "t"), read)
	if err := d.DropUnique("u"); err != nil {
		t.Fatal(err)
	}
	if err := d.DropForeignKey("f"); err != nil {
		t.Fatal(err)
	}
	if err := d.DropCheck("c"); err != nil {
		t.Fatal(err)
	}
	if len(read.Uniques) != 1 || len(read.ForeignKeys) != 1 || len(read.Checks) != 1 {
		t.Errorf("the table that was read now holds %d uniques, %d keys, %d checks",
			len(read.Uniques), len(read.ForeignKeys), len(read.Checks))
	}
	// And its key's columns are its own, not the design's.
	if err := d.AddUnique(model.UniqueConstraint{Name: "u2", Columns: []string{"name"}}); err != nil {
		t.Fatal(err)
	}
	if read.Uniques[0].Name != "u" {
		t.Errorf("the table that was read now calls its unique %q", read.Uniques[0].Name)
	}

	// Writing into one, rather than deleting from it, is what a shared array
	// shows: a delete leaves the original's length alone and hides it.
	again := NewDesign(model.NewRef(model.KindTable, "public", "t"), read)
	if err := again.ChangeCheck("c", model.CheckConstraint{Name: "c", Expression: "false"}); err != nil {
		t.Fatal(err)
	}
	if read.Checks[0].Expression != "true" {
		t.Errorf("the table that was read now checks %q", read.Checks[0].Expression)
	}
	if err := again.ChangeUnique("u", model.UniqueConstraint{Name: "u", Columns: []string{"name"}}); err != nil {
		t.Fatal(err)
	}
	if read.Uniques[0].Name != "u" {
		t.Errorf("the table that was read now calls its unique %q", read.Uniques[0].Name)
	}
}

// A column no constraint names can go; one that a key is made of cannot,
// because dropping it would take the key with it.
func TestAColumnAConstraintNamesCannotBeDropped(t *testing.T) {
	for _, c := range []struct{ column, says string }{
		{"id", "this table's primary key"},
		{"name", "the unique constraint people_name_key"},
		{"note", "the foreign key people_note_fk"},
	} {
		t.Run(c.column, func(t *testing.T) {
			d := keyed()
			err := d.DropColumn(c.column)
			if err == nil {
				t.Fatal("it was dropped")
			}
			if !strings.Contains(err.Error(), c.says) {
				t.Errorf("it said %v, wanted something about %q", err, c.says)
			}
		})
	}

	// Once the constraint is gone, the column can go too.
	d := keyed()
	if err := d.DropUnique("people_name_key"); err != nil {
		t.Fatal(err)
	}
	if err := d.DropColumn("name"); err != nil {
		t.Errorf("a column nothing names any more was refused: %v", err)
	}
}

// A check's expression is the engine's own language and is not read here, so
// a check naming a dropped column is the server's to refuse.
func TestACheckDoesNotHoldAColumnDown(t *testing.T) {
	d := keyed()
	if err := d.DropUnique("people_name_key"); err != nil {
		t.Fatal(err)
	}
	if err := d.DropColumn("name"); err != nil {
		t.Errorf("a column only a check's text names was refused: %v", err)
	}
}

func TestWhatAConstraintWillNotBe(t *testing.T) {
	for _, c := range []struct {
		what string
		do   func(*Design) error
		says string
	}{
		{"a key on no columns", func(d *Design) error {
			return d.SetPrimaryKey("k", nil)
		}, "needs at least one column"},
		{"a key on a column nobody has", func(d *Design) error {
			return d.SetPrimaryKey("k", []string{"nobody"})
		}, "no column called"},
		{"a key on the same column twice", func(d *Design) error {
			return d.SetPrimaryKey("k", []string{"id", "id"})
		}, "twice"},
		{"a key on a nullable column", func(d *Design) error {
			return d.SetPrimaryKey("k", []string{"note"})
		}, "cannot be"},
		{"a unique on a column nobody has", func(d *Design) error {
			return d.AddUnique(model.UniqueConstraint{Columns: []string{"nobody"}})
		}, "no column called"},
		{"a name another constraint has", func(d *Design) error {
			return d.AddUnique(model.UniqueConstraint{Name: "people_name_len", Columns: []string{"name"}})
		}, "already has a constraint"},
		{"a foreign key to nowhere", func(d *Design) error {
			return d.AddForeignKey(model.ForeignKey{Columns: []string{"name"}, RefColumns: []string{"x"}})
		}, "needs a table to refer to"},
		{"a foreign key of mismatched width", func(d *Design) error {
			return d.AddForeignKey(model.ForeignKey{Columns: []string{"name"},
				RefTable: "t", RefColumns: []string{"a", "b"}})
		}, "must match"},
		{"a foreign key that does something impossible", func(d *Design) error {
			return d.AddForeignKey(model.ForeignKey{Columns: []string{"name"}, RefTable: "t",
				RefColumns: []string{"x"}, OnDelete: "EXPLODE"})
		}, "not something a foreign key can do"},
		{"a check with nothing in it", func(d *Design) error {
			return d.AddCheck(model.CheckConstraint{Name: "c", Expression: "  "})
		}, "needs an expression"},
		{"dropping a unique that is not there", func(d *Design) error {
			return d.DropUnique("nobody")
		}, "no unique constraint called"},
		{"dropping a foreign key that is not there", func(d *Design) error {
			return d.DropForeignKey("nobody")
		}, "no foreign key called"},
		{"dropping a check that is not there", func(d *Design) error {
			return d.DropCheck("nobody")
		}, "no check constraint called"},
	} {
		t.Run(c.what, func(t *testing.T) {
			d := keyed()
			err := c.do(d)
			if err == nil {
				t.Fatal("it was allowed")
			}
			if !strings.Contains(err.Error(), c.says) {
				t.Errorf("it said %v, wanted something about %q", err, c.says)
			}
			if d.Changed() {
				t.Errorf("a refusal left the design changed: %+v", d.ConstraintChanges())
			}
		})
	}
}

// An engine names a constraint nobody named, and names it differently each
// time, so two unnamed ones are compared by what they say.
func TestConstraintsNobodyNamed(t *testing.T) {
	read := &model.Table{
		Columns: []model.Column{text("a"), text("b")},
		Checks:  []model.CheckConstraint{{Expression: "a is not null"}},
	}
	d := NewDesign(model.NewRef(model.KindTable, "public", "t"), read)
	if d.Changed() {
		t.Fatalf("an unnamed check read as %+v", d.ConstraintChanges())
	}
	// A second unnamed one is an addition, not a change to the first.
	if err := d.AddCheck(model.CheckConstraint{Expression: "b is not null"}); err != nil {
		t.Fatal(err)
	}
	changes := d.ConstraintChanges()
	if len(changes) != 1 || changes[0].Change != ColumnAdded {
		t.Fatalf("it reads %+v", changes)
	}
	// And an unnamed one can be added beside another unnamed one, because
	// there is no name to clash.
	if err := d.AddCheck(model.CheckConstraint{Expression: "a <> b"}); err != nil {
		t.Errorf("a second unnamed check was refused: %v", err)
	}
}

// A constraint is followed by its name and not by where it sits, so adding
// one in front of another does not make every one after it read as changed.
func TestConstraintsAreFollowedByNameAndNotByPosition(t *testing.T) {
	read := &model.Table{
		Columns: []model.Column{text("a"), text("b")},
		Uniques: []model.UniqueConstraint{
			{Name: "u_a", Columns: []string{"a"}},
			{Name: "u_b", Columns: []string{"b"}},
		},
	}
	d := NewDesign(model.NewRef(model.KindTable, "public", "t"), read)

	// Drop the first and add it again at the end: the same two constraints,
	// in the other order.
	if err := d.DropUnique("u_a"); err != nil {
		t.Fatal(err)
	}
	if err := d.AddUnique(model.UniqueConstraint{Name: "u_a", Columns: []string{"a"}}); err != nil {
		t.Fatal(err)
	}
	if d.Changed() {
		t.Errorf("the same constraints in another order read as %+v", d.ConstraintChanges())
	}
}

// A change that cannot be made must leave what was there. Dropping first and
// adding after would leave the table without the constraint it had.
func TestAConstraintThatCannotBeChangedIsLeftAsItWas(t *testing.T) {
	d := keyed()
	err := d.ChangeUnique("people_name_key", model.UniqueConstraint{
		Name: "people_name_key", Columns: []string{"nobody"}})
	if err == nil {
		t.Fatal("a unique on a column nobody has was allowed")
	}
	if d.Changed() {
		t.Errorf("a refused change left the design as %+v", d.ConstraintChanges())
	}
	_, uniques, _, _ := d.Constraints()
	if len(uniques) != 1 || uniques[0].Columns[0] != "name" {
		t.Errorf("the constraint that was there is now %+v", uniques)
	}

	// The same for a foreign key and a check.
	if err := d.ChangeForeignKey("people_note_fk", model.ForeignKey{Name: "people_note_fk",
		Columns: []string{"note"}, RefTable: "notes", RefColumns: []string{"a", "b"}}); err == nil {
		t.Error("a key of the wrong width was allowed")
	}
	if err := d.ChangeCheck("people_name_len", model.CheckConstraint{Name: "people_name_len"}); err == nil {
		t.Error("a check with no expression was allowed")
	}
	if d.Changed() {
		t.Errorf("refused changes left the design as %+v", d.ConstraintChanges())
	}
}
