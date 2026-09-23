package oracle

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/sqlscript"
	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

// Writing Oracle's own SQL (ARCH-2, NFR-S6).
//
// Every identifier that reaches a statement goes through QuoteIdentifier,
// and every value through a placeholder. Nothing else here writes SQL.

// DefaultPageSize is the browse window when none is asked for (NFR-P11).
const DefaultPageSize = 1000

type dialect struct{}

// QuoteIdentifier writes a name in double quotes, doubling any inside it.
//
// Always quoted, never bare: an unquoted name folds to upper case, and a
// name this application was given is the name it was given.
func (dialect) QuoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// QualifyRef names an object with its schema: "SCHEMA"."TABLE".
func (d dialect) QualifyRef(ref model.ObjectRef) string {
	if len(ref.Path) >= 2 {
		return d.QuoteIdentifier(ref.Path[0]) + "." + d.QuoteIdentifier(ref.Path[1])
	}
	return d.QuoteIdentifier(ref.Name())
}

// Placeholder is :1, :2, and so on.
func (dialect) Placeholder(i int) string { return ":" + strconv.Itoa(i) }

func (dialect) Classify(statement string) source.Access { return classify(statement) }

// builder collects the values a statement binds, so that nothing a person
// typed is ever written into it (NFR-S6).
type builder struct {
	d    dialect
	args []any
}

func (b *builder) bind(v any) string {
	b.args = append(b.args, bindable(v))
	mark := b.d.Placeholder(len(b.args))
	if _, ok := v.(time.Time); ok {
		// A time is bound carrying a zone, and Oracle compares a DATE or a
		// TIMESTAMP with one by reading the column in the session's zone.
		// Casting the value strips the zone from the comparison, so a row
		// read from the grid is found again by the value it was read as.
		return "CAST(" + mark + " AS TIMESTAMP)"
	}
	return mark
}

// bindable is a value as the driver binds it. An exact number and a
// document travel as their own text, which is how they arrived and how
// Oracle reads them back into the column's type (ADR-0026).
func bindable(v any) any {
	switch x := v.(type) {
	case model.Decimal:
		return string(x)
	case model.JSON:
		return string(x)
	}
	return v
}

// BuildBrowse renders the statement a Browse runs.
func (d dialect) BuildBrowse(ref model.ObjectRef, opt source.BrowseOptions) (source.Statement, error) {
	if !browsableKinds[ref.Kind] {
		return source.Statement{}, fmt.Errorf("oracle: %s is not browsable", ref)
	}
	if opt.Seek != nil || opt.Follow {
		return source.Statement{}, errors.New("oracle: seek and follow apply only to stream sources")
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
			sb.WriteString(d.column(c))
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
	sb.WriteString(" OFFSET " + b.bind(max(opt.Offset, 0)) + " ROWS FETCH NEXT " + b.bind(limit) + " ROWS ONLY")
	return d.readsOnly(source.Statement{SQL: sb.String(), Args: b.args}, opt)
}

// column writes a name a browse selects. ROWID is the row's address rather
// than one of its columns, and is the one name here that is not quoted:
// quoting it would name a column somebody would have had to make.
func (d dialect) column(name string) string {
	if isRowID(name) {
		return rowIDColumn
	}
	return d.QuoteIdentifier(name)
}

// rowIDColumn is how a row's address is selected and named, so that a grid
// reads it as a column like any other.
const rowIDColumn = `ROWID AS "ROWID"`

// rowIDName is what that column is called.
const rowIDName = "ROWID"

func isRowID(name string) bool { return strings.EqualFold(name, rowIDName) }

// orderBy writes the sort. Oracle says where the NULLs go in the standard's
// own words, so there is no expression standing in for it.
func (d dialect) orderBy(sb *strings.Builder, sorts []source.Sort) {
	for i, s := range sorts {
		if i == 0 {
			sb.WriteString(" ORDER BY ")
		} else {
			sb.WriteString(", ")
		}
		if isRowID(s.Column) {
			sb.WriteString("ROWID")
		} else {
			sb.WriteString(d.QuoteIdentifier(s.Column))
		}
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

// likeEscape is the LIKE escape character. Not a backslash: what a
// backslash means in a pattern depends on the session, and this does not.
const likeEscape = "!"

// text renders a column as something LIKE can work on. A number, a date and
// a character column all have a text form; a large object does not have one
// this can ask for, and the server says so in its own words.
func (d dialect) text(col string) string { return "TO_CHAR(" + col + ")" }

// filter renders one predicate, meaning what it means on every engine.
func (b *builder) filter(f source.Filter) (string, error) {
	col := b.d.QuoteIdentifier(f.Column)
	if isRowID(f.Column) {
		col = "ROWID"
	}
	arity := func(n int) error {
		if len(f.Values) != n {
			return fmt.Errorf("oracle: filter %q on %q takes %d value(s), got %d", f.Op, f.Column, n, len(f.Values))
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
				return "", fmt.Errorf("oracle: cannot order-compare %q with NULL", f.Column)
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
		// Oracle's LIKE is about case and a search somebody types is not,
		// so both sides are lowered; the text is escaped, so "50%" is not a
		// pattern.
		needle := "%" + escapeLike(strings.ToLower(fmt.Sprint(f.Values[0]))) + "%"
		clause = "LOWER(" + b.d.text(col) + ") LIKE " + b.bind(needle) + " ESCAPE '" + likeEscape + "'"
	case source.OpRegex:
		if err := arity(1); err != nil {
			return "", err
		}
		clause = "REGEXP_LIKE(" + b.d.text(col) + ", " + b.bind(f.Values[0]) + ")"
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
		return "", fmt.Errorf("oracle: unsupported filter operator %q", f.Op)
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

func escapeLike(s string) string {
	return strings.NewReplacer(likeEscape, likeEscape+likeEscape,
		"%", likeEscape+"%", "_", likeEscape+"_").Replace(s)
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
		cond, err := sqlscript.Predicate(sqllex.Oracle, opt.Where)
		if err != nil {
			return fmt.Errorf("oracle: %w", err)
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
		return source.Statement{}, errors.New("oracle: a WHERE clause may only read, and this one could change something")
	}
	return st, nil
}

// buildDistinct renders a column's distinct values among the rows the
// filters select, most frequent first (source.DistinctLister).
func (d dialect) buildDistinct(ref model.ObjectRef, column string, opt source.BrowseOptions, limit int) (source.Statement, error) {
	if !browsableKinds[ref.Kind] {
		return source.Statement{}, fmt.Errorf("oracle: %s is not browsable", ref)
	}
	if limit <= 0 {
		return source.Statement{}, errors.New("oracle: a list of distinct values needs a limit")
	}
	b := &builder{d: d}
	col := d.QuoteIdentifier(column)
	var sb strings.Builder
	sb.WriteString("SELECT " + col + ", COUNT(*) FROM " + d.QualifyRef(ref))
	if err := b.where(&sb, opt); err != nil {
		return source.Statement{}, err
	}
	sb.WriteString(" GROUP BY " + col + " ORDER BY COUNT(*) DESC, " + col +
		" NULLS LAST FETCH NEXT " + b.bind(int64(limit)) + " ROWS ONLY")
	return d.readsOnly(source.Statement{SQL: sb.String(), Args: b.args}, opt)
}
