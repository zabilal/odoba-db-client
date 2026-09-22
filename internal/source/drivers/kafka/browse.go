package kafka

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
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
	if opt.Seek != nil && opt.Seek.Mode == source.SeekEnd && !opt.Follow {
		// The end of a log is where nothing has been written yet, so a read
		// that stops there reads nothing at all. It means something only
		// while following, which is what it is for (FR-13.6).
		return nil, errors.New("kafka: reading from the end of a log returns nothing unless the log is being followed")
	}

	limit := boundOf(opt)
	if opt.Offset > 0 {
		// A log is read from a position, not by skipping a count: what the
		// grid means by an offset is a row number, and a record's offset is
		// its own. Seeking (T2.64) is how a person says where to start.
		return nil, errors.New("kafka: a log is read from a position rather than by skipping rows")
	}

	topic := ref.Name()
	// Where each log begins and ends, asked now: the first is what a read
	// from the beginning means, and the second is what lets a read end.
	listedEnds, err := s.admin.ListEndOffsets(ctx, topic)
	if err != nil {
		return nil, err
	}
	listedStarts, err := s.admin.ListStartOffsets(ctx, topic)
	if err != nil {
		return nil, err
	}
	begins, err := offsets(listedStarts[topic])
	if err != nil {
		return nil, err
	}
	ends, err := offsets(listedEnds[topic])
	if err != nil {
		return nil, err
	}

	// A time is a third question, and only asked when somebody asked by time.
	timed := map[int32]int64{}
	if opt.Seek != nil && opt.Seek.Mode == source.SeekTimestamp {
		listed, err := s.admin.ListOffsetsAfterMilli(ctx, opt.Seek.Time.UnixMilli(), topic)
		if err != nil {
			return nil, err
		}
		for partition, o := range listed[topic] {
			// A partition with nothing at or after that time answers with its
			// end offset, and is then dropped by the check below that there
			// is nothing to read past the end. It needs no guard of its own.
			if o.Err == nil {
				timed[partition] = o.Offset
			}
		}
	}

	spans, err := spansOf(begins, ends, timed, opt.Seek, opt.Follow)
	if err != nil {
		return nil, err
	}
	at := map[string]map[int32]kgo.Offset{topic: {}}
	until := map[int32]int64{}
	for partition, sp := range spans {
		at[topic][partition] = kgo.NewOffset().At(sp.from)
		until[partition] = sp.to
	}

	opts, err := clientOf(s.cfg)
	if err != nil {
		return nil, err
	}
	// No consumer group is named, which is what makes this a reader rather
	// than a participant.
	opts = append(opts, kgo.ConsumePartitions(at))
	opts = append(opts, readBounds()...)
	client, err := kgo.NewClient(opts...)
	if err != nil {
		return nil, &source.ConnectError{Kind: source.ConnectConfig,
			Hint: "These settings could not become a reader.", Err: err}
	}
	return &recordRows{client: client, topic: topic, until: until,
		left: limit, following: opt.Follow}, nil
}

// boundOf is how many records a read stops after.
//
// Every consume is bounded (FR-13.20), and a caller who names no bound gets
// this driver's own rather than all of a log: an unbounded read of a topic
// nobody has aged out is how an inspection becomes an incident.
//
// A tail is the exception and is not one really. It has no count to stop at,
// because a log being written to has no end to reach; it is bounded instead
// by whoever is reading it — the grid keeps what it can show and lets the
// rest go (NFR-P10) — and by being cancelled.
func boundOf(opt source.BrowseOptions) int64 {
	if opt.Follow {
		return 0
	}
	if opt.Limit <= 0 {
		return records
	}
	return opt.Limit
}

// How much a read may pull at once (FR-13.20).
//
// franz-go's own defaults are far larger, and they are defaults for a service
// that means to keep up with a topic. This is an application somebody is
// looking at a topic through: a bound small enough that a tail on a busy log
// cannot saturate the link or balloon in memory, and large enough that no
// ordinary record fails to fit. A record bigger than the partition bound
// still arrives — a broker always returns at least one batch — so this slows
// a pathological topic rather than breaking it.
const (
	fetchBytes          = 16 << 20 // 16 MiB across a fetch
	fetchPartitionBytes = 4 << 20  // 4 MiB from any one partition
	fetchWait           = 5 * time.Second
)

// readBounds are what keeps a read from asking for more than it can use.
func readBounds() []kgo.Opt {
	return []kgo.Opt{
		kgo.FetchMaxBytes(fetchBytes),
		kgo.FetchMaxPartitionBytes(fetchPartitionBytes),
		kgo.FetchMaxWait(fetchWait),
	}
}

// span is where one partition's read begins, and the offset it stops before.
type span struct {
	from int64
	to   int64
}

// offsets are what a listing said, by partition, or the first failure in it. A
// partition nobody could read the offsets of is not a partition that is empty.
func offsets(listed map[int32]kadm.ListedOffset) (map[int32]int64, error) {
	out := make(map[int32]int64, len(listed))
	for partition, o := range listed {
		if o.Err != nil {
			return nil, fmt.Errorf("kafka: partition %d: %w", partition, o.Err)
		}
		out[partition] = o.Offset
	}
	return out, nil
}

// spansOf is where each partition's read begins and ends (FR-13.5).
//
// A count back from the end is a count back from each partition's own end,
// because each log has one: asking for the last hundred records of a topic of
// eight partitions asks for the last hundred of each. Anything before a log
// begins is the beginning of it — a log is aged out from the front, and a
// position that has been aged past is not an error — and a partition with
// nothing left to read is left out rather than waited on.
// positionOf is where a seek lands in one partition, given where that
// partition begins and ends and — where the seek names a time — the offset
// the broker gave for that time.
//
// It is shared between reading a log and moving a group's offsets, so that
// "from that timestamp" means the same thing in both, which is what the model
// asks for where it holds a reset's seek. What it does not decide is whether
// there is anything there to read: that question belongs to reading alone,
// and moving a group to the end of a log is an ordinary thing to want.
//
// found is false where the seek names nothing in this partition, which today
// means a time nothing was written at or after.
func positionOf(start, end, atTime int64, timed bool, seek *source.Seek, following bool) (from int64, found bool, err error) {
	from = start
	if following && (seek == nil || seek.Mode == source.SeekBeginning) {
		// Following with nowhere named begins where the log is now: a tail is
		// about what comes next, not about what is already there.
		from = end
	}
	if seek != nil {
		switch seek.Mode {
		case source.SeekBeginning:
		case source.SeekEnd:
			from = end
		case source.SeekOffset:
			from = seek.Offset
		case source.SeekLast:
			from = end - seek.Count
		case source.SeekTimestamp:
			if !timed {
				return 0, false, nil // nothing in this log at or after that time
			}
			from = atTime
		default:
			return 0, false, fmt.Errorf("kafka: there is no way to read a log from %d", seek.Mode)
		}
	}
	if from < start {
		from = start
	}
	return from, true, nil
}

func spansOf(begins, ends, timed map[int32]int64, seek *source.Seek, following bool) (map[int32]span, error) {
	only := map[int32]bool{}
	if seek != nil {
		for _, p := range seek.Partitions {
			only[p] = true
		}
	}
	out := map[int32]span{}
	for partition, start := range begins {
		if len(only) > 0 && !only[partition] {
			continue
		}
		end, ok := ends[partition]
		if !ok {
			continue
		}
		at, haveTime := timed[partition]
		from, found, err := positionOf(start, end, at, haveTime, seek, following)
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		if from >= end && !following {
			// Nothing there to read, which is reading's own question to ask.
			continue
		}
		to := end
		if following {
			// A log being followed has no end to stop before.
			to = -1
		}
		out[partition] = span{from: from, to: to}
	}
	return out, nil
}

// recordRows is a bounded read of a topic's records.
type recordRows struct {
	client *kgo.Client
	topic  string
	until  map[int32]int64 // where each partition had ended when the read began
	left   int64

	// following keeps the stream open: Next waits for what is written next
	// rather than saying the log has ended, because it has not.
	following bool

	held []*kgo.Record
	mu   sync.Mutex

	// closed is not kept under mu. Closing has to work while a following read
	// is waiting, and that read holds mu for as long as it waits — so a close
	// that wanted the same lock could never run (T2.65).
	closed atomic.Bool
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

	if r.closed.Load() || (!r.following && r.left <= 0) {
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
	if !r.following {
		r.left--
	}
	return rowOf(rec), nil
}

// fill reads another batch, and says whether there is nothing left to read.
func (r *recordRows) fill(ctx context.Context) (bool, error) {
	if !r.following && len(r.until) == 0 {
		return true, nil
	}
	max := int(r.left)
	if r.following {
		// However many arrive: a tail takes what there is and waits for more.
		max = 0
	}
	fetches := r.client.PollRecords(ctx, max)
	if r.closed.Load() {
		// Closed while waiting in there, which ends the read rather than
		// breaking it: what a closed client says about the poll is noise.
		return true, nil
	}
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
		if r.following {
			// Nothing is past the end of a log somebody is still writing to.
			r.held = append(r.held, rec)
			return
		}
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
	if r.following {
		// Never done: the log ends when the reader stops, not before.
		return false, nil
	}
	return len(r.held) == 0 && len(r.until) == 0, nil
}

func (r *recordRows) Close() error {
	if r.closed.Swap(true) {
		return nil
	}
	// Closing the client is what ends a poll that is waiting for records, and
	// a tail waits in one by design. Taking no lock here is the point: the
	// reader holds it until something arrives, which may be never.
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
