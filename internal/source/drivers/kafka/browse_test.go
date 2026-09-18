package kafka

import (
	"testing"

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
		got, err := spansOf(begins, ends, nil, seek)
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
	got, err := spansOf(begins, ends, nil, &source.Seek{Mode: source.SeekOffset, Offset: 40})
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != (span{from: 40, to: 100}) || got[2] != (span{from: 40, to: 50}) {
		t.Errorf("a read from 40: %+v", got)
	}

	// Before the log begins is the beginning of it: a log is aged out from
	// the front, and asking for what has gone is not an error.
	got, err = spansOf(begins, ends, nil, &source.Seek{Mode: source.SeekOffset, Offset: 0})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].from != 10 || got[2].from != 5 {
		t.Errorf("a read from before the beginning: %+v", got)
	}

	// Past the end there is nothing to read, so that log is left out rather
	// than waited on.
	got, err = spansOf(begins, ends, nil, &source.Seek{Mode: source.SeekOffset, Offset: 60})
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
	got, err := spansOf(begins, ends, nil, &source.Seek{Mode: source.SeekLast, Count: 20})
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != (span{from: 80, to: 100}) || got[2] != (span{from: 30, to: 50}) {
		t.Errorf("the last twenty of each: %+v", got)
	}

	// More than the log holds is the whole log, not a negative offset.
	got, err = spansOf(begins, ends, nil, &source.Seek{Mode: source.SeekLast, Count: 1_000_000})
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
	got, err := spansOf(begins, ends, map[int32]int64{0: 70}, &source.Seek{Mode: source.SeekTimestamp})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != (span{from: 70, to: 100}) {
		t.Errorf("a read from a time: %+v", got)
	}
}

func TestAReadCanBeNarrowedToPartitions(t *testing.T) {
	begins, ends := logs()
	got, err := spansOf(begins, ends, nil, &source.Seek{Partitions: []int32{2}})
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
	got, err = spansOf(begins, ends, nil, &source.Seek{Partitions: []int32{1}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("an empty partition asked for by name: %+v", got)
	}
}

func TestAWayOfReadingALogThatDoesNotExist(t *testing.T) {
	begins, ends := logs()
	_, err := spansOf(begins, ends, nil, &source.Seek{Mode: source.SeekMode(200)})
	if err == nil {
		t.Fatal("a log was read in a way nobody has written")
	}
}
