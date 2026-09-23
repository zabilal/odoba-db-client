package app

import (
	"context"
	"errors"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/capability"
)

// What a column holds (FR-3.14).

// statsFake measures a column and remembers what it was asked.
type statsFake struct {
	browseFake
	asked source.BrowseOptions
	// sampled is what the last sample asked the source for.
	sampled source.BrowseOptions
	stats   bool
}

func (s *statsFake) ColumnStats(_ context.Context, _ model.ObjectRef, def model.ColumnDef,
	opt source.BrowseOptions) (*source.ColumnStats, error) {
	s.asked = opt
	return &source.ColumnStats{Rows: 10, Nulls: 2, Distinct: 5}, nil
}

// Browse remembers what a sample was asked for.
func (s *statsFake) Browse(ctx context.Context, ref model.ObjectRef, opt source.BrowseOptions) (model.RowStream, error) {
	if opt.Limit > 1 {
		s.sampled = opt
	}
	return s.browseFake.Browse(ctx, ref, opt)
}

func (s *statsFake) Capabilities() capability.Capabilities {
	c := s.browseFake.Capabilities()
	c.Data.ColumnStats = s.stats
	return c
}

func measuring(n int64) *statsFake {
	f := &statsFake{stats: true}
	f.n = n
	return f
}

var col = model.ColumnDef{Name: "id", Type: model.DataType{Class: model.TypeInteger}}

// Only the filters decide what is measured. How the grid is sorted or how
// far down it somebody has scrolled says nothing about the whole of it.
func TestOnlyTheFiltersDecideWhatIsMeasured(t *testing.T) {
	f := measuring(100)
	opt := source.BrowseOptions{
		Filters: []source.Filter{{Column: "id", Op: source.OpEqual, Values: []any{1}}},
		Where:   "id > 0",
		Sorts:   []source.Sort{{Column: "id"}},
		Offset:  50, Limit: 10,
	}
	got, err := MeasureColumn(context.Background(), f, model.NewRef(model.KindTable, "t"), col, opt)
	if err != nil {
		t.Fatal(err)
	}
	if got.Rows != 10 {
		t.Errorf("it measured %d rows", got.Rows)
	}
	if len(f.asked.Filters) != 1 || f.asked.Where != "id > 0" {
		t.Errorf("it asked with %+v, want the filters and the WHERE", f.asked)
	}
	if f.asked.Sorts != nil || f.asked.Offset != 0 || f.asked.Limit != 0 {
		t.Errorf("it asked with %+v, want nothing about sorting or paging", f.asked)
	}
}

// A connection that will not measure says so rather than answering nothing.
func TestMeasuringWhatWillNotBeMeasured(t *testing.T) {
	plain := &browseFake{}
	if CanMeasure(plain) {
		t.Error("a connection with no statistics said it had some")
	}
	_, err := MeasureColumn(context.Background(), plain, model.NewRef(model.KindTable, "t"), col,
		source.BrowseOptions{})
	if !errors.Is(err, ErrNoStats) {
		t.Errorf("it said %v", err)
	}
	if ErrNoStats.Error() == "" {
		t.Error("the refusal says nothing")
	}

	// And one that implements it but does not claim it is not asked.
	quiet := &statsFake{}
	if CanMeasure(quiet) {
		t.Error("a connection that does not claim statistics was asked for them")
	}
	if !CanMeasure(measuring(10)) {
		t.Error("a connection that measures columns was not asked")
	}
}

// A sample is of the column and of the rows the filters select, and of
// nothing else: it is a picture of the values, not a page of the grid.
func TestASampleIsOfTheColumnAndNothingElse(t *testing.T) {
	f := measuring(100)
	opt := source.BrowseOptions{
		Filters: []source.Filter{{Column: "id", Op: source.OpEqual, Values: []any{1}}},
		Where:   "id > 0",
		Sorts:   []source.Sort{{Column: "id"}},
		Offset:  50,
	}
	rows, err := SampleColumn(context.Background(), f, model.NewRef(model.KindTable, "t"), col, opt, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		t.Fatal("it sampled nothing")
	}
	got := f.sampled
	if len(got.Columns) != 1 || got.Columns[0] != "id" {
		t.Errorf("it asked for %v, want the one column", got.Columns)
	}
	if len(got.Filters) != 1 || got.Where != "id > 0" {
		t.Errorf("it asked with %+v, want the filters and the WHERE", got)
	}
	if got.Sorts != nil || got.Offset != 0 {
		t.Errorf("it asked with %+v, want nothing about sorting or paging", got)
	}
}

// A sample stops where it was asked to, and asks for a bound where nobody
// gave one: an unbounded read is the thing this must never do.
func TestASampleStopsWhereItWasAskedTo(t *testing.T) {
	f := measuring(10_000)
	rows, err := SampleColumn(context.Background(), f, model.NewRef(model.KindTable, "t"), col,
		source.BrowseOptions{}, 25)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 25 {
		t.Errorf("%d rows, want the 25 asked for", len(rows))
	}
	if f.sampled.Limit != 25 {
		t.Errorf("it asked the source for %d", f.sampled.Limit)
	}
	// And a driver that overruns the limit is still bounded here.
	f.overrun = true
	rows, err = SampleColumn(context.Background(), f, model.NewRef(model.KindTable, "t"), col,
		source.BrowseOptions{}, 25)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 25 {
		t.Errorf("%d rows from a driver that ignored the limit", len(rows))
	}
	// Nobody saying how many is the default rather than all of them.
	if _, err := SampleColumn(context.Background(), f, model.NewRef(model.KindTable, "t"), col,
		source.BrowseOptions{}, 0); err != nil {
		t.Fatal(err)
	}
	if f.sampled.Limit != SampleLimit {
		t.Errorf("with no limit it asked for %d, want %d", f.sampled.Limit, SampleLimit)
	}
}

// A sample of a table with fewer rows than were asked for is what there is.
func TestASampleOfFewerRowsThanWereAskedFor(t *testing.T) {
	f := measuring(3)
	rows, err := SampleColumn(context.Background(), f, model.NewRef(model.KindTable, "t"), col,
		source.BrowseOptions{}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Errorf("%d rows of a table of 3", len(rows))
	}
}
