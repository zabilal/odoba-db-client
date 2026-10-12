package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// table is rows somebody else can change, read again by key as a source
// would read them.
type table struct {
	rows  []model.Row
	asked []string
	fail  error
}

func (tb *table) read(_ context.Context, key []source.Filter) ([]model.Row, error) {
	tb.asked = append(tb.asked, fmt.Sprint(key))
	if tb.fail != nil {
		return nil, tb.fail
	}
	var out []model.Row
	for _, row := range tb.rows {
		match := true
		for _, f := range key {
			at := map[string]int{"id": 0, "name": 1, "score": 2}[f.Column]
			switch f.Op {
			case source.OpIsNull:
				match = match && row[at] == nil
			case source.OpEqual:
				match = match && reflect.DeepEqual(row[at], f.Values[0])
			default:
				match = false
			}
		}
		if match {
			out = append(out, row)
		}
	}
	return out, nil
}

// drifts describes what a check found, a row a line.
func drifts(ds []Drifted) string {
	var out []string
	for _, d := range ds {
		line := fmt.Sprintf("%s %v", [...]string{"insert", "update", "delete"}[d.Kind], d.Key)
		if d.Gone {
			line += " gone"
		}
		for _, c := range d.Columns {
			line += fmt.Sprintf(" %s:%v->%v", c.Name, c.Was, c.Now)
			if c.Edited {
				line += "!"
			}
		}
		out = append(out, line)
	}
	return strings.Join(out, "; ")
}

func TestARowChangedSinceItWasReadIsFound(t *testing.T) {
	when := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	read := []model.Row{
		{int64(1), "ann", 1.5}, {int64(2), "bob", math.NaN()}, {int64(3), "cy", []byte{1, 2}},
		{int64(4), "di", when}, {int64(5), "ed", nil}, {int64(6), "flo", 6.0}, {int64(7), "gus", math.NaN()},
	}
	p := newPending(t)
	for i, name := range []string{"ANN", "BOB", "CY", "DI"} {
		if err := p.Set(read[i], 1, name); err != nil {
			t.Fatal(err)
		}
	}
	p.Delete(read[4])
	p.Delete(read[5])
	p.Delete(read[6])
	p.Add() // a new row was never read, and is not read again
	if !p.Rereads() || newPending(t).Rereads() {
		t.Fatal("a change to a row read is one to read again, and no change is none")
	}
	now := &table{rows: []model.Row{
		{int64(1), "ann", 1.5},                              // as it was
		{int64(2), "rob", math.NaN()},                       // the column being changed, changed; and NaN is still NaN
		{int64(3), "cy", []byte{1, 2, 3}},                   // another column changed
		{int64(4), "di", when.In(time.FixedZone("", 3600))}, // the same moment, said elsewhere
		{int64(5), "ed", int64(0)},                          // a row to be deleted, changed
		{int64(7), "gus", 2.0},                              // what was not a number is one now
	}} // and 6 is gone
	found, unchecked, err := p.Drift(context.Background(), now.read)
	if err != nil || unchecked != 0 {
		t.Fatalf("%v, %d unchecked", err, unchecked)
	}
	want := "update [2] name:bob->rob!; update [3] score:[1 2]->[1 2 3]; delete [5] score:<nil>->0; delete [6] gone; delete [7] score:NaN->2"
	if got := drifts(found); got != want {
		t.Errorf("found\n%s\nfor\n%s", got, want)
	}
	// Each row once, by its key and nothing else, in the order changed.
	if got := strings.Join(now.asked, " "); got != "[{id = [1] false}] [{id = [2] false}] [{id = [3] false}] [{id = [4] false}] [{id = [5] false}] [{id = [6] false}] [{id = [7] false}]" {
		t.Errorf("asked for %s", got)
	}
	// A read that fails is an error, and not rows that are unchanged.
	now.fail = errors.New("connection reset")
	if found, _, err := p.Drift(context.Background(), now.read); err == nil || found != nil {
		t.Errorf("a failed read: %v, %v", found, err)
	}
}

func TestAKeyOfSeveralColumnsOrOfNothingIsAskedForAsItIs(t *testing.T) {
	// A key of two columns, one of them renamed by the query that read it,
	// and one row whose key holds NULL.
	cols := []model.ColumnDef{{Name: "id"}, {Name: "who", OriginColumn: "name"}, {Name: "score"}}
	p, err := NewPending(cols, model.RowIdentity{Kind: model.IdentityUniqueIndex, Columns: []string{"id", "name"}, Target: peopleRef})
	if err != nil {
		t.Fatal(err)
	}
	a, b := model.Row{int64(1), "ann", 1.0}, model.Row{int64(2), nil, 2.0}
	_ = p.Set(a, 2, 10.0)
	_ = p.Set(b, 2, 20.0)
	now := &table{rows: []model.Row{{int64(1), "ann", 1.0}, {int64(1), "zed", 9.0}, {int64(2), nil, 3.0}, {int64(2), "x", 9.0}}}
	found, _, err := p.Drift(context.Background(), now.read)
	if err != nil {
		t.Fatal(err)
	}
	if got := drifts(found); got != "update [2 <nil>] score:2->3!" {
		t.Errorf("found %s", got)
	}
	if got := strings.Join(now.asked, " "); got != "[{id = [1] false} {name = [ann] false}] [{id = [2] false} {name is null [] false}]" {
		t.Errorf("asked for %s", got)
	}
	// A key that finds two rows is not a key: which was read cannot be said,
	// and nothing is claimed of it.
	now.rows = append([]model.Row{{int64(1), "ann", 5.0}}, now.rows...)
	if found, _, _ := p.Drift(context.Background(), now.read); drifts(found) != "update [2 <nil>] score:2->3!" {
		t.Errorf("found %s", drifts(found))
	}
	// A row read again with fewer columns than were read is compared in
	// those it has.
	now.rows = []model.Row{{int64(1), "ann"}, {int64(2), nil, 2.0}}
	if found, _, err := p.Drift(context.Background(), now.read); err != nil || len(found) != 0 {
		t.Errorf("found %s, %v", drifts(found), err)
	}
}

func TestOnlySoManyRowsAreReadAgain(t *testing.T) {
	p := newPending(t)
	now := &table{}
	for i := range DriftLimit + 7 {
		row := model.Row{int64(i), "n", 1.0}
		_ = p.Set(row, 1, "m")
		if i != 3 {
			now.rows = append(now.rows, row)
		}
	}
	found, unchecked, err := p.Drift(context.Background(), now.read)
	if err != nil || unchecked != 7 || len(now.asked) != DriftLimit || drifts(found) != "update [3] gone" {
		t.Errorf("%d read again, %d not, found %s, %v", len(now.asked), unchecked, drifts(found), err)
	}
	// Exactly the limit is every row, and none left over.
	q := newPending(t)
	for i := range DriftLimit {
		_ = q.Set(model.Row{int64(i), "n", 1.0}, 1, "m")
	}
	now.asked = nil
	if _, unchecked, _ := q.Drift(context.Background(), now.read); unchecked != 0 || len(now.asked) != DriftLimit {
		t.Errorf("at the limit: %d read again, %d not", len(now.asked), unchecked)
	}
}

// rereadFake is a source whose rows are read again by key.
type rereadFake struct {
	browseFake
	asked []source.BrowseOptions
	rows  []model.Row
	fail  error
	boom  bool
	late  error
}

func (k *rereadFake) Browse(ctx context.Context, ref model.ObjectRef, opt source.BrowseOptions) (model.RowStream, error) {
	if len(opt.Filters) == 0 {
		return k.browseFake.Browse(ctx, ref, opt)
	}
	k.asked = append(k.asked, opt)
	switch {
	case k.boom:
		panic("fake driver: the read fell over")
	case k.fail != nil:
		return nil, k.fail
	}
	return &handed{rows: k.rows, late: k.late, closed: &k.closed}, nil
}

type handed struct {
	rows   []model.Row
	late   error
	closed interface{ Add(int64) int64 }
}

func (h *handed) Columns() []model.ColumnDef { return nil }
func (h *handed) Close() error               { h.closed.Add(1); return nil }
func (h *handed) Next(context.Context) (model.Row, error) {
	if len(h.rows) == 0 {
		if h.late != nil {
			return nil, h.late
		}
		return nil, io.EOF
	}
	row := h.rows[0]
	h.rows = h.rows[1:]
	return row, nil
}

func TestATablesRowIsReadAgainByItsKeyAlone(t *testing.T) {
	ctx := context.Background()
	src := &rereadFake{browseFake: browseFake{n: 10}}
	// The browse has a filter, an order and a choice of columns of its own.
	b, err := NewBrowseSource(ctx, src, orders, source.BrowseOptions{Columns: []string{"id", "name"}, Where: "id > 5",
		Sorts: []source.Sort{{Column: "name"}}})
	if err != nil {
		t.Fatal(err)
	}
	key := []source.Filter{{Column: "id", Op: source.OpEqual, Values: []any{int64(7)}}}
	src.rows = []model.Row{{int64(7), "a"}, {int64(7), "b"}, {int64(7), "c"}}
	closed := src.closed.Load()
	rows, err := b.ReadByKey(ctx, key)
	// Two at most: enough to see that a key is not one, and no more.
	if err != nil || len(rows) != 2 {
		t.Fatalf("%v, %v", rows, err)
	}
	if src.closed.Load() != closed+1 {
		t.Error("the read was left open, holding a connection")
	}
	// Its columns, so the row can be held against the one read; its key and
	// no other condition, so a row the browse's own filter no longer finds
	// is still found; and no more than two asked for.
	got := src.asked[0]
	if !reflect.DeepEqual(got.Columns, []string{"id", "name"}) || !reflect.DeepEqual(got.Filters, key) || got.Where != "" || got.Sorts != nil || got.Limit != 2 || got.Offset != 0 {
		t.Errorf("asked for %+v", got)
	}
	src.rows = nil
	if rows, err := b.ReadByKey(ctx, key); err != nil || len(rows) != 0 {
		t.Errorf("a row that is gone: %v, %v", rows, err)
	}
	src.fail = errors.New("connection reset")
	if _, err := b.ReadByKey(ctx, key); err == nil || err.Error() != "connection reset" {
		t.Errorf("a read that fails: %v", err)
	}
	src.fail, src.late, src.rows = nil, errors.New("cut off"), []model.Row{{int64(7), "a"}}
	if _, err := b.ReadByKey(ctx, key); err == nil || err.Error() != "cut off" {
		t.Errorf("a read that fails part way: %v", err)
	}
	src.late, src.boom = nil, true
	if _, err := b.ReadByKey(ctx, key); err == nil || !strings.Contains(err.Error(), "reading a row again") {
		t.Errorf("a driver that falls over is an error, not the end of the application: %v", err)
	}
}
