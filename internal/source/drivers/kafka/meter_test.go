package kafka

import (
	"context"
	"testing"

	"github.com/twmb/franz-go/pkg/kadm"
)

// How much a topic holds, counted once (T5.14, FR-13.17).
//
// The suite's cluster is a single broker keeping one copy of everything, so
// nothing live can tell "one copy of each partition" from "every copy of it".
// This can: the log directories are built by hand, with three brokers and three
// copies of each partition.

// dirs is what the brokers say they hold: for each broker, each partition of
// each topic and its size.
func dirs(held map[int32]map[string]map[int32]int64) kadm.DescribedAllLogDirs {
	out := make(kadm.DescribedAllLogDirs, len(held))
	for broker, topics := range held {
		parts := make(kadm.DescribedLogDirTopics, len(topics))
		for topic, sizes := range topics {
			ps := make(map[int32]kadm.DescribedLogDirPartition, len(sizes))
			for p, size := range sizes {
				ps[p] = kadm.DescribedLogDirPartition{Broker: broker, Dir: "/logs",
					Topic: topic, Partition: p, Size: size}
			}
			parts[topic] = ps
		}
		out[broker] = kadm.DescribedLogDirs{"/logs": {Broker: broker, Dir: "/logs", Topics: parts}}
	}
	return out
}

// A topic kept in three copies holds what one of them holds: the copy on the
// broker leading each partition. Counting them all would say a topic carries
// three times what was written to it.
func TestATopicKeptThreeTimesHoldsWhatOneCopyHolds(t *testing.T) {
	// Two partitions, three brokers, every broker holding a copy of both. The
	// brokers are numbered from nought, as a real cluster's are, so that a
	// partition whose leader nobody named cannot be read as broker nought's.
	held := dirs(map[int32]map[string]map[int32]int64{
		0: {"orders": {0: 1000, 1: 2000}},
		1: {"orders": {0: 1000, 1: 2000}},
		2: {"orders": {0: 1000, 1: 2000}},
	})
	// Broker 0 leads partition 0 and broker 2 leads partition 1.
	leaders := map[int32]int32{0: 0, 1: 2}
	got, ok := roomOf(held, "orders", leaders)
	if !ok {
		t.Fatal("it counted nothing")
	}
	if got != 3000 {
		t.Errorf("a topic holding 3000 bytes in three copies reads as %d", got)
	}
	// A partition whose leader nobody named is not counted on a guess: the
	// figure would be some copy or other rather than the log.
	if got, _ := roomOf(held, "orders", map[int32]int32{0: 0}); got != 1000 {
		t.Errorf("with one leader known it reads as %d", got)
	}
	// And nothing is counted where no broker holding it leads it.
	if got, ok := roomOf(held, "orders", map[int32]int32{0: 9}); ok || got != 0 {
		t.Errorf("a leader no broker is reads as %d, %v", got, ok)
	}
}

// Another topic's logs are another topic's, and a size the broker would not give
// is not a size of nothing.
func TestOnlyThisTopicsLogsAreCounted(t *testing.T) {
	held := dirs(map[int32]map[string]map[int32]int64{
		0: {"orders": {0: 500}, "invoices": {0: 90_000}},
	})
	got, ok := roomOf(held, "orders", map[int32]int32{0: 0})
	if !ok || got != 500 {
		t.Errorf("a topic beside a bigger one reads as %d, %v", got, ok)
	}
	// A partition whose size the broker would not give is left out rather than
	// counted as a negative number of bytes.
	unknown := dirs(map[int32]map[string]map[int32]int64{
		0: {"orders": {0: -1, 1: 700}},
	})
	got, ok = roomOf(unknown, "orders", map[int32]int32{0: 0, 1: 0})
	if !ok || got != 700 {
		t.Errorf("a partition of unknown size reads as %d, %v", got, ok)
	}
	// Nothing at all is not a size: a nought would read as an empty topic where
	// the truth is that nobody said.
	if got, ok := roomOf(dirs(nil), "orders", map[int32]int32{0: 0}); ok || got != 0 {
		t.Errorf("brokers that said nothing read as %d, %v", got, ok)
	}
	if got, ok := roomOf(unknown, "orders", map[int32]int32{0: 0}); ok || got != 0 {
		t.Errorf("one partition of unknown size alone reads as %d, %v", got, ok)
	}
}

// Which broker leads which partition, as the metadata says, and nothing about a
// partition the cluster could not describe.
func TestWhichBrokerLeadsWhichPartition(t *testing.T) {
	got := leadersOf(kadm.TopicDetail{Partitions: kadm.PartitionDetails{
		0: {Partition: 0, Leader: 1},
		1: {Partition: 1, Leader: 3},
		2: {Partition: 2, Leader: 2, Err: errNoSuchTopic("orders")},
	}})
	if len(got) != 2 || got[0] != 1 || got[1] != 3 {
		t.Errorf("the leaders read as %v", got)
	}
	if _, ok := got[2]; ok {
		t.Error("a partition the cluster could not describe has a leader")
	}
}

// A topic to measure needs a name, said before a cluster is asked anything.
func TestATopicToMeasureNeedsAName(t *testing.T) {
	var s kafkaSource
	_, err := s.TopicTotals(context.TODO(), "")
	if err == nil {
		t.Fatal("a topic with no name was measured")
	}
	if got := err.Error(); got != "kafka: a topic to measure needs a name" {
		t.Errorf("it says %q", got)
	}
	// And what a cluster that has never heard of one says is about the topic.
	if got := errNoSuchTopic("ghost").Error(); got != `kafka: this cluster has no topic "ghost"` {
		t.Errorf("it says %q", got)
	}
}
