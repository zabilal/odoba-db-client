//go:build conformance

package kafka

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// How much a topic carries, against a real broker (T5.14, FR-13.17).
//
// There is no rate to read, so what is proven here is the reading: the totals go
// up by what was written, the bytes are the leaders' copies rather than every
// copy, and two readings make the rate somebody sees.

// meters is the interface for reading a topic's totals, and the claim that says
// it is there.
func meters(t *testing.T, src source.Source) source.TopicMeter {
	t.Helper()
	if !src.Capabilities().Stream.Throughput {
		t.Fatal("the driver measures topics and does not claim to")
	}
	m, ok := src.(source.TopicMeter)
	if !ok {
		t.Fatal("claims Stream.Throughput but does not implement TopicMeter")
	}
	return m
}

// A topic's totals go up by what was written to it, and two readings make the
// rate. Nothing else about a cluster answers that question at all.
func TestLiveWhatATopicCarriesIsReadFromItsTotals(t *testing.T) {
	src := producing(t, source.Guard{})
	m := meters(t, src)
	ctx := context.Background()
	const topic = "ikigai_it_meter"
	seededTopic(t, src, topic, 2)

	before, err := m.TopicTotals(ctx, topic)
	if err != nil {
		t.Fatalf("reading the totals: %v", err)
	}
	if before.At.IsZero() {
		t.Error("a reading is not stamped")
	}
	if before.Records < 0 {
		t.Errorf("a topic has carried %d records", before.Records)
	}
	// Written to, then read again: the difference is what was written.
	p := src.(source.StreamProducer)
	const wrote = 5
	for i := 0; i < wrote; i++ {
		if _, _, err := p.Produce(ctx, source.ProduceRequest{Topic: topic, Partition: -1,
			Value: []byte(`{"measured":true}`)}); err != nil {
			t.Fatal(err)
		}
	}
	after := settledTotals(t, m, topic, before.Records+wrote)
	if after.Records != before.Records+wrote {
		t.Errorf("%d records were written and the totals moved by %d",
			wrote, after.Records-before.Records)
	}
	// And the two of them make a rate: records a second, over the time between.
	rate, ok := model.RateBetween(before, after)
	if !ok {
		t.Fatal("two readings made no rate")
	}
	if rate.Records <= 0 {
		t.Errorf("five records in a moment reads as %v a second", rate.Records)
	}
	if !rate.At.Equal(after.At) {
		t.Errorf("the rate is stamped %v, not %v", rate.At, after.At)
	}
}

// settledTotals waits for a reading to have caught up with what was written: a
// produce is acknowledged by the leader before every broker's own view of the
// watermark has moved.
func settledTotals(t *testing.T, m source.TopicMeter, topic string, want int64) model.TopicTotals {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		got, err := m.TopicTotals(context.Background(), topic)
		if err != nil {
			t.Fatalf("reading the totals: %v", err)
		}
		if got.Records >= want {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("the totals say %d records, waiting for %d", got.Records, want)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// The bytes are what the leaders hold, not what every copy of the log holds: a
// topic kept in three copies takes three times the room, and none of that is
// what somebody means by how much it carries.
func TestLiveWhatATopicOccupiesIsCountedOnce(t *testing.T) {
	src := producing(t, source.Guard{})
	m := meters(t, src)
	ctx := context.Background()
	const topic = "ikigai_it_meter_size"
	seededTopic(t, src, topic, 2)
	// Written to, because an empty log occupies nothing and proves nothing.
	p := src.(source.StreamProducer)
	for i := 0; i < 3; i++ {
		if _, _, err := p.Produce(ctx, source.ProduceRequest{Topic: topic, Partition: -1,
			Value: []byte(`{"occupies":"something"}`)}); err != nil {
			t.Fatal(err)
		}
	}
	settledTotals(t, m, topic, 3)

	got, err := m.TopicTotals(ctx, topic)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Sized {
		t.Fatal("this broker would not say how much room the logs take")
	}
	if got.Bytes <= 0 {
		t.Errorf("a topic with records in it occupies %d bytes", got.Bytes)
	}
	// Every copy would be the replication factor times this. One broker keeps
	// one copy, so the figure is the same either way here — what the test can
	// hold is that it is the size of a log rather than of a whole disk.
	parts, _, err := src.(*kafkaSource).logs(ctx, topic)
	if err != nil {
		t.Fatal(err)
	}
	if got.Bytes > 1<<30 {
		t.Errorf("%d partitions of a test topic occupy %d bytes, which is a volume rather than a log",
			len(parts), got.Bytes)
	}
}

// A topic the cluster has never heard of is said rather than measured as nought:
// a sparkline of a topic that is not there would be a quiet topic.
func TestLiveATopicThatIsNotThereCannotBeMeasured(t *testing.T) {
	src := producing(t, source.Guard{})
	m := meters(t, src)
	_, err := m.TopicTotals(context.Background(), "ikigai_it_no_such_topic_at_all")
	if err == nil {
		t.Fatal("a topic that is not there was measured")
	}
	if !strings.Contains(err.Error(), "no topic") {
		t.Errorf("it says %q", err)
	}
	// And a topic with no name is refused before the cluster is asked.
	if _, err := m.TopicTotals(context.Background(), ""); err == nil {
		t.Error("a topic with no name was measured")
	}
}
