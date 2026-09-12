package mongo

import (
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// stages renders a parsed pipeline as extended JSON, for comparison.
func stages(t *testing.T, text string) string {
	t.Helper()
	got, err := parsePipeline(text)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	parts := make([]string, 0, len(got))
	for _, st := range got {
		parts = append(parts, extJSON(st))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func TestAPipelineIsReadAsItIsWritten(t *testing.T) {
	got := stages(t, `[{"$match": {"score": {"$gt": 10}}}, {"$group": {"_id": "$name", "n": {"$sum": 1}}}]`)
	want := `[{"$match":{"score":{"$gt":10}}},{"$group":{"_id":"$name","n":{"$sum":1}}}]`
	if got != want {
		t.Errorf("read as %s, want %s", got, want)
	}
	// The stages keep the order they were written in: a pipeline is an
	// order, and matching after grouping is not matching before it.
	got = stages(t, `[{"$sort": {"n": -1}}, {"$limit": 5}]`)
	if !strings.HasPrefix(got, `[{"$sort"`) {
		t.Errorf("read as %s, want the stages in order", got)
	}
	// One stage may be written without its brackets.
	if got := stages(t, `{"$count": "n"}`); got != `[{"$count":"n"}]` {
		t.Errorf("one stage is read as %s", got)
	}
	// Extended JSON is read, so a pipeline may name an ObjectID.
	got = stages(t, `[{"$match": {"_id": {"$oid": "64b1c0de64b1c0de64b1c0de"}}}]`)
	if !strings.Contains(got, `"$oid"`) {
		t.Errorf("read as %s, want the identifier kept", got)
	}
}

func TestWhatIsNotAPipelineIsRefused(t *testing.T) {
	cases := []struct{ text, says string }{
		{"", "empty"},
		{"   ", "empty"},
		{`"a string"`, "array of stages"},
		{`[`, "not an array"},
		{`[{"$match": }]`, "not an array"},
		{`[]`, "no stages"},
		{`[{}]`, "stage 1 is empty"},
		{`{"$match"`, "not a document"},
	}
	for _, tc := range cases {
		_, err := parsePipeline(tc.text)
		if err == nil {
			t.Errorf("%q was read as a pipeline", tc.text)
			continue
		}
		if !strings.Contains(err.Error(), tc.says) {
			t.Errorf("%q is refused with %q, want it to say %q", tc.text, err, tc.says)
		}
	}
}

func TestAPipelineThatWritesIsKnownToWrite(t *testing.T) {
	for _, text := range []string{
		`[{"$match": {"a": 1}}, {"$out": "copies"}]`,
		`[{"$merge": {"into": "copies"}}]`,
	} {
		p, err := parsePipeline(text)
		if err != nil {
			t.Fatal(err)
		}
		if !writes(p) {
			t.Errorf("%s is not seen to write", text)
		}
	}
	for _, text := range []string{
		`[{"$match": {"a": 1}}]`,
		`[{"$group": {"_id": "$a"}}, {"$sort": {"_id": 1}}]`,
		// A field called $out inside a stage is a field, not a stage.
		`[{"$match": {"$out": 1}}]`,
	} {
		p, err := parsePipeline(text)
		if err != nil {
			t.Fatal(err)
		}
		if writes(p) {
			t.Errorf("%s is seen to write", text)
		}
	}
}

func TestAPipelinesColumnsAreWhatItProduced(t *testing.T) {
	doc := func(t *testing.T, d bson.D) bson.Raw {
		t.Helper()
		raw, err := bson.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	// Every field any document holds is a column, in the order first seen.
	cols := pipelineColumns([]bson.Raw{
		doc(t, bson.D{{Key: "name", Value: "Ada"}, {Key: "n", Value: 2}}),
		doc(t, bson.D{{Key: "name", Value: "Grace"}, {Key: "extra", Value: true}}),
	})
	if len(cols) != 3 || cols[0].Name != "name" || cols[1].Name != "n" || cols[2].Name != "extra" {
		t.Errorf("columns %+v, want every field in the order first seen", cols)
	}
	// A column of a pipeline's making may hold anything, and says so.
	if cols[0].Type.Class != model.TypeUnknown || !cols[0].Type.Nullable {
		t.Errorf("a column is %+v, want nothing claimed about it", cols[0].Type)
	}
	// _id leads where a stage kept it.
	cols = pipelineColumns([]bson.Raw{doc(t, bson.D{{Key: "n", Value: 1}, {Key: "_id", Value: "x"}})})
	if cols[0].Name != "_id" {
		t.Errorf("columns %+v, want the identifier first", cols)
	}
	// A pipeline that produced nothing still has a column to draw.
	if cols := pipelineColumns(nil); len(cols) != 1 || cols[0].Name != "_id" {
		t.Errorf("columns %+v for nothing produced", cols)
	}
}
