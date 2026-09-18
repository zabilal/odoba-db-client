package cassandra

import (
	"encoding/json"
	"fmt"
	"math/big"
	"reflect"
	"strings"
	"time"

	"github.com/gocql/gocql"
	"gopkg.in/inf.v0"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// What a CQL value is, in the model's terms (FR-3.8).
//
// normalize narrows a value to the closed set model.Row permits:
//
//	bool, int64, float64, model.Decimal, string, []byte, time.Time,
//	model.JSON, []any, map[string]any
//
// The grid type-switches only on that set, so anything outside it — a UUID, a
// varint past int64, a duration that is not a length of time — is rendered
// here, once, in the form Cassandra itself would print. The fallback is
// fmt.Sprint rather than a panic: an unfamiliar value should draw as legible
// text, never as a crash.

func normalize(v any) any {
	switch x := v.(type) {
	case nil, bool, int64, float64, string, time.Time, model.Decimal, model.JSON:
		return x
	case int:
		return int64(x)
	case int8:
		return int64(x)
	case int16:
		return int64(x)
	case int32:
		return int64(x)
	case float32:
		return float64(x)
	case []byte:
		return append([]byte(nil), x...)
	case gocql.UUID:
		return x.String()
	case gocql.Duration:
		return durationText(x)
	case time.Duration:
		// CQL's time is a time of day, counted from midnight, rather than a
		// length of anything: it reads as one.
		return timeOfDay(x)
	case *inf.Dec:
		if x == nil {
			return nil
		}
		return model.Decimal(x.String())
	case *big.Int:
		if x == nil {
			return nil
		}
		// A varint holds numbers no int64 can, and its digits are exact.
		return model.Decimal(x.String())
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = normalize(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = normalize(e)
		}
		return out
	}
	// A collection comes back as a slice or a map of its own element type,
	// which only reflection can walk. What it holds is a structure, and the
	// cell viewer reads a structure as JSON (FR-3.9).
	if out, ok := structured(v); ok {
		return out
	}
	if s, ok := v.(fmt.Stringer); ok {
		return s.String()
	}
	return fmt.Sprint(v)
}

// structured renders a collection — a list, a set, a map, a tuple, a
// keyspace's own type — as the JSON of what it holds.
func structured(v any) (any, bool) {
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		out := make([]any, 0, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			out = append(out, normalize(rv.Index(i).Interface()))
		}
		return jsonOf(out), true
	case reflect.Map:
		// A CQL map's keys are values of their own, and JSON's are text: each
		// key is written as it prints, which is how cqlsh shows one.
		out := make(map[string]any, rv.Len())
		for _, k := range rv.MapKeys() {
			out[fmt.Sprint(normalize(k.Interface()))] = normalize(rv.MapIndex(k).Interface())
		}
		return jsonOf(out), true
	}
	return nil, false
}

// jsonOf writes a structure as the JSON the cell viewer reads, or as its own
// printing where it cannot be written.
func jsonOf(v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return model.JSON(b)
}

// derefValue reads what a scan wrote into a destination nothing else knows
// the type of: a collection's slice or map, a decimal, a varint.
func derefValue(v any) any {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return v
	}
	// A pointer to a pointer is how gocql hands back a decimal and a varint,
	// whose Go types are themselves pointers.
	if elem := rv.Elem(); elem.Kind() != reflect.Pointer || !elem.IsNil() {
		return elem.Interface()
	}
	return nil
}

// durationText writes a CQL duration as CQL writes one. It is months, days
// and nanoseconds rather than a length of time, because a month is not a
// number of days and a day is not always 24 hours.
func durationText(d gocql.Duration) string {
	if d.Months == 0 && d.Days == 0 && d.Nanoseconds == 0 {
		return "0s"
	}
	var b strings.Builder
	if d.Months < 0 || d.Days < 0 || d.Nanoseconds < 0 {
		b.WriteByte('-')
	}
	abs := func(n int64) int64 {
		if n < 0 {
			return -n
		}
		return n
	}
	months, days, nanos := abs(int64(d.Months)), abs(int64(d.Days)), abs(d.Nanoseconds)
	if years := months / 12; years > 0 {
		fmt.Fprintf(&b, "%dy", years)
		months %= 12
	}
	if months > 0 {
		fmt.Fprintf(&b, "%dmo", months)
	}
	if days > 0 {
		fmt.Fprintf(&b, "%dd", days)
	}
	if nanos > 0 {
		// The parts a person writes, largest first, leaving out what is zero.
		for _, part := range []struct {
			unit string
			size int64
		}{
			{"h", int64(time.Hour)}, {"m", int64(time.Minute)}, {"s", int64(time.Second)},
			{"ms", int64(time.Millisecond)}, {"us", int64(time.Microsecond)}, {"ns", 1},
		} {
			if n := nanos / part.size; n > 0 {
				fmt.Fprintf(&b, "%d%s", n, part.unit)
				nanos %= part.size
			}
		}
	}
	return b.String()
}

// timeOfDay writes CQL's time: a time of day counted from midnight, to
// whatever precision it was given.
func timeOfDay(d time.Duration) string {
	if d < 0 {
		return d.String()
	}
	h := d / time.Hour
	m := d % time.Hour / time.Minute
	s := d % time.Minute / time.Second
	out := fmt.Sprintf("%02d:%02d:%02d", h, m, s)
	if ns := d % time.Second; ns != 0 {
		out += strings.TrimRight(fmt.Sprintf(".%09d", ns), "0")
	}
	return out
}
