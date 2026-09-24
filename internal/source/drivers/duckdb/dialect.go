//go:build duckdb

package duckdb

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/sqlscript"
	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

// Writing DuckDB's SQL (ARCH-2, NFR-S6).
//
// Every identifier that reaches a statement goes through QuoteIdentifier
// and every value through a placeholder. Nothing else in this driver
// assembles statement text.

// DefaultPageSize bounds a browse that did not ask for a size (NFR-P11).
const DefaultPageSize = 1000

// dialect writes statements. It holds no connection, and the one thing it
// is told is which of a table's columns must be read as their own text —
// a document, which the library decodes into a Go map on the way out and
// cannot be given back (ADR-0146). The zero dialect casts nothing, which
// is what the driver's own statements want.
type dialect struct {
	text map[string]bool
}

var _ source.Dialect = dialect{}

// QuoteIdentifier writes a name in double quotes, doubling any inside it.
//
// Always quoted, never bare: an unquoted name is folded, and a name this
// application was given is the name it was given.
func (dialect) QuoteIdentifier(name string) string {
	name = strings.ReplaceAll(name, "\x00", "")
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// QualifyRef names an object with its database and schema, all three
// parts. A connection here holds every database attached to it, so the
// database is part of the name rather than part of the connection.
func (d dialect) QualifyRef(ref model.ObjectRef) string {
	p := ref.Path
	switch {
	case len(p) >= 3:
		return d.QuoteIdentifier(p[len(p)-3]) + "." +
			d.QuoteIdentifier(p[len(p)-2]) + "." + d.QuoteIdentifier(p[len(p)-1])
	case len(p) == 2:
		return d.QuoteIdentifier(p[0]) + "." + d.QuoteIdentifier(p[1])
	case len(p) == 1:
		return d.QuoteIdentifier(p[0])
	}
	return ""
}

// Placeholder is a question mark: DuckDB binds by position, and the
// values are appended in the order they are written.
func (dialect) Placeholder(int) string { return "?" }

// SplitScript divides a script into statements. See split.go.
func (dialect) SplitScript(script string) []source.ScriptStatement {
	return splitScript(script)
}

// Classify reports what a statement does to the database. See classify.go.
func (dialect) Classify(statement string) source.Access { return classify(statement) }

// browsableKinds are the objects a browse can read rows from. DuckDB's
// materialized views are tables written by a statement, so there is no
// separate kind here.
var browsableKinds = map[model.ObjectKind]bool{
	model.KindTable: true,
	model.KindView:  true,
}

// BuildBrowse renders the SELECT behind a browse.
func (d dialect) BuildBrowse(ref model.ObjectRef, opt source.BrowseOptions) (source.Statement, error) {
	if !browsableKinds[ref.Kind] {
		return source.Statement{}, fmt.Errorf("duckdb: %s is not browsable", ref)
	}
	// REQ-DRV-3: refuse options that cannot be honoured, never ignore them.
	if opt.Seek != nil || opt.Follow {
		return source.Statement{}, errors.New("duckdb: seek and follow apply only to stream sources")
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
			sb.WriteString(d.selected(c))
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
	return d.readsOnly(source.Statement{SQL: sb.String(), Args: b.args}, opt)
}

// selected writes a column as a browse reads it.
//
// A document is read as its own text, and keeps the name it had. The
// library hands a document over as a Go map with its keys sorted and its
// numbers rounded — 1.0 arrives as 1 — and there is no way back from
// that, so the only place to keep the document is in the statement that
// asks for it (ADR-0146).
func (d dialect) selected(name string) string {
	col := d.QuoteIdentifier(name)
	if d.text[name] {
		return col + "::VARCHAR AS " + col
	}
	return col
}

// orderBy writes the sort, always saying where the NULLs go: DuckDB's
// default puts them last ascending and first descending, so flipping a
// sort would otherwise move every NULL row from one end of the grid to
// the other.
func (d dialect) orderBy(sb *strings.Builder, sorts []source.Sort) {
	for i, s := range sorts {
		if i == 0 {
			sb.WriteString(" ORDER BY ")
		} else {
			sb.WriteString(", ")
		}
		sb.WriteString(d.sorted(s.Column))
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

// sorted writes a column a sort names. A row's address is not one of its
// columns and is never quoted.
func (d dialect) sorted(name string) string {
	if isRowID(name) {
		return rowIDName
	}
	return d.QuoteIdentifier(name)
}

// rowIDName is what a row's address is called. It is DuckDB's own name
// for it, hidden from SELECT * and available to anything that asks.
const rowIDName = "rowid"

func isRowID(name string) bool { return name == rowIDName }

// builder collects the values a statement binds, so that nothing a person
// typed is ever written into it (NFR-S6).
type builder struct {
	d    dialect
	args []any
}

func (b *builder) bind(v any) string {
	b.args = append(b.args, v)
	return b.d.Placeholder(len(b.args))
}

var comparisons = map[source.FilterOp]string{
	source.OpEqual: "=", source.OpNotEqual: "<>", source.OpLess: "<",
	source.OpLessEqual: "<=", source.OpGreater: ">", source.OpGreaterEqual: ">=",
}

// filter renders one predicate, meaning what it means on every engine.
func (b *builder) filter(f source.Filter) (string, error) {
	col := b.d.sorted(f.Column)
	arity := func(n int) error {
		if len(f.Values) != n {
			return fmt.Errorf("duckdb: filter %q on %q takes %d value(s), got %d",
				f.Op, f.Column, n, len(f.Values))
		}
		return nil
	}
	var clause string
	switch f.Op {
	case source.OpEqual, source.OpNotEqual, source.OpLess,
		source.OpLessEqual, source.OpGreater, source.OpGreaterEqual:
		if err := arity(1); err != nil {
			return "", err
		}
		if f.Values[0] == nil {
			// "col = NULL" is never true in SQL, so honouring it literally
			// would silently return no rows. NULL is a value somebody can
			// see and pick in the grid, so equality with it means IS NULL.
			switch f.Op {
			case source.OpEqual:
				clause = col + " IS NULL"
			case source.OpNotEqual:
				clause = col + " IS NOT NULL"
			default:
				return "", fmt.Errorf("duckdb: cannot order-compare %q with NULL", f.Column)
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
		clause = col + "::VARCHAR" + op + b.bind(f.Values[0])

	case source.OpContains:
		if err := arity(1); err != nil {
			return "", err
		}
		// The text is escaped so that a per cent somebody typed is a per
		// cent, and the pattern is still bound as a value.
		needle := "%" + escapeLike(fmt.Sprint(f.Values[0])) + "%"
		clause = col + "::VARCHAR ILIKE " + b.bind(needle) + ` ESCAPE '\'`

	case source.OpRegex:
		if err := arity(1); err != nil {
			return "", err
		}
		clause = "regexp_matches(" + col + "::VARCHAR, " + b.bind(f.Values[0]) + ")"

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
		return "", fmt.Errorf("duckdb: unsupported filter operator %q", f.Op)
	}
	if f.Negate {
		clause = "NOT (" + clause + ")"
	}
	return clause, nil
}

// in renders IN and NOT IN with a picklist's meaning: NULL is one of the
// values somebody can tick, and SQL's IN cannot say so by itself.
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
		return "false"
	}
	switch {
	case len(marks) > 0 && hasNil:
		return col + " NOT IN (" + list + ")"
	case len(marks) > 0:
		return "(" + col + " NOT IN (" + list + ") OR " + col + " IS NULL)"
	case hasNil:
		return col + " IS NOT NULL"
	}
	return "true"
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(s)
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
		cond, err := sqlscript.Predicate(sqllex.PostgreSQL, opt.Where)
		if err != nil {
			return fmt.Errorf("duckdb: %w", err)
		}
		sb.WriteString(join + cond)
	}
	return nil
}

// readsOnly refuses a statement whose typed WHERE would change anything: a
// filter reads (FR-3.6).
func (d dialect) readsOnly(st source.Statement, opt source.BrowseOptions) (source.Statement, error) {
	if opt.Where != "" && d.Classify(st.SQL).Mutating() {
		return source.Statement{}, errors.New("duckdb: a WHERE clause may only read, and this one could change something")
	}
	return st, nil
}

// buildDistinct renders a column's distinct values among the rows the
// filters select, most frequent first (source.DistinctLister).
func (d dialect) buildDistinct(ref model.ObjectRef, column string, opt source.BrowseOptions, limit int) (source.Statement, error) {
	if !browsableKinds[ref.Kind] {
		return source.Statement{}, fmt.Errorf("duckdb: %s is not browsable", ref)
	}
	if limit <= 0 {
		return source.Statement{}, errors.New("duckdb: a list of distinct values needs a limit")
	}
	b := &builder{d: d}
	col := d.selected(column)
	var sb strings.Builder
	sb.WriteString("SELECT " + col + ", count(*) FROM " + d.QualifyRef(ref))
	if err := b.where(&sb, opt); err != nil {
		return source.Statement{}, err
	}
	group := d.QuoteIdentifier(column)
	sb.WriteString(" GROUP BY " + group + " ORDER BY count(*) DESC, " + group +
		" NULLS LAST LIMIT " + b.bind(int64(limit)))
	return d.readsOnly(source.Statement{SQL: sb.String(), Args: b.args}, opt)
}
