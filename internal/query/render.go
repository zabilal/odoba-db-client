package query

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// A design rendered as SQL (FR-9.3).
//
// Every identifier goes through the dialect's QuoteIdentifier and every table
// through its QualifyRef, so a name with a space or a reserved word in it is a
// name rather than a syntax error, and the same design reads as each engine's
// own SQL (ARCH-2, NFR-S6).
//
// Values are the exception and are meant to be: a condition's right-hand side
// is text the person typed, and it is written where they put it. What guards
// that is the last thing Render does — it asks the dialect what the statement
// it has just written would do, and refuses to hand back one that could change
// anything. A designer is for reading.

// Render writes the design as SQL for this dialect.
//
// The text is for a person: it is laid out over several lines, in the order a
// SELECT is read, because it goes into an editor where somebody will change
// it. Nothing about it is bound — a statement with a placeholder in it cannot
// be run from an editor, which is where this one is going.
func Render(d *Design, dl source.Dialect) (string, error) {
	if dl == nil {
		return "", fmt.Errorf("query: this source has no SQL to write")
	}
	if err := d.Validate(); err != nil {
		return "", err
	}
	r := &renderer{d: d, dl: dl}
	var b strings.Builder
	b.WriteString("SELECT ")
	if d.Distinct {
		b.WriteString("DISTINCT ")
	}
	b.WriteString(r.outputs())
	b.WriteString("\nFROM " + r.from())
	for _, line := range r.joins() {
		b.WriteString("\n" + line)
	}
	if where := r.conditions(d.Where); where != "" {
		b.WriteString("\nWHERE " + where)
	}
	if group := r.group(); group != "" {
		b.WriteString("\nGROUP BY " + group)
	}
	if having := r.conditions(d.Having); having != "" {
		b.WriteString("\nHAVING " + having)
	}
	if order := r.order(); order != "" {
		b.WriteString("\nORDER BY " + order)
	}
	if d.Limit > 0 {
		// LIMIT is not universal — SQL Server and Oracle spell it as FETCH,
		// and the standard's form is FETCH FIRST — so it is the dialect's
		// own, asked for through a browse of the first table. What comes back
		// is the tail of that statement, which is the clause.
		limit, err := r.limit()
		if err != nil {
			return "", err
		}
		b.WriteString("\n" + limit)
	}
	out := b.String()
	// The last thing, and the point of it: what has been written is read back
	// by the engine's own classifier, and a design that came out as anything
	// but a read is refused. Nothing here can write one — there is no clause
	// in a Design that could — and that is exactly why it is checked: the
	// values are the person's text, and text is where a second statement
	// would come from.
	if dl.Classify(out).Mutating() {
		return "", ErrMutating
	}
	return out, nil
}

type renderer struct {
	d  *Design
	dl source.Dialect
}

// alias is a table's name, quoted.
func (r *renderer) alias(at int) string {
	return r.dl.QuoteIdentifier(r.d.Tables[at].Alias)
}

// column is one column, qualified by the table's name on the canvas.
//
// Always qualified, never bare: a designer that wrote a bare column would
// change what an existing condition meant the moment a second table with a
// column of that name arrived.
func (r *renderer) column(c Column) string {
	return r.alias(c.Table) + "." + r.dl.QuoteIdentifier(c.Name)
}

// outputs is the SELECT list. Nothing chosen selects everything, which is what
// a canvas with tables on it and nothing picked from them means.
func (r *renderer) outputs() string {
	if len(r.d.Outputs) == 0 {
		parts := make([]string, len(r.d.Tables))
		for i := range r.d.Tables {
			parts[i] = r.alias(i) + ".*"
		}
		return strings.Join(parts, ", ")
	}
	parts := make([]string, len(r.d.Outputs))
	for i, o := range r.d.Outputs {
		parts[i] = r.output(o)
	}
	return strings.Join(parts, ", ")
}

func (r *renderer) output(o Output) string {
	var text string
	switch {
	case o.All:
		text = r.alias(o.Column.Table) + ".*"
	default:
		text = r.column(o.Column)
	}
	if o.Aggregate != AggregateNone {
		text = string(o.Aggregate) + "(" + text + ")"
	}
	if o.Alias != "" {
		text += " AS " + r.dl.QuoteIdentifier(o.Alias)
	}
	return text
}

// from is the first table, with its name.
//
// A table is named "schema"."table" AS "alias" — the AS written out, because
// Oracle does not take it on a table and every other engine does, and because
// a reader should not have to know which. Oracle's own dialect is where that
// difference would be handled if it mattered; it does not, because Oracle
// takes the alias without AS and this writes it without.
func (r *renderer) from() string { return r.table(0) }

func (r *renderer) table(at int) string {
	return r.dl.QualifyRef(r.d.Tables[at].Ref) + " " + r.alias(at)
}

// joins are the JOIN lines, in the order the tables are reached.
//
// The order matters and is not the order the joins were made in: a join whose
// left-hand table has not been named yet is not legal SQL, so the tables are
// walked outward from the first and each join is written when the table it
// brings in is reached.
func (r *renderer) joins() []string {
	reached := make([]bool, len(r.d.Tables))
	if len(reached) > 0 {
		reached[0] = true
	}
	used := make([]bool, len(r.d.Joins))
	var out []string
	for again := true; again; {
		again = false
		for i, j := range r.d.Joins {
			if used[i] {
				continue
			}
			// Either end may be the one already reached; the join is written
			// with the unreached table as the one being brought in.
			left, right := j.Left, j.Right
			switch {
			case reached[left] && !reached[right]:
			case reached[right] && !reached[left]:
				left, right = right, left
			default:
				continue
			}
			out = append(out, r.join(j, left, right))
			reached[right], used[i], again = true, true, true
		}
	}
	// A join between two tables both already reached is a further condition on
	// rows the query already has, and it is written last: SQL takes it in the
	// ON of the table it names, and writing it there would mean reordering
	// somebody else's join.
	for i, j := range r.d.Joins {
		if used[i] {
			continue
		}
		out = append(out, r.join(j, j.Left, j.Right))
	}
	return out
}

func (r *renderer) join(j Join, left, right int) string {
	line := string(j.Kind) + " " + r.table(right)
	if j.Kind == JoinCross {
		return line
	}
	parts := make([]string, len(j.On))
	for i, p := range j.On {
		// The pair is written left-to-right as the join holds it, and the
		// join's own ends may have been swapped to reach a table, so the
		// columns follow the ends rather than the pair's own names.
		l, rr := p.Left, p.Right
		if left != j.Left {
			l, rr = p.Right, p.Left
		}
		parts[i] = r.column(Column{Table: left, Name: l}) + " = " + r.column(Column{Table: right, Name: rr})
	}
	return line + " ON " + strings.Join(parts, " AND ")
}

// conditions renders a list of predicates, joined by AND.
func (r *renderer) conditions(list []Condition) string {
	if len(list) == 0 {
		return ""
	}
	parts := make([]string, len(list))
	for i, c := range list {
		parts[i] = r.condition(c)
	}
	return strings.Join(parts, " AND ")
}

func (r *renderer) condition(c Condition) string {
	col := r.column(c.Column)
	if c.Aggregate != AggregateNone {
		col = string(c.Aggregate) + "(" + col + ")"
	}
	op := conditionOps[c.Op]
	switch c.Op {
	case source.OpIsNull, source.OpIsNotNull:
		return col + " " + op
	case source.OpBetween:
		return col + " BETWEEN " + strings.TrimSpace(c.Value) + " AND " + strings.TrimSpace(c.Value2)
	case source.OpIn, source.OpNotIn:
		// The list is the person's own, and a list is what IN takes, so the
		// brackets are added where they have not written them: "1, 2" and
		// "(1, 2)" are the same intention and one of them is a syntax error.
		list := strings.TrimSpace(c.Value)
		if !strings.HasPrefix(list, "(") {
			list = "(" + list + ")"
		}
		return col + " " + op + " " + list
	}
	return col + " " + op + " " + strings.TrimSpace(c.Value)
}

func (r *renderer) group() string {
	if len(r.d.Group) == 0 {
		return ""
	}
	parts := make([]string, len(r.d.Group))
	for i, c := range r.d.Group {
		parts[i] = r.column(c)
	}
	return strings.Join(parts, ", ")
}

func (r *renderer) order() string {
	if len(r.d.Order) == 0 {
		return ""
	}
	parts := make([]string, len(r.d.Order))
	for i, s := range r.d.Order {
		text := r.column(s.Column)
		if s.Aggregate != AggregateNone {
			text = string(s.Aggregate) + "(" + text + ")"
		}
		if s.Descending {
			text += " DESC"
		}
		parts[i] = text
	}
	return strings.Join(parts, ", ")
}

// limit is the clause that bounds the rows, in this engine's own words.
//
// Asked of the dialect rather than written here, because it is the one part of
// a SELECT the engines do not agree on: LIMIT n, FETCH NEXT n ROWS ONLY, TOP n
// before the select list. A browse of the first table is rendered and the
// clause is read out of its tail, which is the only place in this application
// that reads a dialect's SQL — and it is done because the alternative is a
// table of engines in a package that has no business knowing their names
// (REQ-DB-4).
func (r *renderer) limit() (string, error) {
	st, err := r.dl.BuildBrowse(r.d.Tables[0].Ref, source.BrowseOptions{Limit: r.d.Limit})
	if err != nil {
		return "", fmt.Errorf("query: this source cannot say how to bound a query: %w", err)
	}
	clause, ok := limitClause(st.SQL, r.d.Limit)
	if !ok {
		return "", fmt.Errorf("query: this source bounds a query in a way the designer cannot write; "+
			"remove the limit, or write it yourself: %s", st.SQL)
	}
	return clause, nil
}

// limitClause reads the bound out of a rendered browse: the tail from the
// first of the words an engine bounds with, with the count put back where the
// browse bound it.
//
// A browse binds its limit as a parameter, so what comes back says "?" or "$1"
// where the number goes; the number is put in, which is safe because it is
// this package's own integer and not anybody's text. A dialect that bounds
// some other way — before the select list, as TOP does — is not recognised,
// and saying so is better than writing a clause that does nothing.
func limitClause(sql string, n int64) (string, bool) {
	for _, word := range []string{"\nLIMIT ", " LIMIT ", "\nFETCH ", " FETCH ", "\nOFFSET ", " OFFSET "} {
		at := strings.LastIndex(sql, word)
		if at < 0 {
			continue
		}
		clause := strings.TrimSpace(sql[at:])
		// The count, where the browse bound it. One placeholder: a browse with
		// no offset binds only the limit, which is what is asked for here.
		for _, mark := range []string{"?", "$1", ":1", "@p1"} {
			if strings.Count(clause, mark) == 1 {
				return strings.Replace(clause, mark, strconv.FormatInt(n, 10), 1), true
			}
		}
		if strings.Contains(clause, strconv.FormatInt(n, 10)) {
			return clause, true
		}
		return "", false
	}
	return "", false
}
