//go:build conformance

package kafka

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Reading a log from where somebody asks (T2.64, FR-13.5).

// from reads a topic starting where the seek says.
func from(t *testing.T, src source.Source, topic string, seek *source.Seek, limit int64) []model.Row {
	t.Helper()
	ctx := context.Background()
	rs, err := src.Browse(ctx, model.NewRef(model.KindTopic, "cluster", topic),
		source.BrowseOptions{Limit: limit, Seek: seek})
	if err != nil {
		t.Fatalf("browsing %s: %v", topic, err)
	}
	defer rs.Close()

	var out []model.Row
	for {
		row, err := rs.Next(ctx)
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatalf("reading %s: %v", topic, err)
		}
		out = append(out, row)
	}
}

// offsetsOf are the offsets of what came back, in the order it came.
func offsetsOf(t *testing.T, rows []model.Row) []int64 {
	t.Helper()
	out := make([]int64, 0, len(rows))
	for _, row := range rows {
		at, ok := row[1].(int64)
		if !ok {
			t.Fatalf("a record is at %v", row[1])
		}
		out = append(out, at)
	}
	return out
}

// oneLog is a topic of a single partition, so that an offset means one thing.
func oneLog(t *testing.T, src source.Source, name string, records int) {
	t.Helper()
	seededTopic(t, src, name, 1)
	written(t, src, name, records)
}

func TestLiveReadsFromAnOffset(t *testing.T) {
	src := live(t, liveConfig())
	oneLog(t, src, "ikigai_it_seek", 10)

	// Everything from the fifth offset on, and nothing before it.
	rows := from(t, src, "ikigai_it_seek", &source.Seek{Mode: source.SeekOffset, Offset: 5}, 1000)
	if len(rows) == 0 {
		t.Fatal("a read from offset five found nothing")
	}
	for _, at := range offsetsOf(t, rows) {
		if at < 5 {
			t.Errorf("a read from offset five returned offset %d", at)
		}
	}

	// A log is aged out from the front, so asking for what has gone is the
	// beginning rather than an error.
	all := from(t, src, "ikigai_it_seek", &source.Seek{Mode: source.SeekOffset, Offset: -100}, 1000)
	if len(all) < len(rows) {
		t.Errorf("a read from before the beginning found %d records, fewer than from the middle (%d)",
			len(all), len(rows))
	}
}

func TestLiveReadsTheLastFew(t *testing.T) {
	src := live(t, liveConfig())
	oneLog(t, src, "ikigai_it_seek", 10)

	whole := offsetsOf(t, from(t, src, "ikigai_it_seek", nil, 10_000))
	if len(whole) < 3 {
		t.Fatalf("the log holds %d records", len(whole))
	}
	last := offsetsOf(t, from(t, src, "ikigai_it_seek",
		&source.Seek{Mode: source.SeekLast, Count: 3}, 10_000))
	if len(last) != 3 {
		t.Fatalf("the last three are %d records", len(last))
	}
	// The last three of the log, and the same three the whole read ended on.
	if last[0] != whole[len(whole)-3] || last[2] != whole[len(whole)-1] {
		t.Errorf("the last three are %v, and the log ends %v", last, whole[len(whole)-3:])
	}
}

func TestLiveReadsFromATime(t *testing.T) {
	src := live(t, liveConfig())
	oneLog(t, src, "ikigai_it_seek_time", 3)

	// A moment between two batches. The producer and this test share a clock,
	// so what was written after it is what should come back.
	time.Sleep(250 * time.Millisecond)
	when := time.Now()
	time.Sleep(250 * time.Millisecond)
	written(t, src, "ikigai_it_seek_time", 3)

	rows := from(t, src, "ikigai_it_seek_time", &source.Seek{Mode: source.SeekTimestamp, Time: when}, 10_000)
	if len(rows) == 0 {
		t.Fatal("a read from a moment found nothing written after it")
	}
	for i, row := range rows {
		at, ok := row[2].(time.Time)
		if !ok {
			t.Fatalf("record %d arrived at %v", i, row[2])
		}
		if at.Before(when) {
			t.Errorf("a read from %v returned a record written at %v", when, at)
		}
	}
}

func TestLiveAReadFromATimeYetToComeFindsNothing(t *testing.T) {
	src := live(t, liveConfig())
	oneLog(t, src, "ikigai_it_seek_time", 3)

	// A moment nothing can have been written after. Every log answers that it
	// holds nothing at or after it, and nothing is what should come back —
	// not the whole log, which is what happens when that answer is read as a
	// position rather than as an absence.
	rows := from(t, src, "ikigai_it_seek_time", &source.Seek{
		Mode: source.SeekTimestamp, Time: time.Now().Add(time.Hour)}, 10_000)
	if len(rows) != 0 {
		t.Errorf("a read from an hour hence found %d records", len(rows))
	}
}

func TestLiveReadsOnlyTheLogsItIsAskedFor(t *testing.T) {
	src := live(t, liveConfig())
	seededTopic(t, src, "ikigai_it_seek_parts", 4)
	written(t, src, "ikigai_it_seek_parts", 12)

	// Which partition the records went to is the producer's business — it
	// sticks to one log at a time — so this asks for one that has some.
	var want int64 = -1
	for _, row := range from(t, src, "ikigai_it_seek_parts", nil, 10_000) {
		if p, ok := row[0].(int64); ok {
			want = p
			break
		}
	}
	if want < 0 {
		t.Fatal("the topic holds no records at all")
	}

	rows := from(t, src, "ikigai_it_seek_parts", &source.Seek{Partitions: []int32{int32(want)}}, 10_000)
	if len(rows) == 0 {
		t.Fatalf("partition %d holds records and was read as empty", want)
	}
	for i, row := range rows {
		if p, ok := row[0].(int64); !ok || p != want {
			t.Errorf("record %d is from partition %v, and partition %d was asked for", i, row[0], want)
		}
	}
}

func TestLiveWillNotReadFromTheEnd(t *testing.T) {
	src := live(t, liveConfig())
	oneLog(t, src, "ikigai_it_seek", 2)

	// The end of a log is where nothing has been written yet, so a read that
	// stops there reads nothing at all. It means something only while
	// following (T2.65), and this is a read that does not follow.
	rs, err := src.Browse(context.Background(), model.NewRef(model.KindTopic, "cluster", "ikigai_it_seek"),
		source.BrowseOptions{Limit: 10, Seek: &source.Seek{Mode: source.SeekEnd}})
	if err == nil {
		rs.Close()
		t.Fatal("a log was read from its end")
	}
	if !strings.Contains(err.Error(), "follow") {
		t.Errorf("reading from the end is refused with %q", err)
	}
}
