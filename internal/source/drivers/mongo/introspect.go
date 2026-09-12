package mongo

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	mongodriver "go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
)

// The tree is databases, then the classes each holds, then the objects: the
// same shape the relational drivers present, so the explorer, the tabs and
// session restore need to know nothing about documents (REQ-DB-4).
//
// A collection's own children are its classes too — its indexes now, and the
// fields inference finds later (T2.32) — because a collection has no columns
// to list in their place.

// Children lists a node's direct children.
func (s *mongoSource) Children(ctx context.Context, ref model.ObjectRef) (_ []model.Node, err error) {
	defer panics.Recover(&err, "listing what is in a "+string(ref.Kind))
	switch ref.Kind {
	case model.KindDatabase:
		return s.databaseClasses(ctx, ref)
	case model.KindFolder:
		kind, ok := model.ClassOf(ref)
		if !ok {
			return nil, fmt.Errorf("mongodb: no such class %s", ref)
		}
		switch kind {
		case model.KindCollection:
			return s.collections(ctx, ref)
		case model.KindIndex:
			return s.indexes(ctx, ref)
		}
		return nil, nil
	case model.KindCollection:
		return s.collectionClasses(ctx, ref)
	}
	return nil, nil
}

// databaseClasses are the classes a database holds: its collections, with an
// exact count, and nothing where there are none.
func (s *mongoSource) databaseClasses(ctx context.Context, ref model.ObjectRef) ([]model.Node, error) {
	specs, err := s.collectionSpecs(ctx, ref.Name())
	if err != nil {
		return nil, err
	}
	return model.ClassNodes(ref, map[model.ObjectKind]int64{
		model.KindCollection: int64(len(specs)),
	}), nil
}

// collections lists a database's collections and views, in name order.
//
// A view is a collection whose documents another collection's pipeline
// produces. It is listed among them, marked as what it is, rather than given
// a class of its own: it is read the same way, and a tree that separated them
// would say the difference matters more than it does.
func (s *mongoSource) collections(ctx context.Context, class model.ObjectRef) ([]model.Node, error) {
	db := class.Path[0]
	specs, err := s.collectionSpecs(ctx, db)
	if err != nil {
		return nil, err
	}
	out := make([]model.Node, 0, len(specs))
	for _, spec := range specs {
		n := model.Node{
			Ref:         model.NewRef(model.KindCollection, db, spec.Name),
			Label:       spec.Name,
			HasChildren: true,
			Browsable:   true,
		}
		if spec.Type != "" && spec.Type != "collection" {
			// "view" and "timeseries" are what a server says here.
			n.Attrs = map[string]string{"type": spec.Type}
		}
		out = append(out, n)
	}
	return out, nil
}

// collectionClasses are the classes a collection holds: its indexes, and none
// where it is a view, whose documents are another collection's.
func (s *mongoSource) collectionClasses(ctx context.Context, ref model.ObjectRef) ([]model.Node, error) {
	if len(ref.Path) < 2 {
		return nil, fmt.Errorf("mongodb: incomplete reference %s", ref)
	}
	specs, err := s.indexSpecs(ctx, ref.Path[0], ref.Path[1])
	if err != nil && !unsupportedHere(err) {
		return nil, err
	}
	return model.ClassNodes(ref, map[model.ObjectKind]int64{
		model.KindIndex: int64(len(specs)),
	}), nil
}

// indexes lists a collection's indexes.
func (s *mongoSource) indexes(ctx context.Context, class model.ObjectRef) ([]model.Node, error) {
	if len(class.Path) < 3 {
		return nil, fmt.Errorf("mongodb: incomplete reference %s", class)
	}
	db, coll := class.Path[0], class.Path[1]
	specs, err := s.indexSpecs(ctx, db, coll)
	if err != nil && !unsupportedHere(err) {
		return nil, err
	}
	out := make([]model.Node, 0, len(specs))
	for _, spec := range specs {
		idx := indexOf(spec)
		out = append(out, model.Node{
			Ref:   model.NewRef(model.KindIndex, db, coll, idx.Name),
			Label: idx.Name,
			Attrs: map[string]string{"type": indexSummary(idx)},
		})
	}
	return out, nil
}

// Describe loads a collection's full structure for the structure tab.
func (s *mongoSource) Describe(ctx context.Context, ref model.ObjectRef) (_ any, err error) {
	defer panics.Recover(&err, "describing a "+string(ref.Kind))
	if ref.Kind != model.KindCollection || len(ref.Path) < 2 {
		return nil, fmt.Errorf("mongodb: nothing to describe for %s", ref)
	}
	db, name := ref.Path[0], ref.Path[1]
	coll := &model.Collection{Name: name, DocumentsEstimate: -1}
	specs, err := s.indexSpecs(ctx, db, name)
	if err != nil && !unsupportedHere(err) {
		return nil, err
	}
	for _, spec := range specs {
		coll.Indexes = append(coll.Indexes, indexOf(spec))
	}
	cs, known := s.specOf(ctx, db, name)
	if known && cs.Type != "" && cs.Type != "collection" {
		coll.Attrs = map[string]string{"type": cs.Type}
	}
	if known && cs.ReadOnly {
		if coll.Attrs == nil {
			coll.Attrs = map[string]string{}
		}
		coll.Attrs["readOnly"] = "true"
	}
	if n, ok, err := s.estimate(ctx, db, name); err == nil && ok {
		coll.DocumentsEstimate = n
	}
	return coll, nil
}

// Badge is a collection's document count, which the server holds as metadata
// and does not count for (FR-2.5): an estimate, said to be one.
func (s *mongoSource) Badge(ctx context.Context, ref model.ObjectRef) (_ model.Badge, _ bool, err error) {
	defer panics.Recover(&err, "counting a collection's documents")
	if ref.Kind != model.KindCollection || len(ref.Path) < 2 {
		return model.Badge{}, false, nil
	}
	n, ok, err := s.estimate(ctx, ref.Path[0], ref.Path[1])
	if err != nil || !ok {
		return model.Badge{}, false, err
	}
	return model.Badge{Text: strconv.FormatInt(n, 10), Exact: false}, true, nil
}

// estimate is the count the server holds as metadata. A view has none: its
// documents are a pipeline's, and counting them is a query, which FR-2.5
// forbids a badge from being. ok is false there, and the error is not one.
func (s *mongoSource) estimate(ctx context.Context, db, coll string) (int64, bool, error) {
	n, err := s.client.Database(db).Collection(coll).EstimatedDocumentCount(ctx)
	switch {
	case err == nil:
		return n, true, nil
	case unsupportedHere(err):
		return 0, false, nil
	}
	return 0, false, err
}

// unsupportedHere reports the server refusing a command for what the object
// is, rather than failing to run it: a view has neither a document count nor
// indexes of its own, and a collection that has gone has neither either.
func unsupportedHere(err error) bool {
	var ce mongodriver.CommandError
	if !errors.As(err, &ce) {
		return false
	}
	switch ce.Name {
	case "CommandNotSupportedOnView", "NamespaceNotFound":
		return true
	}
	return ce.Code == 166 || ce.Code == 26
}

// collectionSpecs lists a database's collections, in name order and without
// the server's own.
//
// The server answers in no order of its own, and a tree that reordered itself
// between two refreshes would be unreadable. "system." is MongoDB's reserved
// prefix — system.views holds the view definitions the tree shows as views —
// and is hidden as every other driver hides a server's own schemas.
func (s *mongoSource) collectionSpecs(ctx context.Context, db string) ([]mongodriver.CollectionSpecification, error) {
	all, err := s.client.Database(db).ListCollectionSpecifications(ctx, bson.D{})
	if err != nil {
		return nil, err
	}
	specs := make([]mongodriver.CollectionSpecification, 0, len(all))
	for _, spec := range all {
		if !strings.HasPrefix(spec.Name, "system.") {
			specs = append(specs, spec)
		}
	}
	sort.Slice(specs, func(i, j int) bool { return specs[i].Name < specs[j].Name })
	return specs, nil
}

// specOf is what the server says one collection is: whether it is a view, and
// whether it can be written. A listing that fails leaves it unsaid rather
// than failing the structure it is only an annotation on.
func (s *mongoSource) specOf(ctx context.Context, db, name string) (mongodriver.CollectionSpecification, bool) {
	specs, err := s.collectionSpecs(ctx, db)
	if err != nil {
		return mongodriver.CollectionSpecification{}, false
	}
	for _, spec := range specs {
		if spec.Name == name {
			return spec, true
		}
	}
	return mongodriver.CollectionSpecification{}, false
}

// indexSpecs lists a collection's indexes, _id's first and the rest in name
// order: _id is the one every collection has.
func (s *mongoSource) indexSpecs(ctx context.Context, db, coll string) ([]mongodriver.IndexSpecification, error) {
	specs, err := s.client.Database(db).Collection(coll).Indexes().ListSpecifications(ctx)
	if err != nil {
		return nil, err
	}
	sort.Slice(specs, func(i, j int) bool {
		if a, b := specs[i].Name == "_id_", specs[j].Name == "_id_"; a != b {
			return a
		}
		return specs[i].Name < specs[j].Name
	})
	return specs, nil
}

// indexOf reads one index specification into the model.
func indexOf(spec mongodriver.IndexSpecification) model.DocumentIndex {
	idx := model.DocumentIndex{Name: spec.Name, Keys: indexKeys(spec.KeysDocument)}
	if spec.Unique != nil {
		idx.Unique = *spec.Unique
	}
	if spec.Sparse != nil {
		idx.Sparse = *spec.Sparse
	}
	if spec.ExpireAfterSeconds != nil {
		idx.TTL = int64(*spec.ExpireAfterSeconds)
	}
	return idx
}

// indexKeys reads an index's key document, which pairs each field with its
// direction — 1 or -1 — or with the kind of index it is: "text", "2dsphere",
// "hashed". A kind is carried as the key's expression, since it is not an
// order.
func indexKeys(doc bson.Raw) []model.IndexColumn {
	elems, err := doc.Elements()
	if err != nil {
		return nil
	}
	out := make([]model.IndexColumn, 0, len(elems))
	for _, e := range elems {
		col := model.IndexColumn{Name: e.Key()}
		switch v := e.Value(); v.Type {
		case bson.TypeInt32, bson.TypeInt64, bson.TypeDouble:
			// A direction is 1 or -1, written as any of the number types:
			// some tools send 1.0.
			n, _ := v.AsInt64OK()
			col.Descending = n < 0
		case bson.TypeString:
			col.Expression = v.StringValue()
		}
		out = append(out, col)
	}
	return out
}

// indexSummary says what an index is, in the few words a tree node can hold.
func indexSummary(idx model.DocumentIndex) string {
	var parts []string
	for _, k := range idx.Keys {
		switch {
		case k.Expression != "":
			parts = append(parts, k.Name+" "+k.Expression)
		case k.Descending:
			parts = append(parts, k.Name+" ↓")
		default:
			parts = append(parts, k.Name)
		}
	}
	out := strings.Join(parts, ", ")
	var marks []string
	if idx.Unique {
		marks = append(marks, "unique")
	}
	if idx.Sparse {
		marks = append(marks, "sparse")
	}
	if idx.TTL > 0 {
		marks = append(marks, fmt.Sprintf("expires after %ds", idx.TTL))
	}
	if len(marks) > 0 {
		out += " · " + strings.Join(marks, ", ")
	}
	return out
}
