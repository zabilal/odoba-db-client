package cassandra

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/sqlscript"
	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

// The dialect (source.Dialect, T2.49) is what the rest of the application
// asks a source about its language: how a name is written, how an object is
// addressed, where one statement ends and the next begins, what a statement
// does, and what a browse would send.
//
// CQL reads like SQL and is not SQL. It has no OFFSET, no NULLS FIRST, no
// LIKE outside an index built for it, and no way to ask whether a column is
// null. What it cannot do is refused here rather than sent and failed, or —
// worse — quietly dropped (REQ-DRV-3).

// DefaultPageSize is the browse window when none is asked for (NFR-P11).
const DefaultPageSize = 1000

type dialect struct{}

var _ source.Dialect = dialect{}

// browsableKinds are the objects a browse can read rows from. A materialized
// view is read like the table it is written from.
var browsableKinds = map[model.ObjectKind]bool{
	model.KindTable: true, model.KindMaterializedView: true,
}

// QuoteIdentifier double-quotes a name, doubling any quote inside it. CQL
// folds an unquoted name to lower case, so quoting is also what keeps a name
// the case it was made with.
func (dialect) QuoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// QualifyRef names an object with its keyspace: "shop"."people".
func (d dialect) QualifyRef(ref model.ObjectRef) string {
	if len(ref.Path) >= 2 {
		return d.QuoteIdentifier(ref.Path[0]) + "." + d.QuoteIdentifier(ref.Path[1])
	}
	return d.QuoteIdentifier(ref.Name())
}

func (dialect) Placeholder(int) string { return "?" }

// SplitScript splits on semicolons outside strings, comments and batches. A
// batch's own statements are separated by semicolons and are one statement to
// send, which is why it is a block: BEGIN BATCH … APPLY BATCH.
func (dialect) SplitScript(script string) []source.ScriptStatement {
	return sqlscript.SplitWith(sqllex.CQL, script, sqlscript.BatchBlocks, sqlscript.BatchCloses)
}

func (dialect) Classify(statement string) source.Access { return classify(statement) }

// BuildBrowse renders the statement a browse would send (FR-3.6), with the
// limit that bounds it.
func (d dialect) BuildBrowse(ref model.ObjectRef, opt source.BrowseOptions) (source.Statement, error) {
	return d.browse(ref, opt, true)
}

// browse renders a read. limited writes the LIMIT that bounds one page; a
// paged read leaves it off, because CQL's LIMIT bounds the whole query rather
// than a page of it, and what bounds a page there is the size the cluster is
// asked for (T2.51).
func (d dialect) browse(ref model.ObjectRef, opt source.BrowseOptions, limited bool) (source.Statement, error) {
	if !browsableKinds[ref.Kind] {
		return source.Statement{}, fmt.Errorf("cassandra: %s is not browsable", ref)
	}
	if opt.Seek != nil || opt.Follow {
		return source.Statement{}, errors.New("cassandra: seek and follow apply only to stream sources")
	}
	if opt.Offset > 0 {
		// CQL has no OFFSET. A page is where the last one left off, which the
		// server gives as a paging state (T2.51); counting rows to skip would
		// read them all on every page.
		return source.Statement{}, errors.New("cassandra: rows are paged by where the last page ended, not by an offset")
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
		if s.NullsFirst {
			// CQL has no NULLS FIRST or LAST: a clustering column, which is
			// all a row can be ordered by, can never be null.
			return source.Statement{}, fmt.Errorf("cassandra: rows cannot be ordered by where the nulls go, and %q has none", s.Column)
		}
		if i == 0 {
			sb.WriteString(" ORDER BY ")
		} else {
			sb.WriteString(", ")
		}
		sb.WriteString(d.QuoteIdentifier(s.Column))
		if s.Descending {
			sb.WriteString(" DESC")
		}
	}
	if limited {
		limit := opt.Limit
		if limit <= 0 {
			limit = DefaultPageSize
		}
		sb.WriteString(" LIMIT " + b.bind(limit))
	}
	// A typed condition is held to being one condition by Predicate — no
	// second statement, nothing left open — and the statement it lands in
	// begins with SELECT, so there is nothing further to classify.
	return source.Statement{SQL: sb.String(), Args: b.args}, nil
}

type builder struct {
	d    dialect
	args []any
}

func (b *builder) bind(v any) string {
	b.args = append(b.args, v)
	return "?"
}

// comparisons are the operators CQL compares with in a WHERE clause. It has
// no <> there: inequality is a condition on a write, not a way to read.
var comparisons = map[source.FilterOp]string{
	source.OpEqual: "=", source.OpLess: "<",
	source.OpLessEqual: "<=", source.OpGreater: ">", source.OpGreaterEqual: ">=",
}

// filter renders one predicate, or says why CQL has no way to mean it.
func (b *builder) filter(f source.Filter) (string, error) {
	if f.Negate {
		return "", fmt.Errorf("cassandra: CQL cannot ask for the rows a condition does not match, and %q is negated", f.Column)
	}
	col := b.d.QuoteIdentifier(f.Column)
	arity := func(n int) error {
		if len(f.Values) != n {
			return fmt.Errorf("cassandra: filter %q on %q takes %d value(s), got %d", f.Op, f.Column, n, len(f.Values))
		}
		return nil
	}
	if op, ok := comparisons[f.Op]; ok {
		if err := arity(1); err != nil {
			return "", err
		}
		if f.Values[0] == nil {
			// A CQL row has no null to compare with: a column with nothing in
			// it is a column that was never written.
			return "", fmt.Errorf("cassandra: CQL cannot compare %q with nothing", f.Column)
		}
		return col + " " + op + " " + b.bind(f.Values[0]), nil
	}
	if f.Op == source.OpIn {
		if len(f.Values) == 0 {
			return "", fmt.Errorf("cassandra: filter %q on %q takes at least one value", f.Op, f.Column)
		}
		marks := make([]string, 0, len(f.Values))
		for _, v := range f.Values {
			marks = append(marks, b.bind(v))
		}
		return col + " IN (" + strings.Join(marks, ", ") + ")", nil
	}
	return "", fmt.Errorf("cassandra: CQL has no way to mean %q on %q", f.Op, f.Column)
}

// where renders the filters and the typed condition, joined by AND.
//
// Nothing here adds ALLOW FILTERING. A query that would need it reads every
// partition on every node, and that is a person's decision to make rather
// than a driver's to make quietly for them.
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
		cond, err := sqlscript.Predicate(sqllex.CQL, opt.Where)
		if err != nil {
			return fmt.Errorf("cassandra: %w", err)
		}
		sb.WriteString(join + cond)
	}
	return nil
}
