package app

import (
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

func text(name string) model.Column {
	return model.Column{Name: name, Type: model.DataType{
		Class: model.TypeString, Native: "text", Nullable: true, Length: -1}}
}

func designed(rows int64) *Design {
	t := &model.Table{
		Name: "people", RowsEstimate: rows,
		Columns: []model.Column{
			{Name: "id", Type: model.DataType{Class: model.TypeInteger, Native: "int4", Length: -1},
				Identity: true},
			text("name"),
			{Name: "note", Type: model.DataType{Class: model.TypeString, Native: "varchar(40)",
				Nullable: true, Length: 40}, Comment: "what somebody wrote"},
		},
		PrimaryKey: &model.PrimaryKey{Name: "people_pkey", Columns: []string{"id"}},
	}
	return NewDesign(model.NewRef(model.KindTable, "public", "people"), t)
}

func TestADesignStartsAsTheTableItReads(t *testing.T) {
	d := designed(0)
	if d.Changed() {
		t.Error("a design begins changed")
	}
	if got := len(d.Columns()); got != 3 {
		t.Fatalf("it holds %d columns", got)
	}
	if got := d.Columns()[2].Comment; got != "what somebody wrote" {
		t.Errorf("a column's comment is %q", got)
	}
}

// The table read from the server must not move under the design, or reverting
// would put back whatever was last typed.
func TestEditingADesignLeavesWhatWasReadAlone(t *testing.T) {
	read := &model.Table{Columns: []model.Column{text("name")}}
	d := NewDesign(model.NewRef(model.KindTable, "public", "t"), read)
	if err := d.ChangeColumn("name", text("full_name")); err != nil {
		t.Fatal(err)
	}
	if read.Columns[0].Name != "name" {
		t.Errorf("the table that was read now says %q", read.Columns[0].Name)
	}
	if got := d.Original().Columns[0].Name; got != "name" {
		t.Errorf("the design's own record of it says %q", got)
	}
}

func TestAColumnAddedIsAChange(t *testing.T) {
	d := designed(0)
	if err := d.AddColumn(text("nickname")); err != nil {
		t.Fatal(err)
	}
	changes := d.Changes()
	if len(changes) != 1 || changes[0].Kind != ColumnAdded || changes[0].Name != "nickname" {
		t.Fatalf("it reads %+v", changes)
	}
	if got := d.Columns()[3].Position; got != 3 {
		t.Errorf("the new column sits at %d", got)
	}
}

func TestAColumnDroppedIsAChangeAndTheRestCloseUp(t *testing.T) {
	d := designed(0)
	if err := d.DropColumn("name"); err != nil {
		t.Fatal(err)
	}
	changes := d.Changes()
	if len(changes) != 1 || changes[0].Kind != ColumnDropped || changes[0].Name != "name" {
		t.Fatalf("it reads %+v", changes)
	}
	cols := d.Columns()
	if len(cols) != 2 || cols[1].Name != "note" || cols[1].Position != 1 {
		t.Errorf("what is left is %+v", cols)
	}
}

func TestAColumnChangedSaysWhatItWasAndWhatItIs(t *testing.T) {
	d := designed(0)
	wider := d.Columns()[2]
	wider.Type.Native, wider.Type.Length = "varchar(200)", 200
	if err := d.ChangeColumn("note", wider); err != nil {
		t.Fatal(err)
	}
	changes := d.Changes()
	if len(changes) != 1 || changes[0].Kind != ColumnAltered {
		t.Fatalf("it reads %+v", changes)
	}
	if changes[0].From.Type.Length != 40 || changes[0].To.Type.Length != 200 {
		t.Errorf("it went from %d to %d", changes[0].From.Type.Length, changes[0].To.Type.Length)
	}
	if changes[0].Renamed() {
		t.Error("changing a type read as a rename")
	}
}

// A rename is a change to the column that was there, not a drop and an add.
// Told apart wrongly, renaming a column would throw its data away.
func TestARenameIsAChangeAndNotADropAndAnAdd(t *testing.T) {
	d := designed(0)
	renamed := d.Columns()[1]
	renamed.Name = "full_name"
	if err := d.ChangeColumn("name", renamed); err != nil {
		t.Fatal(err)
	}
	changes := d.Changes()
	if len(changes) != 1 {
		t.Fatalf("it reads %+v", changes)
	}
	if changes[0].Kind != ColumnAltered || !changes[0].Renamed() {
		t.Errorf("a rename reads as %s, renamed=%v", changes[0].Kind, changes[0].Renamed())
	}
	if changes[0].From.Name != "name" || changes[0].To.Name != "full_name" {
		t.Errorf("it went from %q to %q", changes[0].From.Name, changes[0].To.Name)
	}
}

// The whole reason a design holds two tables rather than a list of edits.
func TestAColumnPutBackIsNotAChange(t *testing.T) {
	d := designed(0)
	was := d.Columns()[1]
	changed := was
	changed.Type.Native = "varchar(10)"
	if err := d.ChangeColumn("name", changed); err != nil {
		t.Fatal(err)
	}
	if !d.Changed() {
		t.Fatal("changing a column changed nothing")
	}
	if err := d.ChangeColumn("name", was); err != nil {
		t.Fatal(err)
	}
	if d.Changed() {
		t.Errorf("a column changed and changed back still reads as %+v", d.Changes())
	}
}

func TestRevertingOneColumnAndAllOfThem(t *testing.T) {
	d := designed(0)
	wider := d.Columns()[2]
	wider.Type.Native = "text"
	if err := d.ChangeColumn("note", wider); err != nil {
		t.Fatal(err)
	}
	if err := d.AddColumn(text("nickname")); err != nil {
		t.Fatal(err)
	}

	// A column that was added goes away; one that was changed goes back.
	if !d.Revert("nickname") {
		t.Error("reverting an added column did nothing")
	}
	if len(d.Columns()) != 3 {
		t.Errorf("it now holds %d columns", len(d.Columns()))
	}
	if !d.Revert("note") {
		t.Error("reverting a changed column did nothing")
	}
	if d.Changed() {
		t.Errorf("after reverting both: %+v", d.Changes())
	}
	// And a column with nothing to put back says so.
	if d.Revert("note") {
		t.Error("reverting an unchanged column claimed to do something")
	}
	if d.Revert("nothing at all") {
		t.Error("reverting a column that is not there claimed to do something")
	}

	if err := d.DropColumn("name"); err != nil {
		t.Fatal(err)
	}
	d.RevertAll()
	if d.Changed() {
		t.Errorf("after reverting all: %+v", d.Changes())
	}
}

func TestWhatADesignWillNotDo(t *testing.T) {
	for _, c := range []struct {
		what string
		do   func(*Design) error
		says string
	}{
		{"a column with no name", func(d *Design) error { return d.AddColumn(text("  ")) }, "needs a name"},
		{"a column with no type", func(d *Design) error {
			return d.AddColumn(model.Column{Name: "x"})
		}, "needs a type"},
		{"a name already taken", func(d *Design) error { return d.AddColumn(text("name")) }, "already has a column"},
		{"renaming onto a name already taken", func(d *Design) error {
			return d.ChangeColumn("name", text("note"))
		}, "already has a column"},
		{"renaming a column that is not there", func(d *Design) error {
			return d.ChangeColumn("nobody", text("x"))
		}, "no column called"},
		{"changing a column to no name", func(d *Design) error {
			return d.ChangeColumn("name", text(" "))
		}, "needs a name"},
		{"dropping a column that is not there", func(d *Design) error {
			return d.DropColumn("nobody")
		}, "no column called"},
		{"dropping the primary key's column", func(d *Design) error {
			return d.DropColumn("id")
		}, "part of this table's primary key"},
	} {
		t.Run(c.what, func(t *testing.T) {
			d := designed(0)
			err := c.do(d)
			if err == nil {
				t.Fatal("it was allowed")
			}
			if !strings.Contains(err.Error(), c.says) {
				t.Errorf("it said %v, wanted something about %q", err, c.says)
			}
			if d.Changed() {
				t.Errorf("a refusal left the design changed: %+v", d.Changes())
			}
		})
	}
}

// A NOT NULL column added to rows that exist needs a value for every one of
// them, and a design has none to give.
func TestANotNullColumnOnATableWithRows(t *testing.T) {
	strict := text("nickname")
	strict.Type.Nullable = false

	// Where there are rows, it is refused, and says what to do instead.
	err := designed(42).AddColumn(strict)
	if err == nil {
		t.Fatal("it was added to a table with rows")
	}
	for _, want := range []string{"every row would need a value", "nullable", "default"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("it said %v, which does not mention %q", err, want)
		}
	}

	// Where there are none, it is fine.
	if err := designed(0).AddColumn(strict); err != nil {
		t.Errorf("an empty table refused it: %v", err)
	}

	// A default answers the objection.
	withDefault := strict
	withDefault.Default, withDefault.HasDefault = "'nobody'", true
	if err := designed(42).AddColumn(withDefault); err != nil {
		t.Errorf("a column with a default was refused: %v", err)
	}

	// And a table whose size nobody knows is treated as holding rows: a
	// question somebody can answer beats an error from the server.
	if err := designed(-1).AddColumn(strict); err == nil {
		t.Error("a table of unknown size took a NOT NULL column")
	}
}

// Columns are followed by position, so a column that moved because one
// before it went is not itself a change.
func TestAColumnThatOnlyMovedIsNotAChange(t *testing.T) {
	d := designed(0)
	if err := d.DropColumn("name"); err != nil {
		t.Fatal(err)
	}
	for _, c := range d.Changes() {
		if c.Name == "note" {
			t.Errorf("a column that only moved reads as %s", c.Kind)
		}
	}
}
