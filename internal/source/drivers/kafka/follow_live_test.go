//go:build conformance

package kafka

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Following a log, against a real broker (T2.65, FR-13.6).
//
// A tail is the one read that does not end. What these hold is that it waits
// rather than saying the log is over, that what is written next arrives, and
// that giving up on it stops it — because nothing else will.

func TestLiveATailWaitsAndTakesWhatComesNext(t *testing.T) {
	src := live(t, liveConfig())
	oneLog(t, src, "ikigai_it_tail", 2)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rs, err := src.Browse(ctx, model.NewRef(model.KindTopic, "cluster", "ikigai_it_tail"),
		source.BrowseOptions{Follow: true})
	if err != nil {
		t.Fatalf("following: %v", err)
	}
	defer rs.Close()

	// Nothing has been written since it began, so it is waiting rather than
	// ending: a read that returned io.EOF here would tell the grid the log
	// was over when it is not.
	came := make(chan model.Row, 4)
	failed := make(chan error, 1)
	go func() {
		row, err := rs.Next(ctx)
		if err != nil {
			failed <- err
			return
		}
		came <- row
	}()
	select {
	case row := <-came:
		t.Fatalf("a tail returned %v before anything was written", row)
	case err := <-failed:
		t.Fatalf("a tail ended with %v before anything was written", err)
	case <-time.After(2 * time.Second):
		// Still waiting, which is the whole point.
	}

	// Now something is written, and the tail takes it.
	written(t, src, "ikigai_it_tail", 1)
	select {
	case row := <-came:
		if got, ok := row[4].([]byte); !ok || string(got) != "a record" {
			t.Errorf("the record that arrived carries %v", row[4])
		}
	case err := <-failed:
		t.Fatalf("a tail failed while waiting: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("a record was written and the tail never saw it")
	}
}

func TestLiveATailEndsWhenItIsGivenUpOn(t *testing.T) {
	src := live(t, liveConfig())
	oneLog(t, src, "ikigai_it_tail", 1)

	ctx, cancel := context.WithCancel(context.Background())
	rs, err := src.Browse(ctx, model.NewRef(model.KindTopic, "cluster", "ikigai_it_tail"),
		source.BrowseOptions{Follow: true})
	if err != nil {
		t.Fatalf("following: %v", err)
	}
	defer rs.Close()

	stopped := make(chan error, 1)
	go func() {
		_, err := rs.Next(ctx)
		stopped <- err
	}()
	// A tail is stopped by the reader and by nothing else, so this is the
	// only way it ever finishes.
	cancel()
	select {
	case err := <-stopped:
		if err == nil || (!errors.Is(err, context.Canceled) && !errors.Is(err, io.EOF)) {
			t.Errorf("a tail given up on says %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("a tail given up on kept waiting")
	}

	if err := rs.Close(); err != nil {
		t.Errorf("first close: %v", err)
	}
	if err := rs.Close(); err != nil {
		t.Errorf("second close: %v", err)
	}
}

func TestLiveATailFromNowSeesOnlyWhatComesNext(t *testing.T) {
	src := live(t, liveConfig())
	oneLog(t, src, "ikigai_it_tail_now", 3)

	// The end asked for by name, which outside a tail reads nothing at all
	// and inside one means "from now on".
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rs, err := src.Browse(ctx, model.NewRef(model.KindTopic, "cluster", "ikigai_it_tail_now"),
		source.BrowseOptions{Follow: true, Seek: &source.Seek{Mode: source.SeekEnd}})
	if err != nil {
		t.Fatalf("following from the end: %v", err)
	}
	defer rs.Close()

	came := make(chan model.Row, 1)
	failed := make(chan error, 1)
	go func() {
		row, err := rs.Next(ctx)
		if err != nil {
			failed <- err
			return
		}
		came <- row
	}()
	// What was already there is not replayed: this is about what happens now.
	select {
	case row := <-came:
		t.Fatalf("a tail from the end replayed %v", row)
	case err := <-failed:
		t.Fatalf("a tail from the end ended with %v", err)
	case <-time.After(2 * time.Second):
	}

	written(t, src, "ikigai_it_tail_now", 1)
	select {
	case <-came:
	case err := <-failed:
		t.Fatalf("a tail from the end failed: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("a record was written and the tail from the end never saw it")
	}
}

func TestLiveATailIsClosedWhileWaiting(t *testing.T) {
	src := live(t, liveConfig())
	oneLog(t, src, "ikigai_it_tail_shut", 1)

	// Nothing is cancelled here. Closing on its own has to end a read that is
	// waiting, because closing is what a reader who is done with a tail does
	// — and a tail spends nearly all of its life waiting.
	rs, err := src.Browse(context.Background(), model.NewRef(model.KindTopic, "cluster", "ikigai_it_tail_shut"),
		source.BrowseOptions{Follow: true})
	if err != nil {
		t.Fatalf("following: %v", err)
	}

	waiting := make(chan error, 1)
	go func() {
		_, err := rs.Next(context.Background())
		waiting <- err
	}()
	time.Sleep(500 * time.Millisecond) // long enough to be inside the read

	shut := make(chan error, 1)
	go func() { shut <- rs.Close() }()
	select {
	case err := <-shut:
		if err != nil {
			t.Errorf("closing a tail that was waiting: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("closing a tail that was waiting never finished")
	}
	select {
	case err := <-waiting:
		if err != nil && !errors.Is(err, io.EOF) {
			t.Errorf("a tail closed while waiting says %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("a tail closed while waiting never stopped")
	}
	// And it stays closed: there is nothing more to read.
	if _, err := rs.Next(context.Background()); !errors.Is(err, io.EOF) {
		t.Errorf("reading a closed tail says %v", err)
	}
}

func TestLiveATailCanStartFromWhatIsAlreadyThere(t *testing.T) {
	src := live(t, liveConfig())
	oneLog(t, src, "ikigai_it_tail_last", 4)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rs, err := src.Browse(ctx, model.NewRef(model.KindTopic, "cluster", "ikigai_it_tail_last"),
		source.BrowseOptions{Follow: true, Seek: &source.Seek{Mode: source.SeekLast, Count: 2}})
	if err != nil {
		t.Fatalf("following from the last two: %v", err)
	}
	defer rs.Close()

	// Somebody following from a position wants what is there and then what
	// comes next, so these two are already waiting to be read.
	for i := 0; i < 2; i++ {
		read := make(chan error, 1)
		go func() {
			_, err := rs.Next(ctx)
			read <- err
		}()
		select {
		case err := <-read:
			if err != nil {
				t.Fatalf("record %d of what was already there: %v", i, err)
			}
		case <-time.After(30 * time.Second):
			t.Fatalf("a tail from the last two never produced record %d", i)
		}
	}
}
