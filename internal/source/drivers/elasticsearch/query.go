package elasticsearch

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// The grid's filters and sorts as a search engine takes them (T5.15, FR-3.3,
// FR-3.4).
//
// A filter a person builds in the grid means the same on every source, and
// where it cannot mean the same it is refused rather than meaning something near
// it (REQ-DRV-3). What makes that harder here than anywhere else is that a
// search engine holds a string two ways. A keyword is the string as it was
// written, and is matched, ordered and compared as one. A text field has been
// analysed — split into words, lower-cased, stemmed — and what the engine holds
// is the words: there is no whole value left to be equal to, no order, and no
// pattern to match across it. The mapping says which a field is, so the mapping
// decides what a filter becomes, and a field with both (a text with a keyword
// beside it, which is what an index makes of a string unless told otherwise) is
// asked through the keyword.

// query is the condition a browse reads under.
func query(mapped []field, opt source.BrowseOptions) (map[string]any, error) {
	if strings.TrimSpace(opt.Where) != "" {
		// A typed condition needs a language to be typed in, and this source
		// has not claimed one (REQ-DRV-3). The engine's own query string would
		// be the obvious candidate and is deliberately not taken by the back
		// door: a condition nobody can be offered is one nobody can check.
		return nil, errors.New("elasticsearch: a typed condition is not taken here; filter by column instead")
	}
	if len(opt.Filters) == 0 {
		return map[string]any{"match_all": map[string]any{}}, nil
	}
	all := make([]any, 0, len(opt.Filters))
	for _, f := range opt.Filters {
		clause, err := filter(mapped, f)
		if err != nil {
			return nil, err
		}
		all = append(all, clause)
	}
	// filter and not must: nothing here is a search to be ranked, and a filter
	// is what the engine caches.
	return map[string]any{"bool": map[string]any{"filter": all}}, nil
}

// find is the mapped field a column names.
func find(mapped []field, column string) (field, error) {
	switch column {
	case "":
		return field{}, errors.New("elasticsearch: a filter needs a field")
	case idColumn:
		// The identity is a keyword in everything but name.
		return field{name: idColumn, exact: idColumn}, nil
	case sourceColumn:
		return field{}, errors.New("elasticsearch: _source is the whole document, which is read and not searched; " +
			"filter by one of its fields")
	}
	for _, f := range mapped {
		if f.name == column {
			return f, nil
		}
	}
	return field{}, fmt.Errorf("elasticsearch: %q is not a field the mapping knows", column)
}

// filter is one predicate.
func filter(mapped []field, f source.Filter) (map[string]any, error) {
	fl, err := find(mapped, f.Column)
	if err != nil {
		return nil, err
	}
	if fl.nested != "" {
		// Each object in a nested field is a document of its own to the
		// engine, matched with a query of its own kind. Asked the ordinary way
		// it matches nothing at all, which would read as "no such rows".
		return nil, fmt.Errorf("elasticsearch: %q is inside a nested object, which cannot be filtered from here", f.Column)
	}
	if fl.exact == "" && !fl.analysed && !(f.Op == source.OpIsNull || f.Op == source.OpIsNotNull) {
		// A place, a shape, a vector, bytes: whether a document holds one can
		// be asked, and nothing else can from here.
		return nil, fmt.Errorf("elasticsearch: %q holds %s, which cannot be compared; "+
			"what can be asked is whether a document has one", f.Column, a(fl.typ.Native))
	}
	arity := func(n int) error {
		if len(f.Values) != n {
			return fmt.Errorf("elasticsearch: filter %q on %q takes %d value(s), got %d",
				f.Op, f.Column, n, len(f.Values))
		}
		return nil
	}
	// whole is the field to ask about a whole value, or a refusal where the
	// engine kept only the words.
	whole := func(doing string) (string, error) {
		if fl.exact == "" {
			return "", fmt.Errorf("elasticsearch: %q is analysed text, which is held as its words: %s. "+
				"A contains filter matches the words", f.Column, doing)
		}
		return fl.exact, nil
	}
	not := func(clause map[string]any) map[string]any {
		return map[string]any{"bool": map[string]any{"must_not": []any{clause}}}
	}
	exists := map[string]any{"exists": map[string]any{"field": fl.name}}

	var clause map[string]any
	switch f.Op {
	case source.OpEqual, source.OpNotEqual:
		if err := arity(1); err != nil {
			return nil, err
		}
		switch {
		case f.Values[0] == nil:
			// Equal to nothing is a document that does not hold the field: a
			// search engine does not index a null, so one written as null and
			// one never written are the same thing to it.
			clause = not(exists)
		case fl.exact == "":
			// The nearest thing analysed text has to equality: these words,
			// in this order, as the field's own analyser reads them.
			clause = map[string]any{"match_phrase": map[string]any{fl.name: wire(f.Values[0])}}
		default:
			clause = map[string]any{"term": map[string]any{fl.exact: wire(f.Values[0])}}
		}
		if f.Op == source.OpNotEqual {
			clause = not(clause)
		}
	case source.OpLess, source.OpLessEqual, source.OpGreater, source.OpGreaterEqual:
		if err := arity(1); err != nil {
			return nil, err
		}
		if f.Values[0] == nil {
			return nil, fmt.Errorf("elasticsearch: cannot order-compare %q with nothing", f.Column)
		}
		on, err := whole("it has no order")
		if err != nil {
			return nil, err
		}
		clause = map[string]any{"range": map[string]any{on: map[string]any{bounds[f.Op]: wire(f.Values[0])}}}
	case source.OpBetween:
		if err := arity(2); err != nil {
			return nil, err
		}
		on, err := whole("it has no order")
		if err != nil {
			return nil, err
		}
		clause = map[string]any{"range": map[string]any{on: map[string]any{
			"gte": wire(f.Values[0]), "lte": wire(f.Values[1])}}}
	case source.OpIn, source.OpNotIn:
		if len(f.Values) == 0 {
			return nil, fmt.Errorf("elasticsearch: filter %q on %q needs at least one value", f.Op, f.Column)
		}
		on, err := whole("it cannot be one of a list")
		if err != nil {
			return nil, err
		}
		vals := make([]any, len(f.Values))
		for i, v := range f.Values {
			vals[i] = wire(v)
		}
		clause = map[string]any{"terms": map[string]any{on: vals}}
		if f.Op == source.OpNotIn {
			clause = not(clause)
		}
	case source.OpIsNull:
		if err := arity(0); err != nil {
			return nil, err
		}
		clause = not(exists)
	case source.OpIsNotNull:
		if err := arity(0); err != nil {
			return nil, err
		}
		clause = exists
	case source.OpContains:
		if err := arity(1); err != nil {
			return nil, err
		}
		text := fmt.Sprint(f.Values[0])
		if fl.exact == "" {
			// What analysed text can be asked: whether it holds these words.
			clause = map[string]any{"match_phrase": map[string]any{fl.name: text}}
			break
		}
		// The text itself, anywhere in the value, whatever its case — what
		// the grid's contains means on every source that can say it.
		clause = map[string]any{"wildcard": map[string]any{fl.exact: map[string]any{
			"value": "*" + escapeWildcard(text) + "*", "case_insensitive": true}}}
	case source.OpLike, source.OpNotLike:
		if err := arity(1); err != nil {
			return nil, err
		}
		on, err := whole("a pattern has nothing to run across")
		if err != nil {
			return nil, err
		}
		clause = map[string]any{"wildcard": map[string]any{on: map[string]any{
			"value": likeToWildcard(fmt.Sprint(f.Values[0]))}}}
		if f.Op == source.OpNotLike {
			clause = not(clause)
		}
	case source.OpRegex:
		if err := arity(1); err != nil {
			return nil, err
		}
		on, err := whole("an expression has nothing to run across")
		if err != nil {
			return nil, err
		}
		clause = map[string]any{"regexp": map[string]any{on: map[string]any{"value": fmt.Sprint(f.Values[0])}}}
	default:
		return nil, fmt.Errorf("elasticsearch: filter %q is not one this source takes", f.Op)
	}
	if f.Negate {
		clause = not(clause)
	}
	return clause, nil
}

var bounds = map[source.FilterOp]string{
	source.OpLess: "lt", source.OpLessEqual: "lte", source.OpGreater: "gt", source.OpGreaterEqual: "gte",
}

// wire is a value as it is written into a query. A time is written the way the
// engine writes one; an exact number as its digits, so that it is not rounded
// on its way to being compared.
func wire(v any) any {
	switch x := v.(type) {
	case time.Time:
		return x.UTC().Format(time.RFC3339Nano)
	case model.Decimal:
		return json.Number(string(x))
	case []byte:
		return string(x)
	}
	return v
}

// escapeWildcard makes text match itself in a wildcard query, whose own
// characters are *, ? and the backslash that escapes them.
func escapeWildcard(text string) string {
	var b strings.Builder
	for _, r := range text {
		if r == '*' || r == '?' || r == '\\' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// likeToWildcard is a LIKE pattern as a wildcard query: % is any run of
// characters and _ is any one, a backslash makes either of them itself, and the
// wildcard's own characters are made to mean themselves.
func likeToWildcard(pattern string) string {
	var b strings.Builder
	escaped := false
	for _, r := range pattern {
		switch {
		case escaped:
			escaped = false
			if r == '*' || r == '?' || r == '\\' {
				b.WriteByte('\\')
			}
			b.WriteRune(r)
		case r == '\\':
			escaped = true
		case r == '%':
			b.WriteByte('*')
		case r == '_':
			b.WriteByte('?')
		case r == '*' || r == '?':
			b.WriteByte('\\')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	if escaped {
		// A pattern ending in a backslash ends in a backslash.
		b.WriteString(`\\`)
	}
	return b.String()
}

// order is the sort a browse reads in.
//
// Always ending in _doc, the order the engine holds documents in: it is the
// cheapest there is, and it is what makes one page follow another when the
// sort somebody chose does not tell two documents apart.
func order(mapped []field, sorts []source.Sort) ([]any, error) {
	out := make([]any, 0, len(sorts)+1)
	for _, s := range sorts {
		fl, err := find(mapped, s.Column)
		if err != nil {
			return nil, err
		}
		switch {
		case fl.name == idColumn:
			// The engine can be made to sort by its identities and by default
			// will not: it would have to load every one of them into memory.
			return nil, errors.New("elasticsearch: documents cannot be sorted by _id; sort by a field instead")
		case fl.nested != "":
			return nil, fmt.Errorf("elasticsearch: %q is inside a nested object, which cannot be sorted by from here", s.Column)
		case fl.exact == "" && fl.analysed:
			return nil, fmt.Errorf("elasticsearch: %q is analysed text, which is held as its words and has no order", s.Column)
		case fl.exact == "":
			return nil, fmt.Errorf("elasticsearch: %q holds %s, which has no order", s.Column, a(fl.typ.Native))
		}
		dir, missing := "asc", "_last"
		if s.Descending {
			dir = "desc"
		}
		if s.NullsFirst {
			missing = "_first"
		}
		out = append(out, map[string]any{fl.exact: map[string]any{"order": dir, "missing": missing}})
	}
	return append(out, "_doc"), nil
}

// a is a mapped type as it is said in a sentence.
func a(native string) string {
	if native == "" {
		return "an object"
	}
	if strings.ContainsRune("aeiou", rune(native[0])) {
		return "an " + native
	}
	return "a " + native
}
