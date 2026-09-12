//go:build conformance

package mongo

// Integration tests against a real MongoDB server (REQ-DRV-1, T2.30). They
// expect the ikigai-mongo container on port 57017 and skip if it is not
// running; IKIGAI_REQUIRE_MONGO=1 makes that a failure, as in CI.

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	mongodriver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

func port() int {
	if v := os.Getenv("IKIGAI_MONGO_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			return p
		}
	}
	return 57017
}

func liveConfig(db string) source.ConnectionConfig {
	return source.ConnectionConfig{DriverID: driverID, Host: "127.0.0.1", Port: port(), Database: db,
		TLS: source.TLSConfig{Mode: "disable"}}
}

// open connects, or skips where no server is running.
func open(t *testing.T, cfg source.ConnectionConfig) source.Source {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	src, err := Driver{}.Open(ctx, cfg)
	if err != nil {
		if os.Getenv("IKIGAI_REQUIRE_MONGO") != "" {
			t.Fatalf("mongodb required but unavailable: %v", err)
		}
		t.Skipf("no mongodb on port %d (docker start ikigai-mongo): %v", port(), err)
	}
	t.Cleanup(func() { src.Close() })
	return src
}

// seed writes a database with something of each kind in it: two collections,
// a view over one, and an index beyond the _id every collection has.
func seed(t *testing.T, src source.Source, db string) {
	t.Helper()
	s := src.(*mongoSource)
	ctx := context.Background()
	d := s.client.Database(db)
	if err := d.Drop(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.client.Database(db).Drop(context.Background()) })
	people := d.Collection("people")
	if _, err := people.InsertMany(ctx, []any{
		map[string]any{"name": "Ada", "score": 42},
		map[string]any{"name": "Grace", "score": 7},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Collection("orders").InsertOne(ctx, map[string]any{"total": 3}); err != nil {
		t.Fatal(err)
	}
	unique := true
	if _, err := people.Indexes().CreateOne(ctx, mongodriver.IndexModel{
		Keys:    bson.D{{Key: "name", Value: 1}, {Key: "score", Value: -1}},
		Options: options.Index().SetName("name_score").SetUnique(unique),
	}); err != nil {
		t.Fatal(err)
	}
	if err := d.CreateView(ctx, "high_scores", "people", mongodriver.Pipeline{
		bson.D{{Key: "$match", Value: bson.D{{Key: "score", Value: bson.D{{Key: "$gt", Value: 10}}}}}},
	}); err != nil {
		t.Fatal(err)
	}
}

// child finds one node by name.
func child(t *testing.T, nodes []model.Node, name string) model.Node {
	t.Helper()
	for _, n := range nodes {
		if n.Ref.Name() == name || n.Label == name {
			return n
		}
	}
	t.Fatalf("no %q among %v", name, labelsOf(nodes))
	return model.Node{}
}

func labelsOf(nodes []model.Node) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.Label)
	}
	return out
}

func TestLiveConnects(t *testing.T) {
	src := open(t, liveConfig("ikigai_it"))
	if err := src.Ping(context.Background()); err != nil {
		t.Errorf("ping: %v", err)
	}
	info, err := src.Info(context.Background())
	if err != nil {
		t.Fatalf("info: %v", err)
	}
	if info.Product != "MongoDB" || info.Version == "" {
		t.Errorf("server %+v, want MongoDB and a version", info)
	}
	if info.Latency <= 0 {
		t.Errorf("latency %v, want the round trip measured", info.Latency)
	}
	// Closing twice is not an error, and a closed connection stops answering.
	if err := src.Close(); err != nil {
		t.Errorf("close: %v", err)
	}
	if err := src.Close(); err != nil {
		t.Errorf("close again: %v", err)
	}
	if err := src.Ping(context.Background()); err == nil {
		t.Error("a closed connection still answers")
	}
}

func TestLiveListsDatabases(t *testing.T) {
	src := open(t, liveConfig("ikigai_it"))
	seed(t, src, "ikigai_it")
	nodes, err := src.Root(context.Background())
	if err != nil {
		t.Fatalf("root: %v", err)
	}
	var found, current bool
	for _, n := range nodes {
		if n.Ref.Kind != model.KindDatabase {
			t.Errorf("node %+v, want a database", n)
		}
		if n.Ref.Name() == "ikigai_it" {
			found = true
			current = n.Attrs["current"] == "true"
		}
	}
	if !found {
		t.Errorf("databases %v, want the one written to", nodes)
	}
	if !current {
		t.Error("the database the connection is in is not marked as such")
	}
}

func TestLiveRefusesWhatIsNotWrittenYet(t *testing.T) {
	src := open(t, liveConfig("ikigai_it"))
	ref := model.NewRef(model.KindCollection, "ikigai_it", "people")
	if _, err := src.Browse(context.Background(), ref, source.BrowseOptions{}); !errors.Is(err, errNotYet) {
		t.Errorf("browse: %v, want it said plainly that it is not written yet", err)
	}
}

func TestLiveListsTheTree(t *testing.T) {
	src := open(t, liveConfig("ikigai_it"))
	seed(t, src, "ikigai_it")
	ctx := context.Background()
	db := model.NewRef(model.KindDatabase, "ikigai_it")

	classes, err := src.Children(ctx, db)
	if err != nil {
		t.Fatalf("classes: %v", err)
	}
	if len(classes) != 1 {
		t.Fatalf("classes %v, want collections alone", labelsOf(classes))
	}
	class := classes[0]
	if kind, ok := model.ClassOf(class.Ref); !ok || kind != model.KindCollection {
		t.Fatalf("class %+v, want the collections", class)
	}
	if class.Badge == nil || class.Badge.Text != "3" || !class.Badge.Exact {
		t.Errorf("badge %+v, want an exact three", class.Badge)
	}

	colls, err := src.Children(ctx, class.Ref)
	if err != nil {
		t.Fatalf("collections: %v", err)
	}
	// The server's own collections are not the person's: system.views holds
	// what the tree already shows as a view.
	for _, n := range colls {
		if strings.HasPrefix(n.Label, "system.") {
			t.Errorf("collections %v, want none of the server's own", labelsOf(colls))
		}
	}
	if got := labelsOf(colls); len(got) != 3 || got[0] != "high_scores" || got[1] != "orders" {
		t.Errorf("collections %v, want them in name order", got)
	}
	people := child(t, colls, "people")
	if people.Ref.Kind != model.KindCollection || !people.Browsable || !people.HasChildren {
		t.Errorf("people %+v, want a browsable collection with children", people)
	}
	if people.Attrs["type"] != "" {
		t.Errorf("people is marked %q, want nothing: it is an ordinary collection", people.Attrs["type"])
	}
	if got := child(t, colls, "high_scores").Attrs["type"]; got != "view" {
		t.Errorf("high_scores is marked %q, want a view", got)
	}

	// A collection holds its indexes.
	inner, err := src.Children(ctx, people.Ref)
	if err != nil {
		t.Fatalf("collection classes: %v", err)
	}
	if len(inner) != 1 {
		t.Fatalf("classes %v, want the indexes alone", labelsOf(inner))
	}
	idxClass := inner[0]
	if kind, ok := model.ClassOf(idxClass.Ref); !ok || kind != model.KindIndex {
		t.Fatalf("class %+v, want the indexes", idxClass)
	}
	indexes, err := src.Children(ctx, idxClass.Ref)
	if err != nil {
		t.Fatalf("indexes: %v", err)
	}
	if got := labelsOf(indexes); len(got) != 2 || got[0] != "_id_" {
		t.Errorf("indexes %v, want _id_ first", got)
	}
	if got := child(t, indexes, "name_score").Attrs["type"]; got != "name, score ↓ · unique" {
		t.Errorf("the index says %q", got)
	}
}

func TestLiveDescribesACollection(t *testing.T) {
	src := open(t, liveConfig("ikigai_it"))
	seed(t, src, "ikigai_it")
	ctx := context.Background()

	desc, err := src.Describe(ctx, model.NewRef(model.KindCollection, "ikigai_it", "people"))
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	coll, ok := desc.(*model.Collection)
	if !ok {
		t.Fatalf("described as %T, want a collection", desc)
	}
	if coll.Name != "people" || coll.DocumentsEstimate != 2 {
		t.Errorf("collection %+v", coll)
	}
	if len(coll.Indexes) != 2 || coll.Indexes[0].Name != "_id_" {
		t.Fatalf("indexes %+v", coll.Indexes)
	}
	if idx := coll.Indexes[1]; !idx.Unique || len(idx.Keys) != 2 || !idx.Keys[1].Descending {
		t.Errorf("index %+v, want the unique compound one", idx)
	}
	// A view says it is one, and has neither indexes nor a count of its
	// own: the server refuses both, which is not a failure to report.
	desc, err = src.Describe(ctx, model.NewRef(model.KindCollection, "ikigai_it", "high_scores"))
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	view := desc.(*model.Collection)
	if view.Attrs["type"] != "view" {
		t.Errorf("the view is marked %q", view.Attrs["type"])
	}
	if view.Attrs["readOnly"] != "true" {
		t.Errorf("the view %+v, want it marked as not writable", view.Attrs)
	}
	// A view has no indexes of its own: the server refuses to list them,
	// which is not a failure to report.
	if len(view.Indexes) != 0 {
		t.Errorf("view %+v, want no indexes", view.Indexes)
	}
	// Nor classes in the tree.
	classes, err := src.Children(ctx, model.NewRef(model.KindCollection, "ikigai_it", "high_scores"))
	if err != nil {
		t.Errorf("a view's classes: %v", err)
	}
	if len(classes) != 0 {
		t.Errorf("a view holds %v", labelsOf(classes))
	}
	// Nothing else has a structure to read, whatever its path.
	if _, err := src.Describe(ctx, model.NewRef(model.KindDatabase, "ikigai_it")); err == nil {
		t.Error("a database was described")
	}
	if _, err := src.Describe(ctx, model.NewRef(model.KindIndex, "ikigai_it", "people", "_id_")); err == nil {
		t.Error("an index was described as though it were the collection")
	}
}

func TestLiveCountsDocuments(t *testing.T) {
	src := open(t, liveConfig("ikigai_it"))
	seed(t, src, "ikigai_it")
	ctx := context.Background()
	badge, ok, err := src.Badge(ctx, model.NewRef(model.KindCollection, "ikigai_it", "people"))
	if err != nil || !ok {
		t.Fatalf("badge: %v %v", badge, err)
	}
	if badge.Text != "2" || badge.Exact {
		t.Errorf("badge %+v, want two, said to be an estimate", badge)
	}
	// Nothing else carries a count, whatever its path.
	if _, ok, err := src.Badge(ctx, model.NewRef(model.KindDatabase, "ikigai_it")); ok || err != nil {
		t.Errorf("a database carries a badge: %v", err)
	}
	if _, ok, err := src.Badge(ctx, model.NewRef(model.KindIndex, "ikigai_it", "people", "_id_")); ok || err != nil {
		t.Errorf("an index carries its collection's count: %v", err)
	}
}

func TestLiveSaysWhatIsWrongWithAConnection(t *testing.T) {
	open(t, liveConfig("ikigai_it")) // skip early where no server is running
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	bad := liveConfig("ikigai_it")
	bad.Port = 1
	_, err := Driver{}.Open(ctx, bad)
	var ce *source.ConnectError
	if !errors.As(err, &ce) || ce.Kind != source.ConnectRefused && ce.Kind != source.ConnectUnreachable {
		t.Errorf("nothing listening: %v", err)
	}

	auth := liveConfig("ikigai_it")
	auth.User = "nobody"
	auth.Secret = func(string) (string, error) { return "wrong", nil }
	_, err = Driver{}.Open(ctx, auth)
	if !errors.As(err, &ce) || ce.Kind != source.ConnectAuth {
		t.Errorf("bad credentials: %v", err)
	}
}

func TestLiveInfersAShape(t *testing.T) {
	src := open(t, liveConfig("ikigai_it"))
	seed(t, src, "ikigai_it")
	ctx := context.Background()
	s := src.(*mongoSource)
	// A field only some documents have, one holding two kinds, and an array
	// of documents: what inference is for.
	if _, err := s.client.Database("ikigai_it").Collection("people").InsertMany(ctx, []any{
		map[string]any{"name": "Edsger", "score": "high", "tags": []any{"a", "b"},
			"items": []any{map[string]any{"sku": "x", "qty": 2}, map[string]any{"sku": "y"}}},
		map[string]any{"name": "Barbara"},
	}); err != nil {
		t.Fatal(err)
	}

	inf, ok := src.(source.ShapeInferrer)
	if !ok {
		t.Fatal("the source cannot infer a shape")
	}
	shape, err := inf.InferShape(ctx, model.NewRef(model.KindCollection, "ikigai_it", "people"), 0)
	if err != nil {
		t.Fatalf("infer: %v", err)
	}
	if shape.Sampled != 4 {
		t.Fatalf("sampled %d, want the four documents there are", shape.Sampled)
	}
	byName := map[string]model.InferredField{}
	for _, f := range shape.Fields {
		byName[f.Name] = f
	}
	if shape.Fields[0].Name != "_id" {
		t.Errorf("fields %v, want the identifier first", shape.Fields)
	}
	if got := byName["name"].Presence; got != 1 {
		t.Errorf("name is in %v of them, want all four", got)
	}
	score := byName["score"]
	if score.Presence != 0.75 || len(score.Types) != 2 {
		t.Errorf("score %+v, want it in three of four and of two types", score)
	}
	if got := byName["items"].Children; len(got) != 2 || got[0].Name != "sku" {
		t.Errorf("items hold %v, want sku and qty", got)
	}
	if got := byName["tags"].Types[0].Type.Native; got != "array<string>" {
		t.Errorf("tags are %q", got)
	}
	// The number asked for bounds what is read.
	small, err := inf.InferShape(ctx, model.NewRef(model.KindCollection, "ikigai_it", "people"), 2)
	if err != nil {
		t.Fatalf("infer: %v", err)
	}
	if small.Sampled != 2 {
		t.Errorf("sampled %d, want the two asked for", small.Sampled)
	}
	// A view is sampled too: its documents are a pipeline's, and a person
	// browsing one needs its shape as much as a collection's.
	if _, err := inf.InferShape(ctx, model.NewRef(model.KindCollection, "ikigai_it", "high_scores"), 10); err != nil {
		t.Errorf("a view could not be sampled: %v", err)
	}
	// Nothing else has documents to sample.
	if _, err := inf.InferShape(ctx, model.NewRef(model.KindDatabase, "ikigai_it"), 10); err == nil {
		t.Error("a database was sampled")
	}
	if _, err := inf.InferShape(ctx, model.NewRef(model.KindIndex, "ikigai_it", "people", "_id_"), 10); err == nil {
		t.Error("an index was sampled")
	}
}
