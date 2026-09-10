package filterexpr

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

var (
	text    = model.DataType{Class: model.TypeString}
	integer = model.DataType{Class: model.TypeInteger}
	decimal = model.DataType{Class: model.TypeDecimal}
	boolean = model.DataType{Class: model.TypeBool}
	date    = model.DataType{Class: model.TypeDate}
)

func f(op source.FilterOp, vals ...any) source.Filter {
	return source.Filter{Column: "c", Op: op, Values: vals}
}

func TestParse(t *testing.T) {
	for _, c := range []struct {
		text string
		typ  model.DataType
		want source.Filter
	}{
		{"smith", text, f(source.OpContains, "smith")},
		{"42", integer, f(source.OpEqual, int64(42))},
		{"2026-01-01", date, f(source.OpEqual, "2026-01-01")},
		{"=smith", text, f(source.OpEqual, "smith")},
		{"!=x", text, f(source.OpNotIn, "x")},
		{"<> x", text, f(source.OpNotIn, "x")},
		{">100", integer, f(source.OpGreater, int64(100))},
		{">= 1.50", decimal, f(source.OpGreaterEqual, "1.50")},
		{"<2026-06-01", date, f(source.OpLess, "2026-06-01")},
		{"a,b, c", text, f(source.OpIn, "a", "b", "c")},
		{"1,2,NULL", integer, f(source.OpIn, int64(1), int64(2), nil)},
		{"!a,b", text, f(source.OpNotIn, "a", "b")},
		{"!x", text, f(source.OpNotIn, "x")},
		{"10..20", integer, f(source.OpBetween, int64(10), int64(20))},
		{"-5..5", integer, f(source.OpBetween, int64(-5), int64(5))},
		{"~^A.*z$", text, f(source.OpRegex, "^A.*z$")},
		{"NULL", integer, f(source.OpIsNull)},
		{"null", text, f(source.OpIsNull)},
		{"!NULL", text, f(source.OpIsNotNull)},
		{"= NULL", text, f(source.OpIsNull)},
		{"!= null", text, f(source.OpIsNotNull)},
		{"ab%", text, f(source.OpLike, "ab%")},
		{"user_id", text, f(source.OpContains, "user_id")},
		{`"NULL"`, text, f(source.OpContains, "NULL")},
		{`"a,b"`, text, f(source.OpContains, "a,b")},
		{`"x",'y,z'`, text, f(source.OpIn, "x", "y,z")},
		{`="=x"`, text, f(source.OpEqual, "=x")},
		{"yes", boolean, f(source.OpEqual, true)},
	} {
		got, err := Parse("c", c.text, c.typ)
		if err != nil || len(got) != 1 || !reflect.DeepEqual(got[0], c.want) {
			t.Errorf("%q: %+v, %v; want %+v", c.text, got, err, c.want)
		}
	}
	neg, _ := Parse("c", "!~tmp", text)
	if len(neg) != 1 || neg[0].Op != source.OpRegex || !neg[0].Negate {
		t.Errorf("!~ should be a negated regex: %+v", neg)
	}
}

func TestEmptyIsNoFilter(t *testing.T) {
	if got, err := Parse("c", "   ", text); got != nil || err != nil {
		t.Errorf("%+v, %v", got, err)
	}
}

func TestMistakesAreExplained(t *testing.T) {
	for _, c := range []struct {
		text, want string
		typ        model.DataType
	}{
		{">abc", "not a whole number", integer},
		{"1.5", "not a whole number", integer},
		{">", "needs a value", text},
		{"~", "needs a pattern", text},
		{"> NULL", "nothing is greater", text},
		{"maybe", "not true or false", boolean},
		{"1e", "not a number", decimal},
		{"!", "needs a value", text},
	} {
		_, err := Parse("c", c.text, c.typ)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: %v, want an error saying %q", c.text, err, c.want)
		}
	}
}

func TestPickReadsBackAsWhatWasPicked(t *testing.T) {
	for _, c := range []struct {
		vals   []any
		negate bool
		typ    model.DataType
		text   string
		want   source.Filter
	}{
		{[]any{"a"}, false, text, "=a", f(source.OpEqual, "a")},
		{[]any{"a", "b,c", nil}, false, text, `a,"b,c",NULL`, f(source.OpIn, "a", "b,c", nil)},
		{[]any{"a,b", "c,d"}, false, text, `"a,b","c,d"`, f(source.OpIn, "a,b", "c,d")},
		{[]any{"NULL"}, false, text, `="NULL"`, f(source.OpEqual, "NULL")},
		{[]any{" x", `say "hi"`}, false, text, `" x",'say "hi"'`, f(source.OpIn, " x", `say "hi"`)},
		{[]any{"a,b"}, true, text, `!"a,b"`, f(source.OpNotIn, "a,b")},
		{[]any{"x", ">y"}, true, text, `!x,">y"`, f(source.OpNotIn, "x", ">y")},
		{[]any{nil}, true, text, "!NULL", f(source.OpIsNotNull)},
		{[]any{int64(1), int64(2)}, false, integer, "1,2", f(source.OpIn, int64(1), int64(2))},
		{[]any{time.Date(2000, 1, 2, 0, 0, 0, 0, time.UTC)}, false, date, "=2000-01-02", f(source.OpEqual, "2000-01-02")},
	} {
		got := Pick(c.vals, c.negate)
		if got != c.text {
			t.Errorf("Pick(%q, %v) = %q, want %q", c.vals, c.negate, got, c.text)
			continue
		}
		back, err := Parse("c", got, c.typ)
		if err != nil || len(back) != 1 || !reflect.DeepEqual(back[0], c.want) {
			t.Errorf("%q reads back as %+v, %v; want %+v", got, back, err, c.want)
		}
	}
	if Pick(nil, false) != "" {
		t.Error("nothing picked is no text")
	}
}
