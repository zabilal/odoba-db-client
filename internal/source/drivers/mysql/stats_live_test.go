//go:build conformance

package mysql

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// What a column holds (FR-3.14), on both servers.

func statsOf(t *testing.T, src *mysqlSource, column string, class model.TypeClass,
	opt source.BrowseOptions) *source.ColumnStats {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	got, err := src.ColumnStats(ctx, people,
		model.ColumnDef{Name: column, Type: model.DataType{Class: class}}, opt)
	if err != nil {
		t.Fatalf("ColumnStats(%s): %v", column, err)
	}
	return got
}

// A column of numbers has every figure there is.
func TestLiveMeasuresAColumnOfNumbers(t *testing.T) {
	each(t, func(t *testing.T, srv server) {
		src := open(t, srv, source.Guard{})
		got := statsOf(t, src, "score", model.TypeDecimal, source.BrowseOptions{})

		if got.Rows != 101 {
			t.Errorf("it measured %d rows, want 101", got.Rows)
		}
		if got.Nulls != 0 {
			t.Errorf("%d nulls", got.Nulls)
		}
		if got.Distinct != 101 {
			t.Errorf("%d distinct scores, want 101", got.Distinct)
		}
		if got.Min == nil || got.Max == nil {
			t.Errorf("the smallest is %v and the largest %v", got.Min, got.Max)
		}
		if !got.HasMean || got.Mean <= 0 {
			t.Errorf("the average is %v (measured: %v)", got.Mean, got.HasMean)
		}
		if got.Duration <= 0 {
			t.Errorf("it took %v", got.Duration)
		}
	})
}

// A column of text has everything but an average.
func TestLiveMeasuresAColumnOfText(t *testing.T) {
	each(t, func(t *testing.T, srv server) {
		src := open(t, srv, source.Guard{})
		got := statsOf(t, src, "name", model.TypeString, source.BrowseOptions{})
		if got.Distinct != 101 {
			t.Errorf("%d distinct names, want 101", got.Distinct)
		}
		if got.Min == nil || got.Max == nil {
			t.Errorf("the smallest is %v and the largest %v", got.Min, got.Max)
		}
		if got.HasMean {
			t.Errorf("it averaged a column of text: %v", got.Mean)
		}
	})
}

// A column nothing is in is counted, and the rest of the figures are what
// an empty column has: nothing.
func TestLiveMeasuresAColumnOfNulls(t *testing.T) {
	each(t, func(t *testing.T, srv server) {
		src := open(t, srv, source.Guard{})
		got := statsOf(t, src, "seen", model.TypeTimestamp, source.BrowseOptions{})
		if got.Rows != got.Nulls {
			t.Errorf("%d rows and %d nulls; nothing has been seen", got.Rows, got.Nulls)
		}
		if got.Distinct != 0 {
			t.Errorf("%d distinct values in a column of nothing", got.Distinct)
		}
		if got.Min != nil || got.Max != nil {
			t.Errorf("a column of nothing has a smallest %v and a largest %v", got.Min, got.Max)
		}
	})
}

// The figures are about the rows the filters select.
func TestLiveMeasuresOnlyTheRowsTheFiltersSelect(t *testing.T) {
	each(t, func(t *testing.T, srv server) {
		src := open(t, srv, source.Guard{})
		all := statsOf(t, src, "score", model.TypeDecimal, source.BrowseOptions{})
		some := statsOf(t, src, "score", model.TypeDecimal, source.BrowseOptions{
			Filters: []source.Filter{{Column: "id", Op: source.OpLessEqual, Values: []any{int64(10)}}},
		})
		if some.Rows != 10 {
			t.Errorf("%d rows of %d", some.Rows, all.Rows)
		}
		if some.Max == all.Max {
			t.Errorf("the largest of ten rows is the largest of a hundred: %v", some.Max)
		}
	})
}

// A WHERE that could change something is refused before it runs.
func TestLiveAWhereThatCouldChangeSomethingIsRefused(t *testing.T) {
	each(t, func(t *testing.T, srv server) {
		src := open(t, srv, source.Guard{})
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		// The second of these is one condition, balanced, with no semicolon
		// in it — everything the WHERE reader asks for — and the statement
		// it makes still writes.
		for _, where := range []string{
			"1=1; DROP TABLE ikigai_it.writes",
			"EXISTS (DELETE FROM ikigai_it.writes)",
		} {
			_, err := src.ColumnStats(ctx, people,
				model.ColumnDef{Name: "score", Type: model.DataType{Class: model.TypeDecimal}},
				source.BrowseOptions{Where: where})
			if err == nil {
				t.Fatalf("%q was run", where)
			}
			if !strings.Contains(err.Error(), "WHERE") {
				t.Errorf("%q said %v, which does not name what was wrong", where, err)
			}
		}
		// And the table it was aimed at is still there.
		if got := statsOf(t, src, "score", model.TypeDecimal, source.BrowseOptions{}); got.Rows != 101 {
			t.Errorf("%d rows after the attempt", got.Rows)
		}
	})
}

// Something that is not a table has no columns to measure.
func TestLiveWhatCannotBeMeasured(t *testing.T) {
	each(t, func(t *testing.T, srv server) {
		src := open(t, srv, source.Guard{})
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		// One that names something whole but not something with rows in
		// it, and one that names a table without saying which database's:
		// the second would otherwise be looked for in whatever database
		// this connection is on.
		for _, tc := range []struct {
			ref  model.ObjectRef
			says string
		}{
			{model.NewRef(model.KindIndex, "ikigai_it", "people", "PRIMARY"), "not browsable"},
			{model.NewRef(model.KindTable, "people"), "incomplete"},
		} {
			_, err := src.ColumnStats(ctx, tc.ref, model.ColumnDef{Name: "score"}, source.BrowseOptions{})
			if err == nil {
				t.Fatalf("%s was measured", tc.ref)
			}
			if !strings.Contains(err.Error(), tc.says) {
				t.Errorf("%s said %v", tc.ref, err)
			}
		}
	})
}

// A connection that measures columns says so.
func TestLiveSaysItMeasuresColumns(t *testing.T) {
	each(t, func(t *testing.T, srv server) {
		if !open(t, srv, source.Guard{}).Capabilities().Data.ColumnStats {
			t.Error("this connection measures columns and does not say so")
		}
	})
}
