package sqlite

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/sqlscript"
	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

// DefaultPageSize is the browse window when none is asked for (NFR-P11).
const DefaultPageSize = 1000

type dialect struct{}

// QuoteIdentifier double-quotes a name, doubling any quote inside it.
func (dialect) QuoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// QualifyRef names an object with its database: "main"."t".
func (d dialect) QualifyRef(ref model.ObjectRef) string {
	if len(ref.Path) >= 2 {
		return d.QuoteIdentifier(ref.Path[0]) + "." + d.QuoteIdentifier(ref.Path[1])
	}
	return d.QuoteIdentifier(ref.Name())
}

func (dialect) Placeholder(int) string { return "?" }

// SplitScript splits on semicolons outside strings, comments and trigger
// bodies.
func (dialect) SplitScript(script string) []source.ScriptStatement {
	return sqlscript.Split(sqllex.SQLite, script, sqlscript.TriggerBlocks)
}

func (dialect) Classify(statement string) source.Access { return classify(statement) }

// BuildBrowse renders the statement a Browse runs.
func (d dialect) BuildBrowse(ref model.ObjectRef, opt source.BrowseOptions) (source.Statement, error) {
	if !browsableKinds[ref.Kind] {
		return source.Statement{}, fmt.Errorf("sqlite: %s is not browsable", ref)
	}
	if opt.Seek != nil || opt.Follow {
		return source.Statement{}, errors.New("sqlite: seek and follow apply only to stream sources")
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
		sb.WriteString(d.QuoteIdentifier(s.Column))
		if s.Descending {
			sb.WriteString(" DESC")
		}
		// Explicit, so flipping a sort does not move every NULL row from one
		// end of the grid to the other.
		if s.NullsFirst {
			sb.WriteString(" NULLS FIRST")
		} else {
			sb.WriteString(" NULLS LAST")
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

// buildCount renders the count of the rows a browse would return.
func (d dialect) buildCount(ref model.ObjectRef, opt source.BrowseOptions) (source.Statement, error) {
	if !browsableKinds[ref.Kind] {
		return source.Statement{}, fmt.Errorf("sqlite: %s is not browsable", ref)
	}
	b := &builder{d: d}
	var sb strings.Builder
	sb.WriteString("SELECT count(*) FROM " + d.QualifyRef(ref))
	if err := b.where(&sb, opt); err != nil {
		return source.Statement{}, err
	}
	return d.readsOnly(source.Statement{SQL: sb.String(), Args: b.args}, opt)
}

// buildDistinct renders a column's distinct values among the rows the
// filters select, most frequent first (source.DistinctLister).
func (d dialect) buildDistinct(ref model.ObjectRef, column string, opt source.BrowseOptions, limit int) (source.Statement, error) {
	if !browsableKinds[ref.Kind] {
		return source.Statement{}, fmt.Errorf("sqlite: %s is not browsable", ref)
	}
	if limit <= 0 {
		return source.Statement{}, errors.New("sqlite: a list of distinct values needs a limit")
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

type builder struct {
	d    dialect
	args []any
}

func (b *builder) bind(v any) string {
	if t, ok := v.(time.Time); ok {
		b.args = append(b.args, t.UTC().Format("2006-01-02 15:04:05.000"))
		return "strftime('" + timeText + "', ?)"
	}
	b.args = append(b.args, v)
	return "?"
}

// timeText is the one layout both sides of a time comparison are put in.
//
// The driver reads a DATE or DATETIME column's text as a time.Time, and
// writes a time.Time back in a layout of its own, so the date read from
// '2000-01-01' would never equal it again: a date picked from a filter list
// would match no rows. strftime reads every layout SQLite's date functions
// accept, so a time operand and its column are both compared through it.
const timeText = "%Y-%m-%d %H:%M:%f"

// hasTime reports whether any operand is a time.
func hasTime(vals []any) bool {
	for _, v := range vals {
		if _, ok := v.(time.Time); ok {
			return true
		}
	}
	return false
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
		cond, err := sqlscript.Predicate(sqllex.SQLite, opt.Where)
		if err != nil {
			return fmt.Errorf("sqlite: %w", err)
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
		return source.Statement{}, errors.New("sqlite: a WHERE clause may only read, and this one could change something")
	}
	return st, nil
}

var comparisons = map[source.FilterOp]string{
	source.OpEqual: "=", source.OpNotEqual: "<>", source.OpLess: "<",
	source.OpLessEqual: "<=", source.OpGreater: ">", source.OpGreaterEqual: ">=",
}

// filter renders one predicate, with the PostgreSQL driver's semantics: a
// filter a person builds in the grid means the same on every engine.
func (b *builder) filter(f source.Filter) (string, error) {
	col := b.d.QuoteIdentifier(f.Column)
	if hasTime(f.Values) {
		col = "strftime('" + timeText + "', " + col + ")" // see timeText
	}
	arity := func(n int) error {
		if len(f.Values) != n {
			return fmt.Errorf("sqlite: filter %q on %q takes %d value(s), got %d", f.Op, f.Column, n, len(f.Values))
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
			// "= NULL" is never true; equality with the NULL a person picked
			// in the grid means IS NULL. Ordering against NULL means nothing.
			switch f.Op {
			case source.OpEqual:
				clause = col + " IS NULL"
			case source.OpNotEqual:
				clause = col + " IS NOT NULL"
			default:
				return "", fmt.Errorf("sqlite: cannot order-compare %q with NULL", f.Column)
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
		clause = "CAST(" + col + " AS TEXT)" + op + b.bind(f.Values[0])
	case source.OpContains:
		if err := arity(1); err != nil {
			return "", err
		}
		// SQLite's LIKE ignores ASCII case already. The user's text is
		// escaped, so "50%" is not a pattern.
		needle := "%" + escapeLike(fmt.Sprint(f.Values[0])) + "%"
		clause = "CAST(" + col + " AS TEXT) LIKE " + b.bind(needle) + ` ESCAPE '\'`
	case source.OpRegex:
		// SQLite has no REGEXP unless an extension provides it; refusing is
		// honest, ignoring would show unfiltered rows as filtered (REQ-DRV-3).
		return "", errors.New("sqlite: regular-expression filters are not supported")
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
		return "", fmt.Errorf("sqlite: unsupported filter operator %q", f.Op)
	}
	if f.Negate {
		clause = "NOT (" + clause + ")"
	}
	return clause, nil
}

// in renders IN and NOT IN with a picklist's meaning: a nil stands for NULL,
// and NOT IN keeps NULL rows unless nil is listed (see the PostgreSQL
// driver's in for the reasoning).
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
		return "0" // an empty picklist selects nothing
	}
	switch {
	case len(marks) > 0 && hasNil:
		return col + " NOT IN (" + list + ")"
	case len(marks) > 0:
		return "(" + col + " NOT IN (" + list + ") OR " + col + " IS NULL)"
	case hasNil:
		return col + " IS NOT NULL"
	}
	return "1" // excluding nothing keeps everything
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
