package mongo

import (
	"errors"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	mongodriver "go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// keys builds an index's key document, as the server sends it.
func keys(t *testing.T, pairs ...any) bson.Raw {
	t.Helper()
	d := bson.D{}
	for i := 0; i+1 < len(pairs); i += 2 {
		d = append(d, bson.E{Key: pairs[i].(string), Value: pairs[i+1]})
	}
	raw, err := bson.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestIndexKeysReadTheirDirection(t *testing.T) {
	got := indexKeys(keys(t, "name", int32(1), "score", int32(-1)))
	if len(got) != 2 {
		t.Fatalf("keys %+v", got)
	}
	if got[0].Name != "name" || got[0].Descending {
		t.Errorf("first key %+v, want name ascending", got[0])
	}
	if got[1].Name != "score" || !got[1].Descending {
		t.Errorf("second key %+v, want score descending", got[1])
	}
	// The keys stay in the order the index has them: a compound index on
	// (a, b) is not the index on (b, a).
	if got := indexKeys(keys(t, "b", int32(1), "a", int32(1))); got[0].Name != "b" {
		t.Errorf("keys %+v, want the index's own order", got)
	}
	// A double is a direction too: some servers send 1.0.
	if got := indexKeys(keys(t, "x", -1.0)); !got[0].Descending {
		t.Errorf("key %+v, want descending", got[0])
	}
	// A kind of index is not a direction.
	got = indexKeys(keys(t, "body", "text", "loc", "2dsphere"))
	if got[0].Expression != "text" || got[0].Descending {
		t.Errorf("key %+v, want a text index", got[0])
	}
	if got[1].Expression != "2dsphere" {
		t.Errorf("key %+v, want a geospatial index", got[1])
	}
	// A key document that is not one leaves no keys behind.
	if got := indexKeys(bson.Raw("not bson")); got != nil {
		t.Errorf("keys %+v from a broken document", got)
	}
}

func TestIndexOfReadsWhatTheServerSaid(t *testing.T) {
	yes, no := true, false
	ttl := int32(3600)
	spec := mongodriver.IndexSpecification{
		Name: "email_1", KeysDocument: keys(t, "email", int32(1)),
		Unique: &yes, Sparse: &no, ExpireAfterSeconds: &ttl,
	}
	idx := indexOf(spec)
	if idx.Name != "email_1" || !idx.Unique || idx.Sparse || idx.TTL != 3600 {
		t.Errorf("index %+v", idx)
	}
	if len(idx.Keys) != 1 || idx.Keys[0].Name != "email" {
		t.Errorf("keys %+v", idx.Keys)
	}
	// A sparse index is one.
	sparse := indexOf(mongodriver.IndexSpecification{
		Name: "opt_1", KeysDocument: keys(t, "opt", int32(1)), Sparse: &yes, Unique: &no})
	if !sparse.Sparse || sparse.Unique {
		t.Errorf("index %+v, want it sparse and not unique", sparse)
	}
	// What the server left unsaid is left unset, not guessed.
	bare := indexOf(mongodriver.IndexSpecification{Name: "_id_", KeysDocument: keys(t, "_id", int32(1))})
	if bare.Unique || bare.Sparse || bare.TTL != 0 {
		t.Errorf("index %+v, want nothing claimed the server did not say", bare)
	}
}

func TestIndexSummarySaysWhatItIs(t *testing.T) {
	cases := []struct {
		idx  model.DocumentIndex
		want string
	}{
		{model.DocumentIndex{Keys: []model.IndexColumn{{Name: "name"}}}, "name"},
		{model.DocumentIndex{Keys: []model.IndexColumn{{Name: "a"}, {Name: "b", Descending: true}}}, "a, b ↓"},
		{model.DocumentIndex{Keys: []model.IndexColumn{{Name: "body", Expression: "text"}}}, "body text"},
		{model.DocumentIndex{Keys: []model.IndexColumn{{Name: "e"}}, Unique: true}, "e · unique"},
		{model.DocumentIndex{Keys: []model.IndexColumn{{Name: "e"}}, Unique: true, Sparse: true}, "e · unique, sparse"},
		{model.DocumentIndex{Keys: []model.IndexColumn{{Name: "at"}}, TTL: 60}, "at · expires after 60s"},
	}
	for _, tc := range cases {
		if got := indexSummary(tc.idx); got != tc.want {
			t.Errorf("summary %q, want %q", got, tc.want)
		}
	}
}

func TestUnsupportedHere(t *testing.T) {
	// A view has no indexes, and a collection that has gone has nothing at
	// all; both are the server saying so, not failing.
	for _, err := range []error{
		mongodriver.CommandError{Name: "CommandNotSupportedOnView"},
		mongodriver.CommandError{Name: "NamespaceNotFound"},
		mongodriver.CommandError{Code: 166},
		mongodriver.CommandError{Code: 26},
	} {
		if !unsupportedHere(err) {
			t.Errorf("%v is taken for a failure", err)
		}
	}
	for _, err := range []error{
		mongodriver.CommandError{Name: "HostUnreachable", Code: 6},
		mongodriver.CommandError{Name: "Unauthorized", Code: 13},
		errors.New("the network went away"),
	} {
		if unsupportedHere(err) {
			t.Errorf("%v is taken for the server saying no", err)
		}
	}
}
