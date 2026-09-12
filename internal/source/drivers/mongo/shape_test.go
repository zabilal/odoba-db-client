package mongo

import (
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// observe reads documents into a shape, as InferShape does once the server
// has answered.
func observe(t *testing.T, docs ...any) *model.DocumentShape {
	t.Helper()
	root := &shapeNode{children: map[string]*shapeNode{}}
	var n int64
	for _, d := range docs {
		raw, err := bson.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		n++
		root.observeDocument(raw, 0)
	}
	return &model.DocumentShape{Sampled: n, Fields: root.fields(n)}
}

func field(t *testing.T, shape *model.DocumentShape, name string) model.InferredField {
	t.Helper()
	for _, f := range shape.Fields {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("no field %q in %+v", name, shape.Fields)
	return model.InferredField{}
}

func names(fields []model.InferredField) []string {
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		out = append(out, f.Name)
	}
	return out
}

func TestShapeSaysHowOftenAFieldWasSeen(t *testing.T) {
	shape := observe(t,
		bson.M{"_id": 1, "name": "Ada", "score": 42},
		bson.M{"_id": 2, "name": "Grace"},
		bson.M{"_id": 3, "name": "Alan", "score": 7},
		bson.M{"_id": 4, "name": "Edsger", "nickname": "E"},
	)
	if shape.Sampled != 4 {
		t.Errorf("sampled %d, want 4", shape.Sampled)
	}
	if got := field(t, shape, "name").Presence; got != 1 {
		t.Errorf("name is in %v of them, want all", got)
	}
	if got := field(t, shape, "score").Presence; got != 0.5 {
		t.Errorf("score is in %v of them, want half", got)
	}
	if got := field(t, shape, "nickname").Presence; got != 0.25 {
		t.Errorf("nickname is in %v of them, want a quarter", got)
	}
}

func TestShapeOrdersByWhatIsMostThere(t *testing.T) {
	shape := observe(t,
		bson.M{"_id": 1, "rare": 1, "common": 1},
		bson.M{"_id": 2, "common": 1},
		bson.M{"_id": 3, "common": 1, "alike": 1},
		bson.M{"_id": 4, "blike": 1, "alike": 1},
	)
	// The identifier every document has first, then the fields most of them
	// hold, then by name where two are held as often.
	if got := names(shape.Fields); len(got) != 5 || got[0] != "_id" || got[1] != "common" || got[2] != "alike" {
		t.Errorf("fields %v, want _id, common, alike first", got)
	}
	if got := names(shape.Fields); got[3] != "blike" || got[4] != "rare" {
		t.Errorf("fields %v, want the two rarest in name order", got)
	}
}

func TestShapeKeepsEveryTypeAFieldWasSeenWith(t *testing.T) {
	shape := observe(t,
		bson.M{"v": "one"},
		bson.M{"v": int32(2)},
		bson.M{"v": int32(3)},
		bson.M{"v": nil},
	)
	f := field(t, shape, "v")
	if len(f.Types) != 3 {
		t.Fatalf("types %+v, want int, string and null", f.Types)
	}
	// The most frequent first: a field is mostly one thing and sometimes
	// another, and that is what a person needs to see.
	if f.Types[0].Type.Native != "int" || f.Types[0].Count != 2 {
		t.Errorf("first type %+v, want int seen twice", f.Types[0])
	}
	if f.Types[0].Type.Class != model.TypeInteger {
		t.Errorf("int is classed %v", f.Types[0].Type.Class)
	}
	var natives []string
	for _, ot := range f.Types {
		natives = append(natives, ot.Type.Native)
	}
	if natives[1] != "null" || natives[2] != "string" {
		t.Errorf("types %v, want the rest in name order", natives)
	}
}

func TestShapeReadsIntoDocumentsAndArrays(t *testing.T) {
	shape := observe(t,
		bson.M{"_id": 1, "address": bson.M{"city": "Kyoto", "post": "600"}, "items": []any{
			bson.M{"sku": "a", "qty": int32(1)},
			bson.M{"sku": "b"},
		}},
		bson.M{"_id": 2, "address": bson.M{"city": "Oslo"}},
	)
	addr := field(t, shape, "address")
	if addr.Types[0].Type.Class != model.TypeStruct {
		t.Errorf("address is %+v, want a document", addr.Types[0])
	}
	if got := names(addr.Children); len(got) != 2 || got[0] != "city" {
		t.Errorf("address holds %v, want city first: both documents have one", got)
	}
	if got := addr.Children[0].Presence; got != 1 {
		t.Errorf("city is in %v of the addresses, want all", got)
	}
	if got := addr.Children[1].Presence; got != 0.5 {
		t.Errorf("post is in %v of the addresses, want half", got)
	}
	// An array's members are read as the field's own, and counted against
	// the members rather than the documents.
	items := field(t, shape, "items")
	if got := names(items.Children); len(got) != 2 || got[0] != "sku" {
		t.Errorf("items hold %v, want sku and qty", got)
	}
	if got := items.Children[0].Presence; got != 1 {
		t.Errorf("sku is in %v of the items, want all", got)
	}
}

func TestShapeNamesAnArrayByWhatIsInIt(t *testing.T) {
	shape := observe(t,
		bson.M{"tags": []any{"a", "b"}, "mixed": []any{"a", int32(1)}, "empty": []any{}},
	)
	tags := field(t, shape, "tags").Types[0].Type
	if tags.Native != "array<string>" || tags.Class != model.TypeArray {
		t.Errorf("tags is %+v, want an array of strings", tags)
	}
	if tags.Element == nil || tags.Element.Class != model.TypeString {
		t.Errorf("tags' element is %+v", tags.Element)
	}
	// An array of two kinds is an array, and says no more.
	if got := field(t, shape, "mixed").Types[0].Type; got.Native != "array" || got.Element != nil {
		t.Errorf("mixed is %+v, want an array and no more", got)
	}
	if got := field(t, shape, "empty").Types[0].Type; got.Native != "array" {
		t.Errorf("empty is %+v", got)
	}
}

func TestShapeReadsBsonTypesIntoTheModelsClasses(t *testing.T) {
	oid := bson.NewObjectID()
	dec, err := bson.ParseDecimal128("1.25")
	if err != nil {
		t.Fatal(err)
	}
	shape := observe(t, bson.M{
		"oid": oid, "when": time.Now(), "n": int64(7), "f": 1.5, "yes": true,
		"raw": bson.Binary{Subtype: 0, Data: []byte{1}},
		"uid": bson.Binary{Subtype: bson.TypeBinaryUUID, Data: make([]byte, 16)},
		"dec": dec, "re": bson.Regex{Pattern: "^a"}, "ts": bson.Timestamp{T: 1, I: 2},
		"min": bson.MinKey{}, "max": bson.MaxKey{},
	})
	want := map[string]struct {
		native string
		class  model.TypeClass
	}{
		"oid":  {"objectId", model.TypeString},
		"when": {"date", model.TypeTimestamp},
		"n":    {"long", model.TypeInteger},
		"f":    {"double", model.TypeFloat},
		"yes":  {"bool", model.TypeBool},
		"raw":  {"binData", model.TypeBytes},
		"uid":  {"uuid", model.TypeUUID},
		"dec":  {"decimal", model.TypeDecimal},
		"re":   {"regex", model.TypeString},
		"ts":   {"timestamp", model.TypeTimestamp},
		"min":  {"minKey", model.TypeUnknown},
		"max":  {"maxKey", model.TypeUnknown},
	}
	for name, w := range want {
		got := field(t, shape, name).Types[0].Type
		if got.Native != w.native || got.Class != w.class {
			t.Errorf("%s is %q/%v, want %q/%v", name, got.Native, got.Class, w.native, w.class)
		}
	}
	// A date carries its zone: MongoDB keeps instants, not local times.
	if !field(t, shape, "when").Types[0].Type.TimeZone {
		t.Error("a date is stored without a zone")
	}
	// A null says the field may be null.
	if got := observe(t, bson.M{"v": nil}); !field(t, got, "v").Types[0].Type.Nullable {
		t.Error("a null value does not say the field may be null")
	}
}

func TestShapeStopsGoingDeeper(t *testing.T) {
	deep := bson.M{"a": bson.M{"b": bson.M{"c": bson.M{"d": bson.M{"e": bson.M{"f": 1}}}}}}
	shape := observe(t, deep)
	f := field(t, shape, "a")
	depth := 1
	for len(f.Children) > 0 {
		depth++
		f = f.Children[0]
	}
	if depth > maxDepth+1 {
		t.Errorf("read %d levels down, want no more than %d", depth, maxDepth+1)
	}
	if depth < 3 {
		t.Errorf("read only %d levels down", depth)
	}
}

func TestShapeStopsAtSoManyFields(t *testing.T) {
	doc := bson.M{}
	for i := 0; i < maxFields+50; i++ {
		doc[string(rune('a'+i%26))+string(rune('a'+i/26))] = i
	}
	shape := observe(t, doc)
	if len(shape.Fields) != maxFields {
		t.Errorf("%d fields, want no more than %d", len(shape.Fields), maxFields)
	}
}
