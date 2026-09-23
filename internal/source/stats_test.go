package source

import (
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// Deciding what can be measured about a column (FR-3.14).
//
// The judgement is the same on every engine — an average of text is nothing
// — so it is made once and each driver renders it in its own quoting.

func def(class model.TypeClass) model.ColumnDef {
	return model.ColumnDef{Name: "c", Type: model.DataType{Class: class}}
}

// Every column is counted, whatever it holds: how many rows there are and
// how many of them hold something is what a panel is opened for.
func TestEveryColumnIsCounted(t *testing.T) {
	for _, class := range []model.TypeClass{
		model.TypeInteger, model.TypeString, model.TypeJSON, model.TypeBytes, model.TypeGeometry,
	} {
		q := StatsFor(`"c"`, def(class))
		if len(q.Select) < 2 || q.Select[0] != "count(*)" || q.Select[1] != `count("c")` {
			t.Errorf("%v asks for %v", class, q.Select)
		}
	}
}

// A column of numbers is asked everything.
func TestAColumnOfNumbersIsAskedEverything(t *testing.T) {
	q := StatsFor(`"c"`, def(model.TypeInteger))
	if !q.Distinct || !q.Extremes || !q.Mean {
		t.Errorf("a column of numbers: distinct %v, extremes %v, mean %v", q.Distinct, q.Extremes, q.Mean)
	}
	sql := q.SQL()
	for _, want := range []string{`count(DISTINCT "c")`, `min("c")`, `max("c")`, `avg("c")`} {
		if !strings.Contains(sql, want) {
			t.Errorf("it asks %q, without %q", sql, want)
		}
	}
}

// A column of text has a smallest and a largest and no average.
func TestAColumnOfTextIsNotAveraged(t *testing.T) {
	q := StatsFor(`"c"`, def(model.TypeString))
	if !q.Extremes || q.Mean {
		t.Errorf("a column of text: extremes %v, mean %v", q.Extremes, q.Mean)
	}
	if strings.Contains(q.SQL(), "avg(") {
		t.Errorf("it asks %q", q.SQL())
	}
}

// A column an engine has no order for is counted and nothing more.
func TestAColumnWithNoOrder(t *testing.T) {
	q := StatsFor(`"c"`, def(model.TypeJSON))
	if q.Extremes || q.Mean {
		t.Errorf("a JSON column: extremes %v, mean %v", q.Extremes, q.Mean)
	}
	if !q.Distinct {
		t.Error("a JSON column's different values can still be counted")
	}
}

// A column of pictures is not counted by its different values: that is a
// long way to no answer.
func TestAColumnOfBytesIsNotCountedByValue(t *testing.T) {
	for _, class := range []model.TypeClass{model.TypeBytes, model.TypeGeometry} {
		if q := StatsFor(`"c"`, def(class)); q.Distinct {
			t.Errorf("%v counted its different values", class)
		}
	}
}

// A row of answers is read back in the order it was asked for.
func TestARowOfAnswersIsReadBack(t *testing.T) {
	q := StatsFor(`"c"`, def(model.TypeInteger))
	got, err := q.Read(model.Row{int64(100), int64(80), int64(40), int64(1), int64(99), 42.5})
	if err != nil {
		t.Fatal(err)
	}
	if got.Rows != 100 || got.Nulls != 20 {
		t.Errorf("%d rows and %d nulls, want 100 and 20", got.Rows, got.Nulls)
	}
	if got.Distinct != 40 {
		t.Errorf("%d distinct", got.Distinct)
	}
	if got.Min != int64(1) || got.Max != int64(99) {
		t.Errorf("the smallest is %v and the largest %v", got.Min, got.Max)
	}
	if !got.HasMean || got.Mean != 42.5 {
		t.Errorf("the average is %v (measured: %v)", got.Mean, got.HasMean)
	}
}

// A column that was asked less has its answers read from where they are.
func TestFewerAnswersAreReadFromWhereTheyAre(t *testing.T) {
	q := StatsFor(`"c"`, def(model.TypeJSON))
	got, err := q.Read(model.Row{int64(10), int64(4), int64(3)})
	if err != nil {
		t.Fatal(err)
	}
	if got.Rows != 10 || got.Nulls != 6 || got.Distinct != 3 {
		t.Errorf("read %+v", got)
	}
	if got.Min != nil || got.Max != nil || got.HasMean {
		t.Errorf("a column that was not asked was answered: %+v", got)
	}
}

// A column that was asked nothing but its counts answers nothing else, and
// says so rather than saying none.
func TestAColumnAskedOnlyItsCounts(t *testing.T) {
	q := StatsFor(`"c"`, def(model.TypeBytes))
	if len(q.Select) != 2 {
		t.Fatalf("a column of bytes is asked %v", q.Select)
	}
	got, err := q.Read(model.Row{int64(10), int64(4)})
	if err != nil {
		t.Fatal(err)
	}
	if got.Distinct != -1 {
		t.Errorf("it counted %d different values without being asked", got.Distinct)
	}
	if got.Min != nil || got.Max != nil || got.HasMean {
		t.Errorf("it answered %+v", got)
	}
}

// A row of the wrong width is a defect rather than a datum.
func TestARowOfTheWrongWidth(t *testing.T) {
	q := StatsFor(`"c"`, def(model.TypeInteger))
	if _, err := q.Read(model.Row{int64(1), int64(2)}); err == nil {
		t.Error("a short row was read as a column's figures")
	}
	if _, err := q.Read(nil); err == nil {
		t.Error("no row at all was read as a column's figures")
	}
	// And a count that is not a number is not a count.
	if _, err := q.Read(model.Row{"many", int64(2), int64(1), 1, 2, 3.0}); err == nil {
		t.Error("a count of \"many\" was read")
	}
	if _, err := q.Read(model.Row{int64(1), "some", int64(1), 1, 2, 3.0}); err == nil {
		t.Error("a count of \"some\" was read")
	}
}

// A count comes back in whatever width the driver gave it, and an average
// in whatever a decimal travels as.
func TestFiguresAreReadWhateverShapeTheyArriveIn(t *testing.T) {
	for _, v := range []any{int64(7), int32(7), 7, uint64(7), 7.0, model.Decimal("7")} {
		if got, ok := Int64(v); !ok || got != 7 {
			t.Errorf("%T(%v) read as %d, %v", v, v, got, ok)
		}
	}
	if _, ok := Int64("7"); ok {
		t.Error("a count read from text")
	}
	for _, v := range []any{7.5, float32(7.5), model.Decimal("7.5")} {
		if got, ok := Float64(v); !ok || got != 7.5 {
			t.Errorf("%T(%v) read as %v, %v", v, v, got, ok)
		}
	}
	if got, ok := Float64(int64(7)); !ok || got != 7 {
		t.Errorf("a whole number read as %v, %v", got, ok)
	}
	for _, v := range []any{"7.5", nil, model.Decimal("not a number")} {
		if _, ok := Float64(v); ok {
			t.Errorf("%T(%v) read as an average", v, v)
		}
	}
}
