package elasticsearch

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// What a document holds, as the grid holds it (T5.15, FR-3.8).
//
// A document arrives as JSON, and JSON has one kind of number. Read the easy
// way every one of them becomes a float64, and an identifier of nineteen digits
// comes back with its last three changed. So numbers are read as their digits
// and become what the mapping says they are — the engine having already
// decided — or, where the mapping does not say, the narrowest thing that holds
// them exactly.

// narrow is a value as the model holds one, given what the mapping says the
// field is.
func narrow(v any, t model.DataType) any {
	switch x := v.(type) {
	case nil, bool:
		return x
	case json.Number:
		if t.Class == model.TypeTimestamp {
			// A date written as a number is milliseconds since 1970, which is
			// the only number the default date format takes.
			if ms, err := x.Int64(); err == nil {
				return time.UnixMilli(ms).UTC()
			}
		}
		return number(x, t.Class == model.TypeFloat)
	case string:
		if t.Class == model.TypeTimestamp {
			if at, ok := instant(x); ok {
				return at
			}
		}
		return x
	case []any:
		// An array of what the field is: a search engine has no array type,
		// and any field may hold several values.
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = narrow(item, t)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, item := range x {
			out[k] = narrow(item, model.DataType{})
		}
		return out
	}
	return v
}

// number is a JSON number as the narrowest thing that holds it exactly: an
// integer where it is one and fits, a float where it has a fraction, and its
// own digits where neither holds all of it.
func number(n json.Number, float bool) any {
	text := n.String()
	if !float && !strings.ContainsAny(text, ".eE") {
		if i, err := strconv.ParseInt(text, 10, 64); err == nil {
			return i
		}
		// An unsigned_long past what an int64 holds, or an integer longer
		// than either.
		return model.Decimal(text)
	}
	f, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return model.Decimal(text)
	}
	return f
}

// layouts are the ways a date arrives as text under the default format,
// strict_date_optional_time: a date, or a date and a time with or without a
// zone.
var layouts = []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02"}

// instant reads a date the engine wrote. An index may be told to write its
// dates any way at all; one that is not read here stays the text it was, which
// is still what the document says.
func instant(text string) (time.Time, bool) {
	for _, layout := range layouts {
		if at, err := time.Parse(layout, text); err == nil {
			return at.UTC(), true
		}
	}
	return time.Time{}, false
}

// at is the value a document holds under a field's name, walking the dots a
// flattened name is made of. Nothing where the document has not got it, which is
// a document that did not say rather than a document that said nothing.
//
// Where the walk meets an array of objects it goes into each of them and
// answers what it found in all, which is how the engine itself reads such a
// field: "authors.name" is every author's name.
func at(held any, path []string) any {
	if len(path) == 0 {
		return held
	}
	switch x := held.(type) {
	case map[string]any:
		// A document may write "a.b" as one key or as b inside a; the engine
		// reads both as the same field, so both are looked for, the longest
		// key last.
		for i := 1; i <= len(path); i++ {
			if next, ok := x[strings.Join(path[:i], ".")]; ok {
				if v := at(next, path[i:]); v != nil {
					return v
				}
			}
		}
		return nil
	case []any:
		var out []any
		for _, item := range x {
			if v := at(item, path); v != nil {
				out = append(out, v)
			}
		}
		if out == nil {
			return nil
		}
		return out
	}
	return nil
}
