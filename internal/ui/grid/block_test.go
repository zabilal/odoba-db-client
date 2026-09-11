package grid

import (
	"reflect"
	"testing"
)

func TestAPastedBlockIsReadAsItsShapeSays(t *testing.T) {
	for _, c := range []struct {
		name, text string
		want       [][]string
	}{
		{"nothing", "", nil},
		{"one value", "x", [][]string{{"x"}}},
		{"a line break at the end is none", "x\r\n", [][]string{{"x"}}},
		{"TSV", "1\ta\n2\tb\r\n", [][]string{{"1", "a"}, {"2", "b"}}},
		{"TSV quoted as CSV", "1\t\"two\nlines\"\t\"say \"\"hi\"\"\"", [][]string{{"1", "two\nlines", `say "hi"`}}},
		{"TSV of rows of different lengths", "1\ta\n2", [][]string{{"1", "a"}, {"2"}}},
		{"TSV of one line", "1\ta", [][]string{{"1", "a"}}},
		{"CSV", "1,\"a,b\"\r\n2,c", [][]string{{"1", "a,b"}, {"2", "c"}}},
		{"one line with a comma is one value", "Smith, John", [][]string{{"Smith, John"}}},
		{"lines whose commas differ are values", "a, b\nc", [][]string{{"a, b"}, {"c"}}},
		{"lines whose numbers of commas differ are values", "a,b\nc,d,e", [][]string{{"a,b"}, {"c,d,e"}}},
		{"a quote inside a TSV value", "5\" screen\tx", [][]string{{`5" screen`, "x"}}},
		{"lines, one blank", "a\n\nb", [][]string{{"a"}, {""}, {"b"}}},
	} {
		if got := ParseBlock(c.text); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}
