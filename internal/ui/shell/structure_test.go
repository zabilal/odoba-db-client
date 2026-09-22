package shell

import (
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/store"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer"
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

func TestStructureShowsATopicsPartitions(t *testing.T) {
	topic := &model.Topic{Name: "orders", ReplicationFactor: 2,
		Partitions: []model.Partition{
			// Read and written through broker 1, both copies keeping up.
			{ID: 0, Leader: 1, Replicas: []int32{1, 2}, ISR: []int32{1, 2},
				LowWatermark: 0, HighWatermark: 100},
			// A copy behind, and a log nobody could read the ends of.
			{ID: 1, Leader: 2, Replicas: []int32{2, 3}, ISR: []int32{2},
				LowWatermark: -1, HighWatermark: -1},
			// Leaderless: it cannot be used at all until one is elected.
			{ID: 2, Leader: -1, Replicas: []int32{3}, ISR: []int32{},
				LowWatermark: 7, HighWatermark: 7},
		}}
	got := strings.Join(labelTexts(structureView(topic, nil, nil)), "\n")
	for _, want := range []string{
		"Partitions", "Leader", "In sync", "Offsets",
		"1, 2", "0 to 100", // a healthy partition, and where its log runs
		"unknown",   // offsets nobody could read
		"7 (empty)", // a log whose ends meet
		"none",      // no leader, and no copy in sync
	} {
		if !strings.Contains(got, want) {
			t.Errorf("a topic's partitions do not say %q:\n%s", want, got)
		}
	}
	// Copies that are behind are counted in words, which is what somebody
	// opens this view to find.
	if !strings.Contains(got, "2 partitions are short of an in-sync copy") {
		t.Errorf("the partitions short of a copy are not named:\n%s", got)
	}
	// Nothing reads as -1: that is a fact about the protocol, not about the
	// data, and a person should never have to know how to read one.
	if strings.Contains(got, "-1") {
		t.Errorf("something reads as -1:\n%s", got)
	}
	// Two things are absent here — a leader, and a copy in sync — and each
	// says so rather than leaving a cell blank.
	if n := strings.Count(got, "none"); n < 2 {
		t.Errorf("what is absent is named %d times, not twice:\n%s", n, got)
	}

	// A topic whose copies all keep up says nothing about copies at all.
	sound := &model.Topic{Name: "audit", ReplicationFactor: 1,
		Partitions: []model.Partition{{ID: 0, Leader: 1, Replicas: []int32{1}, ISR: []int32{1}}}}
	if got := strings.Join(labelTexts(structureView(sound, nil, nil)), "\n"); strings.Contains(got, "short of") {
		t.Errorf("a sound topic is reported as short of a copy:\n%s", got)
	}
	// And one partition short reads as one, not as a plural.
	short := &model.Topic{Name: "audit", ReplicationFactor: 2,
		Partitions: []model.Partition{{ID: 0, Leader: 1, Replicas: []int32{1, 2}, ISR: []int32{1}}}}
	if got := strings.Join(labelTexts(structureView(short, nil, nil)), "\n"); !strings.Contains(got, "1 partition is short") {
		t.Errorf("one partition short reads as:\n%s", got)
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

// A subject in the structure view (T2.74, FR-13.14, ADR-0105).

const (
	subjectV1 = `{"type":"record","name":"Order","fields":[{"name":"id","type":"string"}]}`
	subjectV2 = `{"type":"record","name":"Order","fields":[{"name":"id","type":"string"},{"name":"total","type":"double"}]}`
)

func TestStructureShowsASubjectsVersionsAndWhatChanged(t *testing.T) {
	subject := &model.SchemaSubject{
		Name:          "orders-value",
		Compatibility: "BACKWARD",
		Versions: []model.SchemaVersion{
			{Version: 1, ID: 7, Format: "AVRO", Definition: subjectV1},
			{Version: 2, ID: 9, Format: "AVRO", Definition: subjectV2},
		}}
	got := strings.Join(labelTexts(structureView(subject, nil, nil)), "\n")
	for _, want := range []string{
		"2 versions, the newest of them version 2.",
		"Versions", "Schema id", "AVRO",
		// The rule read back in the registry's own words, and in words.
		"Compatibility: BACKWARD",
		"must be able to read what the version before it wrote",
		"Schema, version 2",
		"What changed",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("a subject does not say %q:\n%s", want, got)
		}
	}
	// Laid out, not shown as the single line a registry keeps it as: one line
	// can be neither read nor compared (ADR-0105).
	if strings.Contains(got, `"fields":[{"name":"id"`) {
		t.Errorf("the schema is shown as the one line it was registered as:\n%s", got)
	}
	// The comparison opens on the newest against the one before it, and what
	// arrived is marked as having arrived — by a sign, not by colour alone.
	if !strings.Contains(got, "+ ") {
		t.Errorf("nothing is marked as having arrived:\n%s", got)
	}
	if !strings.Contains(got, "arrived") {
		t.Errorf("the comparison does not count what changed:\n%s", got)
	}
	var arrived []string
	for _, l := range labelTexts(structureView(subject, nil, nil)) {
		if strings.HasPrefix(l, "+ ") {
			arrived = append(arrived, l)
		}
	}
	if !strings.Contains(strings.Join(arrived, "\n"), "total") {
		t.Errorf("the field the newer version added is not among what arrived: %v", arrived)
	}
}

func TestStructureComparesNothingWhereThereIsNothingToCompare(t *testing.T) {
	one := &model.SchemaSubject{Name: "orders-value",
		Versions: []model.SchemaVersion{{Version: 1, ID: 7, Format: "AVRO", Definition: subjectV1}}}
	got := strings.Join(labelTexts(structureView(one, nil, nil)), "\n")
	if !strings.Contains(got, "One version, and nothing has replaced it.") {
		t.Errorf("a subject with one version says:\n%s", got)
	}
	if strings.Contains(got, "What changed") {
		t.Errorf("a subject with nothing to compare offers a comparison:\n%s", got)
	}
	// Its schema is still there to read.
	if !strings.Contains(got, "Schema, version 1") {
		t.Errorf("the only version's schema is not shown:\n%s", got)
	}

	// A subject with no versions says so rather than drawing an empty table.
	none := strings.Join(labelTexts(structureView(&model.SchemaSubject{Name: "orders-value"}, nil, nil)), "\n")
	if !strings.Contains(none, "Nothing has been registered") {
		t.Errorf("an empty subject says:\n%s", none)
	}
	// And a compatibility mode the registry never gave is not invented.
	if strings.Contains(none, "Compatibility") {
		t.Errorf("a subject with no compatibility mode shows one:\n%s", none)
	}
}

func TestStructureSaysWhenTwoVersionsAreTheSameText(t *testing.T) {
	// Two versions can hold the same text: a schema deleted and registered
	// again is a new version of what was already there.
	same := &model.SchemaSubject{Name: "orders-value",
		Versions: []model.SchemaVersion{
			{Version: 1, ID: 7, Format: "AVRO", Definition: subjectV1},
			{Version: 2, ID: 7, Format: "AVRO", Definition: subjectV1},
		}}
	got := strings.Join(labelTexts(structureView(same, nil, nil)), "\n")
	if !strings.Contains(got, "From version 1 to version 2: the same text.") {
		t.Errorf("two versions of one text read as:\n%s", got)
	}
	if strings.Contains(got, "arrived") {
		t.Errorf("two versions of one text report lines changing:\n%s", got)
	}
}

func TestStructureNamesACompatibilityModeItCannotExplain(t *testing.T) {
	// A registry may grow a level this build has not heard of. Naming it
	// without explaining it is better than explaining it wrongly.
	odd := &model.SchemaSubject{Name: "orders-value", Compatibility: "SIDEWAYS",
		Versions: []model.SchemaVersion{{Version: 1, ID: 1, Format: "AVRO", Definition: subjectV1}}}
	got := strings.Join(labelTexts(structureView(odd, nil, nil)), "\n")
	if !strings.Contains(got, "Compatibility: SIDEWAYS.") {
		t.Errorf("an unfamiliar compatibility mode reads as:\n%s", got)
	}
}

func TestStructureComparesTheTwoMostRecentVersions(t *testing.T) {
	// Three versions, and the comparison opens on the last two: that is the
	// question somebody has when they open a subject (ADR-0105).
	three := &model.SchemaSubject{Name: "orders-value",
		Versions: []model.SchemaVersion{
			{Version: 1, ID: 1, Format: "AVRO",
				Definition: `{"type":"record","name":"Order","fields":[{"name":"scrapped","type":"string"}]}`},
			{Version: 2, ID: 2, Format: "AVRO", Definition: subjectV1},
			{Version: 3, ID: 3, Format: "AVRO", Definition: subjectV2},
		}}
	got := strings.Join(labelTexts(structureView(three, nil, nil)), "\n")
	// The pickers say which two versions are being compared, and they are
	// the newest and the one before it.
	for _, want := range []string{"From version 2 to version 3", "compared with"} {
		if !strings.Contains(got, want) {
			t.Errorf("the comparison does not say %q:\n%s", want, got)
		}
	}
	// Version 1 is two comparisons away and nothing of it is shown — neither
	// as a version being compared nor as a line of one.
	if strings.Contains(got, "version 1") {
		t.Errorf("the comparison opens on the oldest version:\n%s", got)
	}
	if strings.Contains(got, "scrapped") {
		t.Errorf("the comparison reaches back past the version before the newest:\n%s", got)
	}
	// What version 3 added over version 2 is what it shows.
	if !strings.Contains(got, "total") {
		t.Errorf("what the newest version added is not shown:\n%s", got)
	}
}

// A consumer group in the structure view (T2.75, FR-13.10).

func TestStructureShowsWhoIsInAGroupAndWhatTheyRead(t *testing.T) {
	group := &model.ConsumerGroup{ID: "readers", State: "Stable",
		Members: []model.GroupMember{
			{ID: "m-1", ClientID: "ikigai", Host: "/10.0.0.4",
				Assignment: []model.TopicPartition{
					{Topic: "orders", Partition: 0},
					{Topic: "orders", Partition: 2},
					{Topic: "audit", Partition: 1},
				}},
			// In the group and given nothing to read, which is what somebody
			// looks for when a group is not getting through its logs.
			{ID: "m-2", ClientID: "ikigai", Host: "/10.0.0.5"},
		}}
	got := strings.Join(labelTexts(structureView(group, nil, nil)), "\n")
	for _, want := range []string{
		"Stable, with 2 members.",
		"Members", "Member", "Client", "Host", "Assigned",
		"m-1", "/10.0.0.4", "m-2",
		// Gathered by topic, rather than a line for every partition.
		"orders 0, 2", "audit 1",
		"nothing",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("a group does not say %q:\n%s", want, got)
		}
	}
}

func TestStructureSaysWhenAGroupHasNobodyInIt(t *testing.T) {
	empty := &model.ConsumerGroup{ID: "readers", State: "Empty"}
	got := strings.Join(labelTexts(structureView(empty, nil, nil)), "\n")
	if !strings.Contains(got, "Empty, with nobody in it.") {
		t.Errorf("a group with no members says:\n%s", got)
	}
	// And no table of members with nothing in it.
	if strings.Contains(got, "Assigned") {
		t.Errorf("a group with no members draws a table of them:\n%s", got)
	}

	// One member reads as one, not as "1 members".
	one := &model.ConsumerGroup{ID: "readers", State: "Stable",
		Members: []model.GroupMember{{ID: "m-1"}}}
	if got := strings.Join(labelTexts(structureView(one, nil, nil)), "\n"); !strings.Contains(got, "with one member.") {
		t.Errorf("a group of one says:\n%s", got)
	}

	// A state the cluster never gave is said as an absence rather than left
	// as a blank somebody has to interpret.
	none := &model.ConsumerGroup{ID: "readers"}
	if got := strings.Join(labelTexts(structureView(none, nil, nil)), "\n"); !strings.Contains(got, "did not name") {
		t.Errorf("a group with no state says:\n%s", got)
	}
}

func TestStructureOpensForAnObjectWithNoRowsToOpen(t *testing.T) {
	// A cluster has an account of itself and nothing to browse. The action
	// was offered only for what could be read, so this was unreachable — and
	// said nothing about being unreachable, the menu item merely sitting
	// grey as though somebody had decided it (ADR-0106).
	fx := newFixture(t)
	c, err := fx.conns.Create(store.SavedConnection{Name: "kafka1", Driver: "recordfake", Host: "kafka1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	fx.s.Explorer.Refresh(explorer.RootID)
	loaded(t, fx, explorer.RootID)
	loaded(t, fx, view.ConnectionID(c.ID))
	fx.s.Explorer.Tree.Select(view.NodeID(c.ID, model.NewRef(model.KindCluster, "cluster")))
	fx.s.sync()

	if fx.s.menuItems[cmdStructure].Disabled {
		t.Fatal("a cluster has a description and no rows; Open Structure should be offered")
	}
	fx.s.run(cmdStructure)
	tb := fx.onlyTab(t)
	pump(t, fx.q, func() bool { return showsAll(tb, "one broker, calling itself cluster.") })
	// And what it shows is the cluster itself, not the view's apology for a
	// kind of object it does not know.
	if showsAll(tb, "cannot be shown yet") {
		t.Error("the cluster's structure reads as a kind the view cannot show")
	}
}
