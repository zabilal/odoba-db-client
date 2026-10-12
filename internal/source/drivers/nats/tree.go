package nats

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
)

// The explorer tree (FR-2.1, FR-13.1):
//
//	server              [name]
//	  Topics            model.ClassRef
//	    stream          [name, stream]
//	  Consumer groups   model.ClassRef
//	    consumer        [name, stream/consumer]
//
// A stream has nothing under it: it is one log, and a level holding a single
// partition numbered nought would be a level invented to match somebody else's
// shape. A consumer belongs to a stream and is named with it, because two
// streams may each have a consumer of the same name — and neither name can hold
// a slash, so the join can always be taken apart again.

// name is what the server is called in the tree: its cluster's name where it
// is in one, its own otherwise.
func (s *natsSource) name() string {
	if n := s.nc.ConnectedClusterName(); n != "" {
		return n
	}
	if n := s.nc.ConnectedServerName(); n != "" {
		return n
	}
	return "NATS"
}

func (s *natsSource) Root(context.Context) (_ []model.Node, err error) {
	defer panics.Recover(&err, "reading the server")
	name := s.name()
	// It holds its streams, and always says so: the class is shown even when
	// empty, so this never opens onto nothing.
	return []model.Node{{Ref: model.NewRef(model.KindCluster, name), Label: name, HasChildren: true}}, nil
}

func (s *natsSource) Children(ctx context.Context, ref model.ObjectRef) (_ []model.Node, err error) {
	defer panics.Recover(&err, "listing what is in a "+string(ref.Kind))

	switch ref.Kind {
	case model.KindCluster:
		streams, err := s.streams(ctx)
		if err != nil {
			return nil, err
		}
		consumers := 0
		for _, st := range streams {
			consumers += st.State.Consumers
		}
		out := []model.Node{model.ClassNode(ref, model.KindTopic, int64(len(streams)))}
		if consumers > 0 {
			out = append(out, model.ClassNode(ref, model.KindConsumerGroup, int64(consumers)))
		}
		return out, nil
	case model.KindFolder:
		kind, ok := model.ClassOf(ref)
		if !ok || len(ref.Path) < 1 {
			return nil, nil
		}
		switch kind {
		case model.KindTopic:
			return s.streamNodes(ctx, ref.Path[0])
		case model.KindConsumerGroup:
			return s.consumerNodes(ctx, ref.Path[0])
		}
	}
	return nil, nil
}

// streams are the streams this account holds, in name order.
func (s *natsSource) streams(ctx context.Context) ([]*jetstream.StreamInfo, error) {
	lister := s.js.ListStreams(ctx)
	var out []*jetstream.StreamInfo
	for info := range lister.Info() {
		out = append(out, info)
	}
	if err := lister.Err(); err != nil {
		return nil, err
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Config.Name < out[b].Config.Name })
	return out, nil
}

func (s *natsSource) streamNodes(ctx context.Context, server string) ([]model.Node, error) {
	streams, err := s.streams(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]model.Node, 0, len(streams))
	for _, st := range streams {
		out = append(out, model.Node{
			Ref:   model.NewRef(model.KindTopic, server, st.Config.Name),
			Label: st.Config.Name, Browsable: true, Describable: true,
			// How many messages it holds, which the server keeps and says for
			// nothing: a count here is exact and costs no read.
			Badge: &model.Badge{Text: strconv.FormatUint(st.State.Msgs, 10), Exact: true},
		})
	}
	return out, nil
}

// ours marks the consumers this application makes to read a stream with, so
// that they are not shown as somebody's: a person looking at who is reading a
// stream did not ask to see themselves looking.
const ours = "ikigai-reader"

// consumers are a stream's consumers, in name order, without this
// application's own.
func (s *natsSource) consumers(ctx context.Context, stream string) ([]*jetstream.ConsumerInfo, error) {
	st, err := s.js.Stream(ctx, stream)
	if err != nil {
		return nil, missing(err, stream)
	}
	lister := st.ListConsumers(ctx)
	var out []*jetstream.ConsumerInfo
	for info := range lister.Info() {
		if _, mine := info.Config.Metadata[ours]; !mine {
			out = append(out, info)
		}
	}
	if err := lister.Err(); err != nil {
		return nil, err
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Name < out[b].Name })
	return out, nil
}

func (s *natsSource) consumerNodes(ctx context.Context, server string) ([]model.Node, error) {
	streams, err := s.streams(ctx)
	if err != nil {
		return nil, err
	}
	var out []model.Node
	for _, st := range streams {
		if st.State.Consumers == 0 {
			continue
		}
		consumers, err := s.consumers(ctx, st.Config.Name)
		if err != nil {
			return nil, err
		}
		for _, c := range consumers {
			id := groupID(st.Config.Name, c.Name)
			out = append(out, model.Node{
				Ref:   model.NewRef(model.KindConsumerGroup, server, id),
				Label: id, Describable: true,
				Attrs: map[string]string{"state": stateOf(c), "stream": st.Config.Name},
			})
		}
	}
	return out, nil
}

// groupID names a consumer with the stream it reads, and splitGroup takes the
// name apart again.
func groupID(stream, consumer string) string { return stream + "/" + consumer }

func splitGroup(id string) (stream, consumer string, err error) {
	// With no slash there is no consumer's name, which is what is looked for.
	stream, consumer, _ = strings.Cut(id, "/")
	if stream == "" || consumer == "" || strings.Contains(consumer, "/") {
		return "", "", fmt.Errorf("nats: %q is not a consumer: one is named by its stream, a slash and its own name", id)
	}
	return stream, consumer, nil
}

// stateOf is what kind of consumer one is, in the two ways that decide whether
// it will still be there tomorrow and whether anybody is reading through it.
func stateOf(c *jetstream.ConsumerInfo) string {
	kind := "ephemeral"
	if c.Config.Durable != "" {
		kind = "durable"
	}
	if c.NumWaiting > 0 || c.PushBound {
		return kind + ", being read"
	}
	return kind + ", idle"
}

// missing says a stream is not there in this application's words, and leaves
// every other failure as it was.
func missing(err error, stream string) error {
	if errors.Is(err, jetstream.ErrStreamNotFound) {
		return fmt.Errorf("nats: this server has no stream %q", stream)
	}
	return err
}

// Describe is what one stream is, or one consumer.
func (s *natsSource) Describe(ctx context.Context, ref model.ObjectRef) (_ any, err error) {
	defer panics.Recover(&err, "reading the structure")
	switch ref.Kind {
	case model.KindTopic:
		st, err := s.js.Stream(ctx, ref.Name())
		if err != nil {
			return nil, missing(err, ref.Name())
		}
		info, err := st.Info(ctx)
		if err != nil {
			return nil, missing(err, ref.Name())
		}
		return topicOf(info), nil
	case model.KindConsumerGroup:
		c, err := s.consumer(ctx, ref.Name())
		if err != nil {
			return nil, err
		}
		g := &model.ConsumerGroup{ID: ref.Name(), State: stateOf(c)}
		offsets, err := s.GroupOffsets(ctx, ref.Name())
		if err != nil {
			return nil, err
		}
		g.Offsets = offsets
		return g, nil
	}
	return nil, fmt.Errorf("nats: a %s has no structure to read", ref.Kind)
}

// topicOf is a stream as the model holds a log: one partition, from the first
// message it still has to the place the next one will go, and the settings
// that decide what it keeps.
func topicOf(info *jetstream.StreamInfo) *model.Topic {
	c := info.Config
	first, next := int64(info.State.FirstSeq), int64(info.State.LastSeq)+1
	if info.State.Msgs == 0 {
		// An empty stream's first message is the one that has not been
		// written: nothing lies between where it begins and where it ends.
		first = next
	}
	t := &model.Topic{Name: c.Name, ReplicationFactor: int32(c.Replicas),
		Partitions: []model.Partition{{ID: 0, LowWatermark: first, HighWatermark: next}},
		Attrs:      map[string]string{"messages": strconv.FormatUint(info.State.Msgs, 10)}}
	if c.Description != "" {
		t.Attrs["description"] = c.Description
	}
	set := func(name, value string) {
		t.Config = append(t.Config, model.ConfigEntry{Name: name, Value: value, Source: "stream", ReadOnly: true})
	}
	set("subjects", strings.Join(c.Subjects, ", "))
	set("retention", c.Retention.String())
	set("storage", c.Storage.String())
	set("discard", c.Discard.String())
	set("max messages", limit(c.MaxMsgs))
	set("max bytes", limit(c.MaxBytes))
	set("max age", age(c.MaxAge))
	set("replicas", strconv.Itoa(c.Replicas))
	return t
}

// limit is a bound as it is said: a number, or that there is none. JetStream
// writes "no limit" as -1, and a setting shown as -1 reads as a mistake.
func limit(n int64) string {
	if n <= 0 {
		return "unlimited"
	}
	return strconv.FormatInt(n, 10)
}

func age(d time.Duration) string {
	if d <= 0 {
		return "unlimited"
	}
	return d.String()
}

// Badge is how many messages a stream holds, which the server says exactly.
func (s *natsSource) Badge(ctx context.Context, ref model.ObjectRef) (_ model.Badge, _ bool, err error) {
	defer panics.Recover(&err, "counting the messages")
	if ref.Kind != model.KindTopic {
		return model.Badge{}, false, nil
	}
	st, err := s.js.Stream(ctx, ref.Name())
	if err != nil {
		return model.Badge{}, false, missing(err, ref.Name())
	}
	info, err := st.Info(ctx)
	if err != nil {
		return model.Badge{}, false, err
	}
	return model.Badge{Text: strconv.FormatUint(info.State.Msgs, 10), Exact: true}, true, nil
}
