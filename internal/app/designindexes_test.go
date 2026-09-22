package app

import (
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// indexed is the fixture table with an index on it.
func indexedDesign() *Design {
	d := designed(0)
	t := d.Table()
	t.Indexes = []model.Index{{
		Name: "people_name_idx", Method: "btree",
		Columns: []model.IndexColumn{{Name: "name"}},
	}}
	return NewDesign(d.Ref, t)
}

func on(cols ...string) []model.IndexColumn {
	out := make([]model.IndexColumn, len(cols))
	for i, c := range cols {
		out[i] = model.IndexColumn{Name: c}
	}
	return out
}

func TestAnIndexAddedAndDropped(t *testing.T) {
	d := indexedDesign()
	if d.Changed() {
		t.Fatal("a design begins changed")
	}
	if err := d.AddIndex(model.Index{Name: "people_note_idx", Columns: on("note"), Unique: true}); err != nil {
		t.Fatal(err)
	}
	changes := d.IndexChanges()
	if len(changes) != 1 || changes[0].Change != ColumnAdded || changes[0].Name != "people_note_idx" {
		t.Fatalf("it reads %+v", changes)
	}
	if !d.Changed() {
		t.Error("a design with a new index says nothing changed")
	}

	if err := d.DropIndex("people_name_idx"); err != nil {
		t.Fatal(err)
	}
	dropped := 0
	for _, c := range d.IndexChanges() {
		if c.Change == ColumnDropped {
			dropped++
		}
	}
	if dropped != 1 {
		t.Errorf("dropping one index read as %d drops", dropped)
	}
}

// Changed in place is one change, not a drop and a rebuild — which for an
// index is the difference between a moment and an afternoon.
func TestAnIndexChangedInPlaceIsOneChange(t *testing.T) {
	d := indexedDesign()
	if err := d.ChangeIndex("people_name_idx", model.Index{
		Name: "people_name_idx", Method: "hash", Columns: on("name")}); err != nil {
		t.Fatal(err)
	}
	changes := d.IndexChanges()
	if len(changes) != 1 || changes[0].Change != ColumnAltered {
		t.Fatalf("it reads %+v", changes)
	}
	if changes[0].Was.Method != "btree" || changes[0].Now.Method != "hash" {
		t.Errorf("it went from %q to %q", changes[0].Was.Method, changes[0].Now.Method)
	}
}

// The order of an index's columns is what it is: an index on (a, b) answers
// questions an index on (b, a) does not.
func TestAnIndexsColumnsKeepTheirOrderAndDirection(t *testing.T) {
	d := indexedDesign()
	if err := d.AddIndex(model.Index{Name: "two", Columns: []model.IndexColumn{
		{Name: "note", Descending: true}, {Name: "name"}}}); err != nil {
		t.Fatal(err)
	}
	got := d.Indexes()[1]
	if len(got.Columns) != 2 || got.Columns[0].Name != "note" || !got.Columns[0].Descending {
		t.Fatalf("it is on %+v", got.Columns)
	}
	if got.Columns[1].Name != "name" || got.Columns[1].Descending {
		t.Errorf("its second column is %+v", got.Columns[1])
	}

	// And an index on the same columns the other way round is a different
	// index, not the same one.
	if err := d.ChangeIndex("two", model.Index{Name: "two", Columns: []model.IndexColumn{
		{Name: "name"}, {Name: "note", Descending: true}}}); err != nil {
		t.Fatal(err)
	}
	for _, c := range d.IndexChanges() {
		if c.Name == "two" && c.Change != ColumnAdded {
			t.Errorf("reordering an index that was never read reads as %s", c.Change)
		}
	}
}

// An index over an expression is the engine's own language and is not read
// here, so it is kept as it was typed.
func TestAnIndexOverAnExpression(t *testing.T) {
	d := indexedDesign()
	if err := d.AddIndex(model.Index{Name: "lowered", Columns: []model.IndexColumn{
		{Expression: "lower(name)"}}}); err != nil {
		t.Fatalf("an expression index was refused: %v", err)
	}
	got := d.Indexes()[1]
	if got.Columns[0].Expression != "lower(name)" || got.Columns[0].Name != "" {
		t.Errorf("it is on %+v", got.Columns[0])
	}
	// And it does not hold a column down, because nothing here read it.
	if err := d.DropColumn("note"); err != nil {
		t.Errorf("a column only an expression mentions was held: %v", err)
	}
}

// A column an index names cannot be dropped out from under it, the same way
// a key's cannot.
func TestAColumnAnIndexNamesCannotBeDropped(t *testing.T) {
	d := indexedDesign()
	err := d.DropColumn("name")
	if err == nil {
		t.Fatal("it was dropped")
	}
	if !strings.Contains(err.Error(), "the index people_name_idx") {
		t.Errorf("it said %v", err)
	}

	// Once the index is gone, the column can go.
	if err := d.DropIndex("people_name_idx"); err != nil {
		t.Fatal(err)
	}
	if err := d.DropColumn("name"); err != nil {
		t.Errorf("a column no index names any more was refused: %v", err)
	}
}

// An included column is carried by the index too, so it is held the same way.
func TestAnIncludedColumnIsHeldAsWell(t *testing.T) {
	d := indexedDesign()
	if err := d.ChangeIndex("people_name_idx", model.Index{Name: "people_name_idx",
		Columns: on("name"), Include: []string{"note"}}); err != nil {
		t.Fatal(err)
	}
	err := d.DropColumn("note")
	if err == nil || !strings.Contains(err.Error(), "the index people_name_idx") {
		t.Errorf("dropping an included column said %v", err)
	}
}

func TestWhatAnIndexWillNotBe(t *testing.T) {
	for _, c := range []struct {
		what string
		do   func(*Design) error
		says string
	}{
		{"an index on nothing", func(d *Design) error {
			return d.AddIndex(model.Index{Name: "x"})
		}, "needs at least one column"},
		{"an index on a column nobody has", func(d *Design) error {
			return d.AddIndex(model.Index{Name: "x", Columns: on("nobody")})
		}, "no column called"},
		{"an index on the same column twice", func(d *Design) error {
			return d.AddIndex(model.Index{Name: "x", Columns: on("name", "name")})
		}, "twice"},
		{"an index including a column nobody has", func(d *Design) error {
			return d.AddIndex(model.Index{Name: "x", Columns: on("name"), Include: []string{"nobody"}})
		}, "no column called"},
		{"a column both indexed and included", func(d *Design) error {
			return d.AddIndex(model.Index{Name: "x", Columns: on("name"), Include: []string{"name"}})
		}, "can only be one"},
		{"an index under a name something else has", func(d *Design) error {
			return d.AddIndex(model.Index{Name: "people_name_idx", Columns: on("note")})
		}, "already has a constraint"},
		{"changing an index that is not there", func(d *Design) error {
			return d.ChangeIndex("nobody", model.Index{Name: "x", Columns: on("name")})
		}, "no index called"},
		{"dropping an index that is not there", func(d *Design) error {
			return d.DropIndex("nobody")
		}, "no index called"},
	} {
		t.Run(c.what, func(t *testing.T) {
			d := indexedDesign()
			err := c.do(d)
			if err == nil {
				t.Fatal("it was allowed")
			}
			if !strings.Contains(err.Error(), c.says) {
				t.Errorf("it said %v, wanted something about %q", err, c.says)
			}
			if d.Changed() {
				t.Errorf("a refusal left the design changed: %+v", d.IndexChanges())
			}
		})
	}
}

// A change that cannot be made leaves the index that was there.
func TestAnIndexThatCannotBeChangedIsLeftAsItWas(t *testing.T) {
	d := indexedDesign()
	if err := d.ChangeIndex("people_name_idx", model.Index{
		Name: "people_name_idx", Columns: on("nobody")}); err == nil {
		t.Fatal("an index on a column nobody has was allowed")
	}
	if d.Changed() {
		t.Errorf("a refused change left the design as %+v", d.IndexChanges())
	}
	if got := d.Indexes(); len(got) != 1 || got[0].Columns[0].Name != "name" {
		t.Errorf("the index that was there is now %+v", got)
	}
}

// A design must not edit the indexes it was read with.
func TestEditingIndexesLeavesWhatWasReadAlone(t *testing.T) {
	read := &model.Table{
		Columns: []model.Column{text("a"), text("b")},
		Indexes: []model.Index{{Name: "i", Columns: []model.IndexColumn{{Name: "a"}}}},
	}
	d := NewDesign(model.NewRef(model.KindTable, "public", "t"), read)
	if err := d.ChangeIndex("i", model.Index{Name: "i", Columns: on("b"), Unique: true}); err != nil {
		t.Fatal(err)
	}
	if read.Indexes[0].Columns[0].Name != "a" || read.Indexes[0].Unique {
		t.Errorf("the table that was read now indexes %+v", read.Indexes[0])
	}
	d.RevertAll()
	if d.Changed() {
		t.Errorf("after reverting: %+v", d.IndexChanges())
	}
}

// An index's direction is part of what it is: changed from ascending to
// descending, it is a different index and must read as changed.
func TestChangingOnlyAnIndexsDirection(t *testing.T) {
	d := indexedDesign()
	if err := d.ChangeIndex("people_name_idx", model.Index{
		Name: "people_name_idx", Method: "btree",
		Columns: []model.IndexColumn{{Name: "name", Descending: true}}}); err != nil {
		t.Fatal(err)
	}
	changes := d.IndexChanges()
	if len(changes) != 1 || changes[0].Change != ColumnAltered {
		t.Fatalf("turning an index round reads as %+v", changes)
	}
	if !changes[0].Now.Columns[0].Descending {
		t.Error("it did not come back descending")
	}
}
