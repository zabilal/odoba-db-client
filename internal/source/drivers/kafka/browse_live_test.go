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

// Reading a topic's records, against a real broker (T2.63, FR-13.4).

// read takes a window of records from a topic, as the grid does.
func read(t *testing.T, src source.Source, topic string, limit int64) []model.Row {
	t.Helper()
	ctx := context.Background()
	rs, err := src.Browse(ctx, model.NewRef(model.KindTopic, "cluster", topic),
		source.BrowseOptions{Limit: limit})
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

func TestLiveReadsATopicsRecords(t *testing.T) {
	src := live(t, liveConfig())
	seededTopic(t, src, "ikigai_it_reading", 2)
	written(t, src, "ikigai_it_reading", 6)

	rows := read(t, src, "ikigai_it_reading", 100)
	if len(rows) < 6 {
		t.Fatalf("a topic written to six times read back %d records", len(rows))
	}
	cols := len(recordColumns)
	for i, row := range rows {
		if len(row) != cols {
			t.Fatalf("record %d has %d values and there are %d columns", i, len(row), cols)
		}
		// Where it was and when: a partition of the topic, an offset in that
		// partition's log, and a time the broker or the writer set.
		if _, ok := row[0].(int64); !ok {
			t.Errorf("record %d is in partition %v", i, row[0])
		}
		if _, ok := row[1].(int64); !ok {
			t.Errorf("record %d is at offset %v", i, row[1])
		}
		if when, ok := row[2].(time.Time); !ok || when.IsZero() {
			t.Errorf("record %d arrived at %v", i, row[2])
		}
		// What it carried, as the bytes it was written as: what they mean is
		// a decoder's business (T2.68).
		if got, ok := row[4].([]byte); !ok || string(got) != "a record" {
			t.Errorf("record %d carries %v", i, row[4])
		}
	}
}

func TestLiveAReadStopsWhereItWasTold(t *testing.T) {
	src := live(t, liveConfig())
	seededTopic(t, src, "ikigai_it_reading", 2)
	written(t, src, "ikigai_it_reading", 6)

	// The grid always names a bound, and the read honours it rather than
	// draining the log (NFR-P11).
	if rows := read(t, src, "ikigai_it_reading", 3); len(rows) != 3 {
		t.Errorf("a read bounded at three took %d records", len(rows))
	}
	if rows := read(t, src, "ikigai_it_reading", 1); len(rows) != 1 {
		t.Errorf("a read bounded at one took %d records", len(rows))
	}
}

func TestLiveAReadOfALogThatStandsStillEnds(t *testing.T) {
	src := live(t, liveConfig())
	seededTopic(t, src, "ikigai_it_reading", 2)
	written(t, src, "ikigai_it_reading", 2)

	// Asking for more than the log holds ends rather than waiting: where each
	// partition ended is asked before the read begins, so catching up is
	// known rather than waited for.
	done := make(chan int, 1)
	go func() { done <- len(read(t, src, "ikigai_it_reading", 10_000)) }()
	select {
	case n := <-done:
		if n < 2 {
			t.Errorf("a log holding at least two records read back %d", n)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("a read of a log that nobody is writing to never ended")
	}
}

func TestLiveAnEmptyPartitionDoesNotHoldUpARead(t *testing.T) {
	src := live(t, liveConfig())
	// More logs than records, so some of them are certainly empty: a read
	// that waited for an empty partition to produce something would wait for
	// ever, and the default fixture never makes one.
	seededTopic(t, src, "ikigai_it_sparse", 8)
	written(t, src, "ikigai_it_sparse", 1)

	done := make(chan int, 1)
	go func() { done <- len(read(t, src, "ikigai_it_sparse", 10_000)) }()
	select {
	case n := <-done:
		if n < 1 {
			t.Errorf("a topic written to once read back %d records", n)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("a read of a topic with empty partitions never ended")
	}
}

func TestLiveAReadStopsWhenItIsGivenUpOn(t *testing.T) {
	src := live(t, liveConfig())
	seededTopic(t, src, "ikigai_it_reading", 2)
	written(t, src, "ikigai_it_reading", 2)

	ctx, cancel := context.WithCancel(context.Background())
	rs, err := src.Browse(ctx, model.NewRef(model.KindTopic, "cluster", "ikigai_it_reading"),
		source.BrowseOptions{Limit: 10_000})
	if err != nil {
		t.Fatalf("browse: %v", err)
	}
	defer rs.Close()

	// One record proves it is reading; then it is given up on, and what comes
	// back says so rather than being nothing in particular (NFR-P9).
	if _, err := rs.Next(ctx); err != nil {
		t.Fatalf("the first record: %v", err)
	}
	cancel()
	for {
		_, err := rs.Next(ctx)
		if err == nil {
			continue // a record already in hand is still a record
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
			break
		}
		t.Fatalf("a read given up on says %v", err)
	}

	// Closing twice is a no-op: the grid closes on tab close and again on
	// teardown.
	if err := rs.Close(); err != nil {
		t.Errorf("first close: %v", err)
	}
	if err := rs.Close(); err != nil {
		t.Errorf("second close: %v", err)
	}
}

func TestLiveWhatCannotBeAskedOfALog(t *testing.T) {
	src := live(t, liveConfig())
	seededTopic(t, src, "ikigai_it_reading", 2)
	ctx := context.Background()
	topic := model.NewRef(model.KindTopic, "cluster", "ikigai_it_reading")

	for name, opt := range map[string]source.BrowseOptions{
		"a condition": {Where: "value = 1", Limit: 10},
		"an order":    {Sorts: []source.Sort{{Column: "offset"}}, Limit: 10},
		"a filter":    {Filters: []source.Filter{{Column: "key", Op: source.OpEqual, Values: []any{"k"}}}, Limit: 10},
	} {
		rs, err := src.Browse(ctx, topic, opt)
		if err == nil {
			rs.Close()
			t.Errorf("%s was accepted by a log", name)
			continue
		}
		// The refusal says what a log is, not merely that it said no.
		if !strings.Contains(err.Error(), "kafka:") {
			t.Errorf("%s is refused with %q", name, err)
		}
	}
}

// Every consume is bounded (T2.82, FR-13.20).

func TestLiveAReadWithNoLimitStopsAtTheDriversOwn(t *testing.T) {
	src := live(t, liveConfig())
	a := administers(t, src)
	ctx := context.Background()
	const topic = "ikigai_it_bounded"

	_ = a.DeleteTopic(ctx, topic, false)
	if err := a.CreateTopic(ctx, source.TopicSpec{
		Name: topic, Partitions: 1, ReplicationFactor: 1}); err != nil {
		t.Fatalf("creating %s: %v", topic, err)
	}
	t.Cleanup(func() { _ = a.DeleteTopic(context.Background(), topic, false) })
	settled(t, src, topic, 1)
	written(t, src, topic, records+20)

	// Naming no limit gets this driver's own, and not the whole log: that
	// is what keeps looking at a topic from becoming an incident.
	if rows := from(t, src, topic, nil, 0); len(rows) != records {
		t.Errorf("a read with no limit returned %d records, not this driver's %d", len(rows), records)
	}
	// And a limit named under it is honoured as it stands.
	if rows := from(t, src, topic, nil, 10); len(rows) != 10 {
		t.Errorf("a read of ten returned %d records", len(rows))
	}
}
