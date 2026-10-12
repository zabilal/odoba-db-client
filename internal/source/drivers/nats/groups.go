package nats

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
)

// Who is reading a stream, and how far they have got (FR-13.10).
//
// A consumer is what a consumer group is on Kafka: something the server keeps
// that remembers a place in a log on behalf of whoever reads through it. It
// differs in one way that matters to the numbers. Kafka's group commits an
// offset, and everything before it is done. A consumer here is told message by
// message which ones were dealt with, so there are two places and not one: how
// far it has been sent, and how far everything has been acknowledged. What is
// shown as where it is, is the second — the place before which nothing is
// outstanding — and what is shown as how far behind it is, is everything not
// yet acknowledged, sent or not.

// ConsumerGroups lists every stream's consumers, without their progress.
func (s *natsSource) ConsumerGroups(ctx context.Context) (_ []model.ConsumerGroup, err error) {
	defer panics.Recover(&err, "listing the consumers")
	streams, err := s.streams(ctx)
	if err != nil {
		return nil, err
	}
	var out []model.ConsumerGroup
	for _, st := range streams {
		if st.State.Consumers == 0 {
			continue
		}
		consumers, err := s.consumers(ctx, st.Config.Name)
		if err != nil {
			return nil, err
		}
		for _, c := range consumers {
			out = append(out, model.ConsumerGroup{ID: groupID(st.Config.Name, c.Name), State: stateOf(c)})
		}
	}
	return out, nil
}

// consumer is one consumer as the server describes it now.
func (s *natsSource) consumer(ctx context.Context, id string) (*jetstream.ConsumerInfo, error) {
	stream, name, err := splitGroup(id)
	if err != nil {
		return nil, err
	}
	c, err := s.js.Consumer(ctx, stream, name)
	if err != nil {
		switch {
		case errors.Is(err, jetstream.ErrConsumerNotFound):
			// Said to be missing rather than answered with no progress,
			// which would read as a consumer that is up to date.
			return nil, fmt.Errorf("nats: stream %q has no consumer %q", stream, name)
		case errors.Is(err, jetstream.ErrStreamNotFound):
			return nil, missing(err, stream)
		}
		return nil, err
	}
	return c.Info(ctx)
}

// GroupOffsets is one consumer's progress through its stream.
func (s *natsSource) GroupOffsets(ctx context.Context, groupID string) (_ []model.GroupOffset, err error) {
	defer panics.Recover(&err, "reading a consumer's progress")
	c, err := s.consumer(ctx, groupID)
	if err != nil {
		return nil, err
	}
	st, err := s.js.Stream(ctx, c.Stream)
	if err != nil {
		return nil, missing(err, c.Stream)
	}
	info, err := st.Info(ctx)
	if err != nil {
		return nil, err
	}
	return []model.GroupOffset{progressOf(c, info.State.LastSeq)}, nil
}

// progressOf is a consumer's place in its stream, as the model holds a group's
// place in a partition: the next message it has to deal with, the place the
// next message will be written, and how many it has not yet acknowledged.
func progressOf(c *jetstream.ConsumerInfo, last uint64) model.GroupOffset {
	o := model.GroupOffset{Topic: c.Stream, Partition: 0, End: int64(last) + 1,
		// Not the distance between the two places: a consumer may read only
		// some of a stream's subjects, and then most of what lies between
		// here and the end was never its to read. The server counts what is.
		Lag: int64(c.NumPending) + int64(c.NumAckPending)}
	if c.Delivered.Consumer == 0 {
		// Never sent anything: it has no place yet, which is not the same as
		// being at the beginning.
		o.Current = -1
		return o
	}
	o.Current = int64(c.AckFloor.Stream) + 1
	return o
}

// TopicTotals reads how much a stream has carried and how much room it takes
// (FR-13.17).
func (s *natsSource) TopicTotals(ctx context.Context, topic string) (_ model.TopicTotals, err error) {
	defer panics.Recover(&err, "measuring a stream")
	st, err := s.js.Stream(ctx, topic)
	if err != nil {
		return model.TopicTotals{}, missing(err, topic)
	}
	info, err := st.Info(ctx)
	if err != nil {
		return model.TopicTotals{}, err
	}
	// The last sequence is every message ever written, kept or not: a
	// sequence is never used twice, so it never falls. The bytes are what is
	// kept now, and can.
	return model.TopicTotals{At: time.Now(), Records: int64(info.State.LastSeq),
		Bytes: int64(info.State.Bytes), Sized: true}, nil
}
