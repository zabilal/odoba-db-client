package firebird

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

// textCast is what a value is turned into to be matched as text.
//
// Firebird has no unbounded VARCHAR: the longest one is 32765 bytes, which in
// UTF-8 is 8191 characters, and that is the widest a cast can be. A value
// longer than that is cut, so a LIKE over a very long text can match on less
// than all of it. That is a limit of the engine rather than a choice here,
// and it is the whole of it: 8191 characters is longer than anything anybody
// filters on by eye.
const textCast = "VARCHAR(8191)"

type dialect struct{}

// QuoteIdentifier double-quotes a name, doubling any quote inside it.
//
// Quoting always, as everywhere else, and it matters more here: Firebird
// folds an unquoted name to upper case, so PEOPLE and "PEOPLE" are the same
// table while "people" is a different one that probably does not exist. A
// name read out of the catalogue is the name as stored, and quoting it is
// what asks for that one.
func (dialect) QuoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// QualifyRef names an object. Only the object: Firebird has no schemas and no
// way to qualify by the database, there being only ever the one a connection
// is to, so the database in a ref is the tree's own bookkeeping.
func (d dialect) QualifyRef(ref model.ObjectRef) string {
	return d.QuoteIdentifier(ref.Name())
}

func (dialect) Placeholder(int) string { return "?" }

// SplitScript splits on semicolons outside strings, comments and PSQL blocks.
//
// A procedure or trigger body is full of semicolons that do not end the
// statement, and Firebird's own tools handle that with SET TERM, which
// changes the terminator for a while. Both ways of writing a script are in
// use, so both are read: SET TERM where it is there, and the shape of a
// BEGIN … END block where it is not.
func (dialect) SplitScript(script string) []source.ScriptStatement {
	return sqlscript.SplitTerminated(sqllex.Firebird, script, sqlscript.FirebirdBlocks)
}

func (dialect) Classify(statement string) source.Access { return classify(statement) }

// BuildBrowse renders the statement a Browse runs.
func (d dialect) BuildBrowse(ref model.ObjectRef, opt source.BrowseOptions) (source.Statement, error) {
	if !browsableKinds[ref.Kind] {
		return source.Statement{}, fmt.Errorf("firebird: %s is not browsable", ref)
	}
	if opt.Seek != nil || opt.Follow {
		return source.Statement{}, errors.New("firebird: seek and follow apply only to stream sources")
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
	if err := b.order(&sb, opt.Sorts); err != nil {
		return source.Statement{}, err
	}
	limit := opt.Limit
	if limit <= 0 {
		limit = DefaultPageSize
	}
	// OFFSET/FETCH rather than Firebird's own FIRST/SKIP, which come before
	// the select list: the standard form is what Firebird 3 and later prefer,
	// and it puts the window at the end where every other engine's is.
	// OFFSET comes first and must, even when it is zero, because FETCH
	// without it is legal and FETCH before it is not.
	if opt.Offset > 0 {
		sb.WriteString(" OFFSET " + b.bind(opt.Offset) + " ROWS")
	}
	sb.WriteString(" FETCH NEXT " + b.bind(limit) + " ROWS ONLY")
	return d.readsOnly(source.Statement{SQL: sb.String(), Args: b.args}, opt)
}

// buildCount renders the count of the rows a browse would return.
func (d dialect) buildCount(ref model.ObjectRef, opt source.BrowseOptions) (source.Statement, error) {
	if !browsableKinds[ref.Kind] {
		return source.Statement{}, fmt.Errorf("firebird: %s is not browsable", ref)
	}
	b := &builder{d: d}
	var sb strings.Builder
	sb.WriteString("SELECT COUNT(*) FROM " + d.QualifyRef(ref))
	if err := b.where(&sb, opt); err != nil {
		return source.Statement{}, err
	}
	return d.readsOnly(source.Statement{SQL: sb.String(), Args: b.args}, opt)
}

// buildDistinct renders a column's distinct values among the rows the filters
// select, most frequent first (source.DistinctLister).
func (d dialect) buildDistinct(ref model.ObjectRef, column string, opt source.BrowseOptions, limit int) (source.Statement, error) {
	if !browsableKinds[ref.Kind] {
		return source.Statement{}, fmt.Errorf("firebird: %s is not browsable", ref)
	}
	if limit <= 0 {
		return source.Statement{}, errors.New("firebird: a list of distinct values needs a limit")
	}
	b := &builder{d: d}
	col := d.QuoteIdentifier(column)
	var sb strings.Builder
	sb.WriteString("SELECT " + col + ", COUNT(*) FROM " + d.QualifyRef(ref))
	if err := b.where(&sb, opt); err != nil {
		return source.Statement{}, err
	}
	// Grouped and ordered by the column itself, not by its position: Firebird
	// takes either, and a name says what it means.
	sb.WriteString(" GROUP BY " + col + " ORDER BY COUNT(*) DESC, " + col +
		" FETCH NEXT " + b.bind(int64(limit)) + " ROWS ONLY")
	return d.readsOnly(source.Statement{SQL: sb.String(), Args: b.args}, opt)
}

// buildStats renders the one statement that measures a column (FR-3.14).
func (d dialect) buildStats(ref model.ObjectRef, def model.ColumnDef,
	opt source.BrowseOptions) (source.Statement, source.StatsQuery, error) {
	if !browsableKinds[ref.Kind] {
		return source.Statement{}, source.StatsQuery{}, fmt.Errorf("firebird: %s is not browsable", ref)
	}
	q := source.StatsFor(d.QuoteIdentifier(def.Name), def)
	b := &builder{d: d}
	var sb strings.Builder
	sb.WriteString("SELECT " + q.SQL() + " FROM " + d.QualifyRef(ref))
	if err := b.where(&sb, opt); err != nil {
		return source.Statement{}, source.StatsQuery{}, err
	}
	st, err := d.readsOnly(source.Statement{SQL: sb.String(), Args: b.args}, opt)
	return st, q, err
}

// UpsertAround writes a row over the row already there with its key
// (sqlscript.UpsertRewriter, FR-10.6, ADR-0052).
//
// Firebird's form is UPDATE OR INSERT INTO t (cols) VALUES (…) MATCHING
// (keys), which is two extra words at the front and a clause at the back
// rather than something appended to an INSERT — so it is written as what goes
// around one. MERGE would say the same thing and would mean writing the
// values twice, once for the match and once for the insert, so the row would
// be bound twice for no gain.
func (d dialect) UpsertAround(keys, _ []string) (before, after string) {
	quoted := make([]string, len(keys))
	for i, k := range keys {
		quoted[i] = d.QuoteIdentifier(k)
	}
	return "UPDATE OR ", " MATCHING (" + strings.Join(quoted, ", ") + ")"
}

type builder struct {
	d    dialect
	args []any
}

func (b *builder) bind(v any) string {
	b.args = append(b.args, v)
	return "?"
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
		cond, err := sqlscript.Predicate(sqllex.Firebird, opt.Where)
		if err != nil {
			return fmt.Errorf("firebird: %w", err)
		}
		sb.WriteString(join + cond)
	}
	return nil
}

// order renders the sort. NULLS FIRST and NULLS LAST are written out rather
// than left to the engine's default, so that reversing a sort does not move
// every empty row from one end of the grid to the other.
func (b *builder) order(sb *strings.Builder, sorts []source.Sort) error {
	for i, s := range sorts {
		if s.Column == "" {
			return errors.New("firebird: a sort needs a column")
		}
		if i == 0 {
			sb.WriteString(" ORDER BY ")
		} else {
			sb.WriteString(", ")
		}
		sb.WriteString(b.d.QuoteIdentifier(s.Column))
		if s.Descending {
			sb.WriteString(" DESC")
		}
		if s.NullsFirst {
			sb.WriteString(" NULLS FIRST")
		} else {
			sb.WriteString(" NULLS LAST")
		}
	}
	return nil
}

// readsOnly refuses a statement whose typed WHERE would change anything: a
// filter reads (FR-3.6). Classify is conservative, so what it cannot vouch
// for is refused too.
func (d dialect) readsOnly(st source.Statement, opt source.BrowseOptions) (source.Statement, error) {
	if opt.Where != "" && d.Classify(st.SQL).Mutating() {
		return source.Statement{}, errors.New("firebird: a WHERE clause may only read, and this one could change something")
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
	arity := func(n int) error {
		if len(f.Values) != n {
			return fmt.Errorf("firebird: filter %q on %q takes %d value(s), got %d", f.Op, f.Column, n, len(f.Values))
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
				return "", fmt.Errorf("firebird: cannot order-compare %q with NULL", f.Column)
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
		clause = "CAST(" + col + " AS " + textCast + ")" + op + b.bind(f.Values[0])
	case source.OpContains:
		if err := arity(1); err != nil {
			return "", err
		}
		// CONTAINING, which is Firebird's own and is what the grid means:
		// anywhere in the value, ignoring case. LIKE here is case sensitive,
		// so a LIKE '%ada%' would miss Ada — and CONTAINING takes the text
		// as text, so "50%" is not a pattern and needs no escaping.
		clause = "CAST(" + col + " AS " + textCast + ") CONTAINING " + b.bind(fmt.Sprint(f.Values[0]))
	case source.OpRegex:
		// Firebird has SIMILAR TO, which is the SQL standard's pattern
		// language and not the POSIX one this operator means: [[:digit:]]
		// and \d are not in it, and anchoring works the other way round.
		// Accepting a POSIX pattern here would match the wrong rows and
		// present them as filtered (REQ-DRV-3).
		return "", errors.New("firebird: regular-expression filters are not supported")
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
		return "", fmt.Errorf("firebird: unsupported filter operator %q", f.Op)
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
		return "1 = 0" // an empty picklist selects nothing
	}
	switch {
	case len(marks) > 0 && hasNil:
		return col + " NOT IN (" + list + ")"
	case len(marks) > 0:
		return "(" + col + " NOT IN (" + list + ") OR " + col + " IS NULL)"
	case hasNil:
		return col + " IS NOT NULL"
	}
	return "1 = 1" // excluding nothing keeps everything
}
