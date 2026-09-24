package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/capability"
)

// Searching a database's structure (FR-2.7).

// searchable is a small database with somewhere for each kind of text to
// hide: a column, a default, a check, a view's body and a procedure's.
type searchable struct {
	source.Source
	fails     string // the object whose Describe falls over
	children  error  // what listing a schema's classes answers
	described []string
}

func (*searchable) Capabilities() capability.Capabilities {
	return capability.Capabilities{Paradigm: model.ParadigmRelational}
}

func (s *searchable) Root(context.Context) ([]model.Node, error) {
	return []model.Node{{Ref: model.NewRef(model.KindDatabase, "sales"), Label: "sales", HasChildren: true}}, nil
}

func (s *searchable) Children(_ context.Context, ref model.ObjectRef) ([]model.Node, error) {
	switch ref.Kind {
	case model.KindDatabase:
		return []model.Node{{Ref: model.NewRef(model.KindSchema, "sales", "public"), Label: "public"}}, nil
	case model.KindSchema:
		if s.children != nil {
			return nil, s.children
		}
		return []model.Node{
			model.ClassNode(ref, model.KindTable, 1),
			model.ClassNode(ref, model.KindView, 1),
			model.ClassNode(ref, model.KindRoutine, 1),
		}, nil
	}
	kind, ok := model.ClassOf(ref)
	if !ok {
		return nil, nil
	}
	name := map[model.ObjectKind]string{
		model.KindTable: "orders", model.KindView: "recent_orders", model.KindRoutine: "settle",
	}[kind]
	return []model.Node{{Ref: model.NewRef(kind, "sales", "public", name), Label: name,
		Browsable: kind == model.KindTable}}, nil
}

func (s *searchable) Describe(_ context.Context, ref model.ObjectRef) (any, error) {
	s.described = append(s.described, ref.Name())
	if ref.Name() == s.fails {
		return nil, errors.New("that one fell over")
	}
	switch ref.Kind {
	case model.KindTable:
		return &model.Table{Name: ref.Name(), RowsEstimate: -1, Comment: "what orders are",
			Columns: []model.Column{
				{Name: "id", Position: 1, Type: model.DataType{Native: "integer"}},
				{Name: "paid_total", Position: 2, Type: model.DataType{Native: "numeric"},
					Default: "0.00", HasDefault: true, Comment: "what was charged in all"},
				{Name: "shipped_at", Position: 3, Type: model.DataType{Native: "timestamptz"}},
				{Name: "total2024", Position: 4, Type: model.DataType{Native: "numeric"}},
				{Name: "slug", Position: 5, Type: model.DataType{Native: "text"},
					Generated: "lower(name)"},
			},
			Indexes: []model.Index{{Name: "ix_paid", Predicate: "settled IS NULL",
				Columns: []model.IndexColumn{{Name: "paid_total", Descending: true},
					{Expression: "lower(slug)"}},
				Include: []string{"shipped_at"}}},
			Checks:      []model.CheckConstraint{{Name: "ck_positive", Expression: "paid_total >= 100"}},
			ForeignKeys: []model.ForeignKey{{Name: "fk_customer", Columns: []string{"id"}, RefTable: "customers"}},
			Triggers: []model.Trigger{{Name: "tg_audit", Timing: "AFTER", Events: []string{"UPDATE"},
				Condition: "OLD.paid_total IS DISTINCT FROM NEW.paid_total", Definition: "EXECUTE FUNCTION log_change()"}},
		}, nil
	case model.KindView:
		return &model.View{Name: ref.Name(), Comment: "orders of the last week",
			Columns:    []model.Column{{Name: "summary", Position: 1, Type: model.DataType{Native: "text"}}},
			Definition: "SELECT id, paid_total AS total\n  FROM orders\n WHERE paid_total > 0"}, nil
	case model.KindRoutine:
		return &model.Routine{Name: ref.Name(), Kind: model.RoutineProcedure, Comment: "settles what is owed",
			Parameters: []model.Parameter{{Name: "at", Type: model.DataType{Native: "date"}}},
			Definition: "BEGIN\n  UPDATE orders SET settled = at;\nEND"}, nil
	}
	return nil, errors.New("nothing of that kind")
}

// all runs a search and collects everything it finds.
func all(t *testing.T, src source.Source, q Query) []Hit {
	t.Helper()
	var out []Hit
	if err := Search(context.Background(), src, model.ObjectRef{}, q, func(h Hit) bool {
		out = append(out, h)
		return true
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

// A column is found wherever it is, and the hit says which object and
// which column, so that a list of them can be read.
func TestSearchFindsAColumn(t *testing.T) {
	got := all(t, &searchable{}, Query{Text: "paid_total"})
	var where []string
	for _, h := range got {
		where = append(where, h.Node.Ref.Name()+": "+h.In)
	}
	for _, want := range []string{"orders: column paid_total", "orders: check ck_positive",
		"recent_orders: the definition"} {
		if !has(where, want) {
			t.Errorf("%q was not among %v", want, where)
		}
	}
}

// A string in a procedure's body is found, and the hit is the line it is
// on rather than the whole body.
func TestSearchFindsAStringInABody(t *testing.T) {
	got := all(t, &searchable{}, Query{Text: "SET settled"})
	if len(got) != 1 {
		t.Fatalf("it found %+v", got)
	}
	h := got[0]
	if h.Node.Ref.Name() != "settle" || h.In != "the body" {
		t.Errorf("it found it in %s %s", h.Node.Ref.Name(), h.In)
	}
	if h.Line != "UPDATE orders SET settled = at;" {
		t.Errorf("the line reads %q", h.Line)
	}
	if h.Line[h.At:h.At+len("SET settled")] != "SET settled" {
		t.Errorf("the match is said to start at %d of %q", h.At, h.Line)
	}
}

// Case is folded unless somebody asks for it to be kept.
func TestSearchFoldsCaseUnlessAsked(t *testing.T) {
	if got := all(t, &searchable{}, Query{Text: "PAID_TOTAL"}); len(got) == 0 {
		t.Error("a folded search found nothing")
	}
	if got := all(t, &searchable{}, Query{Text: "PAID_TOTAL", Case: true}); len(got) != 0 {
		t.Errorf("a search that keeps case found %+v", got)
	}
}

// A whole-word search answers with the word and not with what merely
// holds it.
func TestSearchCanAskForAWholeWord(t *testing.T) {
	loose := all(t, &searchable{}, Query{Text: "id"})
	if len(loose) == 0 {
		t.Fatal("a loose search found nothing")
	}
	whole := all(t, &searchable{}, Query{Text: "id", Whole: true})
	for _, h := range whole {
		if strings.Contains(h.In, "paid") {
			t.Errorf("a whole-word search answered with %q", h.In)
		}
	}
	if len(whole) >= len(loose) {
		t.Errorf("whole-word found %d of %d: it narrowed nothing", len(whole), len(loose))
	}
}

// The hits arrive as they are found, and stopping stops the walk rather
// than reading on and throwing the rest away.
func TestSearchStopsWhenToldTo(t *testing.T) {
	src := &searchable{}
	var got []Hit
	if err := Search(context.Background(), src, model.ObjectRef{}, Query{Text: "paid_total"}, func(h Hit) bool {
		got = append(got, h)
		return false
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("it went on to %d hits", len(got))
	}
	if len(src.described) != 1 {
		t.Errorf("it described %v after being told to stop", src.described)
	}
}

// Cancelling stops it.
func TestSearchStopsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := Search(ctx, &searchable{}, model.ObjectRef{}, Query{Text: "paid_total"}, func(Hit) bool { return true })
	if !errors.Is(err, context.Canceled) {
		t.Errorf("it said %v", err)
	}
}

// An object that cannot be described is passed over: one kind nobody
// wrote a Describe for is not a database nobody can search.
func TestSearchPassesOverWhatItCannotDescribe(t *testing.T) {
	got := all(t, &searchable{fails: "orders"}, Query{Text: "paid_total"})
	if len(got) == 0 {
		t.Fatal("one object that could not be read stopped the search")
	}
	for _, h := range got {
		if h.Node.Ref.Name() == "orders" {
			t.Errorf("it reported %+v from an object it could not read", h)
		}
	}
}

// A tree that cannot be listed is a failure: there is nothing to search
// and saying so is the only honest answer.
func TestSearchSaysWhenTheTreeCannotBeRead(t *testing.T) {
	err := Search(context.Background(), &searchable{children: errors.New("the server hung up")},
		model.ObjectRef{}, Query{Text: "paid_total"}, func(Hit) bool { return true })
	if err == nil || !strings.Contains(err.Error(), "hung up") {
		t.Errorf("it said %v", err)
	}
}

// Nothing to look for finds nothing, and asks the server nothing.
func TestSearchForNothingAsksNothing(t *testing.T) {
	src := &searchable{}
	if got := all(t, src, Query{Text: "   "}); len(got) != 0 {
		t.Errorf("it found %+v", got)
	}
	if len(src.described) != 0 {
		t.Errorf("it read %v", src.described)
	}
}

// A search under one node is under that node: the scope is what somebody
// picked in the tree.
func TestSearchUnderOneNode(t *testing.T) {
	src := &searchable{}
	var got []Hit
	under := model.ClassRef(model.NewRef(model.KindSchema, "sales", "public"), model.KindView)
	if err := Search(context.Background(), src, under, Query{Text: "paid_total"}, func(h Hit) bool {
		got = append(got, h)
		return true
	}); err != nil {
		t.Fatal(err)
	}
	if len(src.described) != 1 || src.described[0] != "recent_orders" {
		t.Errorf("it read %v", src.described)
	}
	if len(got) != 1 || got[0].In != "the definition" {
		t.Errorf("it found %+v", got)
	}
}

// The name of an object is looked at too: finding a table is finding it.
func TestSearchFindsAnObjectByName(t *testing.T) {
	got := all(t, &searchable{}, Query{Text: "recent"})
	if len(got) != 1 || got[0].In != "the name" || got[0].Node.Ref.Name() != "recent_orders" {
		t.Errorf("it found %+v", got)
	}
}

// Every place the search says it looks is a place it looks: a column's
// name, type, default and comment; an index's columns, expressions,
// included columns and predicate; a check, a foreign key, a trigger's
// condition and body; a view's columns, comment and definition; a
// routine's parameters, comment and body.
func TestSearchLooksEverywhereItSays(t *testing.T) {
	for _, c := range []struct{ text, obj, in string }{
		{"orders", "orders", "the name"},
		{"what orders are", "orders", "the comment"},
		{"paid_total", "orders", "column paid_total"},
		{"numeric", "orders", "column paid_total"},
		{"0.00", "orders", "column paid_total's default"},
		{"lower(name)", "orders", "column slug's default"},
		{"was charged", "orders", "column paid_total's comment"},
		{"ix_paid", "orders", "index ix_paid"},
		{"lower(slug)", "orders", "index ix_paid"},
		{"shipped_at", "orders", "index ix_paid"},
		{"settled IS NULL", "orders", "index ix_paid"},
		{"ck_positive", "orders", "check ck_positive"},
		{"paid_total >= 100", "orders", "check ck_positive"},
		{"fk_customer", "orders", "foreign key fk_customer"},
		{"customers", "orders", "foreign key fk_customer"},
		{"tg_audit", "orders", "trigger tg_audit"},
		{"DISTINCT FROM", "orders", "trigger tg_audit"},
		{"log_change", "orders", "trigger tg_audit"},
		{"summary", "recent_orders", "column summary"},
		{"the last week", "recent_orders", "the comment"},
		{"FROM orders", "recent_orders", "the definition"},
		{"at date", "settle", "parameter at"},
		{"settles what is owed", "settle", "the comment"},
		{"UPDATE orders", "settle", "the body"},
	} {
		var found bool
		for _, h := range all(t, &searchable{}, Query{Text: c.text}) {
			found = found || (h.Node.Ref.Name() == c.obj && h.In == c.in)
		}
		if !found {
			t.Errorf("%q was not found in %s %s", c.text, c.obj, c.in)
		}
	}
}

// A whole word is a whole word: what a name is made of is on both sides
// of it, and an underscore and a digit are both part of a name.
func TestWhatCountsAsAWholeWord(t *testing.T) {
	for _, c := range []struct {
		text string
		want bool
	}{
		{"paid_total", true}, // the column itself
		// It stands alone in the view's body, though "paid_total" holds it
		// first: finding one that is not a word is not the end of looking.
		{"total", true},
		{"low", false},      // half of lower(), past a letter
		{"paid", false},     // half of paid_total and of ix_paid, past an underscore
		{"10", false},       // half of 100, past a digit
		{"total2024", true}, // the column itself again
		{"2024", false},     // half of it, past a digit
		{"orders", true},    // a word of its own in the view's body
	} {
		got := all(t, &searchable{}, Query{Text: c.text, Whole: true})
		if (len(got) > 0) != c.want {
			t.Errorf("a whole-word search for %q found %d", c.text, len(got))
		}
	}
}

// A search is of the objects in a place, not of the places they are in: a
// schema's name is not a match, and neither is a class folder's label.
func TestSearchDoesNotMatchWhereObjectsLive(t *testing.T) {
	for _, text := range []string{"public", "sales"} {
		if got := all(t, &searchable{}, Query{Text: text}); len(got) != 0 {
			t.Errorf("searching %q found %+v", text, got)
		}
	}
}
