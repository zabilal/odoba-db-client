package nats

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Reading a stream's messages into the grid (FR-13.4).
//
// A stream is read as a log is: from a position, to a bound. Where it ends is
// asked first, so that catching up is something known rather than something
// waited for, and the read stops at the bound the grid gave it (NFR-P11).
//
// It is read through a consumer, because that is the only way JetStream hands
// over messages in order — but one of the kind that changes nothing. It is
// ephemeral, it acknowledges nothing, no other consumer's place moves because
// of it, and it is removed when the read ends. So looking at a stream cannot
// perturb whoever is really reading it (FR-13.19), and it is marked as this
// application's own so that it is not listed among them (tree.go).

// records is how many a browse reads when the caller names no bound.
const records = 256

// idle is how long the server keeps a reader nobody is using before removing
// it. A read that ends removes its own; this is for one that could not.
const idle = 30 * time.Second

// CanFollow is a stream: its messages are what there is to read, and a
// consumer is something to look at rather than to read.
func (s *natsSource) CanFollow(ref model.ObjectRef) bool { return ref.Kind == model.KindTopic }

func (s *natsSource) Browse(ctx context.Context, ref model.ObjectRef, opt source.BrowseOptions) (_ model.RowStream, err error) {
	defer panics.Recover(&err, "reading messages")

	if !s.CanFollow(ref) {
		return nil, fmt.Errorf("nats: a %s holds no messages to read", ref.Kind)
	}
	switch {
	case opt.Where != "":
		// There is no language to write a condition in, so there is nothing
		// this could mean (REQ-DRV-3).
		return nil, errors.New("nats: a stream takes no condition; there is no language to write one in")
	case len(opt.Filters) > 0:
		return nil, errors.New("nats: a server does not filter messages by what is in them; a filter is applied to what has been read")
	case len(opt.Sorts) > 0:
		return nil, errors.New("nats: messages are in the order they were written, which is the only order a stream has")
	case opt.Offset > 0:
		return nil, errors.New("nats: a stream is read from a position rather than by skipping rows")
	case opt.Seek != nil && opt.Seek.Mode == source.SeekEnd && !opt.Follow:
		return nil, errors.New("nats: reading from the end of a stream returns nothing unless the stream is being followed")
	}

	name := ref.Name()
	st, err := s.js.Stream(ctx, name)
	if err != nil {
		return nil, missing(err, name)
	}
	info, err := st.Info(ctx)
	if err != nil {
		return nil, missing(err, name)
	}
	first, last := info.State.FirstSeq, info.State.LastSeq
	if info.State.Msgs == 0 {
		first = last + 1
	}
	conf, found, err := positionOf(first, last, opt.Seek, opt.Follow)
	if err != nil {
		return nil, err
	}
	if !found {
		return &messageRows{}, nil // nothing to read, and nothing was asked of the server
	}
	conf.InactiveThreshold = idle
	conf.Metadata = map[string]string{ours: "true"}

	cons, err := s.js.OrderedConsumer(ctx, name, conf)
	if err != nil {
		return nil, missing(err, name)
	}
	rows := &messageRows{js: s.js, stream: name, cons: cons, until: last,
		left: boundOf(opt), following: opt.Follow}
	if !opt.Follow {
		// How much there is to read, asked of the reader itself: a read from
		// a time has no sequence to compare with, and a stream whose last
		// messages were removed has an end nothing will arrive at.
		if state, err := cons.Info(ctx); err != nil {
			_ = rows.Close()
			return nil, err
		} else if state.NumPending == 0 {
			_ = rows.Close()
			return &messageRows{}, nil
		}
	}
	iter, err := cons.Messages()
	if err != nil {
		_ = rows.Close()
		return nil, err
	}
	rows.iter = iter
	return rows, nil
}

// boundOf is how many messages a read stops after. A tail has no count to
// stop at; it is bounded by whoever is reading it and by being cancelled.
func boundOf(opt source.BrowseOptions) int64 {
	if opt.Follow {
		return 0
	}
	if opt.Limit <= 0 {
		return records
	}
	return opt.Limit
}

// positionOf is where a read begins, as the consumer that will do it is told,
// and whether there is anything there to read.
//
// first and last are the stream's first and last sequences; an empty stream's
// first is one past its last. A read that is not following and begins past the
// end finds nothing. One that names partitions and does not name nought finds
// nothing either: a stream is one log, and it is partition nought.
func positionOf(first, last uint64, seek *source.Seek, following bool) (jetstream.OrderedConsumerConfig, bool, error) {
	var conf jetstream.OrderedConsumerConfig
	from := first
	if following && (seek == nil || seek.Mode == source.SeekBeginning) {
		// Following with nowhere named is following what comes next.
		from = last + 1
	}
	if seek != nil {
		if len(seek.Partitions) > 0 {
			only := false
			for _, p := range seek.Partitions {
				only = only || p == 0
			}
			if !only {
				return conf, false, nil
			}
		}
		switch seek.Mode {
		case source.SeekBeginning:
		case source.SeekEnd:
			from = last + 1
		case source.SeekOffset:
			from = first
			if seek.Offset > int64(first) {
				from = uint64(seek.Offset)
			}
		case source.SeekLast:
			from = first
			if seek.Count < int64(last+1-first) {
				from = last + 1 - uint64(max(seek.Count, 0))
			}
		case source.SeekTimestamp:
			at := seek.Time
			conf.DeliverPolicy = jetstream.DeliverByStartTimePolicy
			conf.OptStartTime = &at
			return conf, following || last >= first, nil
		default:
			return conf, false, fmt.Errorf("nats: there is no way to read a stream from %d", seek.Mode)
		}
	}
	if from > last {
		if !following {
			return conf, false, nil
		}
		conf.DeliverPolicy = jetstream.DeliverNewPolicy
		return conf, true, nil
	}
	conf.DeliverPolicy = jetstream.DeliverByStartSequencePolicy
	conf.OptStartSeq = from
	return conf, true, nil
}

// messageRows hands messages over as rows. The zero value is a read with
// nothing in it.
type messageRows struct {
	js     jetstream.JetStream
	stream string
	cons   jetstream.Consumer
	iter   jetstream.MessagesContext

	until     uint64 // the stream's last sequence when the read began
	left      int64
	following bool
	done      bool

	mu     sync.Mutex
	closed atomic.Bool
}

// The columns are a Kafka record's, so that the same window reads both
// (ADR-0176). partition is always nought: a stream is one log. key is the
// subject a message was published on, which is text and is what a message has
// where a record has a key.
var messageColumns = []model.ColumnDef{
	{Name: "partition", Type: model.DataType{Class: model.TypeInteger, Native: "int32"}, ReadOnly: true},
	{Name: "offset", Type: model.DataType{Class: model.TypeInteger, Native: "sequence"}, ReadOnly: true},
	{Name: "timestamp", Type: model.DataType{Class: model.TypeTimestamp, Native: "timestamp"}, ReadOnly: true},
	{Name: "key", Type: model.DataType{Class: model.TypeString, Native: "subject"}, ReadOnly: true},
	{Name: "value", Type: model.DataType{Class: model.TypeBytes, Native: "bytes"}, ReadOnly: true},
	{Name: "headers", Type: model.DataType{Class: model.TypeArray, Native: "headers", Nullable: true}, ReadOnly: true},
}

func (r *messageRows) Columns() []model.ColumnDef { return messageColumns }

func (r *messageRows) Next(ctx context.Context) (_ model.Row, err error) {
	defer panics.Recover(&err, "reading a message")
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r.iter == nil || r.done || r.closed.Load() || (!r.following && r.left <= 0) {
		return nil, io.EOF
	}
	msg, err := r.iter.Next(jetstream.NextContext(ctx))
	if err != nil {
		if r.closed.Load() || errors.Is(err, jetstream.ErrMsgIteratorClosed) {
			return nil, io.EOF // closed from under it: the read is over, not broken
		}
		return nil, err
	}
	meta, err := msg.Metadata()
	if err != nil {
		return nil, fmt.Errorf("nats: a message arrived without saying where it was in the stream: %w", err)
	}
	if !r.following {
		r.left--
		// The end is the end as it was when the read began: what has been
		// written since belongs to the next read, and nothing more is coming
		// that this one asked for.
		if meta.Sequence.Stream >= r.until || meta.NumPending == 0 {
			r.done = true
		}
	}
	return rowOf(msg, meta), nil
}

// Close ends the read and removes the reader it was made with.
func (r *messageRows) Close() error {
	if r.closed.Swap(true) {
		return nil
	}
	if r.iter != nil {
		r.iter.Stop()
	}
	if r.cons != nil {
		// Asked for and not waited on for long: the server removes an idle
		// reader by itself, so this is tidiness rather than correctness.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if name := r.cons.CachedInfo().Name; name != "" {
			_ = r.js.DeleteConsumer(ctx, r.stream, name)
		}
	}
	return nil
}

func rowOf(msg jetstream.Msg, meta *jetstream.MsgMetadata) model.Row {
	row := model.Row{int64(0), int64(meta.Sequence.Stream), meta.Timestamp.UTC(), msg.Subject(), msg.Data(), nil}
	if row[4] == nil {
		row[4] = []byte{} // a message with nothing in it carries nothing, which is not no value
	}
	if hs := msg.Headers(); len(hs) > 0 {
		names := make([]string, 0, len(hs))
		for name := range hs {
			names = append(names, name)
		}
		// A header's names come from a map and have no order of their own;
		// the same message has to read the same way twice.
		sort.Strings(names)
		var headers []any
		for _, name := range names {
			for _, v := range hs[name] {
				headers = append(headers, map[string]any{"key": name, "value": []byte(v)})
			}
		}
		row[5] = headers
	}
	return row
}
