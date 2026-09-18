//go:build conformance

package kafka

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Topics in the tree, against a real broker (T2.60, FR-13.2).

// seededTopic makes a topic to look at, or leaves the one already there: a
// test that has run before must read the same as one that has not.
func seededTopic(t *testing.T, src source.Source, name string, partitions int32) {
	t.Helper()
	s := src.(*kafkaSource)
	resps, err := s.admin.CreateTopics(context.Background(), partitions, 1, nil, name)
	if err != nil {
		t.Fatalf("seeding %s: %v", name, err)
	}
	for _, r := range resps.Sorted() {
		if r.Err != nil && !errors.Is(r.Err, kerr.TopicAlreadyExists) {
			t.Fatalf("seeding %s: %v", r.Topic, r.Err)
		}
	}
}

// topicsClass is the node a cluster holds its topics under.
func topicsClass(t *testing.T, src source.Source) model.Node {
	t.Helper()
	ctx := context.Background()
	roots, err := src.Root(ctx)
	if err != nil || len(roots) != 1 {
		t.Fatalf("root: %v, %v", roots, err)
	}
	classes, err := src.Children(ctx, roots[0].Ref)
	if err != nil {
		t.Fatalf("the cluster's classes: %v", err)
	}
	for _, c := range classes {
		if kind, ok := model.ClassOf(c.Ref); ok && kind == model.KindTopic {
			return c
		}
	}
	t.Fatalf("the cluster holds no topics class: %+v", classes)
	return model.Node{}
}

func TestLiveTheClusterHoldsItsTopics(t *testing.T) {
	src := live(t, liveConfig())
	seededTopic(t, src, "ikigai_it_orders", 3)
	ctx := context.Background()

	class := topicsClass(t, src)
	// The class is there whether or not anything is in it, so the cluster
	// never opens onto nothing.
	if !class.HasChildren && class.Badge != nil && class.Badge.Text != "0" {
		t.Errorf("the topics class is %+v", class)
	}

	nodes, err := src.Children(ctx, class.Ref)
	if err != nil {
		t.Fatalf("the topics: %v", err)
	}
	var found *model.Node
	for i, n := range nodes {
		if n.Label == "ikigai_it_orders" {
			found = &nodes[i]
		}
		// Kafka's own topics are the cluster's workings, and are not listed.
		if strings.HasPrefix(n.Label, "__") {
			t.Errorf("an internal topic is in the tree: %s", n.Label)
		}
		if n.Ref.Kind != model.KindTopic {
			t.Errorf("%s is in the topics class as a %s", n.Label, n.Ref.Kind)
		}
	}
	if found == nil {
		t.Fatalf("the seeded topic is not in the tree: %+v", nodes)
	}
	// How many logs it is cut into, which naming it already said.
	if found.Badge == nil || found.Badge.Text != "3" {
		t.Errorf("the topic is badged %+v", found.Badge)
	}
	// Its partitions are under it, and it says so before anybody asks, so the
	// expander is drawn without a fetch (T2.62).
	if !found.HasChildren {
		t.Error("a topic claims no children though its partitions are listed")
	}
}

func TestLiveWhatTheTreeSaysMatchesWhatTheClusterHolds(t *testing.T) {
	src := live(t, liveConfig())
	seededTopic(t, src, "ikigai_it_orders", 3)
	ctx := context.Background()
	s := src.(*kafkaSource)

	// Every topic the cluster has, internal ones included.
	md, err := s.admin.Metadata(ctx)
	if err != nil {
		t.Fatalf("metadata: %v", err)
	}
	var internal, ordinary int
	for _, d := range md.Topics.Sorted() {
		if d.IsInternal {
			internal++
		} else {
			ordinary++
		}
	}

	nodes, err := src.Children(ctx, topicsClass(t, src).Ref)
	if err != nil {
		t.Fatalf("the topics: %v", err)
	}
	// The tree is the cluster's topics less Kafka's own: that is the filter,
	// stated as a count rather than as a hope that an internal topic exists.
	if len(nodes) != ordinary {
		t.Errorf("the tree lists %d topics, and the cluster holds %d that are not its own (%d that are)",
			len(nodes), ordinary, internal)
	}
}

func TestLiveDescribesATopic(t *testing.T) {
	src := live(t, liveConfig())
	seededTopic(t, src, "ikigai_it_orders", 3)
	ctx := context.Background()

	nodes, err := src.Children(ctx, topicsClass(t, src).Ref)
	if err != nil {
		t.Fatalf("the topics: %v", err)
	}
	var ref model.ObjectRef
	for _, n := range nodes {
		if n.Label == "ikigai_it_orders" {
			ref = n.Ref
		}
	}
	if ref.IsZero() {
		t.Fatal("the seeded topic is not in the tree")
	}

	written(t, src, "ikigai_it_orders", 5)

	desc, err := src.Describe(ctx, ref)
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	top, ok := desc.(*model.Topic)
	if !ok {
		t.Fatalf("a topic is described as %T", desc)
	}
	if top.Name != "ikigai_it_orders" || top.Internal {
		t.Errorf("the topic reads as %+v", top)
	}
	if len(top.Partitions) != 3 {
		t.Fatalf("the topic has %d partitions", len(top.Partitions))
	}
	// One broker in this rig, so one copy of each partition, and it leads.
	if top.ReplicationFactor != 1 {
		t.Errorf("the topic is kept %d times", top.ReplicationFactor)
	}
	for _, p := range top.Partitions {
		if p.Leader < 0 || len(p.Replicas) != 1 || len(p.ISR) != 1 {
			t.Errorf("partition %d reads as %+v", p.ID, p)
		}
		// A log knows where it starts and where it ends, and not knowing is
		// not the same as being empty.
		if p.LowWatermark < 0 || p.HighWatermark < 0 {
			t.Errorf("partition %d has unknown watermarks: %+v", p.ID, p)
		}
	}
	// What was written is there to be read. More may be, if this test has run
	// before: a log keeps what it was given.
	if n := top.MessageCount(); n < 5 {
		t.Errorf("a topic written to five times holds %d records", n)
	}
	// What it occupies is said as what it is: the bytes of every replica.
	size, ok := top.Attrs["size on disk"]
	if !ok {
		t.Fatalf("the topic says nothing about its size: %+v", top.Attrs)
	}
	if !strings.Contains(size, "across all replicas") {
		t.Errorf("the size reads as %q, which would be taken for the data's own", size)
	}
}

// written puts a few records in a topic, so that what is counted is not
// nothing: an empty log proves only that zero reads as zero.
func written(t *testing.T, src source.Source, topic string, records int) {
	t.Helper()
	s := src.(*kafkaSource)
	ctx := context.Background()
	for i := 0; i < records; i++ {
		if err := s.client.ProduceSync(ctx, &kgo.Record{
			Topic: topic, Value: []byte("a record")}).FirstErr(); err != nil {
			t.Fatalf("writing to %s: %v", topic, err)
		}
	}
}
