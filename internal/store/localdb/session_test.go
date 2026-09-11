package localdb

import (
	"reflect"
	"testing"
)

func TestSessionRoundTrips(t *testing.T) {
	d, _ := open(t)
	if _, ok, err := d.Session(ctx); ok || err != nil {
		t.Fatalf("no session yet: ok=%v err=%v", ok, err)
	}
	want := Session{Width: 1100, Height: 700, Sidebar: 0.3, Active: 1, Split: "down", SplitOffset: 0.6, Tabs: []SessionTab{
		{Kind: SessionObject, ConnectionID: "c1", Pinned: true, RefKind: "table", RefPath: []string{"db", "public", "people"},
			Label: "people", Filters: map[string]string{"name": "ann"}, Sorts: []SessionSort{{Column: "id", Descending: true}},
			Where: "id > 3"},
		{Kind: SessionQuery, ConnectionID: "c1", ScratchID: "s1", SavedID: "q1", Pane: 1, Front: true},
	}}
	if err := d.PutSession(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, ok, err := d.Session(ctx)
	if !ok || err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, ok=%v, err=%v", got, ok, err)
	}
}

func TestAnUnreadableSessionIsAnErrorNotAPanic(t *testing.T) {
	d, _ := open(t)
	d.Put(ctx, "session", []byte(`{"Tabs": 3}`))
	if _, ok, err := d.Session(ctx); ok || err == nil {
		t.Errorf("ok=%v err=%v", ok, err)
	}
}
