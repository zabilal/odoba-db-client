//go:build conformance

package postgres

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// What a column holds (FR-3.14).
//
// The figures are the server's own answers about its own data, so nothing
// here can be settled against a fake. What is claimed is that they are the
// answers to the questions asked — over the rows the filters select, and
// about the column named.

func statsOf(t *testing.T, src *pgSource, column string, class model.TypeClass,
	opt source.BrowseOptions) *source.ColumnStats {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := src.ColumnStats(ctx, ordersRef, model.ColumnDef{Name: column, Type: model.DataType{Class: class}}, opt)
	if err != nil {
		t.Fatalf("ColumnStats(%s): %v", column, err)
	}
	return st
}

// A column of numbers has every figure: how many rows, how many nulls, how
// many different values, the smallest, the largest and the average.
func TestLiveMeasuresAColumnOfNumbers(t *testing.T) {
	src := openSource(t, false)
	got := statsOf(t, src, "total", model.TypeDecimal, source.BrowseOptions{})

	if got.Rows != 2000 {
		t.Errorf("it measured %d rows, want 2000", got.Rows)
	}
	// Every seventh row has no total.
	if want := int64(2000 / 7); got.Nulls != want {
		t.Errorf("%d nulls, want %d", got.Nulls, want)
	}
	if got.Distinct <= 0 || got.Distinct > got.Rows {
		t.Errorf("%d distinct values of %d rows", got.Distinct, got.Rows)
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
}

// A column of text has everything but an average: the average of text is
// nothing at all.
func TestLiveMeasuresAColumnOfText(t *testing.T) {
	src := openSource(t, false)
	got := statsOf(t, src, "status", model.TypeString, source.BrowseOptions{})

	if got.Rows != 2000 || got.Nulls != 0 {
		t.Errorf("%d rows and %d nulls", got.Rows, got.Nulls)
	}
	if got.Distinct != 4 {
		t.Errorf("%d distinct statuses, want 4", got.Distinct)
	}
	if got.Min != "paid" || got.Max != "shipped" {
		t.Errorf("the smallest is %v and the largest %v", got.Min, got.Max)
	}
	if got.HasMean {
		t.Errorf("it averaged a column of text: %v", got.Mean)
	}
}

// A column an engine cannot put in order has the figures it can have, and
// no more.
func TestLiveMeasuresAColumnWithNoOrder(t *testing.T) {
	src := openSource(t, false)
	got := statsOf(t, src, "meta", model.TypeJSON, source.BrowseOptions{})

	if got.Rows != 2000 {
		t.Errorf("%d rows", got.Rows)
	}
	if got.Nulls == 0 {
		t.Error("every row has a meta, and a fifth of them should not")
	}
	if got.Min != nil || got.Max != nil {
		t.Errorf("a JSON column was given a smallest %v and a largest %v", got.Min, got.Max)
	}
	if got.HasMean {
		t.Error("a JSON column was averaged")
	}
}

// The figures are about the rows the filters select, not about the table:
// somebody who narrowed the grid is asking about what is left.
func TestLiveMeasuresOnlyTheRowsTheFiltersSelect(t *testing.T) {
	src := openSource(t, false)
	all := statsOf(t, src, "total", model.TypeDecimal, source.BrowseOptions{})
	paid := statsOf(t, src, "total", model.TypeDecimal, source.BrowseOptions{
		Filters: []source.Filter{{Column: "status", Op: source.OpEqual, Values: []any{"paid"}}},
	})
	if paid.Rows >= all.Rows {
		t.Errorf("filtered to %d rows of %d", paid.Rows, all.Rows)
	}
	if paid.Rows != all.Rows/4 {
		t.Errorf("%d paid of %d, want a quarter", paid.Rows, all.Rows)
	}
	// And a WHERE somebody typed does the same.
	where := statsOf(t, src, "total", model.TypeDecimal, source.BrowseOptions{Where: "total > 400"})
	if where.Rows >= all.Rows || where.Rows == 0 {
		t.Errorf("%d rows over 400 of %d", where.Rows, all.Rows)
	}
}

// A WHERE that could change something is refused before it runs, as it is
// everywhere else a person's own SQL reaches a statement (NFR-S6).
func TestLiveAWhereThatCouldChangeSomethingIsRefused(t *testing.T) {
	src := openSource(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	// The last of these is one condition, balanced, with no semicolon in
	// it — everything the WHERE reader asks for — and the statement it
	// makes still writes. That is what the second reading is for.
	for _, where := range []string{
		"1=1); DROP TABLE ikigai_it.probe_log; --",
		"1=1; DROP TABLE ikigai_it.probe_log",
		"nextval('ikigai_it.orders_id_seq') > 0",
	} {
		_, err := src.ColumnStats(ctx, ordersRef,
			model.ColumnDef{Name: "total", Type: model.DataType{Class: model.TypeDecimal}},
			source.BrowseOptions{Where: where})
		if err == nil {
			t.Fatalf("%q was run", where)
		}
		if !strings.Contains(err.Error(), "WHERE") {
			t.Errorf("%q said %v, which does not name what was wrong", where, err)
		}
	}
	// And the table it was aimed at is still there.
	if got := statsOf(t, src, "total", model.TypeDecimal, source.BrowseOptions{}); got.Rows != 2000 {
		t.Errorf("%d rows after the attempt", got.Rows)
	}
}

// Something that is not a table has no columns to measure.
func TestLiveWhatCannotBeMeasured(t *testing.T) {
	src := openSource(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	// A reference with nothing to read from, and one that names something
	// whole but not something with rows in it.
	for _, tc := range []struct {
		ref  model.ObjectRef
		says string
	}{
		{model.NewRef(model.KindSchema, "ikigai_test", "ikigai_it"), "incomplete"},
		{model.NewRef(model.KindIndex, "ikigai_test", "ikigai_it", "orders_pkey"), "not browsable"},
	} {
		_, err := src.ColumnStats(ctx, tc.ref, model.ColumnDef{Name: "total"}, source.BrowseOptions{})
		if err == nil {
			t.Fatalf("%s was measured", tc.ref)
		}
		if !strings.Contains(err.Error(), tc.says) {
			t.Errorf("%s said %v, which is the server's answer rather than this one", tc.ref, err)
		}
	}
}

// A connection that measures columns says so.
func TestLiveSaysItMeasuresColumns(t *testing.T) {
	if !openSource(t, false).Capabilities().Data.ColumnStats {
		t.Error("this connection measures columns and does not say so")
	}
}
