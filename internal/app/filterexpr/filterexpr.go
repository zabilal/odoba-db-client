// Package filterexpr parses the grid's filter row (FR-3.5): the short
// notation a person types into a column's filter cell, turned into the
// filters a source applies. Parsing is not SQL. The driver's dialect renders
// the filters and binds every value (ARCH-2, NFR-S6).
//
//	text          contains text, ignoring case (text columns); equals (others)
//	=x            equals x
//	!=x  <>x  !x  is not x; NULL rows stay, since NULL is not x either
//	>x  >=x  <x  <=x
//	a,b,c         is one of them          !a,b,c   is none of them
//	x..y          between x and y, inclusive
//	~re           matches the regular expression; !~re does not
//	NULL          is NULL;  !NULL  is not NULL
//	a%b           LIKE, % standing for any run of characters
//	"…"           literal: quotes keep commas, operators and NULL as text
//
// Values are converted by the column's type, so ">abc" on a number column is
// an error the person sees, not a comparison the server makes with a string.
// Decimals travel as text, so no digit is lost on the way.
package filterexpr

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Parse turns one column's filter text into filters. Empty text is no
// filter. The error explains what is wrong in the person's terms.
func Parse(column, text string, t model.DataType) ([]source.Filter, error) {
	s := strings.TrimSpace(text)
	if s == "" {
		return nil, nil
	}
	one := func(op source.FilterOp, negate bool, vals ...any) ([]source.Filter, error) {
		return []source.Filter{{Column: column, Op: op, Values: vals, Negate: negate}}, nil
	}

	if lit, ok := unquote(s); ok {
		if textual(t) {
			return one(source.OpContains, false, lit)
		}
		v, err := value(lit, t)
		if err != nil {
			return nil, err
		}
		return one(source.OpEqual, false, v)
	}

	switch {
	case strings.EqualFold(s, "null"):
		return one(source.OpIsNull, false)
	case strings.EqualFold(s, "!null"), strings.EqualFold(s, "not null"):
		return one(source.OpIsNotNull, false)
	case strings.HasPrefix(s, "!~"), strings.HasPrefix(s, "~"):
		negate := s[0] == '!'
		re := strings.TrimSpace(strings.TrimLeft(s, "!~"))
		if re == "" {
			return nil, errors.New("~ needs a pattern after it")
		}
		return one(source.OpRegex, negate, re)
	}

	for _, o := range []struct {
		prefix string
		op     source.FilterOp
	}{
		{">=", source.OpGreaterEqual}, {"<=", source.OpLessEqual}, {"!=", source.OpNotEqual},
		{"<>", source.OpNotEqual}, {">", source.OpGreater}, {"<", source.OpLess}, {"=", source.OpEqual},
	} {
		if !strings.HasPrefix(s, o.prefix) {
			continue
		}
		rest := strings.TrimSpace(s[len(o.prefix):])
		if rest == "" {
			return nil, fmt.Errorf("%s needs a value after it", o.prefix)
		}
		if strings.EqualFold(rest, "null") {
			switch o.op {
			case source.OpEqual:
				return one(source.OpIsNull, false)
			case source.OpNotEqual:
				return one(source.OpIsNotNull, false)
			}
			return nil, errors.New("nothing is greater or less than NULL; use NULL or !NULL")
		}
		lit, _ := unquote(rest)
		if !isQuoted(rest) {
			lit = rest
		}
		v, err := value(lit, t)
		if err != nil {
			return nil, err
		}
		if o.op == source.OpNotEqual {
			// SQL's <> drops NULL rows too, which nobody typing !=x asked
			// for. NOT IN keeps them, as unticking x in a picklist does (see
			// the drivers' in).
			return one(source.OpNotIn, false, v)
		}
		return one(o.op, false, v)
	}

	negate := false
	if strings.HasPrefix(s, "!") {
		negate, s = true, strings.TrimSpace(s[1:])
		if s == "" {
			return nil, errors.New("! needs a value after it")
		}
	}

	if items, ok := split(s); ok && len(items) > 1 {
		vals := make([]any, 0, len(items))
		for _, it := range items {
			if !it.quoted && strings.EqualFold(it.text, "null") {
				vals = append(vals, nil) // IN's nil stands for NULL rows (see the drivers' in)
				continue
			}
			v, err := value(it.text, t)
			if err != nil {
				return nil, err
			}
			vals = append(vals, v)
		}
		op := source.OpIn
		if negate {
			op = source.OpNotIn
		}
		return one(op, false, vals...)
	}

	if negate {
		v, err := value(s, t)
		if err != nil {
			return nil, err
		}
		return one(source.OpNotIn, false, v)
	}

	if lo, hi, ok := strings.Cut(s, ".."); ok && strings.TrimSpace(lo) != "" && strings.TrimSpace(hi) != "" {
		a, err := value(strings.TrimSpace(lo), t)
		if err != nil {
			return nil, err
		}
		b, err := value(strings.TrimSpace(hi), t)
		if err != nil {
			return nil, err
		}
		return one(source.OpBetween, false, a, b)
	}

	if textual(t) {
		if strings.Contains(s, "%") {
			return one(source.OpLike, false, s)
		}
		return one(source.OpContains, false, s)
	}
	v, err := value(s, t)
	if err != nil {
		return nil, err
	}
	return one(source.OpEqual, false, v)
}

// textual reports whether plain text means "contains" for a column.
func textual(t model.DataType) bool {
	switch t.Class {
	case model.TypeString, model.TypeUnknown, model.TypeJSON, model.TypeEnum, model.TypeXML:
		return true
	}
	return false
}

var number = regexp.MustCompile(`^[+-]?(\d+\.?\d*|\.\d+)([eE][+-]?\d+)?$`)

// value converts a typed value for a column: a whole number for an integer
// column, a number for a float, a checked number kept as text for a decimal,
// a truth value for a boolean. Anything else goes as text, for the server to
// read as the column's type.
func value(s string, t model.DataType) (any, error) {
	switch t.Class {
	case model.TypeInteger:
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not a whole number", s)
		}
		return n, nil
	case model.TypeFloat:
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not a number", s)
		}
		return f, nil
	case model.TypeDecimal:
		if !number.MatchString(s) {
			return nil, fmt.Errorf("%q is not a number", s)
		}
		return s, nil
	case model.TypeBool:
		switch strings.ToLower(s) {
		case "true", "t", "yes", "y", "1":
			return true, nil
		case "false", "f", "no", "n", "0":
			return false, nil
		}
		return nil, fmt.Errorf("%q is not true or false", s)
	}
	return s, nil
}

type item struct {
	text   string
	quoted bool
}

// split divides a list at commas outside quotes. ok is false for a quote
// left open.
func split(s string) ([]item, bool) {
	var out []item
	var b strings.Builder
	quoted, in := false, byte(0)
	flush := func() {
		text := strings.TrimSpace(b.String())
		if lit, ok := unquote(text); ok {
			out = append(out, item{lit, true})
		} else {
			out = append(out, item{text, quoted})
		}
		b.Reset()
		quoted = false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case in != 0:
			b.WriteByte(c)
			if c == in {
				in = 0
			}
		case c == '"' || c == '\'':
			in = c
			quoted = true
			b.WriteByte(c)
		case c == ',':
			flush()
		default:
			b.WriteByte(c)
		}
	}
	if in != 0 {
		return nil, false
	}
	flush()
	return out, true
}

func isQuoted(s string) bool {
	_, ok := unquote(s)
	return ok
}

// unquote strips one pair of matching double or single quotes.
func unquote(s string) (string, bool) {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1], true
	}
	return "", false
}
