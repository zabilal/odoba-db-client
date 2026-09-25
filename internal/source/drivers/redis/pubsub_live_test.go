//go:build conformance

package redis

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Listening to a channel against a real server (FR-12.5).
//
// A channel exists only while somebody is listening to it, which makes these
// tests the shape they are: the listener comes first, and the server can only
// name a channel once it has one.

// A channel being followed answers when somebody publishes, and goes on
// answering: what is said reaches whoever is listening and is kept by nobody.
func TestLiveListeningToAChannel(t *testing.T) {
	src := live(t, liveConfig("0"))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c := conn(t, src, 0)

	ref := model.NewRef(model.KindChannel, "ikigai.live")
	rs, err := src.Browse(ctx, ref, source.BrowseOptions{Follow: true})
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	// A second channel, listened to and never spoken on: two are what it takes
	// to say the tree lists them in an order rather than in whichever order the
	// server happened to answer.
	other, err := src.Browse(ctx, model.NewRef(model.KindChannel, "ikigai.alive"),
		source.BrowseOptions{Follow: true})
	if err != nil {
		t.Fatalf("listening to the second: %v", err)
	}
	defer other.Close()

	read, stopReading := context.WithCancel(ctx)
	rows := make(chan model.Row, 4)
	fail := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			row, err := rs.Next(read)
			if err != nil {
				fail <- err
				return
			}
			rows <- row
		}
	}()
	// The reader stops before the subscription closes under it, as a Tail does.
	defer func() { stopReading(); <-done }()

	// Published after the browse returned, which is the promise the browse
	// makes: it waits for the server to confirm the subscription, so a message
	// sent the moment after cannot be one this stream missed.
	if err := c.Publish(ctx, "ikigai.live", "hello").Err(); err != nil {
		t.Fatal(err)
	}
	var row model.Row
	select {
	case row = <-rows:
	case err := <-fail:
		t.Fatalf("listening: %v", err)
	case <-time.After(15 * time.Second):
		t.Fatal("nothing was heard")
	}
	if len(row) != 2 {
		t.Fatalf("a message is %d columns: %v", len(row), row)
	}
	if row[1] != "hello" {
		t.Errorf("it heard %#v", row[1])
	}
	// The time is when it arrived here. Redis sends none of its own and keeps
	// no record to stamp, so this is the only time there is.
	at, ok := row[0].(time.Time)
	if !ok || at.IsZero() {
		t.Fatalf("it arrived at %#v", row[0])
	}
	if d := time.Since(at); d < 0 || d > time.Minute {
		t.Errorf("it arrived %v ago", d)
	}

	// And while this is listening, the server can name the channel — which is
	// the only way it can: a channel nobody has subscribed to is not a channel
	// as far as Redis is concerned.
	roots, err := src.Root(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var class model.ObjectRef
	for _, n := range roots {
		if k, ok := model.ClassOf(n.Ref); ok && k == model.KindChannel {
			class = n.Ref
			if !n.HasChildren {
				t.Error("the Channels folder cannot be opened")
			}
		}
	}
	if class.IsZero() {
		t.Fatalf("the channels are nowhere in %d root nodes", len(roots))
	}
	kids, err := src.Children(ctx, class)
	if err != nil {
		t.Fatal(err)
	}
	var mine []string
	for _, n := range kids {
		if n.Ref.Kind != model.KindChannel {
			t.Errorf("the Channels folder holds a %s", n.Ref.Kind)
		}
		if strings.HasPrefix(n.Label, "ikigai.") {
			mine = append(mine, n.Label)
		}
	}
	// Both, in name order: a tree that moved its rows about between two
	// listings of the same server would be a tree nobody could point at.
	if len(mine) != 2 || mine[0] != "ikigai.alive" || mine[1] != "ikigai.live" {
		t.Errorf("the channels being listened to are %v", mine)
	}
	// And they are not under a database, whatever a database is asked: pub/sub
	// is not scoped by SELECT, and a channel under db0 would be a channel that
	// somebody on db7 could publish to unseen.
	under, err := src.Children(ctx, model.NewRef(model.KindDatabase, "db0"))
	if err != nil || len(under) != 0 {
		t.Errorf("db0 holds %d nodes, %v", len(under), err)
	}
	// Closed twice, because a stream's Close is allowed to be: the window lets
	// a tail go and the tab that held it is closed after.
	if err := rs.Close(); err != nil {
		t.Error(err)
	}
	if err := rs.Close(); err != nil {
		t.Errorf("closing it again: %v", err)
	}
}

// A channel nobody is listening to is not in the tree, because the server has
// no way to know of it: it is not a channel yet, and a tree that invented one
// would be a tree with a name nothing published to.
func TestLiveAQuietChannelIsNotThere(t *testing.T) {
	src := live(t, liveConfig("0"))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c := conn(t, src, 0)
	// Published with nobody listening: it reaches nobody and is kept nowhere.
	if err := c.Publish(ctx, "ikigai.quiet", "into the void").Err(); err != nil {
		t.Fatal(err)
	}
	names, err := src.(*redisSource).channels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if name == "ikigai.quiet" {
			t.Error("a channel nobody is listening to is named anyway")
		}
	}
	// And with nobody listening to anything, the tree has no Channels folder at
	// all: an empty folder that was always there would say less than its
	// absence does.
	roots, err := src.Root(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range roots {
		if k, ok := model.ClassOf(n.Ref); ok && k == model.KindChannel {
			t.Errorf("there is a Channels folder holding %v", n.Badge)
		}
	}
	// A database's own children are still nothing: its keys are rows, and the
	// channels are not under it.
	kids, err := src.Children(ctx, model.NewRef(model.KindDatabase, "db0"))
	if err != nil || len(kids) != 0 {
		t.Errorf("a database holds %d nodes, %v", len(kids), err)
	}
}

// A listener is let go of at once, even on a channel nobody is speaking on.
//
// The socket read a subscription waits on is not interrupted by a context, so a
// stream that read in Next would notice a cancel only when the next message
// arrived — never, on a quiet channel — and the window letting a tail go would
// wait with it (app.Tail.Close waits for its reader).
func TestLiveAQuietListenerIsLetGoAtOnce(t *testing.T) {
	src := live(t, liveConfig("0"))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	rs, err := src.Browse(ctx, model.NewRef(model.KindChannel, "ikigai.silent"),
		source.BrowseOptions{Follow: true})
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	defer rs.Close()

	read, stopReading := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		_, err := rs.Next(read)
		done <- err
	}()
	// Long enough to be waiting on the channel rather than not started.
	time.Sleep(250 * time.Millisecond)
	stopReading()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("it gave up with %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("it is still waiting for a message nobody is sending")
	}
}
