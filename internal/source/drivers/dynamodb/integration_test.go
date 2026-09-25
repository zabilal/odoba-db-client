//go:build conformance

package dynamodb

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/conformance"
)

// The live suite, against DynamoDB Local (T4.6), which is the real service's
// own implementation of its API and runs offline:
//
//	docker run -d --name ikigai-dynamodb -p 58000:8000 \
//	  amazon/dynamodb-local:latest -jar DynamoDBLocal.jar -inMemory -sharedDb
//
// IKIGAI_REQUIRE_DYNAMODB=1 turns a skip into a failure, which is what CI
// sets. What it cannot prove is anything about the real service's credentials
// or its endpoints: DynamoDB Local takes any credentials at all, so the
// classification of a refused one is held by a unit test over the SDK's own
// error types instead, and is said to be that in ADR-0157.

const region = "eu-west-2"

func required() bool { return os.Getenv("IKIGAI_REQUIRE_DYNAMODB") != "" }

func endpoint() string {
	if v := os.Getenv("IKIGAI_DYNAMODB_ENDPOINT"); v != "" {
		return v
	}
	return "http://127.0.0.1:58000"
}

func config(g source.Guard) source.ConnectionConfig {
	return source.ConnectionConfig{
		DriverID: driverID,
		User:     "local",
		Secret:   func(string) (string, error) { return "local", nil },
		Params:   map[string]string{"region": region, "endpoint": endpoint()},
		Guard:    g,
	}
}

func skipOrFail(t *testing.T, err error) {
	t.Helper()
	if required() {
		t.Fatalf("DynamoDB required but unavailable: %v", err)
	}
	t.Skipf("no DynamoDB at %s: %v", endpoint(), err)
}

// dial connects as the application would.
func dial(t *testing.T, g source.Guard) source.Source {
	t.Helper()
	var d Driver
	src, err := d.Open(context.Background(), config(g))
	if err != nil {
		skipOrFail(t, err)
	}
	t.Cleanup(func() { src.Close() })
	return src
}

func ref(name string) model.ObjectRef { return model.NewRef(model.KindCollection, region, name) }

// seed puts the tables the suite reads into the service. A region holds one
// set of tables, so they are deleted and made again rather than made fresh.
//
// PEOPLE has a key of two attributes, because that is the shape most of the
// driver's decisions are about; WRITES has one, and it is called "name"
// because the suite's document checks write items whose only certain attribute
// is that — an item needs its key, and the key has to be something the suite
// supplies.
func seed(t *testing.T) {
	t.Helper()
	src := dial(t, source.Guard{})
	c := src.(*dynamoSource).client
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	for _, name := range []string{"PEOPLE", "WRITES", "EMPTY"} {
		c.DeleteTable(ctx, &dynamodb.DeleteTableInput{TableName: aws.String(name)})
	}
	mk := func(in *dynamodb.CreateTableInput) {
		t.Helper()
		in.BillingMode = ddbtypes.BillingModePayPerRequest
		if _, err := c.CreateTable(ctx, in); err != nil {
			t.Fatalf("creating %s: %v", aws.ToString(in.TableName), err)
		}
	}
	mk(&dynamodb.CreateTableInput{
		TableName: aws.String("PEOPLE"),
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("id"), AttributeType: ddbtypes.ScalarAttributeTypeN},
			{AttributeName: aws.String("name"), AttributeType: ddbtypes.ScalarAttributeTypeS},
			{AttributeName: aws.String("score"), AttributeType: ddbtypes.ScalarAttributeTypeN},
		},
		KeySchema: []ddbtypes.KeySchemaElement{
			{AttributeName: aws.String("id"), KeyType: ddbtypes.KeyTypeHash},
			{AttributeName: aws.String("name"), KeyType: ddbtypes.KeyTypeRange},
		},
		GlobalSecondaryIndexes: []ddbtypes.GlobalSecondaryIndex{{
			IndexName:  aws.String("by_score"),
			KeySchema:  []ddbtypes.KeySchemaElement{{AttributeName: aws.String("score"), KeyType: ddbtypes.KeyTypeHash}},
			Projection: &ddbtypes.Projection{ProjectionType: ddbtypes.ProjectionTypeKeysOnly},
		}},
	})
	mk(&dynamodb.CreateTableInput{
		TableName: aws.String("WRITES"),
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("name"), AttributeType: ddbtypes.ScalarAttributeTypeS},
		},
		KeySchema: []ddbtypes.KeySchemaElement{{AttributeName: aws.String("name"), KeyType: ddbtypes.KeyTypeHash}},
	})
	mk(&dynamodb.CreateTableInput{
		TableName: aws.String("EMPTY"),
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("k"), AttributeType: ddbtypes.ScalarAttributeTypeS},
		},
		KeySchema: []ddbtypes.KeySchemaElement{{AttributeName: aws.String("k"), KeyType: ddbtypes.KeyTypeHash}},
	})
	for i := 1; i <= 100; i++ {
		item := map[string]ddbtypes.AttributeValue{
			"id":    &ddbtypes.AttributeValueMemberN{Value: strconv.Itoa(i)},
			"name":  &ddbtypes.AttributeValueMemberS{Value: "person " + strconv.Itoa(i)},
			"score": &ddbtypes.AttributeValueMemberN{Value: fmt.Sprintf("%.1f", float64(i)*1.5)},
			"meta":  &ddbtypes.AttributeValueMemberM{Value: map[string]ddbtypes.AttributeValue{"i": &ddbtypes.AttributeValueMemberN{Value: strconv.Itoa(i)}}},
			"flag":  &ddbtypes.AttributeValueMemberBOOL{Value: i%2 == 0},
		}
		if i%3 == 0 {
			// Not every item has every attribute, which is the point of a
			// store with no declared shape.
			item["tags"] = &ddbtypes.AttributeValueMemberSS{Value: []string{"a", "b"}}
			item["pic"] = &ddbtypes.AttributeValueMemberB{Value: []byte{0xff, 0xfe, 0x00}}
			item["nothing"] = &ddbtypes.AttributeValueMemberNULL{Value: true}
		}
		if _, err := c.PutItem(ctx, &dynamodb.PutItemInput{
			TableName: aws.String("PEOPLE"), Item: item}); err != nil {
			t.Fatalf("seeding: %v", err)
		}
	}
}

func TestConformance(t *testing.T) {
	seed(t)
	conformance.Run(t, conformance.Target{
		Name: "dynamodb",
		Open: func(_ context.Context, t *testing.T) source.Source { return dial(t, source.Guard{}) },
		OpenGuarded: func(_ context.Context, t *testing.T, g source.Guard) source.Source {
			return dial(t, g)
		},
		Browsable: ref("PEOPLE"),
		Writable:  ref("WRITES"),
		// No Conditions: the suite's WHERE checks need a query language, and
		// this source has none, so they skip. That a typed condition is
		// refused rather than ignored is held by a test of this driver's own.
	})
}

// A value comes back as what it is, and a number keeps every digit: DynamoDB
// holds 38 of them and a float64 holds fewer.
func TestLiveAValueIsWhatItIs(t *testing.T) {
	seed(t)
	src := dial(t, source.Guard{})
	ctx := context.Background()
	rs, err := src.Browse(ctx, ref("PEOPLE"), source.BrowseOptions{
		Filters: []source.Filter{{Column: "id", Op: source.OpEqual, Values: []any{int64(3)}}},
		Limit:   1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	row, err := rs.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]any{}
	for i, c := range rs.Columns() {
		if i < len(row) {
			got[c.Name] = row[i]
		}
	}
	if got["id"] != int64(3) {
		t.Errorf("the id is %v (%T)", got["id"], got["id"])
	}
	if got["name"] != "person 3" {
		t.Errorf("the name is %v (%T)", got["name"], got["name"])
	}
	// 4.5 is not a whole number, so it keeps its digits rather than becoming a
	// float that might not print as 4.5 again.
	if d, ok := got["score"].(model.Decimal); !ok || string(d) != "4.5" {
		t.Errorf("the score is %v (%T)", got["score"], got["score"])
	}
	if got["flag"] != false {
		t.Errorf("the flag is %v (%T)", got["flag"], got["flag"])
	}
	if b, ok := got["pic"].([]byte); !ok || len(b) != 3 {
		t.Errorf("the bytes are %v (%T)", got["pic"], got["pic"])
	}
	// A map reads as the value it holds, not as DynamoDB's wire encoding of
	// it: {"i": 3}, not {"i": {"N": "3"}}.
	if j, ok := got["meta"].(model.JSON); !ok || string(j) != `{"i":3}` {
		t.Errorf("the map is %v (%T)", got["meta"], got["meta"])
	}
	if j, ok := got["tags"].(model.JSON); !ok || string(j) != `["a","b"]` {
		t.Errorf("the set is %v (%T)", got["tags"], got["tags"])
	}
	// An attribute set to NULL is there and holds nothing.
	if got["nothing"] != nil {
		t.Errorf("the null is %v (%T)", got["nothing"], got["nothing"])
	}
}

// The columns are the attributes a sample holds, the key's first, and each
// says how many of the sampled items had it.
func TestLiveTheShapeIsSampled(t *testing.T) {
	seed(t)
	src := dial(t, source.Guard{})
	shape, err := src.(source.ShapeInferrer).InferShape(context.Background(), ref("PEOPLE"), 30)
	if err != nil {
		t.Fatal(err)
	}
	if shape.Sampled != 30 {
		t.Errorf("it sampled %d items", shape.Sampled)
	}
	if len(shape.Fields) < 6 {
		t.Fatalf("it found %d attributes: %+v", len(shape.Fields), shape.Fields)
	}
	if shape.Fields[0].Name != "id" || shape.Fields[1].Name != "name" {
		t.Errorf("the key is not first: %s, %s", shape.Fields[0].Name, shape.Fields[1].Name)
	}
	by := map[string]model.InferredField{}
	for _, f := range shape.Fields {
		by[f.Name] = f
	}
	if p := by["id"].Presence; p != 1 {
		t.Errorf("every item has an id, and it says %v", p)
	}
	// A third of the items have tags, so the presence must be a fraction:
	// inference is never to be mistaken for a schema.
	if p := by["tags"].Presence; p <= 0 || p >= 1 {
		t.Errorf("a third of the items have tags, and it says %v", p)
	}
}

// An empty table still shows the attributes its key is made of: they are the
// only ones every item is certain to have, and they are declared.
func TestLiveAnEmptyTableStillHasItsKey(t *testing.T) {
	seed(t)
	src := dial(t, source.Guard{})
	rs, err := src.Browse(context.Background(), ref("EMPTY"), source.BrowseOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	cols := rs.Columns()
	if len(cols) != 1 || cols[0].Name != "k" {
		t.Errorf("its columns are %+v", cols)
	}
	if id := model.IdentityOf(rs); id.Kind != model.IdentityPrimaryKey ||
		len(id.Columns) != 1 || id.Columns[0] != "k" {
		t.Errorf("its items are told apart by %+v", id)
	}
}

// A key of two attributes is reported in the order the service states, which
// is the order a key's values are given in.
func TestLiveAKeyOfTwoIsInOrder(t *testing.T) {
	seed(t)
	src := dial(t, source.Guard{})
	rs, err := src.Browse(context.Background(), ref("PEOPLE"), source.BrowseOptions{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	id := model.IdentityOf(rs)
	if len(id.Columns) != 2 || id.Columns[0] != "id" || id.Columns[1] != "name" {
		t.Errorf("its items are told apart by %+v", id)
	}
}

// Paging works, and a later page does not repeat the first: the service has no
// offset, so this is the driver reading past what the grid has seen.
func TestLivePagingDoesNotRepeatItself(t *testing.T) {
	seed(t)
	src := dial(t, source.Guard{})
	ctx := context.Background()
	read := func(offset, limit int64) []string {
		rs, err := src.Browse(ctx, ref("PEOPLE"), source.BrowseOptions{Offset: offset, Limit: limit})
		if err != nil {
			t.Fatal(err)
		}
		defer rs.Close()
		at := -1
		for i, c := range rs.Columns() {
			if c.Name == "name" {
				at = i
			}
		}
		var out []string
		for {
			row, err := rs.Next(ctx)
			if err != nil {
				return out
			}
			out = append(out, fmt.Sprint(row[at]))
		}
	}
	first := read(0, 10)
	second := read(10, 10)
	if len(first) != 10 || len(second) != 10 {
		t.Fatalf("pages of %d and %d", len(first), len(second))
	}
	seen := map[string]bool{}
	for _, n := range first {
		seen[n] = true
	}
	for _, n := range second {
		if seen[n] {
			t.Errorf("%s is on both pages", n)
		}
	}
}

// A sort is refused rather than ignored: a scan has no order to ask for, and
// unsorted items shown as sorted is the failure REQ-DRV-3 is about.
func TestLiveASortIsRefused(t *testing.T) {
	src := dial(t, source.Guard{})
	_, err := src.Browse(context.Background(), ref("PEOPLE"), source.BrowseOptions{
		Sorts: []source.Sort{{Column: "id"}}, Limit: 1})
	if err == nil {
		t.Error("it sorted a scan")
	}
}

// A table's structure: its key, its index, and what the index projects —
// which is the commonest surprise about a DynamoDB index.
func TestLiveATableReportsItsStructure(t *testing.T) {
	seed(t)
	src := dial(t, source.Guard{})
	got, err := src.Describe(context.Background(), ref("PEOPLE"))
	if err != nil {
		t.Fatal(err)
	}
	coll, ok := got.(*model.Collection)
	if !ok {
		t.Fatalf("it described a table as %T", got)
	}
	if coll.Name != "PEOPLE" {
		t.Errorf("it is called %q", coll.Name)
	}
	if coll.Attrs["key"] != "id, name" {
		t.Errorf("its key reads %q", coll.Attrs["key"])
	}
	if len(coll.Indexes) != 1 {
		t.Fatalf("its indexes are %+v", coll.Indexes)
	}
	ix := coll.Indexes[0]
	if ix.Name != "by_score" || len(ix.Keys) != 1 || ix.Keys[0].Name != "score" {
		t.Errorf("its index is %+v", ix)
	}
	if ix.Attrs["kind"] != "global" || ix.Attrs["projects"] != "the keys only" {
		t.Errorf("its index says %v", ix.Attrs)
	}
	// The item count is the service's estimate, which a description may carry
	// and a badge says is one.
	if coll.DocumentsEstimate < 0 {
		t.Errorf("it reports %d items", coll.DocumentsEstimate)
	}
	badge, ok, err := src.Badge(context.Background(), ref("PEOPLE"))
	if err != nil || !ok {
		t.Fatalf("its badge: %v %v", ok, err)
	}
	if badge.Exact {
		t.Error("the badge claims to be exact, and the service's count is hours old")
	}
}

// The tree: a region, its collections, and a table's indexes under it.
func TestLiveTheTree(t *testing.T) {
	seed(t)
	src := dial(t, source.Guard{})
	ctx := context.Background()
	root, err := src.Root(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(root) != 1 || root[0].Label != "Collections" {
		t.Fatalf("the root is %+v", root)
	}
	tables, err := src.Children(ctx, root[0].Ref)
	if err != nil {
		t.Fatal(err)
	}
	var people model.Node
	for _, n := range tables {
		if n.Label == "PEOPLE" {
			people = n
		}
	}
	if people.Ref.IsZero() || !people.Browsable {
		t.Fatalf("PEOPLE is %+v among %+v", people, tables)
	}
	classes, err := src.Children(ctx, people.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if len(classes) != 1 || classes[0].Label != "Indexes" {
		t.Fatalf("what is under PEOPLE: %+v", classes)
	}
	indexes, err := src.Children(ctx, classes[0].Ref)
	if err != nil {
		t.Fatal(err)
	}
	if len(indexes) != 1 || indexes[0].Label != "by_score" {
		t.Fatalf("its indexes are %+v", indexes)
	}
	// An empty table has no index class, rather than one that opens onto
	// nothing.
	empty, err := src.Children(ctx, ref("EMPTY"))
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Errorf("a table with no indexes shows %+v", empty)
	}
}

// A new item is new: its key must not be taken, or PutItem would overwrite
// whatever is there and report success.
func TestLiveAnInsertDoesNotOverwrite(t *testing.T) {
	seed(t)
	src := dial(t, source.Guard{})
	w := src.(source.Writer)
	ctx := context.Background()
	target := ref("WRITES")
	id := model.RowIdentity{Kind: model.IdentityPrimaryKey, Columns: []string{"name"}, Target: target}
	add := func(name string, n int64) *source.WriteOutcome {
		t.Helper()
		plan, err := w.Plan(ctx, source.Changeset{Target: target, Identity: id,
			Changes: []source.RowChange{{Kind: source.ChangeInsert,
				Values: map[string]any{"name": name, "n": n}}}})
		if err != nil {
			t.Fatal(err)
		}
		out, err := w.Apply(ctx, plan)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if out := add("first", 1); out.Err != nil {
		t.Fatalf("adding one: %+v", out)
	}
	out := add("first", 2)
	if out.Err == nil {
		t.Fatal("it added an item whose key was taken")
	}
	// And the item that was there is as it was.
	rs, err := src.Browse(ctx, target, source.BrowseOptions{
		Filters: []source.Filter{{Column: "name", Op: source.OpEqual, Values: []any{"first"}}}, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	row, err := rs.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i, c := range rs.Columns() {
		if c.Name == "n" && fmt.Sprint(row[i]) != "1" {
			t.Errorf("the item was overwritten: n is %v", row[i])
		}
	}
}

// A change to an item that has gone fails rather than putting it back, which
// is what UpdateItem does without a condition.
func TestLiveAChangeToAGoneItemDoesNotRecreateIt(t *testing.T) {
	seed(t)
	src := dial(t, source.Guard{})
	w := src.(source.Writer)
	ctx := context.Background()
	target := ref("WRITES")
	id := model.RowIdentity{Kind: model.IdentityPrimaryKey, Columns: []string{"name"}, Target: target}
	plan, err := w.Plan(ctx, source.Changeset{Target: target, Identity: id,
		Changes: []source.RowChange{{Kind: source.ChangeUpdate, Key: []any{"never-there"},
			Values: map[string]any{"n": int64(9)}}}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := w.Apply(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	if out.Err == nil || out.FailedAt != 0 {
		t.Fatalf("it said %+v", out)
	}
	n, err := src.(source.Countable).Count(ctx, target, source.BrowseOptions{
		Filters: []source.Filter{{Column: "name", Op: source.OpEqual, Values: []any{"never-there"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("it created %d items", n)
	}
}

// A changeset keyed by anything but the table's own key is refused: an
// UpdateItem with the wrong key creates an item rather than changing one.
func TestLiveAWrongKeyIsRefused(t *testing.T) {
	seed(t)
	src := dial(t, source.Guard{})
	w := src.(source.Writer)
	target := ref("PEOPLE")
	_, err := w.Plan(context.Background(), source.Changeset{Target: target,
		Identity: model.RowIdentity{Kind: model.IdentityPrimaryKey, Columns: []string{"id"}, Target: target},
		Changes:  []source.RowChange{{Kind: source.ChangeUpdate, Key: []any{int64(1)}, Values: map[string]any{"score": int64(0)}}}})
	if err == nil {
		t.Error("a change keyed by half the key was planned")
	}
}

// Info says where it is, and that it is not the service when it is not.
func TestLiveInfoSaysWhereItIs(t *testing.T) {
	src := dial(t, source.Guard{})
	info, err := src.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Product == "Amazon DynamoDB" {
		t.Error("it calls a container the service")
	}
	if info.Attrs["region"] != region {
		t.Errorf("it reports the region %q", info.Attrs["region"])
	}
	if info.Attrs["endpoint"] != endpoint() {
		t.Errorf("it reports the endpoint %q", info.Attrs["endpoint"])
	}
}

// A wrong endpoint says the service could not be reached.
func TestLiveAWrongEndpointSaysSo(t *testing.T) {
	cfg := config(source.Guard{})
	cfg.Params["endpoint"] = "http://127.0.0.1:1"
	var d Driver
	_, err := d.Open(context.Background(), cfg)
	var ce *source.ConnectError
	if !errors.As(err, &ce) {
		t.Fatalf("it said %v", err)
	}
	if ce.Kind != source.ConnectUnreachable {
		t.Errorf("it called that %v: %v", ce.Kind, err)
	}
}

// A table that is not there says so rather than answering an empty one.
func TestLiveNoSuchTable(t *testing.T) {
	src := dial(t, source.Guard{})
	if _, err := src.Describe(context.Background(), ref("NOT-A-TABLE")); err == nil {
		t.Error("it described a table that does not exist")
	}
}
