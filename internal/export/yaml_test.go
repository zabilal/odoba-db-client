package export

import (
	"math"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// YAML is a list of records, each value written in the one form that cannot be
// read as something else.
func TestYAMLWritesEachValueSoItCannotBeMisread(t *testing.T) {
	got := export(t, sample(), Options{Format: YAML})
	want := "- id: 1\n" +
		`  name: "say \"hi\", <then> go\nnow"` + "\n" +
		"  big: 12345678901234567890.123456789\n" + // every digit, as a number
		"  f: .nan\n" + // which YAML can say and JSON cannot
		`  at: "2026-09-10T12:30:00Z"` + "\n" +
		`  day: "2026-09-10"` + "\n" +
		`  data: !!binary "3q0="` + "\n" +
		`  doc: {"a":[1,2]}` + "\n" + // what is made of other values, as JSON, which YAML reads
		`  tags: ["x",1.50]` + "\n" +
		"  nothing: null\n" +
		"  id_2: 2\n" // a second column of the same name is still a column
	if got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
}

// Text stays text, whatever it looks like: a string that reads as a number, a
// boolean, a date or nothing is in quotes, and so is a key that would.
func TestYAMLKeepsTextAsText(t *testing.T) {
	cols := []model.ColumnDef{
		col("no", model.TypeString, false), col("plain_name", model.TypeString, false), col("with space", model.TypeString, false),
		col("1st", model.TypeString, false), col("", model.TypeString, false), col("null", model.TypeString, false),
		col("a:b", model.TypeString, false), col("Y", model.TypeString, false),
	}
	got := export(t, &rows{cols: cols, data: []model.Row{{"no", "1.0", "2026-03-04", "~", "", "null", "- x", "# not a comment"}}}, Options{Format: YAML})
	want := `- "no": "no"` + "\n" +
		`  plain_name: "1.0"` + "\n" +
		`  "with space": "2026-03-04"` + "\n" +
		`  "1st": "~"` + "\n" +
		`  "": ""` + "\n" +
		`  "null": "null"` + "\n" +
		`  "a:b": "- x"` + "\n" +
		`  "Y": "# not a comment"` + "\n"
	if got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
}

func TestYAMLWritesNumbersAsNumbers(t *testing.T) {
	cols := []model.ColumnDef{col("v", model.TypeFloat, false)}
	for name, c := range map[string]struct {
		v    any
		want string
	}{
		"an integer":                      {int64(-7), "-7"},
		"a float":                         {1.5, "1.5"},
		"a float that is whole":           {3.0, "3.0"}, // or it reads back as an integer
		"a large float":                   {1e21, "1e+21"},
		"infinity":                        {math.Inf(1), ".inf"},
		"minus infinity":                  {math.Inf(-1), "-.inf"},
		"an exact number":                 {model.Decimal("0.10"), "0.10"},
		"an exact number that is not one": {model.Decimal("NaN"), `"NaN"`},
		"true":                            {true, "true"},
		"a row of values":                 {[]any{int64(1), "a", nil}, `[1,"a",null]`},
		"a document":                      {map[string]any{"k": model.Decimal("1.50")}, `{"k":1.50}`},
		"a document that is not one":      {model.JSON(`{broken`), `"{broken"`},
		"nothing in bytes":                {[]byte{}, `!!binary ""`},
	} {
		got := export(t, &rows{cols: cols, data: []model.Row{{c.v}}}, Options{Format: YAML})
		if got != "- v: "+c.want+"\n" {
			t.Errorf("%s: %q, want %q", name, got, "- v: "+c.want+"\n")
		}
	}
}

// No rows is an empty list, which an empty file is not; a row shorter than its
// columns has nothing in the rest; and a result with no columns still has its
// rows.
func TestYAMLAtItsEdges(t *testing.T) {
	cols := []model.ColumnDef{col("a", model.TypeInteger, false), col("b", model.TypeInteger, false)}
	if got := export(t, &rows{cols: cols}, Options{Format: YAML}); got != "[]\n" {
		t.Errorf("no rows: %q", got)
	}
	if got := export(t, &rows{cols: cols, data: []model.Row{{int64(1)}, {int64(2), int64(3)}}}, Options{Format: YAML}); got != "- a: 1\n  b: null\n- a: 2\n  b: 3\n" {
		t.Errorf("a short row: %q", got)
	}
	if got := export(t, &rows{data: []model.Row{{}, {}}}, Options{Format: YAML}); got != "- {}\n- {}\n" {
		t.Errorf("no columns: %q", got)
	}
}
