package app

import (
	"math"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// Adding up what is selected (FR-3.15).

// Every figure, over a set of numbers.
func TestASetOfNumbersIsAddedUp(t *testing.T) {
	got := Aggregates([]any{int64(1), int64(2), int64(3), int64(4)})
	if got.Cells != 4 || got.Filled != 4 || got.Numbers != 4 {
		t.Errorf("%d cells, %d values, %d numbers", got.Cells, got.Filled, got.Numbers)
	}
	if got.Sum != 10 || got.Mean != 2.5 || got.Min != 1 || got.Max != 4 {
		t.Errorf("sum %v, average %v, smallest %v, largest %v", got.Sum, got.Mean, got.Min, got.Max)
	}
}

// Cells, values and numbers are three different counts, and telling them
// apart is the whole of what makes a total honest.
func TestCellsValuesAndNumbersAreCountedApart(t *testing.T) {
	got := Aggregates([]any{int64(1), nil, "two", int64(3)})
	if got.Cells != 4 {
		t.Errorf("%d cells, want 4", got.Cells)
	}
	if got.Filled != 3 {
		t.Errorf("%d values, want 3: one of them is nothing", got.Filled)
	}
	if got.Numbers != 2 {
		t.Errorf("%d numbers, want 2: one of them is a word", got.Numbers)
	}
	if got.Sum != 4 {
		t.Errorf("sum %v, want the numbers alone", got.Sum)
	}
	if got.Mean != 2 {
		t.Errorf("average %v, want the total over the numbers rather than over the cells", got.Mean)
	}
}

// A set with no numbers in it has no total, rather than a total of nothing.
func TestASetWithNoNumbersHasNoTotal(t *testing.T) {
	got := Aggregates([]any{"a", "b", nil})
	if got.Numbers != 0 {
		t.Errorf("%d numbers", got.Numbers)
	}
	if got.Sum != 0 || got.Mean != 0 || got.Min != 0 || got.Max != 0 {
		t.Errorf("a set of words has sum %v, average %v, smallest %v, largest %v",
			got.Sum, got.Mean, got.Min, got.Max)
	}
	// And nothing at all is nothing at all.
	if got := Aggregates(nil); got.Cells != 0 || got.Numbers != 0 {
		t.Errorf("nothing added up to %+v", got)
	}
}

// Everything an engine counts with is a number here, whatever width it
// arrived in.
func TestWhatCountsAsANumber(t *testing.T) {
	for _, v := range []any{
		int(1), int8(1), int16(1), int32(1), int64(1),
		uint(1), uint8(1), uint16(1), uint32(1), uint64(1),
		float32(1), float64(1), true, model.Decimal("1"),
	} {
		if got := Aggregates([]any{v}); got.Numbers != 1 || got.Sum != 1 {
			t.Errorf("%T(%v) added up to %+v", v, v, got)
		}
	}
	if got := Aggregates([]any{false}); got.Numbers != 1 || got.Sum != 0 {
		t.Errorf("false added up to %+v", got)
	}
	for _, v := range []any{"1", []byte("1"), model.Decimal("not a number"), struct{}{}} {
		if got := Aggregates([]any{v}); got.Numbers != 0 {
			t.Errorf("%T(%v) was counted as a number", v, v)
		}
	}
}

// Neither an infinity nor a not-a-number is added up: a total that came
// back as NaN because one row held one would say nothing about the rest.
func TestWhatIsNotAddedUp(t *testing.T) {
	got := Aggregates([]any{1.0, math.NaN(), math.Inf(1), math.Inf(-1), 2.0})
	if got.Numbers != 2 || got.Sum != 3 {
		t.Errorf("it added up to %+v", got)
	}
	if got.Filled != 5 {
		t.Errorf("%d values, want all five: they are there, they are just not numbers", got.Filled)
	}
}

// A decimal is read from the text it travels in, so that a column of money
// adds up.
func TestADecimalAddsUp(t *testing.T) {
	got := Aggregates([]any{model.Decimal("1.10"), model.Decimal("2.20")})
	if got.Numbers != 2 {
		t.Fatalf("%d numbers", got.Numbers)
	}
	if math.Abs(got.Sum-3.3) > 1e-9 {
		t.Errorf("sum %v, want 3.3", got.Sum)
	}
}

// One number is its own total, average, smallest and largest.
func TestOneNumber(t *testing.T) {
	got := Aggregates([]any{7.5})
	if got.Sum != 7.5 || got.Mean != 7.5 || got.Min != 7.5 || got.Max != 7.5 {
		t.Errorf("one number added up to %+v", got)
	}
}
