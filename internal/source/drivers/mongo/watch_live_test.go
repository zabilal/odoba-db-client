//go:build conformance

package mongo

// Following a collection against a real server (FR-12.5).
//
// A change stream needs an oplog, which a standalone server has not got, so
// these go against ikigai-mongo-rs on port 57018 — a single-node replica set
// kept apart from the standalone every other test here uses, so that nothing
// already proven against a standalone is changed by proving this.
//
// The standalone's refusal is worth a test of its own, and is below: a person
// browsing a collection is not thinking about replica sets, so what they are
// told has to say what to do about it.

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// replicaPort is where the replica set is.
func replicaPort() int {
	if v := os.Getenv("IKIGAI_MONGO_RS_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			return p
		}
	}
	return 57018
}

// openReplica connects to the replica set, or skips where it is not running.
func openReplica(t *testing.T, db string) source.Source {
	t.Helper()
	cfg := source.ConnectionConfig{DriverID: driverID, Host: "127.0.0.1", Port: replicaPort(),
		Database: db, TLS: source.TLSConfig{Mode: "disable"}}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	src, err := Driver{}.Open(ctx, cfg)
	if err != nil {
		if os.Getenv("IKIGAI_REQUIRE_MONGO") != "" {
			t.Fatalf("a mongodb replica set is required but unavailable: %v", err)
		}
		t.Skipf("no mongodb replica set on port %d (docker start ikigai-mongo-rs): %v",
			replicaPort(), err)
	}
	t.Cleanup(func() { src.Close() })
	return src
}

// A collection being followed answers when somebody writes: what happened, to
// which document, and what the document is now.
func TestLiveFollowingACollection(t *testing.T) {
	const db = "ikigai_watch"
	src := openReplica(t, db)
	s := src.(*mongoSource)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	coll := s.client.Database(db).Collection("events")
	if err := s.client.Database(db).Drop(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.client.Database(db).Drop(context.Background()) })
	if _, err := coll.InsertOne(ctx, map[string]any{"_id": "before", "n": 0}); err != nil {
		t.Fatal(err)
	}

	ref := model.NewRef(model.KindCollection, db, "events")
	rs, err := src.Browse(ctx, ref, source.BrowseOptions{Follow: true})
	if err != nil {
		t.Fatalf("following: %v", err)
	}
	defer rs.Close()

	// Its columns are a change's, not a document's: a deletion has no document
	// at all, and showing one for it would show a document that is not there.
	cols := rs.Columns()
	want := []string{"at", "change", "_id", "document", "fields"}
	if len(cols) != len(want) {
		t.Fatalf("it has %d columns: %+v", len(cols), cols)
	}
	for i, name := range want {
		if cols[i].Name != name {
			t.Errorf("column %d is %q, want %q", i, cols[i].Name, name)
		}
	}

	// Writes made while it is following, each of a different kind — and each
	// read before the next is made. The document a change carries is looked up
	// when the change is read, so a document deleted before its update had been
	// read would arrive with no document at all: true of the server, and not
	// what this is about.
	read, stopReading := context.WithCancel(ctx)
	rows := make(chan model.Row, 8)
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
	// The reader stops before the stream closes under it: a change stream is
	// one goroutine's at a time, which is how a Tail holds one too.
	defer func() { stopReading(); <-done }()

	change := func(what string) model.Row {
		t.Helper()
		select {
		case row := <-rows:
			return row
		case err := <-fail:
			t.Fatalf("reading the %s: %v", what, err)
		case <-time.After(20 * time.Second):
			t.Fatalf("the %s never arrived", what)
		}
		return nil
	}

	// A moment for the stream to be watching before anything is written: a
	// change made before it started is a change it will not see, which is what
	// following means.
	time.Sleep(500 * time.Millisecond)
	if _, err := coll.InsertOne(ctx, map[string]any{"_id": "after", "n": 1}); err != nil {
		t.Fatal(err)
	}
	inserted := change("insert")
	if _, err := coll.UpdateOne(ctx, map[string]any{"_id": "after"},
		map[string]any{"$set": map[string]any{"n": 2}}); err != nil {
		t.Fatal(err)
	}
	updated := change("update")
	if _, err := coll.DeleteOne(ctx, map[string]any{"_id": "after"}); err != nil {
		t.Fatal(err)
	}
	deleted := change("deletion")

	got := []model.Row{inserted, updated, deleted}
	kinds := []string{"insert", "update", "delete"}
	for i, kind := range kinds {
		if got[i][1] != kind {
			t.Errorf("change %d is %v, want %q", i, got[i][1], kind)
		}
		if got[i][2] != "after" {
			t.Errorf("change %d is about %v", i, got[i][2])
		}
		if at, ok := got[i][0].(time.Time); !ok || at.IsZero() {
			t.Errorf("change %d happened at %v", i, got[i][0])
		}
	}
	// The insert and the update carry the document as it is now; the deletion
	// carries none, because there is none.
	if doc, ok := got[0][3].(map[string]any); !ok || doc["_id"] != "after" {
		t.Errorf("the insert carries %#v", got[0][3])
	}
	if doc, ok := got[1][3].(map[string]any); !ok || doc["n"] == nil {
		t.Errorf("the update carries %#v", got[1][3])
	}
	if got[2][3] != nil {
		t.Errorf("the deletion carries a document: %#v", got[2][3])
	}
	// And the update says which fields moved, which is the other half of what
	// an update is.
	if fields, ok := got[1][4].(map[string]any); !ok || fields["updatedFields"] == nil {
		t.Errorf("the update says its fields are %#v", got[1][4])
	}
}

// A collection that is not one cannot be followed, and says so.
func TestLiveOnlyACollectionIsFollowed(t *testing.T) {
	const db = "ikigai_watch_kinds"
	src := openReplica(t, db)
	ctx := context.Background()
	for _, ref := range []model.ObjectRef{
		model.NewRef(model.KindDatabase, db),
		model.NewRef(model.KindCollection, db), // no collection named
	} {
		if _, err := src.Browse(ctx, ref, source.BrowseOptions{Follow: true}); err == nil {
			t.Errorf("%s was followed", ref)
		}
	}
}

// A standalone server keeps no oplog, so it cannot answer at all — and what it
// says has to tell somebody what to do about it, because a person browsing a
// collection is not thinking about replica sets.
func TestLiveAStandaloneCannotBeFollowed(t *testing.T) {
	const db = "ikigai_watch_standalone"
	src := open(t, liveConfig(db))
	s := src.(*mongoSource)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	coll := s.client.Database(db).Collection("events")
	if _, err := coll.InsertOne(ctx, map[string]any{"n": 1}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.client.Database(db).Drop(context.Background()) })

	rs, err := src.Browse(ctx, model.NewRef(model.KindCollection, db, "events"),
		source.BrowseOptions{Follow: true})
	if err == nil {
		rs.Close()
		t.Fatal("a standalone server followed a collection")
	}
	for _, want := range []string{"replica set", "oplog"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("it says %q, which does not mention %q", err, want)
		}
	}
	// And it is the driver's own error underneath, for anybody who wants it.
	if errors.Unwrap(err) == nil && !strings.Contains(err.Error(), "mongodb:") {
		t.Errorf("it says %q", err)
	}
}

// Following from a time reads the changes since then, which is the one position
// a change stream has.
func TestLiveFollowingFromATime(t *testing.T) {
	const db = "ikigai_watch_time"
	src := openReplica(t, db)
	s := src.(*mongoSource)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	coll := s.client.Database(db).Collection("events")
	if err := s.client.Database(db).Drop(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.client.Database(db).Drop(context.Background()) })

	// A write, then a moment, then the position: the write is before it.
	if _, err := coll.InsertOne(ctx, map[string]any{"_id": "old"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	from := time.Now()
	time.Sleep(1500 * time.Millisecond)
	if _, err := coll.InsertOne(ctx, map[string]any{"_id": "new"}); err != nil {
		t.Fatal(err)
	}

	rs, err := src.Browse(ctx, model.NewRef(model.KindCollection, db, "events"),
		source.BrowseOptions{Follow: true, Seek: &source.Seek{Mode: source.SeekTimestamp, Time: from}})
	if err != nil {
		t.Fatalf("following from a time: %v", err)
	}
	defer rs.Close()
	row, err := rs.Next(ctx)
	if err != nil {
		t.Fatalf("reading from a time: %v", err)
	}
	// The change after the position, and not the one before it.
	if row[2] != "new" {
		t.Errorf("it read the change to %v", row[2])
	}
}

// A collection nobody is writing to is let go of at once.
//
// A tail ends by giving up on the read and waiting for its reader to come back
// (app.Tail.Close), so a stream that noticed a cancel only when the next change
// arrived would hold the window until somebody wrote.
func TestLiveAQuietChangeStreamIsLetGoAtOnce(t *testing.T) {
	const db = "ikigai_watch_quiet"
	src := openReplica(t, db)
	s := src.(*mongoSource)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := s.client.Database(db).Collection("events").
		InsertOne(ctx, map[string]any{"_id": "one"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.client.Database(db).Drop(context.Background()) })

	rs, err := src.Browse(ctx, model.NewRef(model.KindCollection, db, "events"),
		source.BrowseOptions{Follow: true})
	if err != nil {
		t.Fatalf("following: %v", err)
	}
	defer rs.Close()
	read, stopReading := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		_, err := rs.Next(read)
		done <- err
	}()
	// Long enough to be waiting for a change rather than not started.
	time.Sleep(250 * time.Millisecond)
	stopReading()
	select {
	case err := <-done:
		// The cancellation as the caller asked it, not the driver's account of
		// it: a tail reads its own end from this.
		if !errors.Is(err, context.Canceled) {
			t.Errorf("it gave up with %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("it is still waiting for a change nobody is making")
	}
}
