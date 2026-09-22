package kafka

import (
	"context"
	"errors"
	"fmt"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Writing a record (T2.77, FR-13.11).
//
// This is the first thing this driver does that changes anything, and it is
// held to what every write here is held to: read-only mode refuses it, and a
// connection tagged production refuses it without consent given for this
// record and no other (FR-13.21, FR-4.9).

// Produce writes one record and says where it landed.
func (s *kafkaSource) Produce(ctx context.Context, rec source.ProduceRequest) (_ model.TopicPartition, _ int64, err error) {
	defer panics.Recover(&err, "writing a record")

	// Before anything is dialled. A refusal that reached the cluster first
	// would already have cost something, and read-only means no request.
	if err := s.cfg.Guard.Allow(source.AccessWrite, rec.Confirmed); err != nil {
		return model.TopicPartition{}, 0, err
	}
	if rec.Topic == "" {
		return model.TopicPartition{}, 0, errors.New("kafka: a record needs a topic to be written to")
	}
	if err := s.addressed(ctx, rec); err != nil {
		return model.TopicPartition{}, 0, err
	}
	if err := s.readable(ctx, rec); err != nil {
		return model.TopicPartition{}, 0, err
	}

	r := &kgo.Record{Topic: rec.Topic, Partition: rec.Partition, Key: rec.Key, Value: rec.Value}
	for _, h := range rec.Headers {
		r.Headers = append(r.Headers, kgo.RecordHeader{Key: h.Key, Value: h.Value})
	}
	res := s.client.ProduceSync(ctx, r)
	if err := res.FirstErr(); err != nil {
		return model.TopicPartition{}, 0, err
	}
	// Where it actually went, which for a record addressed to no partition
	// is the first anybody knows of it.
	written := res[0].Record
	return model.TopicPartition{Topic: written.Topic, Partition: written.Partition}, written.Offset, nil
}

// addressed refuses a record sent to a partition the topic has not got.
//
// The partitioner cannot refuse it: it answers with an index and has nowhere
// to put an error, so a partition out of range would quietly become some
// other partition. A record in the wrong log is worse than a record refused,
// so the count is checked first — one metadata request, and only where a
// partition was named at all.
func (s *kafkaSource) addressed(ctx context.Context, rec source.ProduceRequest) error {
	if rec.Partition < 0 {
		return nil // hashed by key, wherever that leads
	}
	md, err := s.admin.Metadata(ctx, rec.Topic)
	if err != nil {
		return err
	}
	d, ok := md.Topics[rec.Topic]
	if !ok {
		return fmt.Errorf("kafka: this cluster has no topic %q", rec.Topic)
	}
	if d.Err != nil {
		return d.Err
	}
	if n := len(d.Partitions); int(rec.Partition) >= n {
		return fmt.Errorf("kafka: %s is cut into %d partitions, numbered 0 to %d, so there is no partition %d to write to",
			rec.Topic, n, n-1, rec.Partition)
	}
	return nil
}

// readable refuses a record the subject's schema cannot read.
//
// What is checked is what will be written: the bytes are handed to the same
// decoder a reader would use, and a record that cannot be read back is not
// written at all. A record consumers cannot decode is a production incident
// rather than a warning, and this is the last moment it can be prevented.
//
// Naming no subject skips it, which is the ordinary case. Most topics have no
// schema, and a connection that names a registry must still be able to write
// to them.
func (s *kafkaSource) readable(ctx context.Context, rec source.ProduceRequest) error {
	if rec.Subject == "" {
		return nil
	}
	if s.registry == nil {
		return errNoRegistry
	}
	dec, err := s.Decoder(ctx, rec.Subject)
	if err != nil {
		return err
	}
	if _, err := dec.Decode(rec.Value); err != nil {
		return fmt.Errorf("kafka: this record is not what %s describes, so nothing was written: %w", rec.Subject, err)
	}
	return nil
}

// sentPartition is how a record reaches the partition it was addressed to.
//
// kgo chooses a partitioner once for a whole client rather than once per
// record, so honouring an explicit partition and hashing by key cannot be two
// clients without dialling the cluster twice. This is one partitioner that
// does both: a record addressed to a partition goes there, and one addressed
// to none is hashed by key exactly as the default does it.
//
// "None" is -1 rather than the zero value, because 0 is a partition. A record
// that meant nothing by its partition and a record that meant the first one
// must not look alike.
type sentPartition struct{ otherwise kgo.Partitioner }

func (p sentPartition) ForTopic(topic string) kgo.TopicPartitioner {
	return sentTopicPartition{p.otherwise.ForTopic(topic)}
}

// sentTopicPartition is one topic's share of that, delegating everything it
// does not decide.
type sentTopicPartition struct{ kgo.TopicPartitioner }

func (p sentTopicPartition) Partition(r *kgo.Record, n int) int {
	if r.Partition >= 0 && int(r.Partition) < n {
		return int(r.Partition)
	}
	return p.TopicPartitioner.Partition(r, n)
}
