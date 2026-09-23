package clickhouse

import (
	"context"
	"errors"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/sqlscript"
)

// Writing rows back from the grid (FR-4.4, FR-4.5, ADR-0142).
//
// No row here has an address of its own, so what addresses one is the key
// somebody named for it (model.IdentityChosen, FR-4.7): nothing says those
// columns are unique, and a change that would reach any number of rows but
// one is refused before it is made.
//
// It has to be asked before, not after. Every other engine here says how
// many rows a statement changed, and the shared planner reads that number;
// ClickHouse says what it wrote as progress rather than as a count, so the
// question "how many rows is this change for" is put to the server as its
// own statement first (ADR-0034).

var _ source.Writer = (*clickhouseSource)(nil)

// errNoTransaction is why a plan that failed half way is not undone: there
// is nothing here to undo it in.
var errNoTransaction = errors.New("clickhouse: there is no transaction to undo what was already written")

// rowCheck is the question of how many rows a change is for.
type rowCheck struct {
	SQL  string
	Args []any
}

// Plan renders a changeset as the statements that would write it, each
// value bound, and each change that addresses a row carrying the question
// of whether it addresses exactly one.
func (s *clickhouseSource) Plan(_ context.Context, cs source.Changeset) (*source.WritePlan, error) {
	for _, c := range cs.Changes {
		if c.Kind == source.ChangeInsert && len(c.Values) == 0 {
			// Every other engine writes a row of nothing but defaults with
			// a form of its own; ClickHouse has none, and a column has to
			// be named before DEFAULT can be asked for in it.
			return nil, errors.New("clickhouse: a new row needs a value in at least one column")
		}
	}
	plan, err := sqlscript.PlanWrites(s, s.cfg.Guard, cs, "")
	if err != nil {
		return nil, err
	}
	// Nothing here is undone: there is no transaction, so a plan that fails
	// half way leaves the half that ran (FR-4.5).
	plan.Atomic = false
	for i, c := range cs.Changes {
		if c.Kind != source.ChangeInsert {
			plan.Statements[i].Op = s.rowCheck(cs, c)
		}
	}
	return plan, nil
}

// rowCheck builds the count of the rows a change's key reaches. The key's
// shape has already been settled by the planner, which refuses a key of the
// wrong size and a key value that is NULL.
func (s *clickhouseSource) rowCheck(cs source.Changeset, c source.RowChange) *rowCheck {
	b := &builder{d: s.dialect}
	var sb strings.Builder
	sb.WriteString("SELECT count() FROM " + s.QualifyRef(cs.Target) + " WHERE ")
	for i, k := range cs.Identity.Columns {
		if i > 0 {
			sb.WriteString(" AND ")
		}
		sb.WriteString(s.QuoteIdentifier(k) + " = " + b.bind(c.Key[i]))
	}
	return &rowCheck{SQL: sb.String(), Args: b.args}
}

// Apply runs a plan, asking of each change that addresses a row how many it
// reaches before making it. A change for any number but one is not made,
// and what it would have reached is what says why (sqlscript.ApplyWith).
func (s *clickhouseSource) Apply(ctx context.Context, plan *source.WritePlan) (*source.WriteOutcome, error) {
	if err := sqlscript.AllowWrites(s.cfg.Guard, plan); err != nil {
		return nil, err
	}
	return sqlscript.ApplyWith(plan, func(st source.Statement) (int64, error) {
		if chk, ok := st.Op.(*rowCheck); ok {
			var n int64
			if err := s.db.QueryRowContext(ctx, chk.SQL, chk.Args...).Scan(&n); err != nil {
				return 0, statementError(err, ctx)
			}
			if n != 1 {
				return n, nil
			}
		}
		if _, err := s.db.ExecContext(ctx, st.SQL, st.Args...); err != nil {
			return 0, statementError(err, ctx)
		}
		return 1, nil
	}, noCommit, noRollback), nil
}

// noCommit is what there is to commit: a write here is its own.
func noCommit() error { return nil }

// noRollback is what there is to undo, so that a plan which failed half way
// says the half that ran is still there rather than claiming otherwise.
func noRollback() error { return errNoTransaction }
