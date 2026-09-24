package cockroach

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/sqlscript"
)

// Writing rows back from the grid (FR-4.4, FR-4.5, ADR-0031).
//
// A changeset is written in one transaction and is undone whole if any
// part of it fails. Every row here has an address, so there is no table
// this grid cannot edit (ADR-0145).

var _ source.Writer = (*crdbSource)(nil)

// retryCode is what the server says when a transaction has to start again.
//
// CockroachDB is serialisable, and it keeps that promise by telling a
// transaction that lost a race to run again rather than by making it wait.
// It is the engine working as designed, not a fault.
const retryCode = "40001"

// retries is how many times a changeset is attempted before the person who
// made it is told. Bounded rather than open-ended: under real contention a
// changeset that will not get through should say so rather than keep a
// window waiting.
const retries = 3

// Plan renders a changeset as the statements that would write it, each
// value bound.
func (s *crdbSource) Plan(_ context.Context, cs source.Changeset) (*source.WritePlan, error) {
	return sqlscript.PlanWrites(s, s.cfg.Guard, cs, "DEFAULT VALUES")
}

// Apply runs a plan in one transaction: all of it, or on the first failure
// none of it (FR-4.5).
//
// A transaction told to start again is started again, up to a few times.
// That is safe in a way a retry usually is not: the server's promise with
// this error is that the transaction left nothing behind, so running it a
// second time cannot write anything twice. Asking somebody to press Save
// again for a race they had no part in would be passing on the engine's
// working as if it were their problem.
func (s *crdbSource) Apply(ctx context.Context, plan *source.WritePlan) (*source.WriteOutcome, error) {
	if err := sqlscript.AllowWrites(s.cfg.Guard, plan); err != nil {
		return nil, err
	}
	p, err := s.conn()
	if err != nil {
		return nil, err
	}
	var out *source.WriteOutcome
	for attempt := 0; ; attempt++ {
		tx, err := p.Begin(ctx)
		if err != nil {
			return nil, err
		}
		out = sqlscript.ApplyWith(plan, func(st source.Statement) (int64, error) {
			tag, err := tx.Exec(ctx, st.SQL, st.Args...)
			return tag.RowsAffected(), err
		}, func() error { return tx.Commit(ctx) }, func() error { return tx.Rollback(ctx) })

		if !retryable(out.Err) || attempt+1 >= retries || ctx.Err() != nil {
			break
		}
		// A short wait, longer each time, so that two windows racing over
		// the same rows do not keep colliding at the same moment.
		select {
		case <-ctx.Done():
			return out, nil
		case <-time.After(time.Duration(attempt+1) * 50 * time.Millisecond):
		}
	}
	if retryable(out.Err) {
		out.Err = fmt.Errorf("the server asked for this change to be made again %d times over, "+
			"which means something else is writing the same rows: %w", retries, out.Err)
	}
	return out, nil
}

// retryable reports whether the server asked for the transaction to be run
// again.
func retryable(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == retryCode
}

// UpsertClause writes a row whose key is taken over the row there, as ON
// CONFLICT … DO UPDATE (ADR-0052).
func (d dialect) UpsertClause(keys, cols []string) string { return sqlscript.OnConflict(d, keys, cols) }
