package dynamodb

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// The grid's filters as a DynamoDB filter expression (FR-3.4, FR-3.5).
//
// Every attribute name and every value goes in through a placeholder — #c0 for
// a name, :v0 for a value — which is not a nicety: an attribute name may be
// one of DynamoDB's several hundred reserved words, "name" and "size" among
// them, and a name written into an expression is a name interpolated into a
// statement (NFR-S6). So nothing a person typed ever reaches the expression's
// text.
//
// A filter expression is applied after the items are read, which is the whole
// of what a scan's filter is: the service reads the table, charges for the
// read, and returns what matched. That is why there is no picklist here
// (DistinctValues is false) and why a count over a filter costs a scan.

// expression collects the names and values an expression refers to.
type expression struct {
	names  map[string]string
	values map[string]ddbtypes.AttributeValue
}

// name is the placeholder for an attribute name, made once per name.
func (e *expression) name(attr string) string {
	if e.names == nil {
		e.names = map[string]string{}
	}
	for placeholder, existing := range e.names {
		if existing == attr {
			return placeholder
		}
	}
	placeholder := "#c" + strconv.Itoa(len(e.names))
	e.names[placeholder] = attr
	return placeholder
}

// value is the placeholder for a value, made once per value.
func (e *expression) value(v any) (string, error) {
	av, err := attributeOf(v)
	if err != nil {
		return "", err
	}
	if e.values == nil {
		e.values = map[string]ddbtypes.AttributeValue{}
	}
	placeholder := ":v" + strconv.Itoa(len(e.values))
	e.values[placeholder] = av
	return placeholder, nil
}

// applyTo hands the names and values to a scan.
func (e *expression) applyTo(in *dynamodb.ScanInput) {
	if len(e.names) > 0 {
		in.ExpressionAttributeNames = e.names
	}
	if len(e.values) > 0 {
		in.ExpressionAttributeValues = e.values
	}
}

// filters renders the grid's filters, joined by AND, or "" where there are
// none.
func (e *expression) filters(filters []source.Filter) (string, error) {
	var parts []string
	for _, f := range filters {
		clause, err := e.filter(f)
		if err != nil {
			return "", err
		}
		parts = append(parts, clause)
	}
	return strings.Join(parts, " AND "), nil
}

var comparisons = map[source.FilterOp]string{
	source.OpEqual: "=", source.OpNotEqual: "<>", source.OpLess: "<",
	source.OpLessEqual: "<=", source.OpGreater: ">", source.OpGreaterEqual: ">=",
}

// filter renders one predicate, with the PostgreSQL driver's semantics where
// DynamoDB has them: a filter a person builds in the grid means the same on
// every engine, and where it cannot mean the same it is refused rather than
// meaning something near it.
func (e *expression) filter(f source.Filter) (string, error) {
	if f.Column == "" {
		return "", errors.New("dynamodb: a filter needs an attribute")
	}
	col := e.name(f.Column)
	arity := func(n int) error {
		if len(f.Values) != n {
			return fmt.Errorf("dynamodb: filter %q on %q takes %d value(s), got %d",
				f.Op, f.Column, n, len(f.Values))
		}
		return nil
	}
	var clause string
	switch f.Op {
	case source.OpEqual, source.OpNotEqual, source.OpLess, source.OpLessEqual,
		source.OpGreater, source.OpGreaterEqual:
		if err := arity(1); err != nil {
			return "", err
		}
		if f.Values[0] == nil {
			// An attribute set to NULL and an attribute nobody wrote are two
			// different things here, and the grid shows both as empty. So
			// equality with the empty a person picked means either of them,
			// and ordering against it means nothing.
			switch f.Op {
			case source.OpEqual:
				clause = "(attribute_not_exists(" + col + ") OR attribute_type(" + col + ", :null))"
				e.literal(":null", &ddbtypes.AttributeValueMemberS{Value: "NULL"})
			case source.OpNotEqual:
				clause = "(attribute_exists(" + col + ") AND NOT attribute_type(" + col + ", :null))"
				e.literal(":null", &ddbtypes.AttributeValueMemberS{Value: "NULL"})
			default:
				return "", fmt.Errorf("dynamodb: cannot order-compare %q with nothing", f.Column)
			}
			break
		}
		val, err := e.value(f.Values[0])
		if err != nil {
			return "", err
		}
		clause = col + " " + comparisons[f.Op] + " " + val
	case source.OpContains, source.OpLike, source.OpNotLike:
		if err := arity(1); err != nil {
			return "", err
		}
		text, ok := f.Values[0].(string)
		if !ok {
			text = fmt.Sprint(f.Values[0])
		}
		if f.Op == source.OpContains {
			val, err := e.value(text)
			if err != nil {
				return "", err
			}
			// contains() is the only text search a filter expression has: no
			// case folding and no patterns. The grid's contains filter means
			// exactly this, so this is what it becomes.
			clause = "contains(" + col + ", " + val + ")"
			break
		}
		// LIKE is a pattern language, and DynamoDB has none: begins_with and
		// contains are the whole of it. A pattern whose only wildcard is a
		// trailing % is begins_with and nothing is lost; anything else would
		// have to be matched somewhere it cannot be, and pretending otherwise
		// would show unfiltered items as filtered (REQ-DRV-3).
		prefix, ok := prefixOf(text)
		if !ok {
			return "", fmt.Errorf("dynamodb: %q is a pattern, and the only one here is a prefix: "+
				"text ending in %% matches what begins with it", text)
		}
		val, err := e.value(prefix)
		if err != nil {
			return "", err
		}
		clause = "begins_with(" + col + ", " + val + ")"
		if f.Op == source.OpNotLike {
			clause = "NOT " + clause
		}
	case source.OpRegex:
		return "", errors.New("dynamodb: regular-expression filters are not supported")
	case source.OpIsNull:
		if err := arity(0); err != nil {
			return "", err
		}
		clause = "attribute_not_exists(" + col + ")"
	case source.OpIsNotNull:
		if err := arity(0); err != nil {
			return "", err
		}
		clause = "attribute_exists(" + col + ")"
	case source.OpBetween:
		if err := arity(2); err != nil {
			return "", err
		}
		low, err := e.value(f.Values[0])
		if err != nil {
			return "", err
		}
		high, err := e.value(f.Values[1])
		if err != nil {
			return "", err
		}
		clause = col + " BETWEEN " + low + " AND " + high
	case source.OpIn:
		c, err := e.in(col, f.Values, false)
		if err != nil {
			return "", err
		}
		clause = c
	case source.OpNotIn:
		c, err := e.in(col, f.Values, true)
		if err != nil {
			return "", err
		}
		clause = c
	default:
		return "", fmt.Errorf("dynamodb: unsupported filter operator %q", f.Op)
	}
	if f.Negate {
		clause = "NOT (" + clause + ")"
	}
	return clause, nil
}

// literal puts a value in under a name of its own, for the few clauses that
// need one the grid did not supply.
func (e *expression) literal(placeholder string, av ddbtypes.AttributeValue) {
	if e.values == nil {
		e.values = map[string]ddbtypes.AttributeValue{}
	}
	e.values[placeholder] = av
}

// in renders IN and NOT IN with a picklist's meaning: an empty stands for an
// attribute that is not there, and NOT IN keeps the items that have not got
// it unless empty is listed (see the PostgreSQL driver's in for the
// reasoning).
//
// DynamoDB's IN takes at most a hundred operands, which is the service's
// limit and is said here rather than sent and refused.
func (e *expression) in(col string, vals []any, negate bool) (string, error) {
	var marks []string
	hasNil := false
	for _, v := range vals {
		if v == nil {
			hasNil = true
			continue
		}
		mark, err := e.value(v)
		if err != nil {
			return "", err
		}
		marks = append(marks, mark)
	}
	if len(marks) > maxIn {
		return "", fmt.Errorf("dynamodb: a filter can list at most %d values, and this one lists %d",
			maxIn, len(marks))
	}
	list := strings.Join(marks, ", ")
	missing := "attribute_not_exists(" + col + ")"
	if !negate {
		switch {
		case len(marks) > 0 && hasNil:
			return "(" + col + " IN (" + list + ") OR " + missing + ")", nil
		case len(marks) > 0:
			return col + " IN (" + list + ")", nil
		case hasNil:
			return missing, nil
		}
		// An empty picklist selects nothing, and the only way to say so is a
		// comparison that cannot hold: an attribute both existing and not.
		return "(attribute_exists(" + col + ") AND " + missing + ")", nil
	}
	switch {
	case len(marks) > 0 && hasNil:
		// Excluding some values and also the items that have not got the
		// attribute: both halves are sayable, so both are said.
		return "(NOT " + col + " IN (" + list + ") AND attribute_exists(" + col + "))", nil
	case len(marks) > 0:
		return "(NOT " + col + " IN (" + list + ") OR " + missing + ")", nil
	case hasNil:
		return "attribute_exists(" + col + ")", nil
	}
	// Excluding nothing keeps everything, including the items without the
	// attribute at all.
	return "(attribute_exists(" + col + ") OR " + missing + ")", nil
}

// maxIn is how many values DynamoDB's IN takes.
const maxIn = 100

// prefixOf reads a LIKE pattern that is a prefix and nothing more: text whose
// only wildcard is a % at the end, with no _ anywhere. It reports false for
// every other pattern, which is what makes a pattern DynamoDB cannot match a
// refusal rather than a wrong answer.
func prefixOf(pattern string) (string, bool) {
	if !strings.HasSuffix(pattern, "%") {
		return "", false
	}
	head := strings.TrimSuffix(pattern, "%")
	if strings.ContainsAny(head, "%_") {
		return "", false
	}
	return head, true
}
