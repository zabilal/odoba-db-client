package trino

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	tr "github.com/trinodb/trino-go-client/trino"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/sqlscript"
	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

// Writing Trino's SQL (ARCH-2, NFR-S6).
//
// Every identifier that reaches a statement goes through QuoteIdentifier,
// and every value through a placeholder. Nothing else here writes SQL.

// DefaultPageSize is the browse window when none is asked for (NFR-P11).
const DefaultPageSize = 1000

type dialect struct{}

// QuoteIdentifier double-quotes a name, doubling any quote inside it, which
// is the standard's way and Trino's only one.
func (dialect) QuoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// QualifyRef names an object in full: "catalog"."schema"."table". Always in
// full, because what a bare name means depends on a session this statement
// may not be running in.
func (d dialect) QualifyRef(ref model.ObjectRef) string {
	path := ref.Path
	if ref.Kind == model.KindColumn && len(path) > 3 {
		path = path[:3]
	}
	quoted := make([]string, len(path))
	for i, p := range path {
		quoted[i] = d.QuoteIdentifier(p)
	}
	return strings.Join(quoted, ".")
}

func (dialect) Placeholder(int) string { return "?" }

func (dialect) Classify(statement string) source.Access { return classify(statement) }

// SplitScript divides a script at its semicolons. Trino has no procedural
// body to keep whole: a statement is a statement.
func (dialect) SplitScript(script string) []source.ScriptStatement { return splitScript(script) }

// splitScript is SplitScript, where a dialect value is not to hand.
func splitScript(script string) []source.ScriptStatement {
	return sqlscript.Split(sqllex.Trino, script, nil)
}

var browsableKinds = map[model.ObjectKind]bool{model.KindTable: true, model.KindView: true}

// browsable reports whether a ref names something with rows: a table or a
// view, by its catalog, its schema and its own name.
func browsable(ref model.ObjectRef) bool { return browsableKinds[ref.Kind] && len(ref.Path) == 3 }

// builder collects the values a statement binds, so that nothing a person
// typed is ever written into it (NFR-S6).
type builder struct {
	d    dialect
	args []any
}

// bind takes a value and answers the mark that stands for it.
//
// The client writes each value as a literal of the type Go gave it, so the
// type has to be the one meant: an exact number is passed as one rather than
// as the text that carries it, or Trino is asked to compare a decimal with a
// varchar and refuses.
func (b *builder) bind(v any) string {
	switch x := v.(type) {
	case model.Decimal:
		v = tr.Numeric(x)
	case model.JSON:
		v = string(x)
	}
	b.args = append(b.args, v)
	return "?"
}

// BuildBrowse renders the statement a Browse runs.
func (d dialect) BuildBrowse(ref model.ObjectRef, opt source.BrowseOptions) (source.Statement, error) {
	if !browsable(ref) {
		return source.Statement{}, fmt.Errorf("trino: %s is not browsable", ref)
	}
	if opt.Seek != nil || opt.Follow {
		return source.Statement{}, errors.New("trino: seek and follow apply only to stream sources")
	}
	b := &builder{d: d}
	var sb strings.Builder
	sb.WriteString("SELECT ")
	if len(opt.Columns) == 0 {
		sb.WriteString("*")
	} else {
		for i, c := range opt.Columns {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(d.QuoteIdentifier(c))
		}
	}
	sb.WriteString(" FROM " + d.QualifyRef(ref))
	if err := b.where(&sb, opt); err != nil {
		return source.Statement{}, err
	}
	d.orderBy(&sb, opt.Sorts)
	limit := opt.Limit
	if limit <= 0 {
		limit = DefaultPageSize
	}
	// OFFSET before LIMIT, which is the order Trino reads them in. Both are
	// numbers this wrote and nothing anybody typed, so they are written
	// rather than bound.
	if opt.Offset > 0 {
		sb.WriteString(" OFFSET " + strconv.FormatInt(opt.Offset, 10))
	}
	sb.WriteString(" LIMIT " + strconv.FormatInt(limit, 10))
	return source.Statement{SQL: sb.String(), Args: b.args}, nil
}

// orderBy writes the sort. Trino says where the NULLs go in the standard's
// words, so there is no expression to stand in for it.
func (d dialect) orderBy(sb *strings.Builder, sorts []source.Sort) {
	for i, s := range sorts {
		if i == 0 {
			sb.WriteString(" ORDER BY ")
		} else {
			sb.WriteString(", ")
		}
		sb.WriteString(d.QuoteIdentifier(s.Column))
		if s.Descending {
			sb.WriteString(" DESC")
		}
		if s.NullsFirst {
			sb.WriteString(" NULLS FIRST")
		} else {
			sb.WriteString(" NULLS LAST")
		}
	}
}

var comparisons = map[source.FilterOp]string{
	source.OpEqual: "=", source.OpNotEqual: "<>", source.OpLess: "<",
	source.OpLessEqual: "<=", source.OpGreater: ">", source.OpGreaterEqual: ">=",
}

// text renders a column as something LIKE and regexp_like can work on.
// Nearly every Trino type has a text form, and a column of numbers searched
// for "50" is what somebody typing into the filter bar meant.
func (dialect) text(col string) string { return "CAST(" + col + " AS varchar)" }

// escaped is the clause that makes a backslash the escape in a LIKE. Trino
// has none unless one is named.
const escaped = ` ESCAPE '\'`

// filter renders one predicate, meaning what it means on every engine.
func (b *builder) filter(f source.Filter) (string, error) {
	if f.Column == "" {
		return "", errors.New("trino: a filter needs a column")
	}
	col := b.d.QuoteIdentifier(f.Column)
	arity := func(n int) error {
		if len(f.Values) != n {
			return fmt.Errorf("trino: filter %q on %q takes %d value(s), got %d", f.Op, f.Column, n, len(f.Values))
		}
		return nil
	}
	var clause string
	switch f.Op {
	case source.OpEqual, source.OpNotEqual, source.OpLess, source.OpLessEqual, source.OpGreater, source.OpGreaterEqual:
		if err := arity(1); err != nil {
			return "", err
		}
		if f.Values[0] == nil {
			switch f.Op {
			case source.OpEqual:
				clause = col + " IS NULL"
			case source.OpNotEqual:
				clause = col + " IS NOT NULL"
			default:
				return "", fmt.Errorf("trino: cannot order-compare %q with NULL", f.Column)
			}
			break
		}
		clause = col + " " + comparisons[f.Op] + " " + b.bind(f.Values[0])
	case source.OpLike, source.OpNotLike:
		if err := arity(1); err != nil {
			return "", err
		}
		op := " LIKE "
		if f.Op == source.OpNotLike {
			op = " NOT LIKE "
		}
		clause = b.d.text(col) + op + b.bind(fmt.Sprint(f.Values[0])) + escaped
	case source.OpContains:
		if err := arity(1); err != nil {
			return "", err
		}
		// Both sides lowered, because a search somebody types is not about
		// case and Trino has no LIKE that ignores it; and the text is
		// escaped, so "50%" is not a pattern.
		needle := "%" + escapeLike(strings.ToLower(fmt.Sprint(f.Values[0]))) + "%"
		clause = "lower(" + b.d.text(col) + ") LIKE " + b.bind(needle) + escaped
	case source.OpRegex:
		if err := arity(1); err != nil {
			return "", err
		}
		clause = "regexp_like(" + b.d.text(col) + ", " + b.bind(fmt.Sprint(f.Values[0])) + ")"
	case source.OpIsNull:
		if err := arity(0); err != nil {
			return "", err
		}
		clause = col + " IS NULL"
	case source.OpIsNotNull:
		if err := arity(0); err != nil {
			return "", err
		}
		clause = col + " IS NOT NULL"
	case source.OpBetween:
		if err := arity(2); err != nil {
			return "", err
		}
		clause = col + " BETWEEN " + b.bind(f.Values[0]) + " AND " + b.bind(f.Values[1])
	case source.OpIn:
		clause = b.in(col, f.Values, false)
	case source.OpNotIn:
		clause = b.in(col, f.Values, true)
	default:
		return "", fmt.Errorf("trino: unsupported filter operator %q", f.Op)
	}
	if f.Negate {
		clause = "NOT (" + clause + ")"
	}
	return clause, nil
}

// in renders IN and NOT IN with a picklist's meaning: a NULL in the list is a
// value somebody picked, where SQL's IN would make it match nothing and its
// NOT IN would make it exclude everything.
func (b *builder) in(col string, vals []any, negate bool) string {
	var marks []string
	hasNil := false
	for _, v := range vals {
		if v == nil {
			hasNil = true
			continue
		}
		marks = append(marks, b.bind(v))
	}
	list := strings.Join(marks, ", ")
	if !negate {
		switch {
		case len(marks) > 0 && hasNil:
			return "(" + col + " IN (" + list + ") OR " + col + " IS NULL)"
		case len(marks) > 0:
			return col + " IN (" + list + ")"
		case hasNil:
			return col + " IS NULL"
		}
		return "1 = 0"
	}
	switch {
	case len(marks) > 0 && hasNil:
		return col + " NOT IN (" + list + ")"
	case len(marks) > 0:
		return "(" + col + " NOT IN (" + list + ") OR " + col + " IS NULL)"
	case hasNil:
		return col + " IS NOT NULL"
	}
	return "1 = 1"
}

// escapeLike makes a literal of the user's text, for a LIKE whose escape is
// the backslash.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(s)
}

// where renders the filters and the typed condition, joined by AND.
//
// What bounds the typed condition is the parser, not the classifier: a SELECT
// cannot be made to write, whatever is put in its WHERE. Predicate is what
// keeps it to one condition, with its brackets closed and its comments ended
// (FR-3.6).
func (b *builder) where(sb *strings.Builder, opt source.BrowseOptions) error {
	join := " WHERE "
	for _, f := range opt.Filters {
		clause, err := b.filter(f)
		if err != nil {
			return err
		}
		sb.WriteString(join + clause)
		join = " AND "
	}
	if opt.Where != "" {
		cond, err := sqlscript.Predicate(sqllex.Trino, opt.Where)
		if err != nil {
			return fmt.Errorf("trino: %w", err)
		}
		sb.WriteString(join + cond)
	}
	return nil
}

// buildDistinct renders a column's distinct values among the rows the
// filters select, most frequent first (source.DistinctLister).
func (d dialect) buildDistinct(ref model.ObjectRef, column string, opt source.BrowseOptions, limit int) (source.Statement, error) {
	if !browsable(ref) {
		return source.Statement{}, fmt.Errorf("trino: %s is not browsable", ref)
	}
	if limit <= 0 {
		return source.Statement{}, errors.New("trino: a list of distinct values needs a limit")
	}
	b := &builder{d: d}
	col := d.QuoteIdentifier(column)
	var sb strings.Builder
	sb.WriteString("SELECT " + col + ", count(*) FROM " + d.QualifyRef(ref))
	if err := b.where(&sb, opt); err != nil {
		return source.Statement{}, err
	}
	sb.WriteString(" GROUP BY " + col + " ORDER BY count(*) DESC, " + col +
		" NULLS LAST LIMIT " + strconv.Itoa(limit))
	return source.Statement{SQL: sb.String(), Args: b.args}, nil
}
