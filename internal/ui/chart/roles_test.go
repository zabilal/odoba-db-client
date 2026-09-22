package chart

import (
	"image/color"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// Working out which columns are the axes (FR-11.2).

func col(name string, class model.TypeClass) model.ColumnDef {
	return model.ColumnDef{Name: name, Type: model.DataType{Class: class, Length: -1}}
}

func rows(vals ...[]any) []model.Row {
	out := make([]model.Row, len(vals))
	for i, v := range vals {
		out[i] = model.Row(v)
	}
	return out
}

func day(n int) time.Time { return time.Date(2026, 1, n, 0, 0, 0, 0, time.UTC) }

// A moment in time is the most likely X there is: a result ordered by one is
// almost always a result about change.
func TestATimeIsTheAxisAlongTheBottom(t *testing.T) {
	cols := []model.ColumnDef{col("when", model.TypeTimestamp), col("total", model.TypeInteger)}
	got := Guess(cols, rows([]any{day(1), 5}, []any{day(2), 7}))
	if got.X != 0 {
		t.Errorf("the axis is column %d", got.X)
	}
	if !slices.Equal(got.Y, []int{1}) {
		t.Errorf("it draws %v", got.Y)
	}
	if got.Series != RowNumber {
		t.Errorf("it split the chart by column %d", got.Series)
	}
}

// Failing a time, a column of labels: the categories of a bar chart.
func TestLabelsAreTheAxisWhereThereIsNoTime(t *testing.T) {
	cols := []model.ColumnDef{col("country", model.TypeString), col("sales", model.TypeDecimal)}
	got := Guess(cols, rows([]any{"uk", model.Decimal("5")}, []any{"fr", model.Decimal("7")}))
	if got.X != 0 || !slices.Equal(got.Y, []int{1}) {
		t.Errorf("it read %+v", got)
	}
	// The column along the bottom is not also the one that splits the
	// chart: a category cannot be both where a bar stands and which bar
	// it is.
	if got.Series != RowNumber {
		t.Errorf("the axis column also splits the chart, as %d", got.Series)
	}
}

// A column of few repeated labels splits the numbers into series, and is
// never the same column as the axis.
func TestAFewRepeatedLabelsSplitTheChart(t *testing.T) {
	cols := []model.ColumnDef{
		col("when", model.TypeTimestamp), col("country", model.TypeString), col("sales", model.TypeInteger)}
	got := Guess(cols, rows(
		[]any{day(1), "uk", 5}, []any{day(1), "fr", 6},
		[]any{day(2), "uk", 7}, []any{day(2), "fr", 8}))
	if got.X != 0 || got.Series != 1 || !slices.Equal(got.Y, []int{2}) {
		t.Errorf("it read %+v", got)
	}
}

// A column with a different value in every row is an identifier, not a
// category. A chart split fifty ways is a chart of nothing.
func TestAColumnOfIdentifiersIsNotACategory(t *testing.T) {
	cols := []model.ColumnDef{col("id", model.TypeString), col("total", model.TypeInteger)}
	var rs []model.Row
	for i := 0; i < 50; i++ {
		rs = append(rs, model.Row{string(rune('a'+i%26)) + string(rune('a'+i/26)), i})
	}
	got := Guess(cols, rs)
	if got.Series != RowNumber {
		t.Errorf("it split the chart by column %d", got.Series)
	}
	if got.X != RowNumber {
		t.Errorf("it put identifiers along the bottom, as column %d", got.X)
	}
	if !slices.Equal(got.Y, []int{1}) {
		t.Errorf("it draws %v", got.Y)
	}
}

// A result of one numeric column is that column against its row number.
func TestOneColumnOfNumbers(t *testing.T) {
	got := Guess([]model.ColumnDef{col("n", model.TypeFloat)}, rows([]any{1.5}, []any{2.5}))
	if got.X != RowNumber || !slices.Equal(got.Y, []int{0}) {
		t.Errorf("it read %+v", got)
	}
	// And a column of numbers is never taken for the axis, whatever else is
	// beside it: an axis comes from a column that orders or one that
	// labels, and a number is neither.
	cols := []model.ColumnDef{col("a", model.TypeInteger), col("b", model.TypeInteger)}
	if got := Guess(cols, rows([]any{1, 2})); got.X != RowNumber || !slices.Equal(got.Y, []int{0, 1}) {
		t.Errorf("two numeric columns read as %+v", got)
	}
}

// What cannot be drawn is left out rather than pressed into a role it does
// not fit.
func TestWhatIsNeverDrawn(t *testing.T) {
	for _, class := range []model.TypeClass{
		model.TypeUUID, model.TypeBytes, model.TypeJSON, model.TypeXML,
		model.TypeGeometry, model.TypeNetwork, model.TypeBit, model.TypeArray,
		model.TypeStruct, model.TypeInterval,
	} {
		if Plottable(col("x", class)) {
			t.Errorf("a %v is drawn as a number", class)
		}
	}
	for _, class := range []model.TypeClass{model.TypeInteger, model.TypeFloat, model.TypeDecimal} {
		if !Plottable(col("x", class)) {
			t.Errorf("a %v is not drawn as a number", class)
		}
	}
	// A result of nothing drawable draws nothing.
	got := Guess([]model.ColumnDef{col("id", model.TypeUUID)}, rows([]any{"a"}))
	if len(got.Y) != 0 {
		t.Errorf("it draws %v", got.Y)
	}
}

// A chart is drawn in pixels, so a value is read as the number it is — and
// what is not a number says so rather than being drawn as zero.
func TestWhatReadsAsANumber(t *testing.T) {
	for _, c := range []struct {
		in   any
		want float64
	}{
		{int64(5), 5}, {int32(5), 5}, {int(5), 5}, {uint8(5), 5},
		{float32(1.5), 1.5}, {float64(1.5), 1.5},
		{model.Decimal("1.25"), 1.25},
		{true, 1}, {false, 0},
		{day(1), float64(day(1).UnixNano()) / 1e9},
	} {
		got, ok := Number(c.in)
		if !ok || got != c.want {
			t.Errorf("%v (%T) read as %v, %v; want %v", c.in, c.in, got, ok, c.want)
		}
	}
	for _, in := range []any{nil, "five", model.Decimal("five"), []byte{1}, struct{}{}} {
		if _, ok := Number(in); ok {
			t.Errorf("%v (%T) read as a number", in, in)
		}
	}
}

// Neither infinity nor a not-a-number is a place on an axis, and drawing one
// would move every other point to make room for it.
func TestWhatIsNotAPlaceOnAnAxis(t *testing.T) {
	for _, in := range []float64{math.Inf(1), math.Inf(-1), math.NaN()} {
		if _, ok := Number(in); ok {
			t.Errorf("%v read as a number", in)
		}
	}
}

// A cell is named the way somebody would read it, and a null is named
// rather than left as a gap in a legend.
func TestWhatACellIsCalled(t *testing.T) {
	for _, c := range []struct{ in, want any }{
		{nil, "(null)"},
		{"uk", "uk"},
		{model.Decimal("1.25"), "1.25"},
		{true, "true"},
		{int64(5), "5"},
	} {
		if got := label(c.in); got != c.want {
			t.Errorf("%v (%T) is called %q, want %q", c.in, c.in, got, c.want)
		}
	}
	if got := label(day(1)); got == "" {
		t.Error("a moment has no name")
	}
}

// Reading a result into series.

// A row whose value cannot be read is left out, and how many were left out
// is said: a chart drawn over gaps nobody was told about lies by omission.
func TestRowsThatCannotBeDrawnAreCounted(t *testing.T) {
	cols := []model.ColumnDef{col("when", model.TypeTimestamp), col("total", model.TypeInteger)}
	got := Build(cols, rows(
		[]any{day(1), 5}, []any{day(2), nil}, []any{day(3), 7}),
		Roles{X: 0, Y: []int{1}, Series: RowNumber})

	if got.Skipped != 1 {
		t.Errorf("it left out %d rows", got.Skipped)
	}
	if len(got.Series) != 1 || len(got.Series[0].Points) != 2 {
		t.Errorf("it read %+v", got.Series)
	}
	// And every point still knows which row it came from.
	if got.Series[0].Points[1].Index != 2 {
		t.Errorf("the second point came from row %d", got.Series[0].Points[1].Index)
	}
}

// A category is given a position, because an axis is a line and a word is
// not a place on one — and the words come back to be drawn instead.
func TestCategoriesBecomePositionsAndKeepTheirNames(t *testing.T) {
	cols := []model.ColumnDef{col("country", model.TypeString), col("sales", model.TypeInteger)}
	got := Build(cols, rows([]any{"uk", 5}, []any{"fr", 7}, []any{"uk", 9}),
		Roles{X: 0, Y: []int{1}, Series: RowNumber})

	if !slices.Equal(got.Labels, []string{"uk", "fr"}) {
		t.Errorf("the axis is labelled %v", got.Labels)
	}
	xs := []float64{}
	for _, p := range got.Series[0].Points {
		xs = append(xs, p.X)
	}
	if !slices.Equal(xs, []float64{0, 1, 0}) {
		t.Errorf("the rows are at %v", xs)
	}
}

// A numeric axis keeps its numbers and needs no labels.
func TestANumericAxisIsNotLabelled(t *testing.T) {
	cols := []model.ColumnDef{col("x", model.TypeInteger), col("y", model.TypeInteger)}
	got := Build(cols, rows([]any{10, 1}, []any{20, 2}),
		Roles{X: 0, Y: []int{1}, Series: RowNumber})
	if got.Labels != nil {
		t.Errorf("it labelled the axis %v", got.Labels)
	}
	if got.Series[0].Points[1].X != 20 {
		t.Errorf("the second point is at %v", got.Series[0].Points[1].X)
	}
}

// A result with one row per category per day becomes one series per
// category, in the order they first appear.
func TestOneSeriesPerCategory(t *testing.T) {
	cols := []model.ColumnDef{
		col("when", model.TypeTimestamp), col("country", model.TypeString), col("sales", model.TypeInteger)}
	got := Build(cols, rows(
		[]any{day(1), "uk", 5}, []any{day(1), "fr", 6},
		[]any{day(2), "uk", 7}, []any{day(2), "fr", 8}),
		Roles{X: 0, Y: []int{2}, Series: 1})

	if len(got.Series) != 2 {
		t.Fatalf("it made %d series", len(got.Series))
	}
	if got.Series[0].Name != "uk" || got.Series[1].Name != "fr" {
		t.Errorf("they are called %q and %q", got.Series[0].Name, got.Series[1].Name)
	}
	for _, s := range got.Series {
		if len(s.Points) != 2 {
			t.Errorf("%s has %d points", s.Name, len(s.Points))
		}
	}
}

// A series is named after the column it was drawn from, so a legend says
// what a line is.
func TestASeriesIsNamedAfterItsColumn(t *testing.T) {
	cols := []model.ColumnDef{col("x", model.TypeInteger),
		col("sales", model.TypeInteger), col("returns", model.TypeInteger)}
	got := Build(cols, rows([]any{1, 5, 2}), Roles{X: 0, Y: []int{1, 2}, Series: RowNumber})
	if len(got.Series) != 2 || got.Series[0].Name != "sales" || got.Series[1].Name != "returns" {
		t.Errorf("it made %+v", got.Series)
	}
}

// A line drawn in the order rows arrived zigzags wherever the result was not
// sorted, which reads as data going backwards in time.
func TestALineIsDrawnInOrderAlongItsAxis(t *testing.T) {
	s := Series{Name: "a", Points: []Point{{X: 3, Y: 1}, {X: 1, Y: 2}, {X: 2, Y: 3}}}
	got := Sorted(s)
	var xs []float64
	for _, p := range got.Points {
		xs = append(xs, p.X)
	}
	if !slices.Equal(xs, []float64{1, 2, 3}) {
		t.Errorf("it is at %v", xs)
	}
	// And what it was given is left alone: a bar chart's order is the order
	// somebody asked for.
	if s.Points[0].X != 3 {
		t.Error("sorting changed the series it was given")
	}
}

// One column read as the numbers a histogram counts, with what cannot be
// read left out.
func TestReadingOneColumnOfValues(t *testing.T) {
	got := Values(rows([]any{1}, []any{nil}, []any{3}, []any{"x"}), 0)
	if !slices.Equal(got, []float64{1, 3}) {
		t.Errorf("it read %v", got)
	}
}

// A row shorter than the columns say is not a panic.
func TestARowWithFewerValuesThanColumns(t *testing.T) {
	cols := []model.ColumnDef{col("x", model.TypeInteger), col("y", model.TypeInteger)}
	got := Build(cols, rows([]any{1}), Roles{X: 0, Y: []int{1}, Series: RowNumber})
	if got.Skipped != 1 || len(got.Series[0].Points) != 0 {
		t.Errorf("it read %+v, skipping %d", got.Series, got.Skipped)
	}
}

// Naming the kinds, and which one a result suits.

func TestEveryKindHasAName(t *testing.T) {
	seen := map[string]Kind{}
	for _, k := range Kinds {
		name := KindName(k)
		if name == "" || name == string(k) {
			t.Errorf("%s is called %q, which is not a name for a window", k, name)
		}
		if was, twice := seen[name]; twice {
			t.Errorf("%s and %s are both called %q", was, k, name)
		}
		seen[name] = k
		if got := KindNamed(name); got != k {
			t.Errorf("KindNamed(%q) = %s, want %s", name, got, k)
		}
	}
	if got := KindNamed("something nobody offered"); got != Line {
		t.Errorf("an unknown name gave %s, want a line, which draws anything", got)
	}
}

// A moment along the bottom means change over time, which is a line; a
// column of categories means comparison, which is bars.
func TestTheKindAResultSuits(t *testing.T) {
	cases := []struct {
		name string
		cols []model.ColumnDef
		rows []model.Row
		want Kind
	}{
		{"a moment and a number", []model.ColumnDef{col("at", model.TypeTimestamp), col("n", model.TypeInteger)},
			rows([]any{day(1), 1}, []any{day(2), 2}), Line},
		{"categories and a number", []model.ColumnDef{col("region", model.TypeString), col("n", model.TypeInteger)},
			rows([]any{"north", 1}, []any{"south", 2}), Bar},
		{"one column of numbers", []model.ColumnDef{col("latency", model.TypeFloat)},
			rows([]any{1.0}, []any{2.0}), Histogram},
		{"two columns of numbers", []model.ColumnDef{col("a", model.TypeInteger), col("b", model.TypeInteger)},
			rows([]any{1, 2}, []any{3, 4}), Line},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := Guess(tc.cols, tc.rows)
			if got := Suits(tc.cols, tc.rows, r); got != tc.want {
				t.Errorf("Suits = %s, want %s (roles %+v)", got, tc.want, r)
			}
		})
	}
}

// A numeric X is a scatter: neither a line through unordered rows nor bars
// at arbitrary positions says anything the data did not.
func TestANumericAxisSuitsAScatter(t *testing.T) {
	cols := []model.ColumnDef{col("x", model.TypeFloat), col("y", model.TypeFloat)}
	if got := Suits(cols, rows([]any{1.0, 2.0}), Roles{X: 0, Y: []int{1}, Series: RowNumber}); got != Scatter {
		t.Errorf("Suits = %s, want a scatter", got)
	}
}

// The axis is named after its column, and after nothing where no column
// fills the role.
func TestAnAxisIsNamedAfterItsColumn(t *testing.T) {
	cols := []model.ColumnDef{col("at", model.TypeTimestamp), col("n", model.TypeInteger)}
	if got := ColumnName(cols, 1); got != "n" {
		t.Errorf("ColumnName = %q, want n", got)
	}
	for _, i := range []int{RowNumber, -5, 2} {
		if got := ColumnName(cols, i); got != "" {
			t.Errorf("ColumnName(%d) = %q, want nothing", i, got)
		}
	}
	if got := SeriesTitle([]Series{{Name: "orders"}}); got != "orders" {
		t.Errorf("SeriesTitle = %q, want the one series' name", got)
	}
	if got := SeriesTitle([]Series{{Name: "a"}, {Name: "b"}}); got != "" {
		t.Errorf("SeriesTitle = %q; with several, only the key can say which is which", got)
	}
}

// A moment is a moment whatever kind of column held it.
func TestWhatCountsAsAMoment(t *testing.T) {
	for _, class := range []model.TypeClass{model.TypeDate, model.TypeTime, model.TypeTimestamp} {
		if !Momentary(col("c", class)) {
			t.Errorf("%v is not read as a moment", class)
		}
	}
	for _, class := range []model.TypeClass{model.TypeInteger, model.TypeString, model.TypeFloat} {
		if Momentary(col("c", class)) {
			t.Errorf("%v is read as a moment", class)
		}
	}
}

// Colour is never the only thing telling two series apart, but where it is
// used it has to work on both backgrounds.
func TestTheSeriesColoursDifferAndSuitBothBackgrounds(t *testing.T) {
	for _, dark := range []bool{false, true} {
		set := SeriesColours(dark)
		if len(set) < 8 {
			t.Errorf("dark=%v: %d colours, want enough for a result's worth of series", dark, len(set))
		}
		seen := map[color.NRGBA]bool{}
		for _, c := range set {
			if seen[c] {
				t.Errorf("dark=%v: %v is used twice", dark, c)
			}
			seen[c] = true
			if c.A != 0xFF {
				t.Errorf("dark=%v: %v is part-transparent", dark, c)
			}
			l := luminance(c)
			if dark && l < 0.15 {
				t.Errorf("%v is too dark to see on a dark window", c)
			}
			if !dark && l > 0.75 {
				t.Errorf("%v is too pale to see on a light window", c)
			}
		}
	}
}

// luminance is the relative luminance WCAG measures contrast with.
func luminance(c color.NRGBA) float64 {
	f := func(v uint8) float64 {
		s := float64(v) / 255
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*f(c.R) + 0.7152*f(c.G) + 0.0722*f(c.B)
}

// Whether the bottom axis is made of moments is what decides how it is
// written, and is asked of the column rather than of the numbers in it.
func TestWhetherTheAxisIsMadeOfMoments(t *testing.T) {
	cols := []model.ColumnDef{col("at", model.TypeTimestamp), col("n", model.TypeInteger)}
	if !TimesAlong(cols, 0) {
		t.Error("an axis of timestamps is not read as moments")
	}
	for _, x := range []int{1, RowNumber, -3, 2} {
		if TimesAlong(cols, x) {
			t.Errorf("column %d is read as moments", x)
		}
	}
}
