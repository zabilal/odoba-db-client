// Package rowfilter decides whether a row matches the grid's filters, here
// rather than on the server (FR-13.9).
//
// Every other source is filtered where the data is: the filters go down with
// the browse and the server answers over the whole table. A log will not do
// that — a broker hands over bytes and asks no questions about them — so the
// filtering happens over what has been read, and what that can honestly claim
// is narrower (ADR-0099).
//
// What it must not do is mean something different here. The same filter text
// typed on a table and on a topic has to mean the same thing, so these rules
// are the drivers' rules: contains ignores case, NOT IN keeps NULL rows unless
// NULL was listed, an empty list of values selects nothing, and a pattern's %
// stands for any run of characters.
package rowfilter

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Matcher decides whether a row matches, having read the filters once.
type Matcher struct{ rules []rule }

type rule struct {
	col    int // -1 where the column is not in the row
	op     source.FilterOp
	values []any
	negate bool
	re     *regexp.Regexp // OpRegex, OpLike and OpNotLike
}

// Compile reads the filters against the columns they name. A pattern that is
// not a pattern is a mistake the person sees now, rather than a row that
// quietly never matches.
func Compile(cols []model.ColumnDef, filters []source.Filter) (*Matcher, error) {
	m := &Matcher{}
	for _, f := range filters {
		r := rule{col: -1, op: f.Op, values: f.Values, negate: f.Negate}
		for i, c := range cols {
			if c.Name == f.Column {
				r.col = i
				break
			}
		}
		switch f.Op {
		case source.OpRegex:
			if len(f.Values) != 1 {
				return nil, fmt.Errorf("rowfilter: %s takes one pattern", f.Op)
			}
			re, err := regexp.Compile(text(f.Values[0]))
			if err != nil {
				return nil, fmt.Errorf("rowfilter: %v", err)
			}
			r.re = re
		case source.OpLike, source.OpNotLike:
			if len(f.Values) != 1 {
				return nil, fmt.Errorf("rowfilter: %s takes one pattern", f.Op)
			}
			re, err := regexp.Compile(likeRegex(text(f.Values[0])))
			if err != nil {
				return nil, fmt.Errorf("rowfilter: %v", err)
			}
			r.re = re
		}
		m.rules = append(m.rules, r)
	}
	return m, nil
}

// Match reports whether a row satisfies every filter, as AND does.
func (m *Matcher) Match(row model.Row) bool {
	for _, r := range m.rules {
		if !r.match(row) {
			return false
		}
	}
	return true
}

func (r rule) match(row model.Row) bool {
	if r.col < 0 || r.col >= len(row) {
		// A filter on a column this row has not got matches nothing, rather
		// than everything: it was asked for and cannot be answered.
		return false
	}
	ok := r.holds(row[r.col])
	if r.negate {
		return !ok
	}
	return ok
}

// holds is the predicate itself, before Negate has its say.
func (r rule) holds(v any) bool {
	switch r.op {
	case source.OpIsNull:
		return v == nil
	case source.OpIsNotNull:
		return v != nil
	case source.OpIn:
		return in(v, r.values)
	case source.OpNotIn:
		// NOT IN keeps NULL rows unless NULL was listed, which is what the
		// picklist means by unticking a value: see the drivers' in.
		if v == nil {
			return !listsNull(r.values)
		}
		return !in(v, r.values)
	}
	if v == nil {
		// NULL is not equal to, greater than, or contained in anything. Only
		// the tests above have anything to say about it.
		return false
	}
	// A value that is a list — a record's headers — matches where any one of
	// its elements does. That is what somebody filtering by a header means.
	if list, isList := v.([]any); isList {
		for _, e := range list {
			pair, name := elem(e)
			if r.one(pair) || (name != "" && r.one(name)) {
				return true
			}
		}
		return false
	}
	return r.one(v)
}

// one applies the predicate to a single value.
func (r rule) one(v any) bool {
	switch r.op {
	case source.OpEqual:
		return len(r.values) == 1 && equal(v, r.values[0])
	case source.OpNotEqual:
		return len(r.values) == 1 && !equal(v, r.values[0])
	case source.OpContains:
		return len(r.values) == 1 &&
			strings.Contains(strings.ToLower(text(v)), strings.ToLower(text(r.values[0])))
	case source.OpRegex:
		return r.re != nil && r.re.MatchString(text(v))
	case source.OpLike:
		return r.re != nil && r.re.MatchString(text(v))
	case source.OpNotLike:
		return r.re != nil && !r.re.MatchString(text(v))
	case source.OpLess, source.OpLessEqual, source.OpGreater, source.OpGreaterEqual:
		if len(r.values) != 1 {
			return false
		}
		c, ok := compare(v, r.values[0])
		if !ok {
			return false
		}
		switch r.op {
		case source.OpLess:
			return c < 0
		case source.OpLessEqual:
			return c <= 0
		case source.OpGreater:
			return c > 0
		}
		return c >= 0
	case source.OpBetween:
		if len(r.values) != 2 {
			return false
		}
		lo, okLo := compare(v, r.values[0])
		hi, okHi := compare(v, r.values[1])
		return okLo && okHi && lo >= 0 && hi <= 0
	}
	return false
}

// in reports whether a value is among those listed. A nil in the list stands
// for NULL rows, as the picklist means it.
func in(v any, vals []any) bool {
	for _, want := range vals {
		if want == nil {
			if v == nil {
				return true
			}
			continue
		}
		if v == nil {
			continue
		}
		if list, isList := v.([]any); isList {
			for _, e := range list {
				pair, name := elem(e)
				if equal(pair, want) || (name != "" && equal(name, want)) {
					return true
				}
			}
			continue
		}
		if equal(v, want) {
			return true
		}
	}
	return false
}

func listsNull(vals []any) bool {
	for _, v := range vals {
		if v == nil {
			return true
		}
	}
	return false
}

// equal compares a row's value with one the person typed. Numbers compare as
// numbers however they were carried; everything else compares as its text,
// which is how a byte string and the word somebody typed can be the same
// thing.
func equal(v, want any) bool {
	if a, b, ok := numbers(v, want); ok {
		return a == b
	}
	return text(v) == text(want)
}

// compare orders two values: negative, zero or positive, and whether they can
// be ordered at all.
func compare(v, want any) (int, bool) {
	if a, b, ok := numbers(v, want); ok {
		switch {
		case a < b:
			return -1, true
		case a > b:
			return 1, true
		}
		return 0, true
	}
	if a, ok := v.(time.Time); ok {
		if b, ok := asTime(want); ok {
			return a.Compare(b), true
		}
	}
	return strings.Compare(text(v), text(want)), true
}

// numbers reads two values as numbers, where both are numbers. A decimal is
// carried as text so that no digit is lost, so it is read back here.
func numbers(v, want any) (float64, float64, bool) {
	a, okA := asNumber(v)
	b, okB := asNumber(want)
	return a, b, okA && okB
}

func asNumber(v any) (float64, bool) {
	switch x := v.(type) {
	case int64:
		return float64(x), true
	case int32:
		return float64(x), true
	case int:
		return float64(x), true
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case model.Decimal:
		return parseFloat(string(x))
	case string:
		return parseFloat(x)
	}
	return 0, false
}

func parseFloat(s string) (float64, bool) {
	var f float64
	var extra byte
	// Sscanf rather than ParseFloat so that trailing rubbish is refused
	// rather than ignored: "12abc" is not a number.
	if n, err := fmt.Sscanf(strings.TrimSpace(s), "%g%c", &f, &extra); err != nil && n == 1 {
		return f, true
	}
	return 0, false
}

func asTime(v any) (time.Time, bool) {
	switch x := v.(type) {
	case time.Time:
		return x, true
	case string:
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
			if t, err := time.Parse(layout, strings.TrimSpace(x)); err == nil {
				return t, true
			}
		}
	}
	return time.Time{}, false
}

// text writes a value as the text it is filtered against. Bytes are their
// UTF-8, because a person typing a word means the word, not its bytes.
func text(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case []byte:
		return string(x)
	case model.Decimal:
		return string(x)
	case model.JSON:
		return string(x)
	case time.Time:
		return x.Format(time.RFC3339Nano)
	}
	return fmt.Sprint(v)
}

// elem writes one element of a list two ways: whole, and by the name it goes
// by. A record's header is a name and a value, so it reads as name=value —
// typing the name finds the header, and typing name=value finds that pairing.
// Anything that is not a name and a value has only the one reading.
func elem(e any) (pair, name string) {
	m, ok := e.(map[string]any)
	if !ok {
		return text(e), ""
	}
	n, hasName := m["key"]
	value, hasValue := m["value"]
	if !hasName && !hasValue {
		return text(e), ""
	}
	return text(n) + "=" + text(value), text(n)
}

// likeRegex turns a LIKE pattern into a regular expression: % is any run, _ is
// one character, and everything else is itself. The same translation the
// document store makes, so a pattern means one thing everywhere.
func likeRegex(pattern string) string {
	var b strings.Builder
	b.WriteByte('^')
	for _, r := range pattern {
		switch r {
		case '%':
			b.WriteString(".*")
		case '_':
			b.WriteByte('.')
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteByte('$')
	return b.String()
}
