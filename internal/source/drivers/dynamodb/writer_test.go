package dynamodb

import (
	"strings"
	"testing"

	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// What a change becomes, decided without a service.
//
// The condition on every write is the thing to get right here. PutItem and
// UpdateItem both create the item when it is not there, so without a condition
// an insert would overwrite whatever is at that key and report success, and a
// change to an item somebody else deleted would put it back. Neither is what
// the grid means.

// placeholderFor is the placeholder a call bound an attribute name under.
func placeholderFor(t *testing.T, w *itemWrite, attr string) string {
	t.Helper()
	for placeholder, name := range w.names {
		if name == attr {
			return placeholder
		}
	}
	t.Fatalf("%q was not bound; bound: %v", attr, w.names)
	return ""
}

func opOf(t *testing.T, st source.Statement) *itemWrite {
	t.Helper()
	w, ok := st.Op.(*itemWrite)
	if !ok {
		t.Fatalf("the statement carries %T", st.Op)
	}
	return w
}

func TestAnInsertAsksForItsKeyNotToBeTaken(t *testing.T) {
	st, said, err := writeOf("T", []string{"pk"}, source.RowChange{Kind: source.ChangeInsert,
		Values: map[string]any{"pk": "a", "n": int64(1)}})
	if err != nil {
		t.Fatal(err)
	}
	w := opOf(t, st)
	if w.cond != "attribute_not_exists(#c0)" {
		t.Errorf("its condition is %q", w.cond)
	}
	if placeholderFor(t, w, "pk") != "#c0" {
		t.Errorf("it bound %v", w.names)
	}
	if len(w.item) != 2 || describeAV(w.item["pk"]) != "S:a" || describeAV(w.item["n"]) != "N:1" {
		t.Errorf("the item is %v", w.item)
	}
	if !strings.Contains(said, "n") || !strings.Contains(said, "pk") {
		t.Errorf("it says %q", said)
	}
}

func TestAChangeAndADeleteAskForTheItemToBeThere(t *testing.T) {
	for name, c := range map[string]source.RowChange{
		"a change": {Kind: source.ChangeUpdate, Key: []any{"a"}, Values: map[string]any{"n": int64(2)}},
		"a delete": {Kind: source.ChangeDelete, Key: []any{"a"}},
	} {
		t.Run(name, func(t *testing.T) {
			st, _, err := writeOf("T", []string{"pk"}, c)
			if err != nil {
				t.Fatal(err)
			}
			w := opOf(t, st)
			// The placeholder the key got depends on what else was bound
			// first, so what is checked is that the condition is on the key.
			if w.cond != "attribute_exists("+placeholderFor(t, w, "pk")+")" {
				t.Errorf("its condition is %q, and %v was bound", w.cond, w.names)
			}
			if describeAV(w.key["pk"]) != "S:a" {
				t.Errorf("its key is %v", w.key)
			}
		})
	}
}

// SET before REMOVE, which is the order DynamoDB requires, and both in one
// expression: a change that sets one attribute and takes another away is one
// call.
func TestAChangeSetsAndRemovesInOneExpression(t *testing.T) {
	st, said, err := writeOf("T", []string{"pk"}, source.RowChange{Kind: source.ChangeUpdate,
		Key: []any{"a"}, Values: map[string]any{"n": int64(2), "gone": model.Removed{}}})
	if err != nil {
		t.Fatal(err)
	}
	w := opOf(t, st)
	got := w.updateExpression()
	if !strings.HasPrefix(got, "SET ") || !strings.Contains(got, " REMOVE ") {
		t.Errorf("it reads %q", got)
	}
	if strings.Index(got, "SET") > strings.Index(got, "REMOVE") {
		t.Errorf("REMOVE comes first: %q", got)
	}
	// Both attributes went in through placeholders, and the value through one
	// of its own.
	if len(w.names) != 3 || len(w.vals) != 1 {
		t.Errorf("it bound %v and %v", w.names, w.vals)
	}
	if !strings.Contains(said, "gone") {
		t.Errorf("it says %q", said)
	}
}

// A change of only a removal is still a change, and needs no values bound.
func TestAChangeOfOnlyARemoval(t *testing.T) {
	st, _, err := writeOf("T", []string{"pk"}, source.RowChange{Kind: source.ChangeUpdate,
		Key: []any{"a"}, Values: map[string]any{"gone": model.Removed{}}})
	if err != nil {
		t.Fatal(err)
	}
	w := opOf(t, st)
	if got := w.updateExpression(); got != "REMOVE "+placeholderFor(t, w, "gone") {
		t.Errorf("it reads %q, and %v was bound", got, w.names)
	}
	if len(w.vals) != 0 {
		t.Errorf("it bound %v", w.vals)
	}
}

// A key of two attributes: both go in, in the order the table states them,
// because that is the order the key's values arrive in.
func TestAKeyOfTwoGoesInInOrder(t *testing.T) {
	st, said, err := writeOf("T", []string{"pk", "sk"}, source.RowChange{Kind: source.ChangeDelete,
		Key: []any{"a", int64(3)}})
	if err != nil {
		t.Fatal(err)
	}
	w := opOf(t, st)
	if describeAV(w.key["pk"]) != "S:a" || describeAV(w.key["sk"]) != "N:3" {
		t.Errorf("its key is %v", w.key)
	}
	if !strings.Contains(said, "pk = a") || !strings.Contains(said, "sk = 3") {
		t.Errorf("it says %q", said)
	}
}

func TestAChangeThatCannotBeWrittenIsRefused(t *testing.T) {
	for name, c := range map[string]struct {
		keys   []string
		change source.RowChange
		says   string
	}{
		"a change of nothing": {[]string{"pk"},
			source.RowChange{Kind: source.ChangeUpdate, Key: []any{"a"}}, "changes nothing"},
		"a key of the wrong size": {[]string{"pk", "sk"},
			source.RowChange{Kind: source.ChangeDelete, Key: []any{"a"}}, "1 values for 2"},
		"a key value that is empty": {[]string{"pk"},
			source.RowChange{Kind: source.ChangeUpdate, Key: []any{nil},
				Values: map[string]any{"n": int64(1)}}, "addresses no item"},
		"a key value it cannot hold": {[]string{"pk"},
			source.RowChange{Kind: source.ChangeDelete, Key: []any{struct{}{}}}, "cannot hold"},
		"a value it cannot hold": {[]string{"pk"},
			source.RowChange{Kind: source.ChangeUpdate, Key: []any{"a"},
				Values: map[string]any{"n": struct{}{}}}, "cannot hold"},
		// A new item without its key addresses nothing: DynamoDB would refuse
		// it, and saying so here names the attribute that is missing.
		"a new item with no key": {[]string{"pk"},
			source.RowChange{Kind: source.ChangeInsert, Values: map[string]any{"n": int64(1)}}, "needs a value for pk"},
		"a new item whose key was removed": {[]string{"pk"},
			source.RowChange{Kind: source.ChangeInsert,
				Values: map[string]any{"pk": model.Removed{}, "n": int64(1)}}, "needs a value for pk"},
		"a change of unknown kind": {[]string{"pk"},
			source.RowChange{Kind: source.ChangeKind(9)}, "unknown kind"},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := writeOf("T", c.keys, c.change)
			if err == nil {
				t.Fatal("it was accepted")
			}
			if !strings.Contains(err.Error(), c.says) {
				t.Errorf("it says %q, which does not mention %q", err, c.says)
			}
		})
	}
}

// An attribute nobody gave a value to is an attribute the item has not got:
// there is no default in a store with no declared shape.
func TestANewItemHasNoDefaults(t *testing.T) {
	st, _, err := writeOf("T", []string{"pk"}, source.RowChange{Kind: source.ChangeInsert,
		Values: map[string]any{"pk": "a", "n": model.Default{}}})
	if err != nil {
		t.Fatal(err)
	}
	w := opOf(t, st)
	if len(w.item) != 1 {
		t.Errorf("the item is %v", w.item)
	}
	if _, ok := w.item["n"]; ok {
		t.Error("an attribute nobody gave a value to was written")
	}
}

// A table with no key at all — which no DynamoDB table has — writes without a
// condition rather than with one naming an attribute that does not exist.
func TestATableWithNoKeyWritesWithoutACondition(t *testing.T) {
	st, _, err := writeOf("T", nil, source.RowChange{Kind: source.ChangeInsert,
		Values: map[string]any{"n": int64(1)}})
	if err != nil {
		t.Fatal(err)
	}
	if w := opOf(t, st); w.cond != "" {
		t.Errorf("its condition is %q", w.cond)
	}
}

// The plan's statement says what the call is, in words a person reads before
// consenting to it — the item's key among them.
func TestWhatThePlanSays(t *testing.T) {
	for name, c := range map[string]struct {
		change source.RowChange
		has    []string
	}{
		"an insert": {source.RowChange{Kind: source.ChangeInsert, Values: map[string]any{"pk": "a"}},
			[]string{"PutItem", "T", "pk"}},
		"a change": {source.RowChange{Kind: source.ChangeUpdate, Key: []any{"a"},
			Values: map[string]any{"n": int64(1)}}, []string{"UpdateItem", "T", "pk = a", "SET"}},
		"a delete": {source.RowChange{Kind: source.ChangeDelete, Key: []any{"a"}},
			[]string{"DeleteItem", "T", "pk = a"}},
	} {
		t.Run(name, func(t *testing.T) {
			st, _, err := writeOf("T", []string{"pk"}, c.change)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range c.has {
				if !strings.Contains(st.SQL, want) {
					t.Errorf("it reads %q, which does not mention %q", st.SQL, want)
				}
			}
		})
	}
}

// Nothing but a plan made here is run: a plan carrying no call at all would
// otherwise be applied as a loop over nothing and reported as a success.
func TestAPlanMadeElsewhereIsNotRun(t *testing.T) {
	s := &dynamoSource{}
	out, err := s.Apply(t.Context(), &source.WritePlan{
		Target:     model.NewRef(model.KindCollection, "r", "T"),
		Statements: []source.Statement{{SQL: "PutItem on T"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Err == nil || !strings.Contains(out.Err.Error(), "not made here") {
		t.Errorf("it said %+v", out)
	}
}

// A plan for something that is not a collection is refused before any call.
func TestAPlanForSomethingElseIsRefused(t *testing.T) {
	s := &dynamoSource{}
	if _, err := s.Apply(t.Context(), &source.WritePlan{
		Target: model.NewRef(model.KindIndex, "r", "T", "i")}); err == nil {
		t.Error("it applied a plan to an index")
	}
}

// An unknown kind of change is refused rather than made into nothing.
func TestAnUnknownKindOfCallIsRefused(t *testing.T) {
	w := &itemWrite{kind: source.ChangeKind(9), table: "T"}
	if _, err := w.run(t.Context(), nil); err == nil {
		t.Error("it made a call of an unknown kind")
	}
}

// The expression's optional parts are left out when they are empty, because
// the SDK sends an empty map as an empty map and the service refuses one.
func TestEmptyPartsOfACallAreLeftOut(t *testing.T) {
	if condOrNil("") != nil {
		t.Error("an empty condition was sent")
	}
	if got := condOrNil("x"); got == nil || *got != "x" {
		t.Errorf("a condition became %v", got)
	}
	if namesOrNil(nil) != nil || namesOrNil(map[string]string{}) != nil {
		t.Error("an empty set of names was sent")
	}
	if got := namesOrNil(map[string]string{"#c0": "a"}); len(got) != 1 {
		t.Errorf("the names became %v", got)
	}
	if valuesOrNil(nil) != nil || valuesOrNil(map[string]ddbtypes.AttributeValue{}) != nil {
		t.Error("an empty set of values was sent")
	}
	if got := valuesOrNil(map[string]ddbtypes.AttributeValue{":v0": s("a")}); len(got) != 1 {
		t.Errorf("the values became %v", got)
	}
}

// A changeset of nothing but new items needs no key: nothing addresses them
// yet, which is what lets a file be imported into a table without one.
func TestWhatCountsAsInsertsOnly(t *testing.T) {
	if !insertsOnly([]source.RowChange{{Kind: source.ChangeInsert}, {Kind: source.ChangeInsert}}) {
		t.Error("two new items are not inserts only")
	}
	for name, changes := range map[string][]source.RowChange{
		"a change among them": {{Kind: source.ChangeInsert}, {Kind: source.ChangeUpdate}},
		"a delete among them": {{Kind: source.ChangeInsert}, {Kind: source.ChangeDelete}},
	} {
		if insertsOnly(changes) {
			t.Errorf("%s reads as inserts only", name)
		}
	}
	if !insertsOnly(nil) {
		t.Error("no changes at all is not inserts only")
	}
}

// A key is the table's own key, attribute for attribute and in order: an
// UpdateItem with the wrong key creates an item rather than changing one.
func TestWhichKeysAreTheTables(t *testing.T) {
	for name, c := range map[string]struct {
		given, want []string
		same        bool
	}{
		"the same":        {[]string{"pk", "sk"}, []string{"pk", "sk"}, true},
		"the other way":   {[]string{"sk", "pk"}, []string{"pk", "sk"}, false},
		"half of it":      {[]string{"pk"}, []string{"pk", "sk"}, false},
		"one more":        {[]string{"pk", "sk", "x"}, []string{"pk", "sk"}, false},
		"something else":  {[]string{"id"}, []string{"pk"}, false},
		"neither has one": {nil, nil, true},
	} {
		t.Run(name, func(t *testing.T) {
			if got := sameKey(c.given, c.want); got != c.same {
				t.Errorf("%v against %v reads %v", c.given, c.want, got)
			}
		})
	}
}
