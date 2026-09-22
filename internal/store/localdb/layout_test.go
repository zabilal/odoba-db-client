package localdb

import (
	"context"
	"path/filepath"
	"testing"
)

func layoutDB(t *testing.T) *DB {
	t.Helper()
	d, err := Open(context.Background(), filepath.Join(t.TempDir(), "ikigai.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// A diagram's arrangement is kept per connection and per diagram, so two
// diagrams of one database do not overwrite each other.
func TestALayoutIsKeptPerDiagram(t *testing.T) {
	d := layoutDB(t)
	ctx := context.Background()

	want := DiagramLayout{Moved: map[string]NodePlace{"public.people": {X: 10, Y: 20}},
		Pan: NodePlace{X: -5, Y: 5}, Zoom: 1.25}
	if err := d.PutLayout(ctx, "c1", "public", want); err != nil {
		t.Fatal(err)
	}
	got, ok, err := d.Layout(ctx, "c1", "public")
	if err != nil || !ok {
		t.Fatalf("it read %v, %v", ok, err)
	}
	if got.Zoom != want.Zoom || got.Pan != want.Pan || got.Moved["public.people"].X != 10 {
		t.Errorf("it read %+v", got)
	}
	// Another diagram of the same connection, and the same diagram of
	// another connection, are each their own.
	if _, ok, _ := d.Layout(ctx, "c1", "audit"); ok {
		t.Error("another diagram read this one's arrangement")
	}
	if _, ok, _ := d.Layout(ctx, "c2", "public"); ok {
		t.Error("another connection read this one's arrangement")
	}
}

// A diagram nobody has arranged says so, rather than answering an
// arrangement with nothing in it.
func TestADiagramNobodyHasArranged(t *testing.T) {
	_, ok, err := layoutDB(t).Layout(context.Background(), "c1", "public")
	if err != nil || ok {
		t.Errorf("it said %v, %v", ok, err)
	}
}

// Forgetting an arrangement lays the diagram out afresh next time.
func TestForgettingAnArrangement(t *testing.T) {
	d := layoutDB(t)
	ctx := context.Background()
	if err := d.PutLayout(ctx, "c1", "public", DiagramLayout{Zoom: 1}); err != nil {
		t.Fatal(err)
	}
	if err := d.ForgetLayout(ctx, "c1", "public"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := d.Layout(ctx, "c1", "public"); ok {
		t.Error("it is still there")
	}
}

// An arrangement that cannot be read is an arrangement nobody has: it is
// where boxes were put, and losing it costs a rearrangement rather than
// anything a person cannot do again.
func TestAnArrangementThatCannotBeRead(t *testing.T) {
	d := layoutDB(t)
	ctx := context.Background()
	if err := d.Put(ctx, layoutKey("c1", "public"), []byte("not json")); err != nil {
		t.Fatal(err)
	}
	got, ok, err := d.Layout(ctx, "c1", "public")
	if err != nil {
		t.Errorf("it failed rather than forgetting: %v", err)
	}
	if ok || len(got.Moved) != 0 {
		t.Errorf("it read %+v", got)
	}
}
