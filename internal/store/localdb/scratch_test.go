package localdb

import (
	"strings"
	"testing"
	"time"
)

func TestScratchBuffers(t *testing.T) {
	d, _ := open(t)
	t0 := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	d.Put(ctx, "tabs", []byte(`[]`)) // other keys are not scratch buffers
	d.Put(ctx, "scratch", []byte(`{}`))
	for _, sc := range []Scratch{
		{ID: "b", ConnectionID: "c1", Body: "second", Opened: t0.Add(time.Minute)},
		{ID: "a", ConnectionID: "c2", SavedID: "q1", Body: "first", Opened: t0},
		{ID: "b", ConnectionID: "c1", Body: "second, edited", Opened: t0.Add(time.Minute)},
	} {
		if err := d.PutScratch(ctx, sc); err != nil {
			t.Fatal(err)
		}
	}
	got, err := d.Scratches(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "a" || got[0].SavedID != "q1" || got[0].Body != "first" ||
		got[1].ID != "b" || got[1].Body != "second, edited" || !got[1].Opened.Equal(t0.Add(time.Minute)) {
		t.Fatalf("scratches = %+v", got)
	}

	if err := d.DeleteScratch(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if err := d.DeleteScratch(ctx, "gone"); err != nil {
		t.Errorf("deleting a missing buffer: %v", err)
	}
	if got, _ := d.Scratches(ctx); len(got) != 1 || got[0].ID != "b" {
		t.Errorf("after delete: %+v", got)
	}
	if err := d.PutScratch(ctx, Scratch{Body: "x"}); err == nil {
		t.Error("a buffer without an ID was stored")
	}
}

func TestAnUnreadableScratchDoesNotHideTheOthers(t *testing.T) {
	d, _ := open(t)
	d.PutScratch(ctx, Scratch{ID: "ok", Body: "kept"})
	d.Put(ctx, "scratch/bad", []byte(`{not json`))
	got, err := d.Scratches(ctx)
	if len(got) != 1 || got[0].Body != "kept" {
		t.Errorf("scratches = %+v", got)
	}
	if err == nil || !strings.Contains(err.Error(), "scratch/bad") {
		t.Errorf("err = %v, want it to name the unreadable buffer", err)
	}
	if _, ok, _ := d.Get(ctx, "scratch/bad"); !ok {
		t.Error("an unreadable buffer was thrown away")
	}
}
