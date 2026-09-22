package kafka

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Where each partition's read begins and ends (T2.64, FR-13.5).
//
// This is arithmetic over what the brokers said, so it is proved here rather
// than against a cluster: a log that has been aged past, a position beyond the
// end, a time nothing was written after — each is a state a healthy broker
// will not hold still in long enough to test.

// three logs: one running 10..100, one empty at 0, one running 5..50.
func logs() (begins, ends map[int32]int64) {
	return map[int32]int64{0: 10, 1: 0, 2: 5}, map[int32]int64{0: 100, 1: 0, 2: 50}
}

func TestAReadFromTheBeginningStartsWhereTheLogDoes(t *testing.T) {
	begins, ends := logs()
	for name, seek := range map[string]*source.Seek{
		"nothing asked for":       nil,
		"the beginning asked for": {Mode: source.SeekBeginning},
	} {
		got, err := spansOf(begins, ends, nil, seek, false)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		// The empty log is not in it: there is nothing there to read, and a
		// read that waited on it would wait for ever.
		if len(got) != 2 {
			t.Fatalf("%s: %d logs to read, want 2: %+v", name, len(got), got)
		}
		if got[0] != (span{from: 10, to: 100}) || got[2] != (span{from: 5, to: 50}) {
			t.Errorf("%s: %+v", name, got)
		}
	}
}

func TestAReadFromAnOffsetIsHeldInsideTheLog(t *testing.T) {
	begins, ends := logs()

	// Inside the log: exactly where it was asked for.
	got, err := spansOf(begins, ends, nil, &source.Seek{Mode: source.SeekOffset, Offset: 40}, false)
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != (span{from: 40, to: 100}) || got[2] != (span{from: 40, to: 50}) {
		t.Errorf("a read from 40: %+v", got)
	}

	// Before the log begins is the beginning of it: a log is aged out from
	// the front, and asking for what has gone is not an error.
	got, err = spansOf(begins, ends, nil, &source.Seek{Mode: source.SeekOffset, Offset: 0}, false)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].from != 10 || got[2].from != 5 {
		t.Errorf("a read from before the beginning: %+v", got)
	}

	// Past the end there is nothing to read, so that log is left out rather
	// than waited on.
	got, err = spansOf(begins, ends, nil, &source.Seek{Mode: source.SeekOffset, Offset: 60}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, in := got[2]; in {
		t.Errorf("a log ending at 50 was read from 60: %+v", got)
	}
	if got[0] != (span{from: 60, to: 100}) {
		t.Errorf("the log that does run that far: %+v", got)
	}
}

func TestTheLastFewAreCountedInEachLog(t *testing.T) {
	begins, ends := logs()

	// Each log has its own end, so a count back is a count back in each: this
	// asks for the last twenty and gets twenty from each of the two that have
	// them, which is what a topic of many partitions means by "the last N".
	got, err := spansOf(begins, ends, nil, &source.Seek{Mode: source.SeekLast, Count: 20}, false)
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != (span{from: 80, to: 100}) || got[2] != (span{from: 30, to: 50}) {
		t.Errorf("the last twenty of each: %+v", got)
	}

	// More than the log holds is the whole log, not a negative offset.
	got, err = spansOf(begins, ends, nil, &source.Seek{Mode: source.SeekLast, Count: 1_000_000}, false)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].from != 10 || got[2].from != 5 {
		t.Errorf("more than there is: %+v", got)
	}
}

func TestAReadFromATimeLeavesOutLogsWithNothingAfterIt(t *testing.T) {
	begins, ends := logs()
	// Partition 0 has something at or after that time; partition 2 has not,
	// and is left out rather than read from its beginning — which would be
	// answering a question nobody asked.
	got, err := spansOf(begins, ends, map[int32]int64{0: 70}, &source.Seek{Mode: source.SeekTimestamp}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != (span{from: 70, to: 100}) {
		t.Errorf("a read from a time: %+v", got)
	}
}

func TestAReadCanBeNarrowedToPartitions(t *testing.T) {
	begins, ends := logs()
	got, err := spansOf(begins, ends, nil, &source.Seek{Partitions: []int32{2}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("one partition asked for, %d read: %+v", len(got), got)
	}
	if got[2] != (span{from: 5, to: 50}) {
		t.Errorf("the partition asked for: %+v", got)
	}
	// One that holds nothing is still nothing to read, named or not.
	got, err = spansOf(begins, ends, nil, &source.Seek{Partitions: []int32{1}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("an empty partition asked for by name: %+v", got)
	}
}

func TestAWayOfReadingALogThatDoesNotExist(t *testing.T) {
	begins, ends := logs()
	_, err := spansOf(begins, ends, nil, &source.Seek{Mode: source.SeekMode(200)}, false)
	if err == nil {
		t.Fatal("a log was read in a way nobody has written")
	}
}

// Following a log (T2.65): a tail begins where the log is now, and has no end
// to stop before.

func TestATailBeginsWhereTheLogIsNow(t *testing.T) {
	begins, ends := logs()

	// Nowhere named, and following: a tail is about what comes next, so it
	// starts at the end of each log rather than replaying what is there.
	got, err := spansOf(begins, ends, nil, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	// Every log is kept, the empty one included: a log with nothing in it is
	// exactly where something may be written next.
	if len(got) != 3 {
		t.Fatalf("a tail of three logs follows %d: %+v", len(got), got)
	}
	for partition, want := range ends {
		if got[partition].from != want {
			t.Errorf("partition %d is followed from %d, and ends at %d", partition, got[partition].from, want)
		}
		// Nothing to stop before: the log ends when the reader does.
		if got[partition].to != -1 {
			t.Errorf("partition %d is followed until %d", partition, got[partition].to)
		}
	}

	// The end asked for by name means the same thing.
	named, err := spansOf(begins, ends, nil, &source.Seek{Mode: source.SeekEnd}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(named) != len(got) || named[0] != got[0] {
		t.Errorf("the end asked for: %+v, and the end by default: %+v", named, got)
	}
}

func TestATailCanBeginBeforeWhatIsAlreadyThere(t *testing.T) {
	begins, ends := logs()

	// Somebody following from a position wants what is there and then what
	// comes next, so the start is honoured and there is still no end.
	got, err := spansOf(begins, ends, nil, &source.Seek{Mode: source.SeekLast, Count: 20}, true)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].from != 80 || got[0].to != -1 {
		t.Errorf("a tail from the last twenty: %+v", got[0])
	}
	// A log that holds nothing has nothing behind its end, so a tail of it
	// starts where it is — and is still followed.
	if got[1].from != 0 || got[1].to != -1 {
		t.Errorf("a tail of an empty log: %+v", got[1])
	}
}

// Every consume is bounded (T2.82, FR-13.20).

func TestAReadWithNoBoundGetsThisDriversOwn(t *testing.T) {
	// A caller naming no limit is given this driver's rather than all of a
	// log: an unbounded read of a topic nobody has aged out is how looking
	// at something becomes an incident.
	for _, opt := range []source.BrowseOptions{{}, {Limit: 0}, {Limit: -1}} {
		if got := boundOf(opt); got != records {
			t.Errorf("a read asking for %d reads %d, not this driver's %d", opt.Limit, got, records)
		}
	}
	// A limit somebody named is the limit they named.
	if got := boundOf(source.BrowseOptions{Limit: 10}); got != 10 {
		t.Errorf("a read of ten reads %d", got)
	}
	// And this driver's own is a page rather than a log.
	if records <= 0 || records > 10_000 {
		t.Errorf("this driver's own bound is %d, which is no bound worth having", records)
	}
}

func TestATailIsBoundedByWhatItCannotOutrun(t *testing.T) {
	// A log being written to has no count to stop at, so a tail names none.
	// What holds it instead is how much a fetch may pull, the window of
	// whoever is reading it (NFR-P10), and being cancelled — each of which
	// has a test of its own.
	if got := boundOf(source.BrowseOptions{Follow: true}); got != 0 {
		t.Errorf("a tail stops after %d records", got)
	}

	// franz-go's own fetch defaults are far larger, and they are defaults
	// for a service meaning to keep up with a topic. This is an application
	// somebody is looking at a topic through.
	if fetchBytes <= 0 || fetchBytes > 32<<20 {
		t.Errorf("a fetch may pull %d bytes, which is not a bound worth having", fetchBytes)
	}
	if fetchPartitionBytes <= 0 || fetchPartitionBytes > fetchBytes {
		t.Errorf("one partition may pull %d of a fetch's %d", fetchPartitionBytes, fetchBytes)
	}
	if fetchWait <= 0 || fetchWait > time.Minute {
		t.Errorf("a fetch waits up to %v, which is not a bound worth having", fetchWait)
	}
	if n := len(readBounds()); n != 3 {
		t.Errorf("a read is given %d bounds, not the three it is meant to have", n)
	}
	// And they reach the reader, which is the one thing a test of the values
	// alone would never notice.
	body, err := os.ReadFile("browse.go")
	if err != nil {
		t.Fatalf("reading browse.go: %v", err)
	}
	if !strings.Contains(string(body), "readBounds()...") {
		t.Error("browse.go no longer gives the reader the bounds it works out")
	}
}
