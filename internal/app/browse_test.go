package app

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/capability"
)

// browseFake serves n rows of (id, name), honouring Offset and Limit.
type browseFake struct {
	fakeSource
	n        int64
	opened   atomic.Int64
	closed   atomic.Int64
	failNext atomic.Bool
	overrun  bool // ignore Limit, to prove the adapter bounds it
	exact    bool
	distinct bool
	boom     atomic.Bool // Browse panics, as a broken driver might
	// listed records the options the last Distinct was given.
	listed source.BrowseOptions
}

func (b *browseFake) Distinct(_ context.Context, _ model.ObjectRef, _ string, opt source.BrowseOptions, _ int) ([]source.DistinctValue, error) {
	b.listed = opt
	return []source.DistinctValue{{Value: "a", Count: 3}}, nil
}

func (b *browseFake) Capabilities() capability.Capabilities {
	c := b.fakeSource.Capabilities()
	c.Data.ExactCount = b.exact
	c.Data.DistinctValues = b.distinct
	return c
}

func (b *browseFake) Count(context.Context, model.ObjectRef, source.BrowseOptions) (int64, error) {
	return b.n, nil
}

func (b *browseFake) Browse(_ context.Context, ref model.ObjectRef, opt source.BrowseOptions) (model.RowStream, error) {
	if b.boom.Load() {
		panic("fake driver: the browse fell over")
	}
	if b.failNext.Swap(false) {
		return nil, errors.New("connection reset")
	}
	b.opened.Add(1)
	end := opt.Offset + opt.Limit
	if b.overrun || end > b.n {
		end = b.n
	}
	return &sliceRows{from: opt.Offset, to: end, closed: &b.closed,
		id: model.RowIdentity{Kind: model.IdentityPrimaryKey, Columns: []string{"id"}, Target: ref}}, nil
}

func TestASourceThatDoesNotWriteSaysSo(t *testing.T) {
	ctx := context.Background()
	b, err := NewBrowseSource(ctx, &browseFake{n: 10}, orders, source.BrowseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Plan(ctx, source.Changeset{}); err == nil || !strings.Contains(err.Error(), "does not write rows") {
		t.Errorf("plan: %v", err)
	}
	if _, err := b.Apply(ctx, &source.WritePlan{}); err == nil || !strings.Contains(err.Error(), "does not write rows") {
		t.Errorf("apply: %v", err)
	}
}

type sliceRows struct {
	from, to int64
	closed   *atomic.Int64
	id       model.RowIdentity
}

func (s *sliceRows) Columns() []model.ColumnDef {
	return []model.ColumnDef{{Name: "id"}, {Name: "name"}}
}
func (s *sliceRows) Next(context.Context) (model.Row, error) {
	if s.from >= s.to {
		return nil, io.EOF
	}
	s.from++
	return model.Row{s.from, "row"}, nil
}
func (s *sliceRows) Close() error                { s.closed.Add(1); return nil }
func (s *sliceRows) Identity() model.RowIdentity { return s.id }

var orders = model.NewRef(model.KindTable, "db", "public", "orders")

// The grid's Fetcher shape, restated here so this test fails to compile if
// BrowseSource ever drifts from it. app cannot import the grid (ARCH-1).
type gridFetcher interface {
	Columns() []model.ColumnDef
	Fetch(ctx context.Context, offset, limit int64) ([]model.Row, error)
	Count(ctx context.Context) (int64, error)
}

var _ gridFetcher = (*BrowseSource)(nil)

func TestBrowseSourcePagesAndClosesEveryStream(t *testing.T) {
	src := &browseFake{n: 1000}
	b, err := NewBrowseSource(context.Background(), src, orders, source.BrowseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Columns()) != 2 {
		t.Fatalf("columns not known before the first fetch: %v", b.Columns())
	}
	if !b.Identity().Editable() {
		t.Error("identity from the source was lost")
	}

	page, err := b.Fetch(context.Background(), 256, 256)
	if err != nil || len(page) != 256 || page[0][0] != int64(257) {
		t.Fatalf("page: %d rows, first %v, %v", len(page), page[0][0], err)
	}
	last, _ := b.Fetch(context.Background(), 900, 256)
	if len(last) != 100 {
		t.Errorf("short final page: %d rows", len(last))
	}
	if src.opened.Load() != src.closed.Load() {
		t.Errorf("%d streams opened, %d closed; each open stream holds a connection",
			src.opened.Load(), src.closed.Load())
	}
}

func TestBrowseSourceBoundsAPageTheSourceOverfilled(t *testing.T) {
	src := &browseFake{n: 10_000, overrun: true}
	b, _ := NewBrowseSource(context.Background(), src, orders, source.BrowseOptions{})
	if _, err := b.Fetch(context.Background(), 0, 50); err == nil {
		t.Error("a source ignoring Limit grew the page without bound")
	}
	if src.opened.Load() != src.closed.Load() {
		t.Error("the stream was not closed on the error path")
	}
}

func TestBrowseSourceCountOnlyWhenCheap(t *testing.T) {
	cheap := &browseFake{n: 42, exact: true}
	b, _ := NewBrowseSource(context.Background(), cheap, orders, source.BrowseOptions{})
	if n, _ := b.Count(context.Background()); n != 42 {
		t.Errorf("exact count = %d", n)
	}
	costly := &browseFake{n: 42, exact: false}
	b2, _ := NewBrowseSource(context.Background(), costly, orders, source.BrowseOptions{})
	if n, _ := b2.Count(context.Background()); n != -1 {
		t.Errorf("a full-scan count was performed: %d", n)
	}
}

func TestBrowseSourceSurfacesErrors(t *testing.T) {
	src := &browseFake{n: 10}
	b, _ := NewBrowseSource(context.Background(), src, orders, source.BrowseOptions{})
	src.failNext.Store(true)
	if _, err := b.Fetch(context.Background(), 0, 5); err == nil {
		t.Error("a failed browse returned no error; the grid would show blank rows")
	}
}

func TestDistinctLeavesOutTheColumnsOwnFilter(t *testing.T) {
	src := &browseFake{n: 10, distinct: true}
	name := source.Filter{Column: "name", Op: source.OpIn, Values: []any{"a"}}
	id := source.Filter{Column: "id", Op: source.OpGreater, Values: []any{int64(3)}}
	b, err := NewBrowseSource(context.Background(), src, orders, source.BrowseOptions{Filters: []source.Filter{name, id}, Where: "id < 9"})
	if err != nil {
		t.Fatal(err)
	}
	vals, err := b.Distinct(context.Background(), "name", 50)
	if err != nil || len(vals) != 1 {
		t.Fatalf("%v, %v", vals, err)
	}
	if len(src.listed.Filters) != 1 || src.listed.Filters[0].Column != "id" || src.listed.Where != "id < 9" {
		t.Errorf("listed under %+v; want the id filter and the typed WHERE", src.listed)
	}
}

func TestDistinctNeedsTheCapability(t *testing.T) {
	b, err := NewBrowseSource(context.Background(), &browseFake{n: 10}, orders, source.BrowseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if b.CanListValues() {
		t.Error("a source that does not claim DistinctValues cannot list values")
	}
	if _, err := b.Distinct(context.Background(), "name", 50); err == nil {
		t.Error("Distinct should refuse without the capability")
	}
}
