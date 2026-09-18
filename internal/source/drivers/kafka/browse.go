package kafka

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Reading a topic's records into the grid (T2.63, FR-13.4).
//
// A log is not a table, and the difference shows in every line here. There is
// no ordering to ask for: a partition is ordered by itself and nothing orders
// across partitions. There is no filtering to push down: a broker hands over
// bytes and asks no questions about them. And there is no query language to
// refuse one in.
//
// What there is instead is a position and a bound. This reads the log as it
// stands: where each partition ends is asked first, so that catching up is
// something known rather than something waited for, and the read stops at the
// bound the grid gave it (NFR-P11).

// records is how many a browse reads when the caller names no bound. The grid
// always names one; this is for anything that does not.
const records = 256

// Browse reads records from a topic.
//
// It assigns partitions explicitly and never joins a consumer group or commits
// an offset, so that looking at a topic cannot move anybody else's place in it
// (FR-13.19). That is not an option a caller sets: there is no way to ask this
// for the other thing.
func (s *kafkaSource) Browse(ctx context.Context, ref model.ObjectRef, opt source.BrowseOptions) (_ model.RowStream, err error) {
	defer panics.Recover(&err, "reading records")

	if ref.Kind != model.KindTopic {
		return nil, fmt.Errorf("kafka: %s holds no records to read", ref.Kind)
	}
	if opt.Where != "" {
		// Kafka has no language to write a condition in, so there is nothing
		// this could mean (REQ-DRV-3).
		return nil, errors.New("kafka: a topic takes no condition; there is no language to write one in")
	}
	if len(opt.Filters) > 0 {
		// Filtering records is a thing this application does to what it has
		// read (T2.67), not something a broker will do for it.
		return nil, errors.New("kafka: a broker does not filter records; a filter is applied to what has been read")
	}
	if len(opt.Sorts) > 0 {
		return nil, errors.New("kafka: records are in the order they were written, which is the only order a log has")
	}
	if opt.Seek != nil {
		return nil, errors.New("kafka: this connection reads from the beginning of a log; seeking is not written yet")
	}
	if opt.Follow {
		return nil, errors.New("kafka: this connection reads the log as it stands; following it is not written yet")
	}

	limit := opt.Limit
	if limit <= 0 {
		limit = records
	}
	if opt.Offset > 0 {
		// A log is read from a position, not by skipping a count: what the
		// grid means by an offset is a row number, and a record's offset is
		// its own. Seeking (T2.64) is how a person says where to start.
		return nil, errors.New("kafka: a log is read from a position rather than by skipping rows")
	}

	topic := ref.Name()
	ends, err := s.admin.ListEndOffsets(ctx, topic)
	if err != nil {
		return nil, err
	}
	starts, err := s.admin.ListStartOffsets(ctx, topic)
	if err != nil {
		return nil, err
	}

	// Where to begin, and where there is nothing more to read: both asked now,
	// so that a log which is caught up says so rather than being waited on.
	at := map[string]map[int32]kgo.Offset{topic: {}}
	until := map[int32]int64{}
	for partition, o := range starts[topic] {
		if o.Err != nil {
			return nil, fmt.Errorf("kafka: partition %d: %w", partition, o.Err)
		}
		end, ok := ends[topic][partition]
		if !ok || end.Err != nil {
			return nil, fmt.Errorf("kafka: partition %d: where its log ends could not be read", partition)
		}
		if end.Offset <= o.Offset {
			continue // nothing in it
		}
		at[topic][partition] = kgo.NewOffset().At(o.Offset)
		until[partition] = end.Offset
	}

	opts, err := clientOf(s.cfg)
	if err != nil {
		return nil, err
	}
	// No consumer group is named, which is what makes this a reader rather
	// than a participant.
	opts = append(opts, kgo.ConsumePartitions(at))
	client, err := kgo.NewClient(opts...)
	if err != nil {
		return nil, &source.ConnectError{Kind: source.ConnectConfig,
			Hint: "These settings could not become a reader.", Err: err}
	}
	return &recordRows{client: client, topic: topic, until: until, left: limit}, nil
}

// recordRows is a bounded read of a topic's records.
type recordRows struct {
	client *kgo.Client
	topic  string
	until  map[int32]int64 // where each partition had ended when the read began
	left   int64

	held   []*kgo.Record
	closed bool
	mu     sync.Mutex
}

// recordColumns are what a record is, in the order somebody reads it: where it
// was, when it arrived, and what it carried (FR-13.4).
var recordColumns = []model.ColumnDef{
	{Name: "partition", Type: model.DataType{Class: model.TypeInteger, Native: "int32"}, ReadOnly: true},
	{Name: "offset", Type: model.DataType{Class: model.TypeInteger, Native: "int64"}, ReadOnly: true},
	{Name: "timestamp", Type: model.DataType{Class: model.TypeTimestamp, Native: "timestamp"}, ReadOnly: true},
	{Name: "key", Type: model.DataType{Class: model.TypeBytes, Native: "bytes", Nullable: true}, ReadOnly: true},
	{Name: "value", Type: model.DataType{Class: model.TypeBytes, Native: "bytes", Nullable: true}, ReadOnly: true},
	{Name: "headers", Type: model.DataType{Class: model.TypeArray, Native: "headers", Nullable: true}, ReadOnly: true},
}

func (r *recordRows) Columns() []model.ColumnDef { return recordColumns }

func (r *recordRows) Next(ctx context.Context) (model.Row, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed || r.left <= 0 {
		return nil, io.EOF
	}
	for len(r.held) == 0 {
		done, err := r.fill(ctx)
		if err != nil {
			return nil, err
		}
		if done {
			return nil, io.EOF
		}
	}
	rec := r.held[0]
	r.held = r.held[1:]
	r.left--
	return rowOf(rec), nil
}

// fill reads another batch, and says whether there is nothing left to read.
func (r *recordRows) fill(ctx context.Context) (bool, error) {
	if len(r.until) == 0 {
		return true, nil
	}
	max := int(r.left)
	fetches := r.client.PollRecords(ctx, max)
	if err := ctx.Err(); err != nil {
		return false, err
	}
	// A partition that failed is said so, rather than read as a log that
	// ended: the difference is between nothing more and nothing known.
	if errs := fetches.Errors(); len(errs) > 0 {
		for _, e := range errs {
			if errors.Is(e.Err, context.Canceled) || errors.Is(e.Err, context.DeadlineExceeded) {
				return false, e.Err
			}
			return false, fmt.Errorf("kafka: partition %d: %w", e.Partition, e.Err)
		}
	}
	fetches.EachRecord(func(rec *kgo.Record) {
		end, wanted := r.until[rec.Partition]
		if !wanted || rec.Offset >= end {
			// Past where the log ended when this read began: somebody else is
			// still writing, and this read is of the log as it stood.
			return
		}
		r.held = append(r.held, rec)
		if rec.Offset == end-1 {
			delete(r.until, rec.Partition)
		}
	})
	return len(r.held) == 0 && len(r.until) == 0, nil
}

func (r *recordRows) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	r.client.Close()
	return nil
}

// rowOf is one record as the grid holds it. Its key and value are the bytes
// they were written as: what they mean is a decoder's business (T2.68), and
// nothing here pretends to know.
func rowOf(rec *kgo.Record) model.Row {
	row := model.Row{
		int64(rec.Partition),
		rec.Offset,
		rec.Timestamp,
		nil,
		nil,
		nil,
	}
	if rec.Key != nil {
		row[3] = rec.Key
	}
	if rec.Value != nil {
		row[4] = rec.Value
	}
	if len(rec.Headers) > 0 {
		// A list rather than a map: Kafka lets a header name repeat, and a map
		// would quietly keep one of them.
		headers := make([]any, 0, len(rec.Headers))
		for _, h := range rec.Headers {
			headers = append(headers, map[string]any{"key": h.Key, "value": h.Value})
		}
		row[5] = headers
	}
	return row
}
