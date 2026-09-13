package shell

import (
	"strings"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
)

func TestARowThatNamesSomethingOpensIt(t *testing.T) {
	fx := newFixture(t)
	tb := openKeyspace(t, fx, "keys")
	// Nothing is selected yet, so there is nothing to open.
	if fx.s.canOpenRowObject() {
		t.Error("a row nobody is on names nothing")
	}
	tb.grid.Select(grid.CellID{Row: 0, Col: 0}, grid.CellID{Row: 0, Col: 0})
	if !fx.s.canOpenRowObject() {
		t.Fatal("the first key names a key")
	}
	fx.s.run(cmdOpenRowObject)
	opened := tabOn(fx, "user:1")
	if opened == nil || fx.s.activeTab() != opened || !opened.ref.Equal(firstKey) {
		t.Fatalf("what the row names opens in front: %v", opened)
	}
	// What the key holds is rows of its own shape, read the same way.
	pump(t, fx.q, func() bool {
		if opened.model == nil {
			return false
		}
		_, ok := opened.model.Row(opened.ctx, 1)
		return ok
	})
	cols := opened.model.Columns()
	if len(cols) != 2 || cols[0].Name != "field" || cols[1].Name != "value" {
		t.Errorf("a hash shows %v", cols)
	}
	row, _ := opened.model.Row(opened.ctx, 0)
	if len(row) != 2 || row[0] != "city" {
		t.Errorf("the first row is %v", row)
	}
	// Asked again, the tab it is already in comes forward rather than a
	// second one opening on the same key.
	fx.s.selectTab(tb)
	tb.grid.Select(grid.CellID{Row: 0, Col: 0}, grid.CellID{Row: 0, Col: 0})
	fx.s.run(cmdOpenRowObject)
	if len(fx.s.open) != 2 || fx.s.activeTab() != opened {
		t.Errorf("%d tabs open, and %v in front", len(fx.s.open), fx.s.activeTab().ref)
	}
}

func TestARowThatNamesNothingIsNotOffered(t *testing.T) {
	fx := newFixture(t)
	// A source that does not say its rows are objects offers nothing, and a
	// relational one never does.
	tb := openKeyspace(t, fx, "flat")
	tb.grid.Select(grid.CellID{Row: 0, Col: 0}, grid.CellID{Row: 0, Col: 0})
	if fx.s.canOpenRowObject() {
		t.Error("a store whose rows name nothing offered to open one")
	}
	fx.s.run(cmdOpenRowObject) // does nothing, and does not panic
	if len(fx.s.open) != 1 {
		t.Errorf("%d tabs open", len(fx.s.open))
	}

	table := fx.create(t, "rows", nil)
	fx.s.OpenObject(table.ID, model.Node{Ref: model.NewRef(model.KindTable, "main", "people"), Label: "people", Browsable: true})
	people := tabOn(fx, "people")
	if people == nil {
		t.Fatal("the table did not open")
	}
	pump(t, fx.q, func() bool {
		if people.model == nil {
			return false
		}
		_, ok := people.model.Row(people.ctx, 0)
		return ok
	})
	people.grid.Select(grid.CellID{Row: 0, Col: 0}, grid.CellID{Row: 0, Col: 0})
	if fx.s.canOpenRowObject() {
		t.Error("a table's row named something to open")
	}
}

func TestAKeyspaceAndAKeyAreShownAsWhatTheyAre(t *testing.T) {
	ks := &model.Keyspace{Name: "db0", Keys: 3, Expiring: 1, Figures: []model.FigureGroup{
		{Title: "Memory", Values: []model.Figure{{Name: "used_memory_human", Value: "1.05M"}}},
		{Title: "Statistics", Values: []model.Figure{{Name: "keyspace_hits", Value: "42"}}},
	}}
	got := strings.Join(labelTexts(structureView(ks, nil, nil)), "\n")
	for _, want := range []string{"3 keys, 1 of them set to expire", "Memory", "used_memory_human",
		"1.05M", "Statistics", "keyspace_hits", "42"} {
		if !strings.Contains(got, want) {
			t.Errorf("a keyspace does not say %q:\n%s", want, got)
		}
	}
	// A server that says nothing about itself says so, rather than showing
	// empty headings.
	quiet := strings.Join(labelTexts(structureView(&model.Keyspace{Name: "db0", Keys: -1, Expiring: -1}, nil, nil)), "\n")
	if !strings.Contains(quiet, "says nothing about itself") || strings.Contains(quiet, "keys,") {
		t.Errorf("a server that says nothing:\n%s", quiet)
	}

	key := &model.StoredKey{Name: "user:1", Kind: "hash", TTL: 90 * time.Second,
		Bytes: 104, Length: 2, Encoding: "listpack"}
	got = strings.Join(labelTexts(structureView(key, nil, nil)), "\n")
	for _, want := range []string{"A hash: fields and their values.", "Kind", "hash", "Held as",
		"listpack", "Length", "2", "Bytes", "104", "Expires", "in 1m30s"} {
		if !strings.Contains(got, want) {
			t.Errorf("a key does not say %q:\n%s", want, got)
		}
	}
	// A key that never expires says so, and one the server would say nothing
	// about shows nothing rather than -1.
	plain := strings.Join(labelTexts(structureView(&model.StoredKey{Name: "user:1", Kind: "string",
		Bytes: -1, Length: -1}, nil, nil)), "\n")
	if !strings.Contains(plain, "never") || strings.Contains(plain, "-1") {
		t.Errorf("a key with nothing said about it:\n%s", plain)
	}
	if !strings.Contains(plain, "A string: one value.") {
		t.Errorf("a string does not say what it is:\n%s", plain)
	}
	// A kind nobody here has a line for is still said to be something.
	other := strings.Join(labelTexts(structureView(&model.StoredKey{Name: "ts", Kind: "TSDB-TYPE",
		Bytes: -1, Length: -1}, nil, nil)), "\n")
	if !strings.Contains(other, "A TSDB-TYPE.") {
		t.Errorf("a kind nobody knows:\n%s", other)
	}
}

func TestTheStructureShownIsOfWhatIsInFront(t *testing.T) {
	fx := newFixture(t)
	tb := openKeyspace(t, fx, "keys")
	// Nothing is chosen in the explorer, so it is the object in front —
	// which is the only way to reach one that is not in the tree at all.
	if !fx.s.canOpenStructure() {
		t.Fatal("the object in front has a structure to show")
	}
	fx.s.run(cmdStructure)
	var st *tab
	for _, o := range fx.s.open {
		if o.structure && o.ref.Kind == model.KindDatabase {
			st = o
		}
	}
	if st == nil {
		t.Fatalf("the structure did not open: %v", tabLabels(fx.s))
	}
	pump(t, fx.q, func() bool { return showsAll(st, "3 keys", "used_memory_human") })

	// And a key's own panel, from the tab its value is open in.
	fx.s.selectTab(tb)
	tb.grid.Select(grid.CellID{Row: 0, Col: 0}, grid.CellID{Row: 0, Col: 0})
	fx.s.run(cmdOpenRowObject)
	fx.s.run(cmdStructure)
	var panel *tab
	for _, o := range fx.s.open {
		if o.structure && o.ref.Kind == model.KindKey {
			panel = o
		}
	}
	if panel == nil {
		t.Fatalf("the key's structure did not open: %v", tabLabels(fx.s))
	}
	pump(t, fx.q, func() bool { return showsAll(panel, "A hash", "listpack", "104", "in 1m30s") })
}
