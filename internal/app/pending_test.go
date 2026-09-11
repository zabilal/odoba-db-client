package app

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

var (
	pendingCols = []model.ColumnDef{{Name: "id"}, {Name: "name"}, {Name: "score"}}
	peopleRef   = model.NewRef(model.KindTable, "main", "people")
	byID        = model.RowIdentity{Kind: model.IdentityPrimaryKey, Columns: []string{"id"}, Target: peopleRef}
)

func newPending(t *testing.T) *Pending {
	t.Helper()
	p, err := NewPending(pendingCols, byID)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// changes describes a changeset: each change's kind, key and values.
func changes(cs source.Changeset) string {
	var out []string
	for _, c := range cs.Changes {
		kind := [...]string{"insert", "update", "delete"}[c.Kind]
		out = append(out, fmt.Sprintf("%s %v %v", kind, c.Key, c.Values))
	}
	return fmt.Sprint(out)
}

func TestRowsThatCannotBeToldApartAreNotEdited(t *testing.T) {
	for name, id := range map[string]model.RowIdentity{
		"no key":         {Kind: model.IdentityNone, Target: peopleRef},
		"a log":          {Kind: model.IdentityLogOffset, Columns: []string{"id"}, Target: peopleRef},
		"no columns":     {Kind: model.IdentityPrimaryKey, Target: peopleRef},
		"a key not read": {Kind: model.IdentityPrimaryKey, Columns: []string{"rowid"}, Target: peopleRef},
	} {
		if _, err := NewPending(pendingCols, id); !errors.Is(err, ErrNoRowIdentity) {
			t.Errorf("%s: %v, want ErrNoRowIdentity", name, err)
		}
	}
}

func TestAnEditIsTheColumnChangedAndNoMore(t *testing.T) {
	p := newPending(t)
	ann := model.Row{int64(1), "ann", 3.5}
	if err := p.Set(ann, 1, "anne"); err != nil {
		t.Fatal(err)
	}
	if p.State(ann) != model.RowModified || p.Len() != 1 {
		t.Fatalf("state %v, %d changes", p.State(ann), p.Len())
	}
	if v, ok := p.Value(ann, 1); !ok || v != "anne" {
		t.Errorf("the cell's pending value is %v, %v", v, ok)
	}
	if _, ok := p.Value(ann, 2); ok {
		t.Error("a cell not changed has no pending value")
	}
	if got := changes(p.Changeset(false)); got != "[update [1] map[name:anne]]" {
		t.Errorf("changeset %s", got)
	}
	// The same row read again, after a sort moved it, is the same row.
	again := model.Row{int64(1), "ann", 3.5}
	p.Set(again, 2, 4.0)
	if got := changes(p.Changeset(false)); got != "[update [1] map[name:anne score:4]]" {
		t.Errorf("a row read again should find its edits: %s", got)
	}
	p.Set(again, 1, "ann") // back to what it was
	p.RevertCell(again, 2)
	if p.State(ann) != model.RowUnchanged || p.Len() != 0 {
		t.Errorf("a row set back to what it holds is unchanged: %v, %d", p.State(ann), p.Len())
	}
}

func TestAKeyEditedIsWrittenByTheKeyItHad(t *testing.T) {
	p := newPending(t)
	p.Set(model.Row{int64(1), "ann", 3.5}, 0, int64(9))
	if got := changes(p.Changeset(true)); got != "[update [1] map[id:9]]" {
		t.Errorf("changeset %s", got)
	}
	if cs := p.Changeset(true); !cs.Confirmed || !cs.Target.Equal(peopleRef) || cs.Identity.Kind != model.IdentityPrimaryKey {
		t.Errorf("changeset %+v", cs)
	}
}

func TestADeletedRowLosesItsEditsAndTakesNoMore(t *testing.T) {
	p := newPending(t)
	bob := model.Row{int64(2), "bob", 1.0}
	p.Set(bob, 1, "robert")
	p.Delete(bob)
	if p.State(bob) != model.RowDeleted {
		t.Fatalf("state %v", p.State(bob))
	}
	if got := changes(p.Changeset(false)); got != "[delete [2] map[]]" {
		t.Errorf("changeset %s; the row's edits go with it", got)
	}
	if err := p.Set(bob, 1, "rob"); err == nil {
		t.Error("a deleted row should not be edited")
	}
	p.RevertCell(bob, 1) // not an edit: nothing to undo
	if p.State(bob) != model.RowDeleted {
		t.Error("reverting a cell does not bring a deleted row back")
	}
	p.RevertRow(bob)
	if p.State(bob) != model.RowUnchanged || p.Len() != 0 {
		t.Errorf("reverted, the row is %v with %d changes", p.State(bob), p.Len())
	}
}

func TestNewRowsComeAfterTheOtherChanges(t *testing.T) {
	p := newPending(t)
	first := p.Add()
	if err := p.SetAdded(first, 1, "cat"); err != nil {
		t.Fatal(err)
	}
	second := p.Add()
	p.SetAdded(second, 0, int64(7))
	p.Delete(model.Row{int64(3), "dan", 0.0})
	p.Set(model.Row{int64(1), "ann", 3.5}, 2, 5.0)
	if got := changes(p.Changeset(false)); got != "[delete [3] map[] update [1] map[score:5] insert [] map[name:cat] insert [] map[id:7]]" {
		t.Errorf("changeset %s", got)
	}
	if got := fmt.Sprint(p.Added()); got != "[[DEFAULT cat DEFAULT] [7 DEFAULT DEFAULT]]" {
		t.Errorf("new rows %s", got)
	}
	p.RemoveAdded(first)
	if p.Len() != 3 || fmt.Sprint(p.Added()) != "[[7 DEFAULT DEFAULT]]" {
		t.Errorf("after removing one: %d changes, %v", p.Len(), p.Added())
	}
	if err := p.SetAdded(5, 0, 1); err == nil {
		t.Error("a new row that is not there takes no value")
	}
	if err := p.SetAdded(0, 9, 1); err == nil {
		t.Error("a column that is not there takes no value")
	}
	p.RevertAll()
	if p.Len() != 0 || len(p.Added()) != 0 || len(p.Changeset(false).Changes) != 0 {
		t.Errorf("RevertAll leaves %d", p.Len())
	}
}

func TestADuplicateHasTheRowsValuesButNotItsKey(t *testing.T) {
	p := newPending(t)
	ann := model.Row{int64(1), "ann", 3.5}
	if err := p.Set(ann, 2, 4.0); err != nil {
		t.Fatal(err)
	}
	i := p.Duplicate(ann)
	if got := fmt.Sprint(p.Added()[i]); got != "[DEFAULT ann 4]" {
		t.Errorf("a copy of the row as edited, its key left to the server: %s", got)
	}
	j := p.Duplicate(p.Added()[i])
	if got := fmt.Sprint(p.Added()[j]); got != "[DEFAULT ann 4]" {
		t.Errorf("a new row's copy leaves what it was not given: %s", got)
	}
	bob := p.Add()
	if err := p.SetAdded(bob, 1, "bob"); err != nil {
		t.Fatal(err)
	}
	p.Duplicate(p.Added()[bob])
	if got := changes(p.Changeset(false)); got != "[update [1] map[score:4] insert [] map[name:ann score:4] insert [] map[name:ann score:4] insert [] map[name:bob] insert [] map[name:bob]]" {
		t.Errorf("changeset %s", got)
	}
}

func TestEachChangeSaysWhichRowItIsTo(t *testing.T) {
	p := newPending(t)
	ann, bob := model.Row{int64(1), "ann", 3.5}, model.Row{int64(2), "bob", 1.0}
	p.Delete(bob)
	if err := p.Set(ann, 1, "anne"); err != nil {
		t.Fatal(err)
	}
	p.Add()
	key, added, ok := p.Change(1)
	if !ok || added != -1 || !p.IsRow(ann, key) || p.IsRow(bob, key) {
		t.Errorf("change 2 is to ann: %v %d %v", key, added, ok)
	}
	if key, _, _ := p.Change(0); !p.IsRow(bob, key) || p.IsRow(model.Row{"2", "bob"}, key) {
		t.Error("change 1 is to bob, known by his key's type too")
	}
	if _, added, ok := p.Change(2); !ok || added != 0 {
		t.Errorf("change 3 is to the first new row: %d %v", added, ok)
	}
	if _, _, ok := p.Change(3); ok {
		t.Error("there is no change 4")
	}
	if _, _, ok := p.Change(-1); ok {
		t.Error("nor a change before the first")
	}
}

func TestACellIsNoChangeWhenItsValueIsTheSame(t *testing.T) {
	at := time.Date(2026, 9, 11, 14, 2, 0, 0, time.UTC)
	for _, c := range []struct {
		was, now any
		same     bool
	}{
		{[]byte{1, 2}, []byte{1, 2}, true},
		{[]byte{1, 2}, []byte{1, 3}, false},
		{at, at.In(time.FixedZone("X", 3600)), true}, // the same instant, written elsewhere
		{at, at.Add(time.Second), false},
		{nil, nil, true},
		{nil, "", false},
		{int64(1), int64(1), true},
		{int64(1), "1", false},
	} {
		if got := sameValue(c.was, c.now); got != c.same {
			t.Errorf("sameValue(%v, %v) = %v", c.was, c.now, got)
		}
	}
	p := newPending(t)
	if err := p.Set(model.Row{int64(1), "ann", 3.5}, 5, "x"); err == nil {
		t.Error("a column that is not there takes no value")
	}
	// SQLite's columns can hold 1 and '1' alike: a key is its values' types too.
	p.Set(model.Row{int64(1), "ann", 3.5}, 1, "anne")
	if p.State(model.Row{"1", "ann", 3.5}) != model.RowUnchanged {
		t.Error("a row keyed '1' is not the row keyed 1")
	}
}
