package app

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/testutil/race"
)

// NFR-P10: a live tail of a topic taking 10 000 messages a second stays
// responsive, with memory bounded by the ring buffer.
//
// What this gate can see is the tail, not the window. Nothing in the window
// reaches a tail yet — T2.96 records that — so holding a figure for what a
// person sees would be holding a figure for something that does not exist.
// What it holds instead is the half underneath: that taking records costs a
// small fraction of the time they arrive over, which is what leaves the rest
// of the budget for the window (ADR-0021), and that the heap a tail holds is
// the ring's and not the topic's.

// p10Rate is the requirement's rate, and p10Seconds how long a tail of it is
// measured over. 20 000 records is enough to fill a 1 000-record ring twenty
// times, which is the point: memory must not follow the count.
const (
	p10Rate    = 10_000
	p10Seconds = 2
	p10Keeps   = 1_000
)

// pump hands records over as fast as they are taken, which is a topic that
// is always ahead of its reader — the case the budget is about.
type pump struct{ n, made int }

func (p *pump) Columns() []model.ColumnDef {
	return []model.ColumnDef{
		{Name: "offset", Type: model.DataType{Class: model.TypeInteger}},
		{Name: "value", Type: model.DataType{Class: model.TypeString}},
	}
}

func (p *pump) Next(ctx context.Context) (model.Row, error) {
	if p.made == p.n {
		<-ctx.Done() // the log goes quiet; the tail waits, as it would
		return nil, ctx.Err()
	}
	p.made++
	return model.Row{int64(p.made), "a record of about the size a record is"}, nil
}

func (p *pump) Close() error { return nil }

func TestGateP10LiveTail(t *testing.T) {
	if testing.Short() {
		t.Skip("gate skipped in -short")
	}
	race.SkipTimingGate(t)

	const total = p10Rate * p10Seconds
	heap := func() uint64 {
		runtime.GC()
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		return ms.HeapInuse
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	before := heap()

	feed := &pump{n: total}
	tail, err := NewTail(ctx, &feedSource{rs: feed}, model.NewRef(model.KindTopic, "c", "t"),
		source.BrowseOptions{}, p10Keeps)
	if err != nil {
		t.Fatalf("following: %v", err)
	}
	defer tail.Close()

	start := time.Now()
	deadline := time.Now().Add(30 * time.Second)
	for {
		held, dropped := tail.Rows(), tail.Dropped()
		if int64(len(held))+dropped >= total {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("a tail took %d of %d records", int64(len(held))+dropped, total)
		}
		time.Sleep(time.Millisecond)
	}
	took := time.Since(start)
	after := heap()

	// Responsive: taking a second's records must cost well under a second,
	// or a tail could never keep up with a topic that does not slow down.
	// A quarter is held here, which is the tail keeping up with four times
	// the rate the requirement names.
	budget := p10Seconds * time.Second / 4
	t.Logf("P10: %d records at %d/s taken in %v (budget %v)", total, p10Rate, took.Round(time.Millisecond), budget)
	if took > budget {
		t.Errorf("P10: taking %d records cost %v, over %v", total, took.Round(time.Millisecond), budget)
	}

	// Bounded: the ring holds what it was given room for and no more, and
	// everything else is counted rather than kept.
	if got := len(tail.Rows()); got != p10Keeps {
		t.Errorf("P10: a tail of %d holds %d records", p10Keeps, got)
	}
	if got := tail.Dropped(); got != total-p10Keeps {
		t.Errorf("P10: %d records fell out of the window, want %d", got, total-p10Keeps)
	}

	// And the heap follows the ring, not the topic: twenty times the ring
	// went through it.
	var added uint64
	if after > before {
		added = after - before
	}
	const heapBudget = 16 << 20
	t.Logf("P10: a tail of %d records adds %d MB of heap after %d went through it",
		p10Keeps, added>>20, total)
	if added > heapBudget {
		t.Errorf("P10: a tail adds %d MB of heap, over %d MB", added>>20, heapBudget>>20)
	}

	// The claim is that memory is bounded by the ring, and a budget alone
	// cannot say that: a tail that kept every record would also pass a
	// budget, on a small enough topic. So the same tail is run again over a
	// hundred times the records — two million of them, which a tail that
	// kept them would need something like a hundred megabytes for — and
	// what it holds must not have followed them.
	const times = 100
	hundredfold := tookAndHeld(t, total*times)
	t.Logf("P10: %d times the records add %d MB, against %d MB", times, hundredfold>>20, added>>20)
	if hundredfold > added+heapBudget {
		t.Errorf("P10: %d times the records add %d MB of heap against %d MB, so memory follows the topic",
			times, hundredfold>>20, added>>20)
	}
}

// tookAndHeld runs a tail over n records and answers the heap it added,
// which is the figure that must not grow with n.
func tookAndHeld(t *testing.T, n int) uint64 {
	t.Helper()
	heap := func() uint64 {
		runtime.GC()
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		return ms.HeapInuse
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	before := heap()
	tail, err := NewTail(ctx, &feedSource{rs: &pump{n: n}}, model.NewRef(model.KindTopic, "c", "t"),
		source.BrowseOptions{}, p10Keeps)
	if err != nil {
		t.Fatalf("following: %v", err)
	}
	defer tail.Close()

	deadline := time.Now().Add(60 * time.Second)
	for int64(len(tail.Rows()))+tail.Dropped() < int64(n) {
		if time.Now().After(deadline) {
			t.Fatalf("a tail took %d of %d records", int64(len(tail.Rows()))+tail.Dropped(), n)
		}
		time.Sleep(time.Millisecond)
	}
	after := heap()
	if after <= before {
		return 0
	}
	return after - before
}

// NFR-P9: cancelling a read or a consume takes effect in under 200ms.
//
// The budget is about what the person waiting experiences, and two layers
// answer for it. The window's half is that stopping does not wait for the
// source to notice — TestGateP9StoppingAQueryIsPrompt, in the shell. This
// half is the read itself: a source that watches its context, which every
// driver here does and its live tests prove, is let go of within the budget
// rather than after some poll interval of this layer's own.
const p9Budget = 200 * time.Millisecond

// listening is a source whose reads never end and which watches its
// context, as every driver here does.
type listening struct {
	source.Source
	reading chan struct{} // closed once a read is under way
	once    bool
}

func (d *listening) Browse(context.Context, model.ObjectRef, source.BrowseOptions) (model.RowStream, error) {
	return &listeningRows{d: d}, nil
}

type listeningRows struct{ d *listening }

func (*listeningRows) Columns() []model.ColumnDef {
	return []model.ColumnDef{{Name: "id", Type: model.DataType{Class: model.TypeInteger}}}
}

func (r *listeningRows) Next(ctx context.Context) (model.Row, error) {
	if !r.d.once {
		r.d.once = true
		close(r.d.reading)
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (*listeningRows) Close() error { return nil }

func TestGateP9CancellingAReadIsPrompt(t *testing.T) {
	if testing.Short() {
		t.Skip("gate skipped in -short")
	}
	race.SkipTimingGate(t)

	src := &listening{reading: make(chan struct{})}
	ref := model.NewRef(model.KindTable, "db", "orders")
	b, err := NewBrowseSource(context.Background(), src, ref, source.BrowseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		_, _ = b.Fetch(ctx, 0, 100)
		done <- time.Since(start)
	}()
	<-src.reading // the read is in the source, waiting

	cancel()
	select {
	case took := <-done:
		if took > p9Budget {
			t.Errorf("the read answered %v after it was cancelled; NFR-P9 asks for %v", took, p9Budget)
		}
		t.Logf("cancelled in %v", took)
	case <-time.After(5 * time.Second):
		t.Fatal("the read never answered after it was cancelled")
	}
}
