package dynamodb

import (
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// DynamoDB's values, and the model's.
//
// An attribute is one of nine things: a string, a number, bytes, a boolean,
// the null, a map, a list, or a set of strings, numbers or bytes. Two of
// those decide most of what follows.
//
// A number arrives as text and stays text. DynamoDB's numbers carry up to 38
// significant digits, which is more than a float64 holds, so reading one as a
// float would quietly change values a person is looking at; model.Decimal is
// the model's own way of saying "an exact number, as its digits", and that is
// what a number becomes. One that is plainly a whole number small enough to
// be one is an int64, because a grid right-aligns and sorts those and because
// an item's key is very often a small integer.
//
// The null is not the absence of a value. An attribute set to NULL is there
// and holds nothing, which is a different thing from an attribute nobody
// wrote; the first becomes nil, and the second is simply not in the item.

// valueOf is what an attribute holds.
func valueOf(av ddbtypes.AttributeValue) any {
	switch v := av.(type) {
	case *ddbtypes.AttributeValueMemberS:
		return v.Value
	case *ddbtypes.AttributeValueMemberN:
		return numberOf(v.Value)
	case *ddbtypes.AttributeValueMemberB:
		return append([]byte(nil), v.Value...)
	case *ddbtypes.AttributeValueMemberBOOL:
		return v.Value
	case *ddbtypes.AttributeValueMemberNULL:
		return nil
	case *ddbtypes.AttributeValueMemberM:
		return model.JSON(jsonOf(av))
	case *ddbtypes.AttributeValueMemberL:
		return model.JSON(jsonOf(av))
	case *ddbtypes.AttributeValueMemberSS, *ddbtypes.AttributeValueMemberNS,
		*ddbtypes.AttributeValueMemberBS:
		// A set is not a list, and the difference is worth keeping: the
		// values are unordered and cannot repeat. It is shown as a JSON
		// array, which is the only shape a grid cell has for several values,
		// and the structure view says which attributes are sets.
		return model.JSON(jsonOf(av))
	}
	return nil
}

// numberOf reads DynamoDB's number. A whole number that fits an int64 is one;
// everything else keeps every digit it arrived with.
func numberOf(text string) any {
	if n, err := strconv.ParseInt(text, 10, 64); err == nil {
		return n
	}
	return model.Decimal(text)
}

// classOf is what a grid should expect of an attribute, for the column its
// name becomes.
func classOf(av ddbtypes.AttributeValue) model.DataType {
	dt := model.DataType{Length: -1, Nullable: true}
	switch v := av.(type) {
	case *ddbtypes.AttributeValueMemberS:
		dt.Class, dt.Native = model.TypeString, "S"
	case *ddbtypes.AttributeValueMemberN:
		dt.Native = "N"
		dt.Class = model.TypeDecimal
		if _, err := strconv.ParseInt(v.Value, 10, 64); err == nil {
			dt.Class = model.TypeInteger
		}
	case *ddbtypes.AttributeValueMemberB:
		dt.Class, dt.Native = model.TypeBytes, "B"
	case *ddbtypes.AttributeValueMemberBOOL:
		dt.Class, dt.Native = model.TypeBool, "BOOL"
	case *ddbtypes.AttributeValueMemberNULL:
		dt.Class, dt.Native = model.TypeUnknown, "NULL"
	case *ddbtypes.AttributeValueMemberM:
		dt.Class, dt.Native = model.TypeJSON, "M"
	case *ddbtypes.AttributeValueMemberL:
		dt.Class, dt.Native = model.TypeJSON, "L"
	case *ddbtypes.AttributeValueMemberSS:
		dt.Class, dt.Native = model.TypeJSON, "SS"
	case *ddbtypes.AttributeValueMemberNS:
		dt.Class, dt.Native = model.TypeJSON, "NS"
	case *ddbtypes.AttributeValueMemberBS:
		dt.Class, dt.Native = model.TypeJSON, "BS"
	default:
		dt.Class, dt.Native = model.TypeUnknown, "?"
	}
	return dt
}

// jsonOf renders an attribute as JSON, for the shapes a grid cell has no
// other form for. It is the value rather than DynamoDB's wire encoding of it:
// a map of {"i": {"N": "2"}} reads as {"i": 2}, because the wire's type tags
// are about how it travelled and not about what it holds.
func jsonOf(av ddbtypes.AttributeValue) string {
	var b strings.Builder
	writeJSON(&b, av)
	return b.String()
}

func writeJSON(b *strings.Builder, av ddbtypes.AttributeValue) {
	switch v := av.(type) {
	case *ddbtypes.AttributeValueMemberS:
		b.WriteString(quoteJSON(v.Value))
	case *ddbtypes.AttributeValueMemberN:
		// A number goes in as a number: its digits are already the shortest
		// exact form of itself, and JSON takes them.
		b.WriteString(v.Value)
	case *ddbtypes.AttributeValueMemberB:
		b.WriteString(quoteJSON(base64.StdEncoding.EncodeToString(v.Value)))
	case *ddbtypes.AttributeValueMemberBOOL:
		b.WriteString(strconv.FormatBool(v.Value))
	case *ddbtypes.AttributeValueMemberNULL:
		b.WriteString("null")
	case *ddbtypes.AttributeValueMemberM:
		names := make([]string, 0, len(v.Value))
		for name := range v.Value {
			names = append(names, name)
		}
		// Sorted, because a map has no order of its own and an order that
		// changed between two reads of the same item would read as a change.
		sort.Strings(names)
		b.WriteString("{")
		for i, name := range names {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString(quoteJSON(name) + ":")
			writeJSON(b, v.Value[name])
		}
		b.WriteString("}")
	case *ddbtypes.AttributeValueMemberL:
		b.WriteString("[")
		for i, item := range v.Value {
			if i > 0 {
				b.WriteString(",")
			}
			writeJSON(b, item)
		}
		b.WriteString("]")
	case *ddbtypes.AttributeValueMemberSS:
		writeStrings(b, v.Value)
	case *ddbtypes.AttributeValueMemberNS:
		sorted := append([]string(nil), v.Value...)
		sort.Strings(sorted)
		b.WriteString("[")
		for i, n := range sorted {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString(n)
		}
		b.WriteString("]")
	case *ddbtypes.AttributeValueMemberBS:
		encoded := make([]string, len(v.Value))
		for i, raw := range v.Value {
			encoded[i] = base64.StdEncoding.EncodeToString(raw)
		}
		writeStrings(b, encoded)
	default:
		b.WriteString("null")
	}
}

func writeStrings(b *strings.Builder, vals []string) {
	sorted := append([]string(nil), vals...)
	sort.Strings(sorted)
	b.WriteString("[")
	for i, s := range sorted {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(quoteJSON(s))
	}
	b.WriteString("]")
}

// quoteJSON writes a string as a JSON string. strconv.Quote is Go's escaping
// rather than JSON's, so the few places they differ are done here.
func quoteJSON(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\u%04x`, r)
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// attributeOf is a value as DynamoDB takes it back.
//
// What the grid holds as text to keep it exact goes back as what it is: a
// decimal as a number, JSON as the map or list it describes. A value the grid
// has no type for at all is refused rather than written as its Go formatting,
// because an item written with a value nobody meant is worse than a write that
// did not happen.
func attributeOf(v any) (ddbtypes.AttributeValue, error) {
	switch x := v.(type) {
	case nil:
		return &ddbtypes.AttributeValueMemberNULL{Value: true}, nil
	case string:
		return &ddbtypes.AttributeValueMemberS{Value: x}, nil
	case bool:
		return &ddbtypes.AttributeValueMemberBOOL{Value: x}, nil
	case []byte:
		return &ddbtypes.AttributeValueMemberB{Value: x}, nil
	case model.Decimal:
		if strings.TrimSpace(string(x)) == "" {
			return nil, errors.New("a number with no digits in it")
		}
		return &ddbtypes.AttributeValueMemberN{Value: string(x)}, nil
	case model.JSON:
		return fromJSON(string(x))
	case int:
		return &ddbtypes.AttributeValueMemberN{Value: strconv.FormatInt(int64(x), 10)}, nil
	case int32:
		return &ddbtypes.AttributeValueMemberN{Value: strconv.FormatInt(int64(x), 10)}, nil
	case int64:
		return &ddbtypes.AttributeValueMemberN{Value: strconv.FormatInt(x, 10)}, nil
	case float32:
		return &ddbtypes.AttributeValueMemberN{Value: strconv.FormatFloat(float64(x), 'g', -1, 32)}, nil
	case float64:
		return &ddbtypes.AttributeValueMemberN{Value: strconv.FormatFloat(x, 'g', -1, 64)}, nil
	}
	return nil, fmt.Errorf("dynamodb cannot hold a %T", v)
}
