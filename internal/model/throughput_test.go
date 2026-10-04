package model

import (
	"testing"
	"time"
)

// What two readings say (FR-13.17). A cluster keeps no rate, so this arithmetic
// is the whole of where one comes from — and what it refuses to say matters as
// much as what it says.
func TestWhatTwoReadingsSay(t *testing.T) {
	at := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	before := TopicTotals{At: at, Records: 1000, Bytes: 500_000, Sized: true}

	// Two seconds and two hundred records: a hundred a second.
	got, ok := RateBetween(before, TopicTotals{At: at.Add(2 * time.Second),
		Records: 1200, Bytes: 600_000, Sized: true})
	if !ok {
		t.Fatal("two readings two seconds apart made no rate")
	}
	if got.Records != 100 {
		t.Errorf("200 records over 2s reads as %v a second", got.Records)
	}
	if got.Bytes != 50_000 {
		t.Errorf("100kB over 2s reads as %v a second", got.Bytes)
	}
	if !got.Known() {
		t.Error("a rate with bytes in it says it has none")
	}
	if !got.At.Equal(at.Add(2 * time.Second)) {
		t.Errorf("the rate is stamped %v", got.At)
	}

	// A topic nobody wrote to carries nothing, which is a rate and not a gap.
	got, ok = RateBetween(before, TopicTotals{At: at.Add(time.Second),
		Records: 1000, Bytes: 500_000, Sized: true})
	if !ok || got.Records != 0 || got.Bytes != 0 {
		t.Errorf("a quiet second reads as %+v, %v", got, ok)
	}

	// Retention deleting a segment is not a negative rate, and it is not a
	// nought either: what was written in that interval is not knowable from a
	// log that got smaller.
	got, ok = RateBetween(before, TopicTotals{At: at.Add(time.Second),
		Records: 1100, Bytes: 400_000, Sized: true})
	if !ok {
		t.Fatal("a shrinking log made no rate at all")
	}
	if got.Records != 100 {
		t.Errorf("the records read as %v a second", got.Records)
	}
	if got.Known() || got.Bytes != -1 {
		t.Errorf("a log that shrank says it carried %v bytes a second", got.Bytes)
	}

	// A topic that was deleted and made again starts its watermarks at nought:
	// that is not a negative rate, and not a claim about what was written.
	got, ok = RateBetween(before, TopicTotals{At: at.Add(time.Second),
		Records: 5, Bytes: 100, Sized: true})
	if !ok {
		t.Fatal("a topic that started again made no rate")
	}
	if got.Records != 0 {
		t.Errorf("a topic that started again carried %v records a second", got.Records)
	}

	// A cluster that will not say how big its logs are still says how many
	// records there are.
	got, ok = RateBetween(TopicTotals{At: at, Records: 1000},
		TopicTotals{At: at.Add(time.Second), Records: 1050})
	if !ok || got.Records != 50 {
		t.Errorf("records without bytes read as %+v", got)
	}
	if got.Known() {
		t.Error("a cluster that says nothing about bytes was given a byte rate")
	}
	// Sized on one reading and not the other is not two readings of the same
	// thing: the bytes are unknown for that interval, whichever half is missing.
	got, _ = RateBetween(TopicTotals{At: at, Records: 1000, Bytes: 1, Sized: true},
		TopicTotals{At: at.Add(time.Second), Records: 1000, Bytes: 2})
	if got.Known() {
		t.Error("a pair whose second reading has no size made a byte rate")
	}
	got, _ = RateBetween(TopicTotals{At: at, Records: 1000, Bytes: 1},
		TopicTotals{At: at.Add(time.Second), Records: 1000, Bytes: 2, Sized: true})
	if got.Known() {
		t.Error("a pair whose first reading has no size made a byte rate")
	}

	// And no time between them is no rate: a reading compared with itself would
	// divide by nothing.
	if _, ok := RateBetween(before, before); ok {
		t.Error("one reading made a rate with itself")
	}
	if _, ok := RateBetween(before, TopicTotals{At: at.Add(-time.Second), Records: 2000}); ok {
		t.Error("a clock that went backwards made a rate")
	}
}
