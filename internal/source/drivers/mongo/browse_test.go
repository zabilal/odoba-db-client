package mongo

import (
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// query renders a filter set as the query document the server would get.
func query(t *testing.T, filters ...source.Filter) string {
	t.Helper()
	d, err := filterOf(filters)
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	raw, err := bson.MarshalExtJSON(d, true, false)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestFiltersBecomeAQueryDocument(t *testing.T) {
	cases := []struct {
		f    source.Filter
		want string
	}{
		{source.Filter{Column: "score", Op: source.OpEqual, Values: []any{int32(7)}},
			`{"score":{"$eq":{"$numberInt":"7"}}}`},
		{source.Filter{Column: "score", Op: source.OpGreater, Values: []any{int32(7)}},
			`{"score":{"$gt":{"$numberInt":"7"}}}`},
		{source.Filter{Column: "score", Op: source.OpLessEqual, Values: []any{int32(7)}},
			`{"score":{"$lte":{"$numberInt":"7"}}}`},
		{source.Filter{Column: "name", Op: source.OpIn, Values: []any{"a", "b"}},
			`{"name":{"$in":["a","b"]}}`},
		{source.Filter{Column: "name", Op: source.OpNotIn, Values: []any{"a"}},
			`{"name":{"$nin":["a"]}}`},
		{source.Filter{Column: "score", Op: source.OpBetween, Values: []any{int32(1), int32(9)}},
			`{"score":{"$gte":{"$numberInt":"1"},"$lte":{"$numberInt":"9"}}}`},
		// A field that is not there reads as empty, as it does in the grid.
		{source.Filter{Column: "nick", Op: source.OpIsNull},
			`{"nick":{"$eq":null}}`},
		{source.Filter{Column: "nick", Op: source.OpIsNotNull},
			`{"nick":{"$ne":null}}`},
		// LIKE is a pattern, and its own characters are not a pattern.
		{source.Filter{Column: "name", Op: source.OpLike, Values: []any{"A%d_"}},
			`{"name":{"$regularExpression":{"pattern":"^A.*d.$","options":""}}}`},
		// A pattern's own characters are text, not more pattern.
		{source.Filter{Column: "name", Op: source.OpLike, Values: []any{"a.b%"}},
			`{"name":{"$regularExpression":{"pattern":"^a\\.b.*$","options":""}}}`},
		{source.Filter{Column: "name", Op: source.OpContains, Values: []any{"a.b"}},
			`{"name":{"$regularExpression":{"pattern":"a\\.b","options":"i"}}}`},
		{source.Filter{Column: "name", Op: source.OpRegex, Values: []any{"^a.b$"}},
			`{"name":{"$regularExpression":{"pattern":"^a.b$","options":""}}}`},
	}
	for _, tc := range cases {
		if got := query(t, tc.f); got != tc.want {
			t.Errorf("%s %s: %s, want %s", tc.f.Column, tc.f.Op, got, tc.want)
		}
	}
}

func TestFiltersAreAndedAndNegated(t *testing.T) {
	got := query(t,
		source.Filter{Column: "a", Op: source.OpEqual, Values: []any{"x"}},
		source.Filter{Column: "b", Op: source.OpEqual, Values: []any{"y"}},
	)
	want := `{"$and":[{"a":{"$eq":"x"}},{"b":{"$eq":"y"}}]}`
	if got != want {
		t.Errorf("two filters are %s, want %s", got, want)
	}
	// One filter is itself, not an $and of one.
	if got := query(t, source.Filter{Column: "a", Op: source.OpEqual, Values: []any{"x"}}); got != `{"a":{"$eq":"x"}}` {
		t.Errorf("one filter is %s", got)
	}
	// Negation inverts the condition, not the field.
	got = query(t, source.Filter{Column: "a", Op: source.OpEqual, Values: []any{"x"}, Negate: true})
	if got != `{"a":{"$not":{"$eq":"x"}}}` {
		t.Errorf("a negated filter is %s", got)
	}
	if _, err := filterOf([]source.Filter{{Column: "a", Op: source.OpEqual}}); err == nil {
		t.Error("a filter with no value was accepted")
	}
	if _, err := filterOf([]source.Filter{{Column: "a", Op: "≈"}}); err == nil {
		t.Error("an operator this source has no notion of was accepted")
	}
	if got := query(t); got != `{}` {
		t.Errorf("no filters are %s, want everything", got)
	}
}

func TestSortAndProjection(t *testing.T) {
	got := sortOf([]source.Sort{{Column: "a"}, {Column: "b", Descending: true}})
	if len(got) != 2 || got[0].Value != 1 || got[1].Value != -1 {
		t.Errorf("sort %v", got)
	}
	if got[0].Key != "a" || got[1].Key != "b" {
		t.Errorf("sort %v, want the terms in order", got)
	}
	// _id comes back whether it was asked for or not: without it a document
	// cannot be told from another.
	p := projectionOf([]string{"name"})
	if len(p) != 2 || p[1].Key != "_id" {
		t.Errorf("projection %v, want the identifier too", p)
	}
	if p := projectionOf([]string{"_id", "name"}); len(p) != 2 {
		t.Errorf("projection %v, want the identifier once", p)
	}
}

func TestAColumnIsWhatTheSampleSaw(t *testing.T) {
	ref := model.NewRef(model.KindCollection, "shop", "people")
	one := columnOf(model.InferredField{Name: "name", Presence: 1,
		Types: []model.ObservedType{{Type: model.DataType{Class: model.TypeString, Native: "string"}}}}, ref)
	if one.Type.Class != model.TypeString || one.Type.Nullable {
		t.Errorf("name is %+v, want a string every document has", one.Type)
	}
	if one.Origin.Name() != "people" || one.OriginColumn != "name" {
		t.Errorf("name comes from %v.%s", one.Origin, one.OriginColumn)
	}
	// A field only some documents have may be empty.
	some := columnOf(model.InferredField{Name: "nick", Presence: 0.5,
		Types: []model.ObservedType{{Type: model.DataType{Class: model.TypeString, Native: "string"}}}}, ref)
	if !some.Type.Nullable {
		t.Errorf("nick is %+v, want it able to be empty", some.Type)
	}
	// A field seen with two types is neither: the grid draws what the value
	// is, and says what was seen.
	both := columnOf(model.InferredField{Name: "score", Presence: 0.5, Types: []model.ObservedType{
		{Type: model.DataType{Class: model.TypeInteger, Native: "int"}},
		{Type: model.DataType{Class: model.TypeString, Native: "string"}},
	}}, ref)
	if both.Type.Class != model.TypeUnknown || both.Type.Native != "int or string" {
		t.Errorf("score is %+v, want neither type and both named", both.Type)
	}
	if !both.Type.Nullable {
		t.Errorf("score is %+v, want it able to be empty: half the documents lack it", both.Type)
	}
}

func TestADocumentBecomesARow(t *testing.T) {
	oid := bson.NewObjectID()
	when := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	dec, err := bson.ParseDecimal128("1.250")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := bson.Marshal(bson.D{
		{Key: "_id", Value: oid},
		{Key: "name", Value: "Ada"},
		{Key: "score", Value: int32(42)},
		{Key: "big", Value: int64(1) << 40},
		{Key: "rate", Value: 1.5},
		{Key: "ok", Value: true},
		{Key: "when", Value: when},
		{Key: "dec", Value: dec},
		{Key: "re", Value: bson.Regex{Pattern: "^a", Options: "i"}},
		{Key: "uid", Value: bson.Binary{Subtype: bson.TypeBinaryUUID, Data: make([]byte, 16)}},
		{Key: "raw", Value: bson.Binary{Subtype: 0, Data: []byte{1, 2}}},
		{Key: "none", Value: nil},
		{Key: "address", Value: bson.D{{Key: "city", Value: "Kyoto"}}},
		{Key: "tags", Value: bson.A{"a", "b"}},
		{Key: "unseen", Value: "kept by the server, not drawn"},
	})
	if err != nil {
		t.Fatal(err)
	}
	names := []string{"_id", "name", "score", "big", "rate", "ok", "when", "dec", "re", "uid", "raw", "none", "address", "tags", "missing"}
	cols := make([]model.ColumnDef, 0, len(names))
	for _, n := range names {
		cols = append(cols, model.ColumnDef{Name: n})
	}
	row := rowOf(raw, cols)
	if len(row) != len(cols) {
		t.Fatalf("row is %d wide, want %d", len(row), len(cols))
	}
	at := func(name string) any {
		for i, c := range cols {
			if c.Name == name {
				return row[i]
			}
		}
		return nil
	}
	if at("_id") != oid.Hex() {
		t.Errorf("_id is %v, want its hex", at("_id"))
	}
	if at("name") != "Ada" || at("score") != int64(42) || at("big") != int64(1)<<40 {
		t.Errorf("row %v", row)
	}
	if at("rate") != 1.5 || at("ok") != true {
		t.Errorf("row %v", row)
	}
	if got, ok := at("when").(time.Time); !ok || !got.Equal(when) {
		t.Errorf("when is %v, want the instant", at("when"))
	}
	if at("dec") != model.Decimal("1.250") {
		t.Errorf("dec is %v, want its digits kept as they are", at("dec"))
	}
	if at("re") != "/^a/i" {
		t.Errorf("re is %v", at("re"))
	}
	if at("uid") != "00000000-0000-0000-0000-000000000000" {
		t.Errorf("uid is %v, want it written as a uuid", at("uid"))
	}
	if got, ok := at("raw").([]byte); !ok || len(got) != 2 {
		t.Errorf("raw is %v, want its bytes", at("raw"))
	}
	if at("none") != nil || at("missing") != nil {
		t.Errorf("a null and a field that is not there are %v and %v, want neither", at("none"), at("missing"))
	}
	// An embedded document and an array come back as themselves, which is
	// what the grid draws compactly and the viewer opens in full.
	doc, ok := at("address").(map[string]any)
	if !ok || doc["city"] != "Kyoto" {
		t.Errorf("address is %v", at("address"))
	}
	arr, ok := at("tags").([]any)
	if !ok || len(arr) != 2 || arr[0] != "a" {
		t.Errorf("tags are %v", at("tags"))
	}
	// A document that is not one leaves an empty row rather than failing.
	if got := rowOf(bson.Raw("not bson"), cols); len(got) != len(cols) {
		t.Errorf("a broken document is %v", got)
	}
}
