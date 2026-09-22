//go:build conformance

package kafka

import (
	"context"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Making, unmaking and reshaping topics (T2.78, FR-13.12).

// administers is the interface a connection offers for changing topics, and
// the claim that says it offers it.
func administers(t *testing.T, src source.Source) source.TopicAdmin {
	t.Helper()
	if !src.Capabilities().Stream.TopicAdmin {
		t.Fatal("the driver administers topics and does not claim to")
	}
	a, ok := src.(source.TopicAdmin)
	if !ok {
		t.Fatal("claims Stream.TopicAdmin but does not implement TopicAdmin")
	}
	return a
}

// settled waits for a broker to know a topic as this many partitions.
//
// Creating a topic, and adding to one, are accepted by the controller before
// every broker has heard of it, so reading it back at once can find nothing
// at all. That is the cluster catching up rather than the topic being wrong,
// and waiting for the condition is honest where a fixed pause would only be
// lucky.
func settled(t *testing.T, src source.Source, topic string, want int) []model.Partition {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(20 * time.Second)
	for {
		parts, _, err := src.(*kafkaSource).logs(ctx, topic)
		if err == nil && len(parts) == want {
			return parts
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s is not known as %d partitions: %v", topic, want, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestLiveATopicIsMadeReshapedAndUnmade(t *testing.T) {
	src := producing(t, source.Guard{})
	a := administers(t, src)
	ctx := context.Background()
	const name = "ikigai_it_admin"

	// Start from nothing, whatever an earlier run left behind.
	_ = a.DeleteTopic(ctx, name, false)

	if err := a.CreateTopic(ctx, source.TopicSpec{
		Name: name, Partitions: 2, ReplicationFactor: 1,
		Config: map[string]string{"retention.ms": "600000"},
	}); err != nil {
		t.Fatalf("creating %s: %v", name, err)
	}
	t.Cleanup(func() { _ = a.DeleteTopic(context.Background(), name, false) })

	settled(t, src, name, 2)

	// Making it again is refused and says so, rather than quietly doing
	// nothing: a name already taken is something somebody needs told.
	if err := a.CreateTopic(ctx, source.TopicSpec{
		Name: name, Partitions: 2, ReplicationFactor: 1}); err == nil {
		t.Error("a topic that already exists was created a second time")
	}

	// More logs than it had.
	if err := a.AddPartitions(ctx, name, 2, false); err != nil {
		t.Fatalf("adding partitions to %s: %v", name, err)
	}
	settled(t, src, name, 4)

	// Configured one setting at a time, leaving the rest as they were.
	if err := a.AlterTopicConfig(ctx, name,
		map[string]string{"retention.ms": "1200000"}, false); err != nil {
		t.Fatalf("configuring %s: %v", name, err)
	}

	// And unmade.
	if err := a.DeleteTopic(ctx, name, false); err != nil {
		t.Fatalf("deleting %s: %v", name, err)
	}
}

func TestLiveWhatCannotBeAskedOfATopicReachesNoBroker(t *testing.T) {
	// Each of these is wrong before a cluster is involved, and is refused
	// here so that the answer is a plain one rather than a broker's.
	a := administers(t, producing(t, source.Guard{}))
	ctx := context.Background()

	if err := a.CreateTopic(ctx, source.TopicSpec{Name: "", Partitions: 1, ReplicationFactor: 1}); err == nil {
		t.Error("a topic with no name was created")
	}
	if err := a.AddPartitions(ctx, "ikigai_it_admin", 0, false); err == nil {
		t.Error("a topic was asked to gain no partitions")
	}
	if err := a.AlterTopicConfig(ctx, "ikigai_it_admin", nil, false); err == nil {
		t.Error("a topic was configured with nothing")
	}
}

func TestLiveATopicSaysWhatItIsSetToAndWhoSetIt(t *testing.T) {
	src := producing(t, source.Guard{})
	a := administers(t, src)
	ctx := context.Background()
	const name = "ikigai_it_config"

	_ = a.DeleteTopic(ctx, name, false)
	if err := a.CreateTopic(ctx, source.TopicSpec{Name: name, Partitions: 1, ReplicationFactor: 1,
		Config: map[string]string{"retention.ms": "600000"}}); err != nil {
		t.Fatalf("creating %s: %v", name, err)
	}
	t.Cleanup(func() { _ = a.DeleteTopic(context.Background(), name, false) })
	settled(t, src, name, 1)

	desc, err := src.Describe(ctx, model.NewRef(model.KindTopic, "cluster", name))
	if err != nil {
		t.Fatalf("describing %s: %v", name, err)
	}
	top, ok := desc.(*model.Topic)
	if !ok {
		t.Fatalf("a topic is described as a %T", desc)
	}
	if len(top.Config) == 0 {
		t.Fatal("a topic is described with none of its settings")
	}

	var retention *model.ConfigEntry
	for i, c := range top.Config {
		if c.Name == "retention.ms" {
			retention = &top.Config[i]
		}
		// Every setting says where it came from: that distinction is the
		// whole of what FR-13.12 asks for here.
		if c.Source == "" {
			t.Errorf("%s says nothing about where it came from", c.Name)
		}
	}
	if retention == nil {
		t.Fatal("the setting the topic was created with is not among its settings")
	}
	if retention.Value != "600000" {
		t.Errorf("retention.ms reads as %q", retention.Value)
	}
	// It was given when the topic was made, so it is somebody's choice
	// rather than the cluster's default.
	if !retention.IsOverride() {
		t.Errorf("a setting given at creation reads as inherited: %+v", retention)
	}
	// And most of what a topic has is nobody's choice, which is the other
	// half of the same distinction.
	inherited := 0
	for _, c := range top.Config {
		if !c.IsOverride() {
			inherited++
		}
	}
	if inherited == 0 {
		t.Error("every one of a topic's settings reads as chosen, which no topic's are")
	}
}
