package postgres

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

// dialect implements source.Dialect for PostgreSQL.
//
// It holds no state and no connection: every method is a pure function of its
// arguments. That is what lets the SQL this driver emits be tested
// exhaustively without a server, and it is the only place in the driver where
// statement text is assembled (ARCH-2).
type dialect struct{}

var _ source.Dialect = dialect{}

// DefaultPageSize bounds a browse that did not ask for a size. NFR-P11 forbids
// unbounded reads. The grid always supplies a limit; this is the backstop for
// any caller that does not.
const DefaultPageSize = 1000

// QuoteIdentifier renders an identifier as a quoted PostgreSQL name.
//
// Always quoting, rather than quoting only when necessary, is deliberate. It
// preserves case exactly (an unquoted Orders is folded to orders), it makes
// reserved words safe as column names, and it leaves exactly one rule to get
// right instead of a list of reserved words that drifts between versions.
func (dialect) QuoteIdentifier(name string) string {
	// PostgreSQL identifiers cannot contain NUL, and a NUL reaching the wire
	// truncates the statement server-side. Strip it rather than quote it.
	name = strings.ReplaceAll(name, "\x00", "")
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// QualifyRef renders schema.name.
//
// A PostgreSQL connection is bound to one database, so the database element
// of an ObjectRef's path — there for the explorer — is not part of the name.
// Emitting it would produce a three-part name, which PostgreSQL reads as a
// cross-database reference and rejects.
func (d dialect) QualifyRef(ref model.ObjectRef) string {
	p := ref.Path
	switch {
	case len(p) >= 2:
		return d.QuoteIdentifier(p[len(p)-2]) + "." + d.QuoteIdentifier(p[len(p)-1])
	case len(p) == 1:
		return d.QuoteIdentifier(p[0])
	}
	return ""
}

// Placeholder renders PostgreSQL's positional parameter syntax.
func (dialect) Placeholder(i int) string { return "$" + strconv.Itoa(i) }

// SplitScript divides a script into statements. See split.go.
func (dialect) SplitScript(script string) []source.ScriptStatement {
	return splitScript(sqllex.PostgreSQL, script)
}

// Classify reports what a statement does to the server. See classify.go.
func (dialect) Classify(statement string) source.Access {
	return classify(sqllex.PostgreSQL, statement)
}

// browsableKinds are the objects a browse can read rows from.
var browsableKinds = map[model.ObjectKind]bool{
	model.KindTable:            true,
	model.KindView:             true,
	model.KindMaterializedView: true,
}

// BuildBrowse renders the SELECT behind a browse.
//
// Every user-supplied value is bound as a parameter, and every identifier goes
// through QuoteIdentifier (NFR-S6). The statement is exposed rather than kept
// internal because UX principle 6 requires the grid to show the user the SQL
// behind their filters (FR-3.6).
//
// Ordering for stable paging is the caller's responsibility: this builder
// emits exactly the sorts it is given. The source appends a primary-key
// tiebreaker before calling it, because LIMIT/OFFSET over an incompletely
// ordered result can return the same row on two pages.
func (d dialect) BuildBrowse(ref model.ObjectRef, opt source.BrowseOptions) (source.Statement, error) {
	if !browsableKinds[ref.Kind] {
		return source.Statement{}, fmt.Errorf("postgres: %s is not browsable", ref)
	}
	// REQ-DRV-3: refuse options that cannot be honoured, never ignore them. A
	// table has no log position to seek to and nothing to follow.
	if opt.Seek != nil || opt.Follow {
		return source.Statement{}, errors.New("postgres: seek and follow apply only to stream sources")
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
	sb.WriteString(" FROM ")
	sb.WriteString(d.QualifyRef(ref))

	for i, f := range opt.Filters {
		if i == 0 {
			sb.WriteString(" WHERE ")
		} else {
			sb.WriteString(" AND ")
		}
		clause, err := b.filter(f)
		if err != nil {
			return source.Statement{}, err
		}
		sb.WriteString(clause)
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
		// Always explicit. PostgreSQL's default puts NULLs last ascending but
		// FIRST descending, so flipping a sort would otherwise move every NULL
		// row from the bottom of the grid to the top, which reads as data
		// changing rather than order changing.
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
	sb.WriteString(" LIMIT ")
	sb.WriteString(b.bind(limit))
	if opt.Offset > 0 {
		sb.WriteString(" OFFSET ")
		sb.WriteString(b.bind(opt.Offset))
	}

	return source.Statement{SQL: sb.String(), Args: b.args}, nil
}

// builder accumulates bound parameters while a statement is assembled.
type builder struct {
	d    dialect
	args []any
}

func (b *builder) bind(v any) string {
	b.args = append(b.args, v)
	return b.d.Placeholder(len(b.args))
}

var comparisons = map[source.FilterOp]string{
	source.OpEqual:        "=",
	source.OpNotEqual:     "<>",
	source.OpLess:         "<",
	source.OpLessEqual:    "<=",
	source.OpGreater:      ">",
	source.OpGreaterEqual: ">=",
}

// filter renders one predicate.
func (b *builder) filter(f source.Filter) (string, error) {
	col := b.d.QuoteIdentifier(f.Column)
	arity := func(n int) error {
		if len(f.Values) != n {
			return fmt.Errorf("postgres: filter %q on %q takes %d value(s), got %d",
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
			// silently returns no rows. NULL is a value the user can see and
			// pick in the grid (UX principle 7), so equality with it means
			// IS NULL. Ordering against NULL has no meaning at all.
			switch f.Op {
			case source.OpEqual:
				clause = col + " IS NULL"
			case source.OpNotEqual:
				clause = col + " IS NOT NULL"
			default:
				return "", fmt.Errorf("postgres: cannot order-compare %q with NULL", f.Column)
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
		// Cast to text so a pattern works on any column type, which is what a
		// filter row typed by a person expects.
		clause = col + "::text" + op + b.bind(f.Values[0])

	case source.OpContains:
		if err := arity(1); err != nil {
			return "", err
		}
		// Escape the pattern metacharacters in the user's text. Otherwise a
		// search for "50%" matches everything beginning with 50, and "a_b"
		// matches "axb". The pattern is still bound as a value.
		needle := "%" + escapeLike(fmt.Sprint(f.Values[0])) + "%"
		clause = col + "::text ILIKE " + b.bind(needle) + ` ESCAPE '\'`

	case source.OpRegex:
		if err := arity(1); err != nil {
			return "", err
		}
		clause = col + "::text ~ " + b.bind(f.Values[0])

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
		return "", fmt.Errorf("postgres: unsupported filter operator %q", f.Op)
	}

	if f.Negate {
		clause = "NOT (" + clause + ")"
	}
	return clause, nil
}

// in renders IN and NOT IN with the semantics a filter picklist implies, not
// raw SQL's.
//
// SQL's NOT IN excludes NULL rows as a side effect: "NULL NOT IN ('paid')" is
// NULL, not true. So unticking "paid" in a status picklist would also hide
// every row whose status is NULL, which nobody asked for. Here a nil in the
// value list stands for NULL explicitly:
//
//	IN    matches rows whose value is listed; a nil matches NULL rows.
//	NOT IN matches rows whose value is not listed; NULL rows are kept unless
//	       nil is in the list.
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
		default:
			return "FALSE" // an empty picklist selects nothing
		}
	}
	switch {
	case len(marks) > 0 && hasNil:
		// NOT IN's own semantics drop NULL rows, which is what listing nil asks for.
		return col + " NOT IN (" + list + ")"
	case len(marks) > 0:
		return "(" + col + " NOT IN (" + list + ") OR " + col + " IS NULL)"
	case hasNil:
		return col + " IS NOT NULL"
	default:
		return "TRUE" // excluding nothing keeps everything
	}
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
