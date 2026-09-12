package mongo

import (
	"context"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

var peopleRef = model.NewRef(model.KindCollection, "shop", "people")

var byID = model.RowIdentity{Kind: model.IdentityDocumentID, Columns: []string{"_id"}, Target: peopleRef}

// planned renders a changeset, or fails the test.
func planned(t *testing.T, changes ...source.RowChange) *source.WritePlan {
	t.Helper()
	plan, err := (&mongoSource{}).Plan(context.Background(),
		source.Changeset{Target: peopleRef, Identity: byID, Changes: changes})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	return plan
}

const oidHex = "64b1c0de64b1c0de64b1c0de"

func TestAPlanIsTheCallsItWouldMake(t *testing.T) {
	plan := planned(t,
		source.RowChange{Kind: source.ChangeUpdate, Key: []any{oidHex},
			Values: map[string]any{"name": "Ada", "score": int64(42)}},
		source.RowChange{Kind: source.ChangeDelete, Key: []any{oidHex}},
		source.RowChange{Kind: source.ChangeInsert, Values: map[string]any{"name": "Grace"}},
	)
	if len(plan.Statements) != 3 || len(plan.Descriptions) != 3 {
		t.Fatalf("a plan of %d statements and %d descriptions", len(plan.Statements), len(plan.Descriptions))
	}
	want := []string{
		`db.people.updateOne({"_id":{"$oid":"64b1c0de64b1c0de64b1c0de"}}, {"$set":{"name":"Ada","score":42}})`,
		`db.people.deleteOne({"_id":{"$oid":"64b1c0de64b1c0de64b1c0de"}})`,
		`db.people.insertOne({"name":"Grace"})`,
	}
	for i, w := range want {
		if plan.Statements[i].SQL != w {
			t.Errorf("statement %d is\n%s\nwant\n%s", i+1, plan.Statements[i].SQL, w)
		}
	}
	if got := plan.Descriptions[0]; got != "Change name, score in the document where _id = "+oidHex {
		t.Errorf("it says %q", got)
	}
	if got := plan.Descriptions[2]; got != "Add a document: name" {
		t.Errorf("it says %q", got)
	}
	// A standalone server has no transaction, and the plan says so rather
	// than promising one.
	if plan.Atomic {
		t.Error("the plan claims a transaction this server has not got")
	}
	// Nothing is shown as a bound value: what will be written is in the
	// call, which is what a person reviews.
	for i, st := range plan.Statements {
		if len(st.Args) != 0 {
			t.Errorf("statement %d carries %d values beside the call", i+1, len(st.Args))
		}
		if st.Op == nil {
			t.Errorf("statement %d carries no call to make", i+1)
		}
	}
}

func TestAnUpdateSetsWhatChangedAndRemovesWhatWent(t *testing.T) {
	plan := planned(t, source.RowChange{Kind: source.ChangeUpdate, Key: []any{oidHex},
		Values: map[string]any{"name": "Ada", "nickname": model.Removed{}}})
	got := plan.Statements[0].SQL
	if !strings.Contains(got, `"$set":{"name":"Ada"}`) {
		t.Errorf("%s does not set what changed", got)
	}
	if !strings.Contains(got, `"$unset":{"nickname":""}`) {
		t.Errorf("%s does not remove what went", got)
	}
	// A field removed from a new document is simply not in it.
	plan = planned(t, source.RowChange{Kind: source.ChangeInsert,
		Values: map[string]any{"name": "Ada", "nickname": model.Removed{}}})
	if got := plan.Statements[0].SQL; got != `db.people.insertOne({"name":"Ada"})` {
		t.Errorf("a new document is %s", got)
	}
}

func TestAKeyIsTheDocumentsOwnIdentifier(t *testing.T) {
	// A 24-character hex string is the ObjectID it was read from.
	plan := planned(t, source.RowChange{Kind: source.ChangeDelete, Key: []any{oidHex}})
	if !strings.Contains(plan.Statements[0].SQL, `{"$oid":"`) {
		t.Errorf("%s does not address the document by its ObjectID", plan.Statements[0].SQL)
	}
	// An _id that is not one is itself: a collection may key its documents
	// however it likes.
	plan = planned(t, source.RowChange{Kind: source.ChangeDelete, Key: []any{"ada"}})
	if got := plan.Statements[0].SQL; got != `db.people.deleteOne({"_id":"ada"})` {
		t.Errorf("a string _id is addressed as %s", got)
	}
	plan = planned(t, source.RowChange{Kind: source.ChangeDelete, Key: []any{int64(7)}})
	if got := plan.Statements[0].SQL; got != `db.people.deleteOne({"_id":7})` {
		t.Errorf("a numeric _id is addressed as %s", got)
	}
	// A new document's own _id is read the same way.
	plan = planned(t, source.RowChange{Kind: source.ChangeInsert, Values: map[string]any{"_id": oidHex}})
	if !strings.Contains(plan.Statements[0].SQL, `{"$oid":"`) {
		t.Errorf("a new document's _id is %s", plan.Statements[0].SQL)
	}
}

func TestWhatCannotBeWrittenIsRefused(t *testing.T) {
	s := &mongoSource{}
	ctx := context.Background()
	cases := []struct {
		what string
		says string // what the refusal must say, so a person can act on it
		cs   source.Changeset
	}{
		{"a target that is not a collection", "documents", source.Changeset{
			Target: model.NewRef(model.KindDatabase, "shop"), Identity: byID,
			Changes: []source.RowChange{{Kind: source.ChangeDelete, Key: []any{oidHex}}}}},
		{"an object that is not a collection either", "documents", source.Changeset{
			Target: model.NewRef(model.KindIndex, "shop", "people", "_id_"), Identity: byID,
			Changes: []source.RowChange{{Kind: source.ChangeDelete, Key: []any{oidHex}}}}},
		{"documents with nothing to tell them apart", "_id", source.Changeset{
			Target:   peopleRef,
			Identity: model.RowIdentity{Kind: model.IdentityNone, Columns: []string{"_id"}, Target: peopleRef},
			Changes:  []source.RowChange{{Kind: source.ChangeDelete, Key: []any{oidHex}}}}},
		{"a key that is not the _id", "_id", source.Changeset{
			Target: peopleRef,
			Identity: model.RowIdentity{Kind: model.IdentityChosen, Target: peopleRef,
				Columns: []string{"name"}},
			Changes: []source.RowChange{{Kind: source.ChangeUpdate, Key: []any{"Ada"},
				Values: map[string]any{"score": int64(1)}}}}},
		{"a key of more than one field", "keyed by", source.Changeset{
			Target: peopleRef,
			Identity: model.RowIdentity{Kind: model.IdentityDocumentID, Target: peopleRef,
				Columns: []string{"_id", "name"}},
			Changes: []source.RowChange{{Kind: source.ChangeDelete, Key: []any{oidHex, "Ada"}}}}},
		{"an update that changes nothing", "nothing", source.Changeset{
			Target: peopleRef, Identity: byID,
			Changes: []source.RowChange{{Kind: source.ChangeUpdate, Key: []any{oidHex}}}}},
		{"a change with no key", "values were given", source.Changeset{
			Target: peopleRef, Identity: byID,
			Changes: []source.RowChange{{Kind: source.ChangeUpdate, Values: map[string]any{"a": 1}}}}},
		{"a key that is empty", "empty", source.Changeset{
			Target: peopleRef, Identity: byID,
			Changes: []source.RowChange{{Kind: source.ChangeDelete, Key: []any{nil}}}}},
	}
	for _, tc := range cases {
		_, err := s.Plan(ctx, tc.cs)
		if err == nil {
			t.Errorf("%s was planned", tc.what)
			continue
		}
		if !strings.Contains(err.Error(), tc.says) {
			t.Errorf("%s is refused with %q, want it to say %q", tc.what, err, tc.says)
		}
	}
	// New documents need no key: nothing addresses them yet.
	if _, err := s.Plan(ctx, source.Changeset{Target: peopleRef,
		Changes: []source.RowChange{{Kind: source.ChangeInsert, Values: map[string]any{"a": 1}}}}); err != nil {
		t.Errorf("new documents were refused for want of a key: %v", err)
	}
	// A plan made elsewhere is not applied here.
	out, err := s.Apply(ctx, &source.WritePlan{Target: peopleRef,
		Statements: []source.Statement{{SQL: "db.people.deleteOne({})"}}})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if out.Err == nil {
		t.Error("a plan made elsewhere was applied")
	}
}

func TestAValueIsWrittenAsWhatItIs(t *testing.T) {
	if got := bsonValue(model.Decimal("1.250")); got != bson.NewDecimal128(0x3034000000000000, 1250) {
		if d, ok := got.(bson.Decimal128); !ok || d.String() != "1.250" {
			t.Errorf("a decimal is written as %#v, want its digits kept", got)
		}
	}
	if got, ok := bsonValue([]byte{1, 2}).(bson.Binary); !ok || len(got.Data) != 2 {
		t.Errorf("bytes are written as %#v", bsonValue([]byte{1, 2}))
	}
	if got, ok := bsonValue(model.JSON(`{"a":1}`)).(bson.D); !ok || len(got) != 1 || got[0].Key != "a" {
		t.Errorf("a JSON value is written as %#v, want the document it is", bsonValue(model.JSON(`{"a":1}`)))
	}
	if got := bsonValue(model.JSON("not json")); got != "not json" {
		t.Errorf("text that is not JSON is written as %#v", got)
	}
	if got := bsonValue(model.Default{}); got != nil {
		t.Errorf("a value the server gives is written as %#v", got)
	}
	if got := bsonValue("plain"); got != "plain" {
		t.Errorf("a string is written as %#v", got)
	}
}
