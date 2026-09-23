package sqlite

import (
	"context"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// What a column holds (FR-3.14). A real file, because the figures are the
// engine's own answers about its own data.

func statsOf(t *testing.T, s *sqliteSource, column string, class model.TypeClass,
	opt source.BrowseOptions) *source.ColumnStats {
	t.Helper()
	got, err := s.ColumnStats(context.Background(), peopleRef(),
		model.ColumnDef{Name: column, Type: model.DataType{Class: class}}, opt)
	if err != nil {
		t.Fatalf("ColumnStats(%s): %v", column, err)
	}
	return got
}

func peopleRef() model.ObjectRef { return model.NewRef(model.KindTable, "main", "people") }

// A column of numbers has every figure there is.
func TestMeasuresAColumnOfNumbers(t *testing.T) {
	s := open(t, fixture(t), source.Guard{})
	defer s.Close()
	got := statsOf(t, s, "score", model.TypeFloat, source.BrowseOptions{})

	if got.Rows != 100 {
		t.Errorf("it measured %d rows, want 100", got.Rows)
	}
	if got.Nulls != 0 {
		t.Errorf("%d nulls", got.Nulls)
	}
	if got.Distinct != 100 {
		t.Errorf("%d distinct scores, want 100", got.Distinct)
	}
	if got.Min == nil || got.Max == nil {
		t.Errorf("the smallest is %v and the largest %v", got.Min, got.Max)
	}
	// The scores run 1.5 to 150, so their average is about 75: the ids run
	// 1 to 100 and average 50, which is how a test tells the two apart.
	if !got.HasMean || got.Mean < 70 || got.Mean > 80 {
		t.Errorf("the average is %v (measured: %v), want the scores' own", got.Mean, got.HasMean)
	}
	if got.Duration <= 0 {
		t.Errorf("it took %v", got.Duration)
	}
}

// A column of dates has a smallest and a largest and no average: the
// average of a date is a date nobody asked about.
func TestMeasuresAColumnOfDates(t *testing.T) {
	s := open(t, fixture(t), source.Guard{})
	defer s.Close()
	got := statsOf(t, s, "born", model.TypeDate, source.BrowseOptions{})

	if got.Nulls == 0 {
		t.Error("two rows in three have no date of birth, and none was counted")
	}
	if got.Min == nil || got.Max == nil {
		t.Errorf("the smallest is %v and the largest %v", got.Min, got.Max)
	}
	if got.HasMean {
		t.Errorf("it averaged a column of dates: %v", got.Mean)
	}
}

// A column of bytes is counted and nothing more: the distinct values of a
// column of pictures are a long way to no answer.
func TestMeasuresAColumnOfBytes(t *testing.T) {
	s := open(t, fixture(t), source.Guard{})
	defer s.Close()
	got := statsOf(t, s, "pic", model.TypeBytes, source.BrowseOptions{})

	if got.Rows != 100 || got.Nulls != 100 {
		t.Errorf("%d rows and %d nulls", got.Rows, got.Nulls)
	}
	if got.Distinct != -1 {
		t.Errorf("%d distinct pictures were counted", got.Distinct)
	}
	if got.Min != nil || got.Max != nil || got.HasMean {
		t.Errorf("a column of bytes was given %v, %v and %v", got.Min, got.Max, got.Mean)
	}
}

// The figures are about the rows the filters select.
func TestMeasuresOnlyTheRowsTheFiltersSelect(t *testing.T) {
	s := open(t, fixture(t), source.Guard{})
	defer s.Close()
	all := statsOf(t, s, "score", model.TypeFloat, source.BrowseOptions{})
	some := statsOf(t, s, "score", model.TypeFloat, source.BrowseOptions{
		Filters: []source.Filter{{Column: "id", Op: source.OpLessEqual, Values: []any{int64(10)}}},
	})
	if some.Rows != 10 {
		t.Errorf("%d rows of %d", some.Rows, all.Rows)
	}
	if some.Max == all.Max {
		t.Errorf("the largest of ten rows is the largest of a hundred: %v", some.Max)
	}
}

// Something that is not a table has no columns to measure.
func TestWhatCannotBeMeasured(t *testing.T) {
	s := open(t, fixture(t), source.Guard{})
	defer s.Close()
	_, err := s.ColumnStats(context.Background(),
		model.NewRef(model.KindSchema, "main"), model.ColumnDef{Name: "score"}, source.BrowseOptions{})
	if err == nil {
		t.Fatal("a schema's column was measured")
	}
	if !strings.Contains(err.Error(), "browsable") {
		t.Errorf("it said %v", err)
	}
}

// A connection that measures columns says so.
func TestSQLiteSaysItMeasuresColumns(t *testing.T) {
	s := open(t, fixture(t), source.Guard{})
	defer s.Close()
	if !s.Capabilities().Data.ColumnStats {
		t.Error("SQLite measures columns and does not say so")
	}
}
