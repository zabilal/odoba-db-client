package sqlserver

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/sqlscript"
	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

// Writing SQL Server's own SQL (ARCH-2, NFR-S6).
//
// Every identifier that reaches a statement goes through QuoteIdentifier,
// and every value through a placeholder. Nothing else here writes SQL.

// DefaultPageSize is the browse window when none is asked for (NFR-P11).
const DefaultPageSize = 1000

type dialect struct{}

// QuoteIdentifier writes a name in brackets, which is SQL Server's own way
// and works whatever QUOTED_IDENTIFIER is set to.
//
// A bracket inside a name is doubled, which is how a bracketed name escapes
// one; nothing else needs escaping, because the name ends at the first
// unescaped bracket.
func (dialect) QuoteIdentifier(name string) string {
	return "[" + strings.ReplaceAll(name, "]", "]]") + "]"
}

// QualifyRef writes a name as much as the reference says: the schema and the
// object, and the object alone where there is no schema.
//
// The database a three-part reference names is left off. The statement runs
// on a connection to it already, and naming it again would make a
// cross-database reference of what is not one — which needs a privilege the
// connection may not have.
func (d dialect) QualifyRef(ref model.ObjectRef) string {
	switch len(ref.Path) {
	case 0:
		return ""
	case 1:
		return d.QuoteIdentifier(ref.Path[0])
	}
	return d.QuoteIdentifier(ref.Path[len(ref.Path)-2]) + "." +
		d.QuoteIdentifier(ref.Path[len(ref.Path)-1])
}

// Placeholder is @p1, @p2, and so on: SQL Server binds by name, and the
// driver names the ordinal ones in the order they are given.
func (dialect) Placeholder(i int) string { return "@p" + strconv.Itoa(i) }

func (dialect) Classify(statement string) source.Access { return classify(statement) }

// builder collects the values a statement binds, so that nothing a person
// typed is ever written into it (NFR-S6).
type builder struct {
	d    dialect
	args []any
}

// bind adds a value and answers the placeholder that stands for it.
func (b *builder) bind(v any) string {
	b.args = append(b.args, v)
	return b.d.Placeholder(len(b.args))
}

// BuildBrowse renders the statement a Browse runs.
func (d dialect) BuildBrowse(ref model.ObjectRef, opt source.BrowseOptions) (source.Statement, error) {
	if !browsableKinds[ref.Kind] {
		return source.Statement{}, fmt.Errorf("sqlserver: %s is not browsable", ref)
	}
	if opt.Seek != nil || opt.Follow {
		return source.Statement{}, errors.New("sqlserver: seek and follow apply only to stream sources")
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
	// OFFSET … FETCH is the only paging SQL Server has, and it is part of
	// ORDER BY: there is always one above, even if it orders by nothing.
	limit := opt.Limit
	if limit <= 0 {
		limit = DefaultPageSize
	}
	sb.WriteString(" OFFSET " + b.bind(max(opt.Offset, 0)) + " ROWS FETCH NEXT " + b.bind(limit) + " ROWS ONLY")
	return d.readsOnly(source.Statement{SQL: sb.String(), Args: b.args}, opt)
}

// orderBy writes the sort, and an order over nothing where there is none:
// OFFSET … FETCH requires an ORDER BY, and "(SELECT NULL)" is how T-SQL says
// that the rows may come in whatever order they come in.
func (d dialect) orderBy(sb *strings.Builder, sorts []source.Sort) {
	if len(sorts) == 0 {
		sb.WriteString(" ORDER BY (SELECT NULL)")
		return
	}
	for i, s := range sorts {
		if i == 0 {
			sb.WriteString(" ORDER BY ")
		} else {
			sb.WriteString(", ")
		}
		col := d.QuoteIdentifier(s.Column)
		// SQL Server has no NULLS FIRST/LAST. Sorting on whether the value
		// is null first puts them where they were asked for, and keeps them
		// there when the sort is flipped.
		nulls := " ASC"
		if s.NullsFirst {
			nulls = " DESC"
		}
		sb.WriteString("CASE WHEN " + col + " IS NULL THEN 1 ELSE 0 END" + nulls + ", " + col)
		if s.Descending {
			sb.WriteString(" DESC")
		}
	}
}

var comparisons = map[source.FilterOp]string{
	source.OpEqual: "=", source.OpNotEqual: "<>", source.OpLess: "<",
	source.OpLessEqual: "<=", source.OpGreater: ">", source.OpGreaterEqual: ">=",
}

// likeEscape is the LIKE escape character. Not a backslash, which T-SQL does
// not treat as one, and not a character a pattern is likely to hold.
const likeEscape = "!"

// text casts a column to something LIKE and the comparison operators can
// work on. nvarchar(max) rather than varchar: a column of another alphabet
// compared as varchar loses the characters the code page has no room for.
func (d dialect) text(col string) string { return "CAST(" + col + " AS nvarchar(max))" }

// filter renders one predicate, meaning what it means on every engine.
func (b *builder) filter(f source.Filter) (string, error) {
	col := b.d.QuoteIdentifier(f.Column)
	arity := func(n int) error {
		if len(f.Values) != n {
			return fmt.Errorf("sqlserver: filter %q on %q takes %d value(s), got %d", f.Op, f.Column, n, len(f.Values))
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
				return "", fmt.Errorf("sqlserver: cannot order-compare %q with NULL", f.Column)
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
		// The default collations ignore case already; the user's text is
		// escaped, so "50%" is not a pattern.
		needle := "%" + escapeLike(fmt.Sprint(f.Values[0])) + "%"
		clause = b.d.text(col) + " LIKE " + b.bind(needle) + " ESCAPE '" + likeEscape + "'"
	case source.OpRegex:
		// SQL Server has no regular expressions before 2025, and a filter
		// that silently matched something else would be worse than none.
		return "", errors.New("sqlserver: this server has no regular expressions; use contains or like")
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
		return "", fmt.Errorf("sqlserver: unsupported filter operator %q", f.Op)
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

// escapeLike makes a literal of the user's text. T-SQL's patterns have a
// third wildcard the other engines do not: [abc] matches one of them, so a
// bracket has to be escaped as well as % and _.
func escapeLike(s string) string {
	return strings.NewReplacer(likeEscape, likeEscape+likeEscape,
		"%", likeEscape+"%", "_", likeEscape+"_", "[", likeEscape+"[").Replace(s)
}

// where renders the filters and the typed condition, joined by AND.
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
		cond, err := sqlscript.Predicate(sqllex.SQLServer, opt.Where)
		if err != nil {
			return fmt.Errorf("sqlserver: %w", err)
		}
		sb.WriteString(join + cond)
	}
	return nil
}

// readsOnly refuses a statement whose typed WHERE would change anything: a
// filter reads (FR-3.6). Classify is conservative, so what it cannot vouch
// for is refused too.
func (d dialect) readsOnly(st source.Statement, opt source.BrowseOptions) (source.Statement, error) {
	if opt.Where != "" && d.Classify(st.SQL).Mutating() {
		return source.Statement{}, errors.New("sqlserver: a WHERE clause may only read, and this one could change something")
	}
	return st, nil
}

// buildDistinct renders a column's distinct values among the rows the
// filters select, most frequent first (source.DistinctLister).
func (d dialect) buildDistinct(ref model.ObjectRef, column string, opt source.BrowseOptions, limit int) (source.Statement, error) {
	if !browsableKinds[ref.Kind] {
		return source.Statement{}, fmt.Errorf("sqlserver: %s is not browsable", ref)
	}
	if limit <= 0 {
		return source.Statement{}, errors.New("sqlserver: a list of distinct values needs a limit")
	}
	b := &builder{d: d}
	col := d.QuoteIdentifier(column)
	var sb strings.Builder
	sb.WriteString("SELECT " + col + ", count(*) FROM " + d.QualifyRef(ref))
	if err := b.where(&sb, opt); err != nil {
		return source.Statement{}, err
	}
	// TOP rather than FETCH: the count is not a column of the table, so it
	// cannot be sorted on and paged from in the same breath.
	sb.WriteString(" GROUP BY " + col + " ORDER BY count(*) DESC, " + col +
		" OFFSET 0 ROWS FETCH NEXT " + b.bind(int64(limit)) + " ROWS ONLY")
	return d.readsOnly(source.Statement{SQL: sb.String(), Args: b.args}, opt)
}
