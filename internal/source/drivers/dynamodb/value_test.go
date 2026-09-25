package dynamodb

import (
	"testing"

	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// DynamoDB's nine kinds of value, and what each becomes. Two of them decide
// most of what the driver does with data, and both are here: a number arrives
// as text and holds more digits than a float64, and the null is a value rather
// than the absence of one.

func s(v string) ddbtypes.AttributeValue  { return &ddbtypes.AttributeValueMemberS{Value: v} }
func n(v string) ddbtypes.AttributeValue  { return &ddbtypes.AttributeValueMemberN{Value: v} }
func b(v ...byte) ddbtypes.AttributeValue { return &ddbtypes.AttributeValueMemberB{Value: v} }

func TestWhatAnAttributeHolds(t *testing.T) {
	for name, c := range map[string]struct {
		av   ddbtypes.AttributeValue
		want any
	}{
		"a string":          {s("hello"), "hello"},
		"an empty string":   {s(""), ""},
		"a whole number":    {n("42"), int64(42)},
		"a negative number": {n("-7"), int64(-7)},
		"a fraction":        {n("4.5"), model.Decimal("4.5")},
		"a number too big for an int64": {n("123456789012345678901234567890"),
			model.Decimal("123456789012345678901234567890")},
		// The one that matters: 38 digits is more than a float64 holds, and a
		// value shown wrong in its last place is worse than one shown as text.
		"a number of many digits": {n("1.00000000000000000000000000000000000001"),
			model.Decimal("1.00000000000000000000000000000000000001")},
		"true":         {&ddbtypes.AttributeValueMemberBOOL{Value: true}, true},
		"false":        {&ddbtypes.AttributeValueMemberBOOL{Value: false}, false},
		"the null":     {&ddbtypes.AttributeValueMemberNULL{Value: true}, nil},
		"a map":        {&ddbtypes.AttributeValueMemberM{Value: map[string]ddbtypes.AttributeValue{"i": n("2")}}, model.JSON(`{"i":2}`)},
		"a list":       {&ddbtypes.AttributeValueMemberL{Value: []ddbtypes.AttributeValue{s("x"), n("1")}}, model.JSON(`["x",1]`)},
		"a string set": {&ddbtypes.AttributeValueMemberSS{Value: []string{"b", "a"}}, model.JSON(`["a","b"]`)},
		"a number set": {&ddbtypes.AttributeValueMemberNS{Value: []string{"2", "1"}}, model.JSON(`[1,2]`)},
	} {
		t.Run(name, func(t *testing.T) {
			got := valueOf(c.av)
			if fmtOf(got) != fmtOf(c.want) {
				t.Errorf("it reads as %#v (%T), want %#v", got, got, c.want)
			}
		})
	}
	// Bytes are bytes, and are copied: the SDK's buffer is not the caller's.
	got := valueOf(b(0xff, 0x00))
	raw, ok := got.([]byte)
	if !ok || len(raw) != 2 || raw[0] != 0xff {
		t.Errorf("the bytes read as %#v", got)
	}
	// A set of bytes has no shape a cell can hold but base64 in an array.
	if j := valueOf(&ddbtypes.AttributeValueMemberBS{Value: [][]byte{{0x01}, {0x00}}}); !sameJSON(j, `["AA==","AQ=="]`) {
		t.Errorf("a set of bytes reads as %v", j)
	}
}

// sameJSON reports whether a value is the JSON given. model.JSON is bytes, so
// it is compared as text.
func sameJSON(v any, want string) bool {
	j, ok := v.(model.JSON)
	return ok && string(j) == want
}

func fmtOf(v any) string {
	if v == nil {
		return "<nil>"
	}
	switch x := v.(type) {
	case []byte:
		return string(x)
	}
	return string(mustText(v))
}

func mustText(v any) []byte {
	switch x := v.(type) {
	case string:
		return []byte("S:" + x)
	case int64:
		return []byte("I:" + itoa(x))
	case bool:
		if x {
			return []byte("B:true")
		}
		return []byte("B:false")
	case model.Decimal:
		return []byte("D:" + string(x))
	case model.JSON:
		return []byte("J:" + string(x))
	}
	return []byte("?")
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	if neg {
		return "-" + string(d)
	}
	return string(d)
}

// A map's keys are written in order, because a map has none of its own and an
// order that changed between two reads of the same item would read as a change.
func TestAMapReadsTheSameWayTwice(t *testing.T) {
	av := &ddbtypes.AttributeValueMemberM{Value: map[string]ddbtypes.AttributeValue{
		"z": s("last"), "a": s("first"), "m": s("middle"),
	}}
	const want = `{"a":"first","m":"middle","z":"last"}`
	for i := 0; i < 20; i++ {
		if got := valueOf(av); !sameJSON(got, want) {
			t.Fatalf("read %d gives %s", i, got)
		}
	}
}

// A nested map reads as the value it holds, not as DynamoDB's wire encoding:
// the type tags say how it travelled, not what it is.
func TestANestedValueLosesTheWireTags(t *testing.T) {
	av := &ddbtypes.AttributeValueMemberM{Value: map[string]ddbtypes.AttributeValue{
		"inner": &ddbtypes.AttributeValueMemberL{Value: []ddbtypes.AttributeValue{
			&ddbtypes.AttributeValueMemberM{Value: map[string]ddbtypes.AttributeValue{"n": n("1")}},
			&ddbtypes.AttributeValueMemberNULL{Value: true},
			b(0xff),
		}},
	}}
	const want = `{"inner":[{"n":1},null,"/w=="]}`
	if got := valueOf(av); !sameJSON(got, want) {
		t.Errorf("it reads as %s", got)
	}
}

// Text that would break JSON is escaped, so that a cell holding a quote is
// still JSON a reader takes.
func TestTextIsEscapedAsJSON(t *testing.T) {
	av := &ddbtypes.AttributeValueMemberM{Value: map[string]ddbtypes.AttributeValue{
		`a"b`: s("line\nbreak\ttab\\slash\"quote"),
	}}
	const want = `{"a\"b":"line\nbreak\ttab\\slash\"quote"}`
	if got := valueOf(av); !sameJSON(got, want) {
		t.Errorf("it reads as %s", got)
	}
	// And a control character becomes its escape rather than a raw byte.
	ctrl := &ddbtypes.AttributeValueMemberM{Value: map[string]ddbtypes.AttributeValue{"c": s("\x01")}}
	if got := valueOf(ctrl); !sameJSON(got, `{"c":"\u0001"}`) {
		t.Errorf("a control character reads as %s", got)
	}
}

func TestWhatAGridShouldExpectOfAnAttribute(t *testing.T) {
	for name, c := range map[string]struct {
		av     ddbtypes.AttributeValue
		class  model.TypeClass
		native string
	}{
		"a string":       {s("x"), model.TypeString, "S"},
		"a whole number": {n("1"), model.TypeInteger, "N"},
		// The same DynamoDB type, and a different thing to a grid: one is
		// right-aligned as an integer and the other kept as its digits.
		"a fraction":   {n("1.5"), model.TypeDecimal, "N"},
		"bytes":        {b(1), model.TypeBytes, "B"},
		"a boolean":    {&ddbtypes.AttributeValueMemberBOOL{}, model.TypeBool, "BOOL"},
		"the null":     {&ddbtypes.AttributeValueMemberNULL{Value: true}, model.TypeUnknown, "NULL"},
		"a map":        {&ddbtypes.AttributeValueMemberM{}, model.TypeJSON, "M"},
		"a list":       {&ddbtypes.AttributeValueMemberL{}, model.TypeJSON, "L"},
		"a string set": {&ddbtypes.AttributeValueMemberSS{}, model.TypeJSON, "SS"},
		"a number set": {&ddbtypes.AttributeValueMemberNS{}, model.TypeJSON, "NS"},
		"a byte set":   {&ddbtypes.AttributeValueMemberBS{}, model.TypeJSON, "BS"},
	} {
		t.Run(name, func(t *testing.T) {
			got := classOf(c.av)
			if got.Class != c.class {
				t.Errorf("it reads as %v, want %v", got.Class, c.class)
			}
			if got.Native != c.native {
				t.Errorf("it calls itself %q, want %q", got.Native, c.native)
			}
		})
	}
}

func TestWhatGoesBackIntoAnItem(t *testing.T) {
	for name, c := range map[string]struct {
		v    any
		want string // the attribute's kind and its text
	}{
		"nothing":           {nil, "NULL"},
		"a string":          {"x", "S:x"},
		"a boolean":         {true, "BOOL:true"},
		"bytes":             {[]byte{1, 2}, "B:2"},
		"an exact number":   {model.Decimal("1.50"), "N:1.50"},
		"an int":            {int(3), "N:3"},
		"an int32":          {int32(3), "N:3"},
		"an int64":          {int64(3), "N:3"},
		"a float":           {float64(1.5), "N:1.5"},
		"a float32":         {float32(1.5), "N:1.5"},
		"JSON, as a map":    {model.JSON(`{"a":1}`), "M:1"},
		"JSON, as a list":   {model.JSON(`[1,2]`), "L:2"},
		"JSON, as a string": {model.JSON(`"x"`), "S:x"},
		"JSON, as a number": {model.JSON(`2.5`), "N:2.5"},
		"JSON, as nothing":  {model.JSON(`null`), "NULL"},
	} {
		t.Run(name, func(t *testing.T) {
			av, err := attributeOf(c.v)
			if err != nil {
				t.Fatal(err)
			}
			if got := describeAV(av); got != c.want {
				t.Errorf("%#v becomes %s, want %s", c.v, got, c.want)
			}
		})
	}
}

// describeAV says what an attribute is, in a line, for a test to read.
func describeAV(av ddbtypes.AttributeValue) string {
	switch v := av.(type) {
	case *ddbtypes.AttributeValueMemberNULL:
		return "NULL"
	case *ddbtypes.AttributeValueMemberS:
		return "S:" + v.Value
	case *ddbtypes.AttributeValueMemberN:
		return "N:" + v.Value
	case *ddbtypes.AttributeValueMemberBOOL:
		if v.Value {
			return "BOOL:true"
		}
		return "BOOL:false"
	case *ddbtypes.AttributeValueMemberB:
		return "B:" + itoa(int64(len(v.Value)))
	case *ddbtypes.AttributeValueMemberM:
		return "M:" + itoa(int64(len(v.Value)))
	case *ddbtypes.AttributeValueMemberL:
		return "L:" + itoa(int64(len(v.Value)))
	}
	return "?"
}

// A value the grid has no type for is refused rather than written as its Go
// formatting: an item written with a value nobody meant is worse than a write
// that did not happen.
func TestAValueDynamoDBCannotHoldIsRefused(t *testing.T) {
	for name, v := range map[string]any{
		"a time":                           struct{ Y int }{2026},
		"a number with no digits":          model.Decimal("   "),
		"JSON that is not JSON":            model.JSON(`{not json`),
		"JSON holding something it cannot": model.JSON(`{"a": 1e400}`),
	} {
		t.Run(name, func(t *testing.T) {
			if av, err := attributeOf(v); err == nil {
				t.Errorf("it became %s", describeAV(av))
			}
		})
	}
}

// fromAny's last refusal is unreachable through JSON — the decoder produces
// only the six shapes above it — and is here because it is what stops a change
// to that decoding from writing a value nobody meant. It is proved by calling
// it with what the decoder will not produce.
func TestAValueTheJSONDecoderWillNotProduceIsStillRefused(t *testing.T) {
	// A float64, which UseNumber exists to prevent: if it stopped being
	// passed, every exact number in a document would round on its way in.
	if av, err := fromAny(float64(1.5)); err == nil {
		t.Errorf("a float became %s", describeAV(av))
	}
	if av, err := fromAny([]string{"a"}); err == nil {
		t.Errorf("a slice of strings became %s", describeAV(av))
	}
}

// A JSON array becomes a list and not a set. Guessing a set would change an
// attribute's type on every edit that touched it, and a set cannot be empty,
// so an emptied one would fail at the service in words about the array.
func TestAJSONArrayBecomesAList(t *testing.T) {
	av, err := attributeOf(model.JSON(`["a","b"]`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := av.(*ddbtypes.AttributeValueMemberL); !ok {
		t.Errorf("it became %T", av)
	}
	empty, err := attributeOf(model.JSON(`[]`))
	if err != nil {
		t.Fatal(err)
	}
	if l, ok := empty.(*ddbtypes.AttributeValueMemberL); !ok || len(l.Value) != 0 {
		t.Errorf("an empty array became %T", empty)
	}
}

// A number inside JSON keeps its digits on the way through, rather than
// becoming a float64 and coming back shorter.
func TestANumberInsideJSONKeepsItsDigits(t *testing.T) {
	const exact = "1.00000000000000000000000000000000000001"
	av, err := attributeOf(model.JSON(`{"n":` + exact + `}`))
	if err != nil {
		t.Fatal(err)
	}
	m, ok := av.(*ddbtypes.AttributeValueMemberM)
	if !ok {
		t.Fatalf("it became %T", av)
	}
	got, ok := m.Value["n"].(*ddbtypes.AttributeValueMemberN)
	if !ok {
		t.Fatalf("the number became %T", m.Value["n"])
	}
	if got.Value != exact {
		t.Errorf("it became %s", got.Value)
	}
}
