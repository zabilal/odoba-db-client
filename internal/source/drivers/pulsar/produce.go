package pulsar

import (
	"context"
	"errors"
	"fmt"

	pulsargo "github.com/apache/pulsar-client-go/pulsar"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Writing one message (FR-13.11).
//
// A producer is made for the message and closed after it: this application
// writes one message when somebody asks it to, and a producer left open on a
// topic is a publisher the cluster would list for as long as the window was.

func (s *pulsarSource) Produce(ctx context.Context, rec source.ProduceRequest) (_ model.TopicPartition, _ int64, err error) {
	defer panics.Recover(&err, "writing a message")
	none := model.TopicPartition{}

	if err := s.cfg.Guard.Allow(source.AccessWrite, rec.Confirmed); err != nil {
		return none, 0, err
	}
	switch {
	case rec.Topic == "":
		return none, 0, errors.New("pulsar: a message needs a topic to be written to")
	case rec.Partition >= 0:
		// Which partition a message goes to is the key's to decide, and one
		// partition is a topic of its own that can be written to by name.
		return none, 0, fmt.Errorf("pulsar: a message is not sent to partition %d of a topic; "+
			"it goes where its key sends it, or to that partition's own topic by name", rec.Partition)
	case rec.Subject != "":
		return none, 0, errors.New("pulsar: checking a message against a schema is not done here, so nothing was written")
	case s.client == nil:
		return none, 0, errors.New("pulsar: this connection is closed")
	}
	if err := s.exists(ctx, rec.Topic); err != nil {
		return none, 0, err
	}
	producer, err := s.client.CreateProducer(pulsargo.ProducerOptions{Topic: rec.Topic, Name: ""})
	if err != nil {
		return none, 0, fmt.Errorf("pulsar: %s could not be written to: %w", rec.Topic, err)
	}
	defer producer.Close()

	msg := &pulsargo.ProducerMessage{Payload: rec.Value, Key: string(rec.Key)}
	for _, h := range rec.Headers {
		if msg.Properties == nil {
			msg.Properties = map[string]string{}
		}
		if _, twice := msg.Properties[h.Key]; twice {
			// A property is said once: Pulsar keeps them by name, and the
			// second would silently replace the first.
			return none, 0, fmt.Errorf("pulsar: the property %q is given twice, and a message has each of its properties once", h.Key)
		}
		msg.Properties[h.Key] = string(h.Value)
	}
	id, err := producer.Send(ctx, msg)
	if err != nil {
		return none, 0, fmt.Errorf("pulsar: the message was not written to %s: %w", rec.Topic, err)
	}
	// Where it went, as far as a number can say: the entry, which is its
	// place in the ledger it was written to and not in the topic.
	return model.TopicPartition{Topic: rec.Topic, Partition: max(id.PartitionIdx(), 0)}, id.EntryID(), nil
}
