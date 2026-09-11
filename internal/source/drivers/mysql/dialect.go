package mysql

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/sqlscript"
	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

// DefaultPageSize is the browse window when none is asked for (NFR-P11).
const DefaultPageSize = 1000

type dialect struct{}

// QuoteIdentifier backquotes a name, doubling any backquote inside it.
func (dialect) QuoteIdentifier(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

// QualifyRef names an object with its database: `db`.`table`.
func (d dialect) QualifyRef(ref model.ObjectRef) string {
	if len(ref.Path) >= 2 {
		return d.QuoteIdentifier(ref.Path[0]) + "." + d.QuoteIdentifier(ref.Path[1])
	}
	return d.QuoteIdentifier(ref.Name())
}

func (dialect) Placeholder(int) string { return "?" }

// SplitScript honours DELIMITER, which is how MySQL scripts keep a routine's
// BEGIN … END body in one piece.
func (dialect) SplitScript(script string) []source.ScriptStatement {
	return sqlscript.SplitDelimited(sqllex.MySQL, script)
}

func (dialect) Classify(statement string) source.Access { return classify(statement) }

// BuildBrowse renders the statement a Browse runs.
func (d dialect) BuildBrowse(ref model.ObjectRef, opt source.BrowseOptions) (source.Statement, error) {
	if !browsableKinds[ref.Kind] {
		return source.Statement{}, fmt.Errorf("mysql: %s is not browsable", ref)
	}
	if opt.Seek != nil || opt.Follow {
		return source.Statement{}, errors.New("mysql: seek and follow apply only to stream sources")
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
	for i, s := range opt.Sorts {
		if i == 0 {
			sb.WriteString(" ORDER BY ")
		} else {
			sb.WriteString(", ")
		}
		col := d.QuoteIdentifier(s.Column)
		// MySQL has no NULLS FIRST/LAST. Sorting on "IS NULL" first puts the
		// NULLs where asked, and keeps them there when the sort is flipped.
		nulls := " ASC"
		if s.NullsFirst {
			nulls = " DESC"
		}
		sb.WriteString(col + " IS NULL" + nulls + ", " + col)
		if s.Descending {
			sb.WriteString(" DESC")
		}
	}
	limit := opt.Limit
	if limit <= 0 {
		limit = DefaultPageSize
	}
	sb.WriteString(" LIMIT " + b.bind(limit))
	if opt.Offset > 0 {
		sb.WriteString(" OFFSET " + b.bind(opt.Offset))
	}
	return d.readsOnly(source.Statement{SQL: sb.String(), Args: b.args}, opt)
}

type builder struct {
	d    dialect
	args []any
}

func (b *builder) bind(v any) string {
	b.args = append(b.args, v)
	return "?"
}

var comparisons = map[source.FilterOp]string{
	source.OpEqual: "=", source.OpNotEqual: "<>", source.OpLess: "<",
	source.OpLessEqual: "<=", source.OpGreater: ">", source.OpGreaterEqual: ">=",
}

// likeEscape is the LIKE escape character. Not a backslash: what a backslash
// means inside a MySQL string literal depends on sql_mode, and '\\' breaks
// under NO_BACKSLASH_ESCAPES.
const likeEscape = "!"

// filter renders one predicate, meaning what it means on every engine.
func (b *builder) filter(f source.Filter) (string, error) {
	col := b.d.QuoteIdentifier(f.Column)
	arity := func(n int) error {
		if len(f.Values) != n {
			return fmt.Errorf("mysql: filter %q on %q takes %d value(s), got %d", f.Op, f.Column, n, len(f.Values))
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
				return "", fmt.Errorf("mysql: cannot order-compare %q with NULL", f.Column)
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
		clause = "CAST(" + col + " AS CHAR)" + op + b.bind(f.Values[0])
	case source.OpContains:
		if err := arity(1); err != nil {
			return "", err
		}
		// The default collations ignore case already; the user's text is
		// escaped, so "50%" is not a pattern.
		needle := "%" + escapeLike(fmt.Sprint(f.Values[0])) + "%"
		clause = "CAST(" + col + " AS CHAR) LIKE " + b.bind(needle) + " ESCAPE '" + likeEscape + "'"
	case source.OpRegex:
		if err := arity(1); err != nil {
			return "", err
		}
		clause = "CAST(" + col + " AS CHAR) REGEXP " + b.bind(f.Values[0])
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
		return "", fmt.Errorf("mysql: unsupported filter operator %q", f.Op)
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
		return "FALSE"
	}
	switch {
	case len(marks) > 0 && hasNil:
		return col + " NOT IN (" + list + ")"
	case len(marks) > 0:
		return "(" + col + " NOT IN (" + list + ") OR " + col + " IS NULL)"
	case hasNil:
		return col + " IS NOT NULL"
	}
	return "TRUE"
}

func escapeLike(s string) string {
	return strings.NewReplacer(likeEscape, likeEscape+likeEscape, "%", likeEscape+"%", "_", likeEscape+"_").Replace(s)
}

func contains(s, sub string) bool { return strings.Contains(strings.ToLower(s), sub) }
func lower(s string) string       { return strings.ToLower(s) }

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
		cond, err := sqlscript.Predicate(sqllex.MySQL, opt.Where)
		if err != nil {
			return fmt.Errorf("mysql: %w", err)
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
		return source.Statement{}, errors.New("mysql: a WHERE clause may only read, and this one could change something")
	}
	return st, nil
}

// buildDistinct renders a column's distinct values among the rows the
// filters select, most frequent first (source.DistinctLister).
func (d dialect) buildDistinct(ref model.ObjectRef, column string, opt source.BrowseOptions, limit int) (source.Statement, error) {
	if !browsableKinds[ref.Kind] {
		return source.Statement{}, fmt.Errorf("mysql: %s is not browsable", ref)
	}
	if limit <= 0 {
		return source.Statement{}, errors.New("mysql: a list of distinct values needs a limit")
	}
	b := &builder{d: d}
	col := d.QuoteIdentifier(column)
	var sb strings.Builder
	sb.WriteString("SELECT " + col + ", count(*) FROM " + d.QualifyRef(ref))
	if err := b.where(&sb, opt); err != nil {
		return source.Statement{}, err
	}
	sb.WriteString(" GROUP BY " + col + " ORDER BY count(*) DESC, " + col + " LIMIT " + b.bind(int64(limit)))
	return d.readsOnly(source.Statement{SQL: sb.String(), Args: b.args}, opt)
}
