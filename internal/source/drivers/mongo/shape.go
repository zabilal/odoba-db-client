package mongo

import (
	"context"
	"fmt"
	"sort"

	"go.mongodb.org/mongo-driver/v2/bson"
	mongodriver "go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
)

// Shape inference (FR-12.4). A collection has no declared structure: what it
// holds is whatever its documents hold, and the only way to know is to read
// some of them. So the answer carries its own evidence — how many documents
// were read, every type each field was seen with, and in how many of them —
// and the UI shows it as a sample, never as a schema.

const (
	// defaultSample is how many documents are read when a caller asks for no
	// particular number: enough for a rare field to show up, few enough to
	// come back while a person is still looking at the panel.
	defaultSample = 200

	// maxSample bounds what a caller can ask for. Inference is a convenience,
	// not an export.
	maxSample = 10000

	// maxDepth bounds how far into embedded documents inference goes, and
	// maxFields how many it will record at one level. A document store
	// permits structures no one can read; the panel would be the first to
	// drown in one.
	maxDepth  = 4
	maxFields = 200
)

// InferShape samples a collection's documents and reports what they hold.
func (s *mongoSource) InferShape(ctx context.Context, ref model.ObjectRef, n int) (_ *model.DocumentShape, err error) {
	defer panics.Recover(&err, "sampling a collection's documents")
	if ref.Kind != model.KindCollection || len(ref.Path) < 2 {
		return nil, fmt.Errorf("mongodb: %s has no documents to sample", ref)
	}
	switch {
	case n <= 0:
		n = defaultSample
	case n > maxSample:
		n = maxSample
	}
	coll := s.client.Database(ref.Path[0]).Collection(ref.Path[1])
	// $sample reads a random spread rather than the first n documents, which
	// on a collection written over time are the oldest and the least like the
	// rest of it.
	cur, err := coll.Aggregate(ctx, mongodriver.Pipeline{
		bson.D{{Key: "$sample", Value: bson.D{{Key: "size", Value: int64(n)}}}},
	})
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)

	root := &shapeNode{children: map[string]*shapeNode{}}
	var sampled int64
	for cur.Next(ctx) {
		sampled++
		root.observeDocument(cur.Current, 0)
	}
	if err := cur.Err(); err != nil {
		return nil, err
	}
	return &model.DocumentShape{Sampled: sampled, Fields: root.fields(sampled)}, nil
}

// shapeNode is one field while it is being read: how often it was there, the
// types it held, and what was inside it where it held a document or an array.
type shapeNode struct {
	seen int64 // documents this field was present in
	// inner is how many documents were read inside this field: one for each
	// embedded document, and one for each document in an array. It is what
	// the fields below are counted against, so a field in every member of
	// every array reads as present in all of them.
	inner int64

	types      map[string]*observed
	typeOrder  []string
	children   map[string]*shapeNode
	childOrder []string
}

type observed struct {
	t     model.DataType
	count int64
}

// observeDocument reads one document's fields into the node's children.
func (n *shapeNode) observeDocument(doc bson.Raw, depth int) {
	elems, err := doc.Elements()
	if err != nil {
		return
	}
	for _, e := range elems {
		n.child(e.Key()).observe(e.Value(), depth)
	}
}

// observe records one value of this field.
func (n *shapeNode) observe(v bson.RawValue, depth int) {
	n.seen++
	n.addType(typeOf(v))
	if depth >= maxDepth {
		return
	}
	switch v.Type {
	case bson.TypeEmbeddedDocument:
		n.inner++
		n.observeDocument(v.Document(), depth+1)
	case bson.TypeArray:
		// An array's members are read as the field's own: what matters about
		// items is what an item holds, not that the array has three of them.
		vals, err := v.Array().Values()
		if err != nil {
			return
		}
		for _, item := range vals {
			if item.Type == bson.TypeEmbeddedDocument {
				n.inner++
				n.observeDocument(item.Document(), depth+1)
			}
		}
	}
}

func (n *shapeNode) child(name string) *shapeNode {
	if c, ok := n.children[name]; ok {
		return c
	}
	c := &shapeNode{children: map[string]*shapeNode{}}
	if len(n.children) < maxFields {
		n.children[name] = c
		n.childOrder = append(n.childOrder, name)
	}
	return c
}

func (n *shapeNode) addType(t model.DataType) {
	if n.types == nil {
		n.types = map[string]*observed{}
	}
	o, ok := n.types[t.Native]
	if !ok {
		o = &observed{t: t}
		n.types[t.Native] = o
		n.typeOrder = append(n.typeOrder, t.Native)
	}
	o.count++
}

// fields renders the node's children, in the order a person reads them: the
// identifier every document has first, then the fields most documents hold,
// then by name. Ties broken by name keep two runs of the same sample in the
// same order.
func (n *shapeNode) fields(of int64) []model.InferredField {
	if len(n.childOrder) == 0 {
		return nil
	}
	out := make([]model.InferredField, 0, len(n.childOrder))
	for _, name := range n.childOrder {
		c := n.children[name]
		f := model.InferredField{Name: name, Types: c.observedTypes()}
		if of > 0 {
			f.Presence = float64(c.seen) / float64(of)
		}
		f.Children = c.fields(c.inner)
		out = append(out, f)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if a, b := out[i].Name == "_id", out[j].Name == "_id"; a != b {
			return a
		}
		if out[i].Presence != out[j].Presence {
			return out[i].Presence > out[j].Presence
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// observedTypes are the types this field was seen with, the most frequent
// first: a field holding both a string and a number is a real and common
// condition, and the shape says so rather than choosing one.
func (n *shapeNode) observedTypes() []model.ObservedType {
	out := make([]model.ObservedType, 0, len(n.typeOrder))
	for _, native := range n.typeOrder {
		o := n.types[native]
		out = append(out, model.ObservedType{Type: o.t, Count: o.count})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Type.Native < out[j].Type.Native
	})
	return out
}

// typeOf maps a BSON type onto the model's classes, which is what the grid
// renders and edits by. The native name is the one MongoDB itself uses, so
// that what the panel says can be looked up.
func typeOf(v bson.RawValue) model.DataType {
	switch v.Type {
	case bson.TypeDouble:
		return model.DataType{Class: model.TypeFloat, Native: "double"}
	case bson.TypeString:
		return model.DataType{Class: model.TypeString, Native: "string"}
	case bson.TypeEmbeddedDocument:
		return model.DataType{Class: model.TypeStruct, Native: "object"}
	case bson.TypeArray:
		return arrayType(v)
	case bson.TypeBinary:
		if sub, _ := v.Binary(); sub == bson.TypeBinaryUUID || sub == bson.TypeBinaryUUIDOld {
			return model.DataType{Class: model.TypeUUID, Native: "uuid"}
		}
		return model.DataType{Class: model.TypeBytes, Native: "binData"}
	case bson.TypeObjectID:
		return model.DataType{Class: model.TypeString, Native: "objectId"}
	case bson.TypeBoolean:
		return model.DataType{Class: model.TypeBool, Native: "bool"}
	case bson.TypeDateTime:
		return model.DataType{Class: model.TypeTimestamp, Native: "date", TimeZone: true}
	case bson.TypeNull:
		// A null is not a type of its own, but that a field is sometimes
		// null is exactly what inference is asked for.
		return model.DataType{Class: model.TypeUnknown, Native: "null", Nullable: true}
	case bson.TypeRegex:
		return model.DataType{Class: model.TypeString, Native: "regex"}
	case bson.TypeJavaScript:
		return model.DataType{Class: model.TypeString, Native: "javascript"}
	case bson.TypeCodeWithScope:
		return model.DataType{Class: model.TypeStruct, Native: "javascriptWithScope"}
	case bson.TypeSymbol:
		return model.DataType{Class: model.TypeString, Native: "symbol"}
	case bson.TypeDBPointer:
		return model.DataType{Class: model.TypeString, Native: "dbPointer"}
	case bson.TypeInt32:
		return model.DataType{Class: model.TypeInteger, Native: "int"}
	case bson.TypeTimestamp:
		return model.DataType{Class: model.TypeTimestamp, Native: "timestamp"}
	case bson.TypeInt64:
		return model.DataType{Class: model.TypeInteger, Native: "long"}
	case bson.TypeDecimal128:
		return model.DataType{Class: model.TypeDecimal, Native: "decimal"}
	case bson.TypeMinKey:
		return model.DataType{Class: model.TypeUnknown, Native: "minKey"}
	case bson.TypeMaxKey:
		return model.DataType{Class: model.TypeUnknown, Native: "maxKey"}
	case bson.TypeUndefined:
		return model.DataType{Class: model.TypeUnknown, Native: "undefined"}
	}
	return model.DataType{Class: model.TypeUnknown, Native: "unknown"}
}

// arrayType names an array by what is in it, where everything in it is of one
// type: an array of strings is a more useful thing to be told than an array.
func arrayType(v bson.RawValue) model.DataType {
	t := model.DataType{Class: model.TypeArray, Native: "array"}
	vals, err := v.Array().Values()
	if err != nil || len(vals) == 0 {
		return t
	}
	elem := typeOf(vals[0])
	for _, item := range vals[1:] {
		if typeOf(item).Native != elem.Native {
			return t // mixed: the array is all that can be said
		}
	}
	t.Element = &elem
	t.Native = "array<" + elem.Native + ">"
	return t
}
