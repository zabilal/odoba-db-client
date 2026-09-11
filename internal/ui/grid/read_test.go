package grid

import (
	"context"
	"testing"
)

func TestReadCrossesPagesAndStopsAtTheEnd(t *testing.T) {
	f := NewSyntheticFetcher(600)
	m := NewModel(f)
	rows, err := m.Read(context.Background(), 250, 520)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 270 {
		t.Fatalf("%d rows for [250, 520)", len(rows))
	}
	want, _ := f.Fetch(context.Background(), 250, 1)
	if rows[0][0] != want[0][0] {
		t.Errorf("first row %v, want %v", rows[0], want[0])
	}
	rows, err = m.Read(context.Background(), 590, 1<<40)
	if err != nil || len(rows) != 10 {
		t.Errorf("reading past the end: %d rows, %v; want the last 10", len(rows), err)
	}
	if m.Resident(300) {
		t.Error("a read must not keep the pages it fetched")
	}
}
