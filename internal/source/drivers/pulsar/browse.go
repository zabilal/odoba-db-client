package pulsar

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
	"sync/atomic"

	pulsargo "github.com/apache/pulsar-client-go/pulsar"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Reading a topic's messages into the grid (FR-13.4).
//
// Through a reader, which is Pulsar's own way of looking at a topic without
// taking part in it: it acknowledges nothing, no subscription's place moves
// because of it, and it is gone when it is closed. So looking at a topic cannot
// perturb whoever is really reading it (FR-13.19). While it is open it is a
// subscription of a kind, with a name that says whose, and it is left out of
// the subscriptions shown (tree.go).
//
// A read stops when the reader says there is nothing more, or at the bound the
// grid gave it (NFR-P11). What is written while it reads is read too, up to
// that bound: a reader has no end fixed in advance, and nothing here pretends
// it has.

// records is how many a browse reads when the caller names no bound.
const records = 256

// CanFollow is a topic: its messages are what there is to read.
func (s *pulsarSource) CanFollow(ref model.ObjectRef) bool { return ref.Kind == model.KindTopic }

func (s *pulsarSource) Browse(ctx context.Context, ref model.ObjectRef, opt source.BrowseOptions) (_ model.RowStream, err error) {
	defer panics.Recover(&err, "reading messages")

	if !s.CanFollow(ref) {
		return nil, fmt.Errorf("pulsar: a %s holds no messages to read", ref.Kind)
	}
	switch {
	case opt.Where != "":
		return nil, errors.New("pulsar: a topic takes no condition; there is no language to write one in")
	case len(opt.Filters) > 0:
		return nil, errors.New("pulsar: a broker does not filter messages; a filter is applied to what has been read")
	case len(opt.Sorts) > 0:
		return nil, errors.New("pulsar: messages are in the order they were written, which is the only order a topic has")
	case opt.Offset > 0:
		return nil, errors.New("pulsar: a topic is read from a position rather than by skipping rows")
	}
	start, byTime, err := positionOf(opt.Seek, opt.Follow)
	if err != nil {
		return nil, err
	}
	if s.client == nil {
		return nil, errors.New("pulsar: this connection is closed")
	}
	if err := s.exists(ctx, ref.Name()); err != nil {
		return nil, err
	}
	reader, err := s.client.CreateReader(pulsargo.ReaderOptions{
		Topic: ref.Name(), StartMessageID: start, SubscriptionRolePrefix: ours,
		// No more held than a page: a reader's own default is a thousand
		// messages waiting, of whatever size they are.
		ReceiverQueueSize: 100,
	})
	if err != nil {
		return nil, fmt.Errorf("pulsar: %s could not be read: %w", ref.Name(), err)
	}
	if byTime {
		if err := reader.SeekByTime(opt.Seek.Time); err != nil {
			reader.Close()
			return nil, fmt.Errorf("pulsar: %s could not be read from that time: %w", ref.Name(), err)
		}
	}
	return &messageRows{reader: reader, left: boundOf(opt), following: opt.Follow}, nil
}

// boundOf is how many messages a read stops after. A tail has no count to stop
// at; it is bounded by whoever is reading it and by being cancelled.
func boundOf(opt source.BrowseOptions) int64 {
	if opt.Follow {
		return 0
	}
	if opt.Limit <= 0 {
		return records
	}
	return opt.Limit
}

// positionOf is where a reader begins, and whether it is then moved to a time.
//
// A message here is addressed by a ledger and an entry and not by a number, so
// the two ways of saying where to begin that count — an offset, and the last
// few — have nothing to count with, and are refused rather than answered with
// something near them (REQ-DRV-3).
func positionOf(seek *source.Seek, following bool) (pulsargo.MessageID, bool, error) {
	if seek == nil || seek.Mode == source.SeekBeginning {
		if following {
			// Following with nowhere named is following what comes next.
			return pulsargo.LatestMessageID(), false, nil
		}
		return pulsargo.EarliestMessageID(), false, nil
	}
	if len(seek.Partitions) > 0 {
		return nil, false, errors.New("pulsar: one partition of a topic is a topic of its own; open that one to read it alone")
	}
	switch seek.Mode {
	case source.SeekEnd:
		if !following {
			return nil, false, errors.New("pulsar: reading from the end of a topic returns nothing unless the topic is being followed")
		}
		return pulsargo.LatestMessageID(), false, nil
	case source.SeekTimestamp:
		return pulsargo.EarliestMessageID(), true, nil
	case source.SeekOffset:
		return nil, false, errors.New("pulsar: a message is addressed by a ledger and an entry rather than by a number, " +
			"so there is no offset to read from; read from a time instead")
	case source.SeekLast:
		return nil, false, errors.New("pulsar: a topic does not number its messages, so there is no counting back from its end; " +
			"read from a time instead")
	}
	return nil, false, fmt.Errorf("pulsar: there is no way to read a topic from %d", seek.Mode)
}

// messageRows hands messages over as rows.
type messageRows struct {
	reader    pulsargo.Reader
	left      int64
	following bool

	mu     sync.Mutex
	closed atomic.Bool
}

// The columns are a Kafka record's, so that the same window reads both
// (ADR-0177). offset is the message's address — ledger, entry and, in a batch,
// which of the batch — as text, because it is not a number.
var messageColumns = []model.ColumnDef{
	{Name: "partition", Type: model.DataType{Class: model.TypeInteger, Native: "int32"}, ReadOnly: true},
	{Name: "offset", Type: model.DataType{Class: model.TypeString, Native: "message id"}, ReadOnly: true},
	{Name: "timestamp", Type: model.DataType{Class: model.TypeTimestamp, Native: "publish time"}, ReadOnly: true},
	{Name: "key", Type: model.DataType{Class: model.TypeString, Native: "key", Nullable: true}, ReadOnly: true},
	{Name: "value", Type: model.DataType{Class: model.TypeBytes, Native: "bytes", Nullable: true}, ReadOnly: true},
	{Name: "headers", Type: model.DataType{Class: model.TypeArray, Native: "properties", Nullable: true}, ReadOnly: true},
}

func (r *messageRows) Columns() []model.ColumnDef { return messageColumns }

func (r *messageRows) Next(ctx context.Context) (_ model.Row, err error) {
	defer panics.Recover(&err, "reading a message")
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r.closed.Load() || (!r.following && r.left <= 0) {
		return nil, io.EOF
	}
	if !r.following && !r.reader.HasNext() {
		return nil, io.EOF
	}
	msg, err := r.reader.Next(ctx)
	if err != nil {
		if r.closed.Load() {
			return nil, io.EOF // closed from under it: the read is over, not broken
		}
		return nil, err
	}
	if !r.following {
		r.left--
	}
	return rowOf(msg), nil
}

// Close ends the read and the reader with it. More than once is the same as
// once.
func (r *messageRows) Close() error {
	if !r.closed.Swap(true) {
		r.reader.Close()
	}
	return nil
}

func rowOf(msg pulsargo.Message) model.Row {
	id := msg.ID()
	row := model.Row{int64(id.PartitionIdx()), address(id), msg.PublishTime().UTC(), nil, nil, nil}
	if key := msg.Key(); key != "" {
		row[3] = key
	}
	if !msg.IsNullValue() {
		// Empty where it was written empty, which is not no value.
		row[4] = msg.Payload()
	}
	if props := msg.Properties(); len(props) > 0 {
		names := make([]string, 0, len(props))
		for name := range props {
			names = append(names, name)
		}
		// Properties come from a map and have no order of their own; the same
		// message has to read the same way twice.
		sort.Strings(names)
		headers := make([]any, 0, len(names))
		for _, name := range names {
			headers = append(headers, map[string]any{"key": name, "value": []byte(props[name])})
		}
		row[5] = headers
	}
	return row
}

// address is where a message is, as Pulsar writes it: its ledger and its entry,
// and its place in the batch where it was written in one.
func address(id pulsargo.MessageID) string {
	at := fmt.Sprintf("%d:%d", id.LedgerID(), id.EntryID())
	if id.BatchIdx() >= 0 && id.BatchSize() > 1 {
		at += fmt.Sprintf(":%d", id.BatchIdx())
	}
	return at
}
