package kafka

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kerr"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
)

// How much a topic is carrying (T5.14, FR-13.17).
//
// Kafka's brokers know their own throughput and publish it over JMX, which is
// not a protocol this client speaks and not a port a person browsing a topic has
// any reason to have open. So the rate is measured here instead, from what the
// protocol does answer: the totals, read twice.
//
// Records come from the partitions' high watermarks, which count every record
// ever written whether or not it is still there — so their sum only grows, and
// the difference between two readings is exactly what was produced in between.
//
// Bytes come from the log directories, counted on the broker leading each
// partition rather than on all of them: a topic kept in three copies takes three
// times the room, and none of that is what somebody means by how much it
// carries. That reading can fall, because retention deletes whole segments, and
// a fall says nothing about what was written (model.RateBetween).

// errNoSuchTopic is what a cluster that has never heard of a topic says, in one
// place because three things ask it.
func errNoSuchTopic(name string) error {
	return fmt.Errorf("kafka: this cluster has no topic %q", name)
}

// TopicTotals reads how much has been written to a topic and how much room its
// logs take.
func (s *kafkaSource) TopicTotals(ctx context.Context, topic string) (_ model.TopicTotals, err error) {
	defer panics.Recover(&err, "reading how much a topic carries")

	if topic == "" {
		return model.TopicTotals{}, errors.New("kafka: a topic to measure needs a name")
	}
	md, err := s.admin.Metadata(ctx, topic)
	if err != nil {
		return model.TopicTotals{}, err
	}
	d, ok := md.Topics[topic]
	if !ok || errors.Is(d.Err, kerr.UnknownTopicOrPartition) {
		// A cluster answers a topic it has never heard of by naming it with an
		// error rather than by leaving it out, and either way what it means is
		// that there is no such topic (FR-13.2).
		return model.TopicTotals{}, errNoSuchTopic(topic)
	}
	if d.Err != nil {
		return model.TopicTotals{}, d.Err
	}
	ends, err := s.admin.ListEndOffsets(ctx, topic)
	if err != nil {
		return model.TopicTotals{}, err
	}

	if err := ends.Error(); err != nil {
		return model.TopicTotals{}, err
	}
	totals := model.TopicTotals{At: time.Now()}
	set := make(kadm.TopicsSet)
	ends.Each(func(o kadm.ListedOffset) {
		// A partition with no records answers nought, and one that could not be
		// read answers -1; neither is something to add.
		if o.Offset > 0 {
			totals.Records += o.Offset
		}
		set.Add(topic, o.Partition)
	})
	totals.Bytes, totals.Sized = s.logRoom(ctx, topic, set, leadersOf(d))
	return totals, nil
}

// leadersOf is which broker leads each of a topic's partitions.
func leadersOf(d kadm.TopicDetail) map[int32]int32 {
	out := make(map[int32]int32, len(d.Partitions))
	for _, p := range d.Partitions {
		if p.Err == nil {
			out[p.Partition] = p.Leader
		}
	}
	return out
}

// logRoom is how much room a topic's logs take on the brokers leading them, and
// false where the brokers would not say.
//
// Not saying is an answer here rather than a failure: some managed Kafka refuses
// DescribeLogDirs, and a topic whose records can be counted is worth measuring
// even where its bytes cannot be. A nought would read as an empty topic instead.
func (s *kafkaSource) logRoom(ctx context.Context, topic string, set kadm.TopicsSet, leaders map[int32]int32) (int64, bool) {
	dirs, err := s.admin.DescribeAllLogDirs(ctx, set)
	if err != nil {
		return 0, false
	}
	return roomOf(dirs, topic, leaders)
}

// roomOf is the counting, which is where the answer is decided: one copy of each
// partition, the one on the broker leading it.
//
// Apart from the request so that it can be held to a cluster with more brokers
// than one: a topic kept in three copies takes three times the room, and this is
// the only thing that says none of that is what it carries. The suite's own
// cluster is a single broker, so nothing live can tell the two apart.
//
// It is not what topicSize answers (introspect.go). That one is every replica
// summed, on purpose, because what a topic costs a cluster is every copy of it;
// this is what a topic holds, which is one.
func roomOf(dirs kadm.DescribedAllLogDirs, topic string, leaders map[int32]int32) (int64, bool) {
	var bytes int64
	counted := false
	for broker, held := range dirs {
		held.EachPartition(func(p kadm.DescribedLogDirPartition) {
			if p.Topic != topic || p.Size < 0 {
				return
			}
			if leader, ok := leaders[p.Partition]; !ok || leader != broker {
				return
			}
			bytes += p.Size
			counted = true
		})
	}
	return bytes, counted
}
