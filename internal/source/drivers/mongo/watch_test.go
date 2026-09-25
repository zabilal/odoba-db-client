package mongo

import (
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// A document's key as one column can hold it (FR-12.5). A collection's keys are
// not all of one type — that is the point of a document store — and a column
// that could only hold one of them could not hold a change stream's rows.
func TestADocumentsKeyAsOneColumnHoldsIt(t *testing.T) {
	id := bson.NewObjectID()
	for _, c := range []struct {
		of   any
		want string
	}{
		{"user:1", "user:1"},
		// The hex an ObjectID is written as everywhere else, not the struct.
		{id, id.Hex()},
		{nil, ""},
		{42, "42"},
	} {
		if got := keyText(c.of); got != c.want {
			t.Errorf("keyText(%#v) is %q, want %q", c.of, got, c.want)
		}
	}
}

// Only a collection can be followed, which the window asks before it offers the
// control: a change stream over a whole database or deployment is a different
// object to follow, and one over an index is nothing at all.
func TestOnlyACollectionIsFollowed(t *testing.T) {
	s := &mongoSource{}
	for _, c := range []struct {
		ref  model.ObjectRef
		want bool
	}{
		{model.NewRef(model.KindCollection, "shop", "orders"), true},
		{model.NewRef(model.KindCollection, "shop"), false},
		{model.NewRef(model.KindDatabase, "shop"), false},
		{model.NewRef(model.KindIndex, "shop", "orders", "by_total"), false},
	} {
		if got := s.CanFollow(c.ref); got != c.want {
			t.Errorf("CanFollow(%s) is %v", c.ref, got)
		}
	}
}
