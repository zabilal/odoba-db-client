package clickhouse

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/sqlscript"
	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

// Writing ClickHouse's own SQL (ARCH-2, NFR-S6).
//
// Every identifier that reaches a statement goes through QuoteIdentifier,
// and every value through a placeholder. Nothing else here writes SQL.

// DefaultPageSize is the browse window when none is asked for (NFR-P11).
const DefaultPageSize = 1000

type dialect struct{}

// QuoteIdentifier backquotes a name, doubling any backquote inside it.
//
// ClickHouse takes either a doubled quote or a backslash before it. The
// doubled form is the one every other engine here uses, and it needs no
// rule about what a backslash means.
func (dialect) QuoteIdentifier(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

// QualifyRef names an object with its database: `db`.`table`. There is no
// schema between the two.
func (d dialect) QualifyRef(ref model.ObjectRef) string {
	if len(ref.Path) >= 2 {
		return d.QuoteIdentifier(ref.Path[0]) + "." + d.QuoteIdentifier(ref.Path[1])
	}
	return d.QuoteIdentifier(ref.Name())
}

func (dialect) Placeholder(int) string { return "?" }

// UpdateStatement writes a change to rows the way ClickHouse takes one: as
// an ALTER, and waited for. A mutation is asynchronous by default, and a
// grid that said a row had changed before it had would be telling whoever
// changed it something untrue (sqlscript.UpdateWriter).
func (dialect) UpdateStatement(table, sets, where string) string {
	return "ALTER TABLE " + table + " UPDATE " + sets + " WHERE " + where +
		" SETTINGS mutations_sync = 1"
}

func (dialect) Classify(statement string) source.Access { return classify(statement) }

// SplitScript divides a script at its semicolons. ClickHouse has no
// procedural body to keep whole: a statement is a statement.
func (dialect) SplitScript(script string) []source.ScriptStatement { return splitScript(script) }

// splitScript is SplitScript, where a dialect value is not to hand.
func splitScript(script string) []source.ScriptStatement {
	return sqlscript.Split(sqllex.ClickHouse, script, nil)
}

// builder collects the values a statement binds, so that nothing a person
// typed is ever written into it (NFR-S6).
type builder struct {
	d    dialect
	args []any
}

func (b *builder) bind(v any) string {
	b.args = append(b.args, v)
	return "?"
}

// BuildBrowse renders the statement a Browse runs.
func (d dialect) BuildBrowse(ref model.ObjectRef, opt source.BrowseOptions) (source.Statement, error) {
	if !browsableKinds[ref.Kind] {
		return source.Statement{}, fmt.Errorf("clickhouse: %s is not browsable", ref)
	}
	if opt.Seek != nil || opt.Follow {
		return source.Statement{}, errors.New("clickhouse: seek and follow apply only to stream sources")
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
	sb.WriteString(" LIMIT " + b.bind(limit))
	if opt.Offset > 0 {
		sb.WriteString(" OFFSET " + b.bind(opt.Offset))
	}
	return source.Statement{SQL: sb.String(), Args: b.args}, nil
}

// orderBy writes the sort. ClickHouse says where the NULLs go in the words
// the standard uses, so there is no expression to stand in for it.
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
	source.OpEqual: "=", source.OpNotEqual: "!=", source.OpLess: "<",
	source.OpLessEqual: "<=", source.OpGreater: ">", source.OpGreaterEqual: ">=",
}

// text renders a column as something LIKE and match() can work on. Every
// ClickHouse type has a text form, and a column of numbers searched for
// "50" is what somebody typing into the filter bar meant.
func (d dialect) text(col string) string { return "toString(" + col + ")" }

// filter renders one predicate, meaning what it means on every engine.
func (b *builder) filter(f source.Filter) (string, error) {
	col := b.d.QuoteIdentifier(f.Column)
	arity := func(n int) error {
		if len(f.Values) != n {
			return fmt.Errorf("clickhouse: filter %q on %q takes %d value(s), got %d", f.Op, f.Column, n, len(f.Values))
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
				return "", fmt.Errorf("clickhouse: cannot order-compare %q with NULL", f.Column)
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
		clause = b.d.text(col) + op + b.bind(f.Values[0])
	case source.OpContains:
		if err := arity(1); err != nil {
			return "", err
		}
		// ILIKE, because a search somebody types is not about case; and the
		// text is escaped, so "50%" is not a pattern.
		needle := "%" + escapeLike(fmt.Sprint(f.Values[0])) + "%"
		clause = b.d.text(col) + " ILIKE " + b.bind(needle)
	case source.OpRegex:
		if err := arity(1); err != nil {
			return "", err
		}
		clause = "match(" + b.d.text(col) + ", " + b.bind(f.Values[0]) + ")"
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
		return "", fmt.Errorf("clickhouse: unsupported filter operator %q", f.Op)
	}
	if f.Negate {
		clause = "NOT (" + clause + ")"
	}
	return clause, nil
}

// in renders IN and NOT IN with a picklist's meaning (see the PostgreSQL
// driver's in).
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

// escapeLike makes a literal of the user's text. ClickHouse's patterns
// escape with a backslash and have no ESCAPE clause to name another.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(s)
}

// where renders the filters and the typed condition, joined by AND.
//
// What bounds the typed condition is the parser, not the classifier: a
// ClickHouse SELECT cannot be made to write, whatever is put in its WHERE,
// so refusing a condition the classifier called mutating would be a rule
// that could never fire. Predicate is what keeps it to one condition, with
// its brackets closed and its comments ended (FR-3.6).
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
		cond, err := sqlscript.Predicate(sqllex.ClickHouse, opt.Where)
		if err != nil {
			return fmt.Errorf("clickhouse: %w", err)
		}
		sb.WriteString(join + cond)
	}
	return nil
}

// buildDistinct renders a column's distinct values among the rows the
// filters select, most frequent first (source.DistinctLister).
func (d dialect) buildDistinct(ref model.ObjectRef, column string, opt source.BrowseOptions, limit int) (source.Statement, error) {
	if !browsableKinds[ref.Kind] {
		return source.Statement{}, fmt.Errorf("clickhouse: %s is not browsable", ref)
	}
	if limit <= 0 {
		return source.Statement{}, errors.New("clickhouse: a list of distinct values needs a limit")
	}
	b := &builder{d: d}
	col := d.QuoteIdentifier(column)
	var sb strings.Builder
	sb.WriteString("SELECT " + col + ", count() FROM " + d.QualifyRef(ref))
	if err := b.where(&sb, opt); err != nil {
		return source.Statement{}, err
	}
	sb.WriteString(" GROUP BY " + col + " ORDER BY count() DESC, " + col +
		" NULLS LAST LIMIT " + b.bind(int64(limit)))
	return source.Statement{SQL: sb.String(), Args: b.args}, nil
}
