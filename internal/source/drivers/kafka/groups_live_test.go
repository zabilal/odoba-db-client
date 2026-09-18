//go:build conformance

package kafka

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Partitions and consumer groups in the tree (T2.62, FR-2.2).

// reading joins a consumer group and stays in it, because a group exists
// while something is reading: the caller closes it when it has finished
// looking. Joining is also what makes Kafka create __consumer_offsets, the
// internal topic the tree must not show.
func reading(t *testing.T, group, topic string) *kgo.Client {
	t.Helper()
	cl, err := kgo.NewClient(
		kgo.SeedBrokers(fmt.Sprintf("127.0.0.1:%d", port())),
		kgo.ConsumerGroup(group),
		kgo.ConsumeTopics(topic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
	)
	if err != nil {
		t.Fatalf("joining %s: %v", group, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// One poll is enough to be a member of the group rather than merely a
	// client that means to be.
	cl.PollRecords(ctx, 1)
	return cl
}

// classNamed is the class a cluster holds objects of the given kind under.
func classNamed(t *testing.T, src source.Source, kind model.ObjectKind) model.Node {
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
		if k, ok := model.ClassOf(c.Ref); ok && k == kind {
			return c
		}
	}
	t.Fatalf("the cluster holds no class of %s: %+v", kind, classes)
	return model.Node{}
}

// nodeNamed is the child of ref with the given label.
func nodeNamed(t *testing.T, src source.Source, ref model.ObjectRef, label string) model.Node {
	t.Helper()
	nodes, err := src.Children(context.Background(), ref)
	if err != nil {
		t.Fatalf("the children of %v: %v", ref, err)
	}
	for _, n := range nodes {
		if n.Label == label {
			return n
		}
	}
	t.Fatalf("%q is not under %v: %+v", label, ref, nodes)
	return model.Node{}
}

func TestLiveATopicOpensOntoItsPartitions(t *testing.T) {
	src := live(t, liveConfig())
	seededTopic(t, src, "ikigai_it_orders", 3)
	ctx := context.Background()

	topic := nodeNamed(t, src, classNamed(t, src, model.KindTopic).Ref, "ikigai_it_orders")
	// It says it holds them before anybody asks, so the expander is drawn
	// without a fetch.
	if !topic.HasChildren {
		t.Error("a topic claims no children though its partitions are listed")
	}

	parts, err := src.Children(ctx, topic.Ref)
	if err != nil {
		t.Fatalf("the partitions: %v", err)
	}
	if len(parts) != 3 {
		t.Fatalf("the topic opens onto %d partitions", len(parts))
	}
	for i, p := range parts {
		if p.Ref.Kind != model.KindPartition {
			t.Errorf("%s is under a topic as a %s", p.Label, p.Ref.Kind)
		}
		if want := fmt.Sprintf("partition %d", i); p.Label != want {
			t.Errorf("a partition is called %q, want %q", p.Label, want)
		}
		// A partition is a leaf: what is under it is records, which are the
		// grid's business rather than the tree's.
		if p.HasChildren {
			t.Errorf("%s claims children", p.Label)
		}
		// Which broker it is led from, and where its log runs.
		if p.Attrs["leader"] == "" || p.Attrs["leader"] == "-1" {
			t.Errorf("%s is led by %q", p.Label, p.Attrs["leader"])
		}
		if !strings.Contains(p.Attrs["offsets"], "to") {
			t.Errorf("%s runs %q", p.Label, p.Attrs["offsets"])
		}
		// Every copy is in sync in this rig, and what is true is not remarked
		// upon: the note is there for partitions that are short.
		if _, said := p.Attrs["in sync"]; said {
			t.Errorf("%s is remarked on though its copies keep up: %v", p.Label, p.Attrs)
		}
	}
}

func TestLiveTheClusterHoldsItsConsumerGroups(t *testing.T) {
	src := live(t, liveConfig())
	seededTopic(t, src, "ikigai_it_orders", 3)
	written(t, src, "ikigai_it_orders", 1)

	// Something has to be reading for there to be a group at all.
	cl := reading(t, "ikigai_it_readers", "ikigai_it_orders")
	defer cl.Close()

	group := nodeNamed(t, src, classNamed(t, src, model.KindConsumerGroup).Ref, "ikigai_it_readers")
	if group.Ref.Kind != model.KindConsumerGroup {
		t.Errorf("a group is in the tree as a %s", group.Ref.Kind)
	}
	// What it is doing comes with the listing, so the node says it without
	// anything having been described.
	if group.Attrs["state"] == "" {
		t.Errorf("the group says nothing about what it is doing: %v", group.Attrs)
	}
	// A group is a leaf here: its members and its lag are T2.75 and T2.76.
	if group.HasChildren {
		t.Error("a group claims children before its members are listed")
	}
}

func TestLiveKafkasOwnTopicsStayOutOfTheTree(t *testing.T) {
	src := live(t, liveConfig())
	seededTopic(t, src, "ikigai_it_orders", 3)
	written(t, src, "ikigai_it_orders", 1)

	// Reading through a group is what makes Kafka keep __consumer_offsets,
	// so this is the first point at which the cluster has an internal topic
	// for the filter to leave out.
	cl := reading(t, "ikigai_it_readers", "ikigai_it_orders")
	defer cl.Close()

	ctx := context.Background()
	s := src.(*kafkaSource)
	md, err := s.admin.Metadata(ctx)
	if err != nil {
		t.Fatalf("metadata: %v", err)
	}
	var own []string
	for _, d := range md.Topics.Sorted() {
		if d.IsInternal {
			own = append(own, d.Topic)
		}
	}
	if len(own) == 0 {
		t.Skip("this cluster has no internal topics yet, so there is nothing to leave out")
	}

	nodes, err := src.Children(ctx, classNamed(t, src, model.KindTopic).Ref)
	if err != nil {
		t.Fatalf("the topics: %v", err)
	}
	listed := map[string]bool{}
	for _, n := range nodes {
		listed[n.Label] = true
	}
	for _, name := range own {
		if listed[name] {
			t.Errorf("%s is Kafka's own and is in the tree", name)
		}
	}
	// And it is still a topic: what is hidden is where it appears, not what
	// it is.
	desc, err := src.Describe(ctx, model.NewRef(model.KindTopic, "cluster", own[0]))
	if err != nil {
		t.Fatalf("describing %s: %v", own[0], err)
	}
	if top, ok := desc.(*model.Topic); !ok || !top.Internal {
		t.Errorf("%s is described as %+v", own[0], desc)
	}
}
