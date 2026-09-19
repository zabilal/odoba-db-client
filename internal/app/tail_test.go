package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// A tail keeps the last so many records and no more (T2.65).

// feed is a stream somebody else decides the pace of: it hands over a record
// when told, and otherwise waits, which is what a log being followed does.
type feed struct {
	rows   chan model.Row
	mu     sync.Mutex
	closes int
	fail   error
}

func (f *feed) Columns() []model.ColumnDef {
	return []model.ColumnDef{{Name: "n", Type: model.DataType{Class: model.TypeInteger}}}
}

func (f *feed) Next(ctx context.Context) (model.Row, error) {
	f.mu.Lock()
	err := f.fail
	f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	select {
	case row := <-f.rows:
		return row, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (f *feed) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closes++
	return nil
}

func (f *feed) closed() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closes
}

// followed is a tail over a feed the test writes to.
func followed(t *testing.T, keeps int) (*Tail, *feed) {
	t.Helper()
	f := &feed{rows: make(chan model.Row)}
	tail, err := NewTail(context.Background(), &feedSource{rs: f},
		model.NewRef(model.KindTopic, "c", "t"), source.BrowseOptions{}, keeps)
	if err != nil {
		t.Fatalf("following: %v", err)
	}
	t.Cleanup(func() { tail.Close() })
	return tail, f
}

// feedSource hands out one stream: enough of a source to follow.
type feedSource struct {
	source.Source
	rs      model.RowStream
	follow  bool
	browsed int
}

func (s *feedSource) Browse(ctx context.Context, ref model.ObjectRef, opt source.BrowseOptions) (model.RowStream, error) {
	s.browsed++
	s.follow = opt.Follow
	return s.rs, nil
}

// waitFor gives a tail a moment to take what it was given.
func waitFor(t *testing.T, tail *Tail, rows int) []model.Row {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		held := tail.Rows()
		if len(held) >= rows {
			return held
		}
		if time.Now().After(deadline) {
			t.Fatalf("a tail holds %d records, waiting for %d", len(held), rows)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestATailIsAlwaysAFollowingRead(t *testing.T) {
	f := &feed{rows: make(chan model.Row)}
	src := &feedSource{rs: f}
	tail, err := NewTail(context.Background(), src, model.NewRef(model.KindTopic, "c", "t"),
		source.BrowseOptions{Follow: false}, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer tail.Close()
	// A tail that stopped at the end of the log would not be one, whatever
	// the caller asked for.
	if !src.follow {
		t.Error("a tail was opened as a read that ends")
	}
}

func TestATailKeepsTheLastFewAndCountsWhatItDropped(t *testing.T) {
	tail, f := followed(t, 3)
	for i := 0; i < 5; i++ {
		f.rows <- model.Row{int64(i)}
	}
	held := waitFor(t, tail, 3)

	// The window holds the newest three, oldest first: a tail is about what
	// is happening now.
	if len(held) != 3 || held[0][0] != int64(2) || held[2][0] != int64(4) {
		t.Fatalf("the window holds %v", held)
	}
	// And what fell out is counted rather than lost quietly.
	deadline := time.Now().Add(5 * time.Second)
	for tail.Dropped() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := tail.Dropped(); got != 2 {
		t.Errorf("%d records were dropped, and five were written into a window of three", got)
	}
}

func TestAPausedTailStopsReadingAndDropsNothing(t *testing.T) {
	tail, f := followed(t, 8)
	f.rows <- model.Row{int64(1)}
	waitFor(t, tail, 1)

	tail.Pause()
	if !tail.Paused() {
		t.Error("a paused tail does not say so")
	}

	// A read already asked for finishes: pausing takes effect at the next
	// record, not in the middle of waiting for one. That record is kept, not
	// dropped, which is the promise — so it is allowed to arrive.
	inFlight := make(chan struct{})
	go func() {
		f.rows <- model.Row{int64(2)}
		close(inFlight)
	}()
	select {
	case <-inFlight:
	case <-time.After(2 * time.Second):
		// It was not waiting on a read after all, which is equally fine.
	}

	// The next one is not taken at all while it is paused: the source stops
	// being asked, and what it has waits where it is.
	sent := make(chan struct{})
	go func() {
		f.rows <- model.Row{int64(3)}
		close(sent)
	}()
	select {
	case <-sent:
		t.Fatal("a paused tail went on reading")
	case <-time.After(300 * time.Millisecond):
	}

	tail.Resume()
	if tail.Paused() {
		t.Error("a resumed tail still says it is paused")
	}
	select {
	case <-sent:
	case <-time.After(5 * time.Second):
		t.Fatal("a resumed tail never read what was waiting")
	}
	held := waitFor(t, tail, 2)
	// What was waiting is read once it may be, and in the order it was sent.
	if last := held[len(held)-1]; last[0] != int64(3) {
		t.Errorf("the window ends with %v", last)
	}
	// And nothing was lost by pausing: that is the whole difference between
	// holding off and dropping.
	if got := tail.Dropped(); got != 0 {
		t.Errorf("pausing dropped %d records", got)
	}
}

func TestATailSaysWhatWentWrong(t *testing.T) {
	tail, f := followed(t, 4)
	f.mu.Lock()
	f.fail = errors.New("the broker went away")
	f.mu.Unlock()
	// Wake the read so it takes the failure.
	select {
	case f.rows <- model.Row{int64(0)}:
	default:
	}

	deadline := time.Now().Add(5 * time.Second)
	for tail.Err() == nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if tail.Err() == nil {
		t.Fatal("a tail whose stream failed says nothing went wrong")
	}
}

func TestAClosedTailLetsItsStreamGoOnce(t *testing.T) {
	tail, f := followed(t, 4)
	if err := tail.Close(); err != nil {
		t.Errorf("first close: %v", err)
	}
	if err := tail.Close(); err != nil {
		t.Errorf("second close: %v", err)
	}
	// Ending is not a fault: a tail that was closed has nothing to report.
	if err := tail.Err(); err != nil {
		t.Errorf("a closed tail reports %v", err)
	}
	if got := f.closed(); got != 1 {
		t.Errorf("the stream was closed %d times", got)
	}
}

func TestATailGivenUpOnHasNothingToReport(t *testing.T) {
	f := &feed{rows: make(chan model.Row)}
	ctx, cancel := context.WithCancel(context.Background())
	tail, err := NewTail(ctx, &feedSource{rs: f}, model.NewRef(model.KindTopic, "c", "t"),
		source.BrowseOptions{}, 4)
	if err != nil {
		t.Fatalf("following: %v", err)
	}
	defer tail.Close()

	// Giving up on a tail is one of the two ways it ends — a window shut, a
	// session over — and ending is not a fault anybody needs telling about.
	// Waiting on the follower itself rather than closing: closing would say it
	// was closed, and then this would be holding the wrong thing up.
	cancel()
	select {
	case <-tail.done:
	case <-time.After(5 * time.Second):
		t.Fatal("a tail given up on kept following")
	}
	if err := tail.Err(); err != nil {
		t.Errorf("a tail given up on reports %v", err)
	}
}

func TestAPausedTailCanStillBeClosed(t *testing.T) {
	tail, f := followed(t, 4)

	// Nothing may assume where the follower is: it is started by NewTail and
	// may reach its first read or its first look at being paused in either
	// order, so every send here is one it is free not to take.
	send := func(row model.Row) chan struct{} {
		taken := make(chan struct{})
		go func() {
			select {
			case f.rows <- row:
				close(taken)
			case <-time.After(10 * time.Second):
			}
		}()
		return taken
	}

	tail.Pause()
	// A read already in flight may finish, which is allowed: pausing takes
	// effect at the next record rather than inside a read.
	select {
	case <-send(model.Row{int64(1)}):
	case <-time.After(time.Second):
	}

	// This one says where the follower is. It is not taken, so the follower is
	// not in a read, so it is asleep waiting to be told to carry on — and now
	// nothing but closing will wake it.
	select {
	case <-send(model.Row{int64(2)}):
		t.Fatal("a paused tail went on reading")
	case <-time.After(300 * time.Millisecond):
	}

	done := make(chan error, 1)
	go func() { done <- tail.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("closing a paused tail: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a paused tail was closed and never let go")
	}
	if got := f.closed(); got != 1 {
		t.Errorf("the stream was closed %d times", got)
	}
}
