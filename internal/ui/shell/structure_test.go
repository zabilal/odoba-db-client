package shell

import (
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer/view"
)

// labelTexts gathers the text of every label in a canvas object.
func labelTexts(o fyne.CanvasObject) []string {
	var out []string
	var walk func(fyne.CanvasObject)
	walk = func(o fyne.CanvasObject) {
		switch v := o.(type) {
		case nil:
			return
		case *widget.Label:
			out = append(out, v.Text)
			return
		case *fyne.Container:
			for _, c := range v.Objects {
				walk(c)
			}
			return
		}
		if w, ok := o.(fyne.Widget); ok {
			for _, c := range test.WidgetRenderer(w).Objects() {
				walk(c)
			}
		}
	}
	walk(o)
	return out
}

func showsAll(t *tab, want ...string) bool {
	got := strings.Join(labelTexts(t.item.Content), "\n")
	for _, w := range want {
		if !strings.Contains(got, w) {
			return false
		}
	}
	return true
}

func TestOpenStructureShowsTheTablesShape(t *testing.T) {
	fx := newFixture(t)
	selectItems(t, fx)
	if c, _ := fx.s.reg.Get(cmdStructure); c.Shortcut.Label("darwin") != "⌥⌘O" {
		t.Errorf("Open Structure is on %q, want ⌥⌘O", c.Shortcut.Label("darwin"))
	}
	fx.s.run(cmdStructure)
	tb := fx.onlyTab(t)
	if tb.item.Text != "Structure: items" || !tb.structure {
		t.Fatalf("opened %q", tb.item.Text)
	}
	pump(t, fx.q, func() bool {
		return showsAll(tb, "Columns", "id", "integer", "identity", "name", "text", "'x'", "items_pkey", "items_name", "About 41 rows")
	})
	fx.s.run(cmdStructure)
	if len(fx.s.open) != 1 {
		t.Error("an open structure tab should come forward, not open twice")
	}
}

func TestStructureNeedsAnObjectWithRows(t *testing.T) {
	fx := newFixture(t)
	c := selectItems(t, fx)
	fx.s.Explorer.Tree.Select(view.NodeID(c.ID, model.NewRef(model.KindDatabase, "main")))
	fx.s.sync()
	if !fx.s.menuItems[cmdStructure].Disabled {
		t.Error("a database is not described here; Open Structure should be disabled")
	}
}

func TestADescribeThatPanicsIsSaid(t *testing.T) {
	fx := newFixture(t)
	c := fx.create(t, "db1", nil)
	tb := fx.s.OpenStructure(c.ID, model.Node{Ref: model.NewRef(model.KindTable, "main", "boom"), Label: "boom", Browsable: true})
	pump(t, fx.q, func() bool { return showsAll(tb, "could not read the structure") })
	if !strings.Contains(fx.s.errors.text, "describing fell over") {
		t.Errorf("the error band should say the driver fell over: %q", fx.s.errors.text)
	}
}

func TestAStructureTabComesBack(t *testing.T) {
	fx := newFixture(t)
	fx.s.autosave = time.Hour // so that only quitting saves it
	c := fx.create(t, "db1", nil)
	fx.s.OpenStructure(c.ID, itemsNode)
	fx.s.shutdown()
	s := fx.relaunch(t)
	r := tabNamed(s, "Structure: items")
	if r == nil || !r.structure {
		t.Fatalf("tabs %v; the structure tab should come back as one", tabLabels(s))
	}
	pump(t, fx.q, func() bool { return showsAll(r, "items_pkey") })
}

func TestStructureShowsHowAKeyspaceIsReplicated(t *testing.T) {
	// A keyspace has no columns: what there is to say about it is how many
	// copies of a row there are and where they are kept (FR-12.3).
	schema := &model.Schema{Name: "ikigai_it", Attrs: map[string]string{
		"strategy": "NetworkTopologyStrategy", "dc1": "3", "durable writes": "true",
	}, Tables: []model.Table{{Name: "people"}, {Name: "orders"}},
		Views:     []model.View{{Name: "people_by_score", Materialized: true}},
		UserTypes: []model.UserType{{Name: "address"}}}
	got := strings.Join(labelTexts(structureView(schema, nil, nil)), "\n")
	for _, want := range []string{
		"Replication", "strategy", "NetworkTopologyStrategy", "dc1", "3", "durable writes",
		"What it holds", "Tables", "2", "Materialized views", "1", "Types",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("a keyspace's structure does not say %q:\n%s", want, got)
		}
	}
	// By name, so that the same keyspace reads the same way twice.
	if strings.Index(got, "dc1") > strings.Index(got, "durable writes") ||
		strings.Index(got, "durable writes") > strings.Index(got, "strategy") {
		t.Errorf("the replication is not in name order:\n%s", got)
	}
	// A keyspace with neither views nor types shows neither.
	bare := &model.Schema{Name: "shop", Attrs: map[string]string{"strategy": "SimpleStrategy"}}
	got = strings.Join(labelTexts(structureView(bare, nil, nil)), "\n")
	if strings.Contains(got, "Materialized views") || strings.Contains(got, "Types") {
		t.Errorf("a keyspace shows what it has not got:\n%s", got)
	}
}

func TestStructureShowsHowATopicIsSpread(t *testing.T) {
	topic := &model.Topic{Name: "orders", ReplicationFactor: 3,
		Partitions: []model.Partition{
			{ID: 0, LowWatermark: 0, HighWatermark: 100},
			{ID: 1, LowWatermark: 40, HighWatermark: 60},
		},
		Attrs: map[string]string{"size on disk": "12 MB across all replicas"}}
	got := strings.Join(labelTexts(structureView(topic, nil, nil)), "\n")
	for _, want := range []string{
		"2 partitions", "on 3 brokers", "up to 120 records",
		"What it says about itself", "size on disk", "12 MB across all replicas",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("a topic's structure does not say %q:\n%s", want, got)
		}
	}
	// What is retained is not what was written, and the words have to say so.
	if !strings.Contains(got, "up to") {
		t.Errorf("a topic counts its records as though none had aged out:\n%s", got)
	}

	// One partition reads as one, and a topic of Kafka's own says whose it is.
	own := &model.Topic{Name: "__consumer_offsets", Internal: true, ReplicationFactor: 1,
		Partitions: []model.Partition{{ID: 0}}}
	got = strings.Join(labelTexts(structureView(own, nil, nil)), "\n")
	for _, want := range []string{"one partition", "on one broker", "Kafka's own"} {
		if !strings.Contains(got, want) {
			t.Errorf("an internal topic does not say %q:\n%s", want, got)
		}
	}
	// An empty log holds nothing, and says nothing about how much.
	if strings.Contains(got, "up to") {
		t.Errorf("an empty topic claims records:\n%s", got)
	}

	// Replication that varies by partition is said, not averaged away.
	varies := &model.Topic{Name: "mixed", ReplicationFactor: -1,
		Partitions: []model.Partition{{ID: 0}, {ID: 1}}}
	if got := strings.Join(labelTexts(structureView(varies, nil, nil)), "\n"); !strings.Contains(got, "replicated differently") {
		t.Errorf("a topic replicated unevenly says:\n%s", got)
	}
}

func TestStructureShowsWhatAClusterIs(t *testing.T) {
	// A cluster has no columns either: what there is to say is who its brokers
	// are and which of them answers for the whole (FR-13.1).
	cluster := &model.Cluster{ID: "abc123", Controller: 2,
		Brokers: []model.Broker{
			{ID: 1, Host: "one", Port: 9092, Rack: "rack-a"},
			{ID: 2, Host: "two", Port: 9093},
		},
		Attrs: map[string]string{"version": "v4.1"}}
	got := strings.Join(labelTexts(structureView(cluster, nil, nil)), "\n")
	for _, want := range []string{
		"2 brokers", "abc123", "Brokers", "one:9092", "rack-a", "two:9093",
		"2 (controller)", "What it says about itself", "version", "v4.1",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("a cluster's structure does not say %q:\n%s", want, got)
		}
	}
	// Only the broker that answers for the whole is marked as doing so.
	if strings.Contains(got, "1 (controller)") {
		t.Errorf("a broker that is not the controller is marked as one:\n%s", got)
	}
	// A cluster of one reads as one, not as "1 brokers".
	one := &model.Cluster{ID: "solo", Brokers: []model.Broker{{ID: 1, Host: "h", Port: 9092}}}
	if got := strings.Join(labelTexts(structureView(one, nil, nil)), "\n"); !strings.Contains(got, "one broker") {
		t.Errorf("a cluster of one says:\n%s", got)
	}
	// And one that named no brokers says so, rather than showing an empty table.
	none := &model.Cluster{ID: "empty"}
	if got := strings.Join(labelTexts(structureView(none, nil, nil)), "\n"); !strings.Contains(got, "named no brokers") {
		t.Errorf("a cluster with no brokers says:\n%s", got)
	}
}

func TestStructureShowsACollectionsShape(t *testing.T) {
	coll := &model.Collection{
		Name: "people", DocumentsEstimate: 41,
		Indexes: []model.DocumentIndex{
			{Name: "_id_", Keys: []model.IndexColumn{{Name: "_id"}}},
			{Name: "name_score", Unique: true, Sparse: true, Keys: []model.IndexColumn{
				{Name: "name"}, {Name: "score", Descending: true}}},
			{Name: "seen_ttl", TTL: 3600, Keys: []model.IndexColumn{{Name: "seen"}}},
			{Name: "body_text", Keys: []model.IndexColumn{{Name: "body", Expression: "text"}}},
		},
		Shape: &model.DocumentShape{Sampled: 200, Fields: []model.InferredField{
			{Name: "name", Presence: 1, Types: []model.ObservedType{{Type: model.DataType{Native: "string"}}}},
			{Name: "score", Presence: 0.5, Types: []model.ObservedType{
				{Type: model.DataType{Native: "int"}}, {Type: model.DataType{Native: "string"}}}},
		}},
	}
	got := strings.Join(labelTexts(structureView(coll, nil, nil)), "\n")
	for _, want := range []string{
		"About 41 documents", "Indexes", "_id_", "name_score", "name, score DESC", "unique, sparse",
		"seen_ttl", "3600 seconds", "body text",
		"Fields", "name", "100% of those sampled", "int, string", "50% of those sampled",
		"Fields seen in 200 documents sampled",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the collection's structure does not say %q:\n%s", want, got)
		}
	}
	// A view says what it is, and shows nothing it has not got.
	view := &model.Collection{Name: "high_scores", DocumentsEstimate: -1,
		Attrs: map[string]string{"type": "view", "readOnly": "true"}}
	got = strings.Join(labelTexts(structureView(view, nil, nil)), "\n")
	for _, want := range []string{"A view", "Read-only"} {
		if !strings.Contains(got, want) {
			t.Errorf("the view's structure does not say %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "documents, as the server") || strings.Contains(got, "Fields") {
		t.Errorf("the view's structure shows what it has not got:\n%s", got)
	}
}
