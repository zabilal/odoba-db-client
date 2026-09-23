package cassandra

import (
	"context"
	"errors"

	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/sqlscript"
)

// Writing rows back from the grid (FR-4.4, FR-4.5, ADR-0143).
//
// A row here has a key that addresses it — the partition and clustering
// columns are what a row is — so unlike ClickHouse there is nothing to
// choose and nothing to count. What there is instead is that CQL has no
// writes, only upserts: an UPDATE of a row that is not there makes one, and
// an INSERT over a row that is there overwrites it. Either would be a grid
// doing something nobody asked for, so every statement here carries a
// condition, and Cassandra answers whether it applied.
//
// The condition is a lightweight transaction, which is a round of Paxos
// among the replicas and costs several times what an ordinary write costs.
// That is the price of a grid whose edits mean what they say, and it is
// paid per edited row rather than per read.

var _ source.Writer = (*cassandraSource)(nil)

// errRowAlreadyThere is a new row whose key is taken. CQL would overwrite
// the row that has it, which is not what adding a row means.
var errRowAlreadyThere = errors.New("a row with this key is already there")

// errNoTransaction is why a plan that failed half way is not undone: CQL
// has no transaction to undo it in.
var errNoTransaction = errors.New("cassandra: there is no transaction to undo what was already written")

// conditional says what a statement's condition meant when it did not
// apply: a row that was not there, or a row that already was.
type conditional struct{ inserting bool }

// Plan renders a changeset as the statements that would write it, each one
// conditional on the row being as the grid last read it.
func (s *cassandraSource) Plan(_ context.Context, cs source.Changeset) (*source.WritePlan, error) {
	for _, c := range cs.Changes {
		if c.Kind == source.ChangeInsert && len(c.Values) == 0 {
			// A row here is its key, and CQL has no form for a row of
			// nothing: there would be no key to write it under.
			return nil, errors.New("cassandra: a new row needs at least the columns of its key")
		}
	}
	plan, err := sqlscript.PlanWrites(s, s.cfg.Guard, cs, "")
	if err != nil {
		return nil, err
	}
	// Nothing here is undone: CQL has no transaction, so a plan that fails
	// half way leaves the half that ran (FR-4.5).
	plan.Atomic = false
	for i, c := range cs.Changes {
		inserting := c.Kind == source.ChangeInsert
		if inserting {
			plan.Statements[i].SQL += " IF NOT EXISTS"
		} else {
			plan.Statements[i].SQL += " IF EXISTS"
		}
		plan.Statements[i].Op = conditional{inserting: inserting}
	}
	return plan, nil
}

// Apply runs a plan. A statement whose condition did not hold changed
// nothing, and says which of the two ways it did not hold.
func (s *cassandraSource) Apply(ctx context.Context, plan *source.WritePlan) (*source.WriteOutcome, error) {
	if err := sqlscript.AllowWrites(s.cfg.Guard, plan); err != nil {
		return nil, err
	}
	return sqlscript.ApplyWith(plan, func(st source.Statement) (int64, error) {
		applied, err := s.session.Query(st.SQL, st.Args...).WithContext(ctx).MapScanCAS(map[string]any{})
		if err != nil {
			return 0, err
		}
		if !applied {
			if cond, ok := st.Op.(conditional); ok && cond.inserting {
				return 0, errRowAlreadyThere
			}
			// No row of that key: the shared planner says what that means.
			return 0, nil
		}
		return 1, nil
	}, noCommit, noRollback), nil
}

// noCommit is what there is to commit: a write here is its own.
func noCommit() error { return nil }

// noRollback is what there is to undo, so that a plan which failed half way
// says the half that ran is still there rather than claiming otherwise.
func noRollback() error { return errNoTransaction }
