package transfer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Writing a file's rows into a table (FR-10.6, ADR-0049): the rows go in
// batches, each a changeset of new rows planned and applied in one
// transaction, through the same Writer every edit goes through.

// Writer plans and applies changes, as a source's Writer does.
type Writer interface {
	Plan(ctx context.Context, cs source.Changeset) (*source.WritePlan, error)
	Apply(ctx context.Context, plan *source.WritePlan) (*source.WriteOutcome, error)
}

// DefaultBatch is how many rows go in a transaction unless said otherwise.
const DefaultBatch = 500

// LoadOptions say how rows are written.
type LoadOptions struct {
	Batch     int  // rows a transaction; 0 is DefaultBatch
	Confirmed bool // consent to write to a production connection (FR-4.9)
}

// Loaded is how far a load has got.
type Loaded struct {
	Rows    int64 // the file's rows read
	Written int64 // of those, the rows written
	Elapsed time.Duration
}

// LoadError is a load stopped at one of the file's rows.
type LoadError struct {
	Row int64 // the row's place among the file's rows, from 1
	Err error
	// Undone says nothing of the row's batch was written. It is false only
	// where the server could not undo the rows before it in the batch.
	Undone bool
}

func (e *LoadError) Error() string { return fmt.Sprintf("row %d: %v", e.Row, e.Err) }
func (e *LoadError) Unwrap() error { return e.Err }

// Load writes every row of rs into target as new rows, each made the
// table's values as Coerce does. It stops at the first row with a value that
// would not go in, and at the first the server refuses, saying which; the
// batches before it stay written. progress, when not nil, is told after each
// batch. ctx ending stops it between rows, and a batch under way is undone.
func Load(ctx context.Context, rs model.RowStream, pairs []Pair, to map[string]model.ColumnDef, target model.ObjectRef,
	w Writer, opt LoadOptions, progress func(Loaded)) (l Loaded, err error) {
	start := time.Now()
	defer func() { l.Elapsed = time.Since(start) }()
	size := opt.Batch
	if size <= 0 {
		size = DefaultBatch
	}
	from := rs.Columns()
	var batch []source.RowChange
	write := func() error {
		if len(batch) == 0 {
			return nil
		}
		first := l.Rows - int64(len(batch)) + 1 // the batch's first row in the file
		plan, err := w.Plan(ctx, source.Changeset{Target: target, Changes: batch, Confirmed: opt.Confirmed})
		if err != nil {
			return err
		}
		out, err := w.Apply(ctx, plan)
		switch {
		case err != nil:
			return err
		case out.Err != nil && out.FailedAt >= 0:
			return &LoadError{Row: first + int64(out.FailedAt), Err: out.Err, Undone: out.RolledBack}
		case out.Err != nil:
			return out.Err
		}
		l.Written += int64(len(batch))
		batch = nil // a writer may keep what it was given
		if progress != nil {
			progress(Loaded{Rows: l.Rows, Written: l.Written, Elapsed: time.Since(start)})
		}
		return nil
	}
	for {
		row, err := rs.Next(ctx) // each of the files' readers stops when ctx ends
		if errors.Is(err, io.EOF) {
			return l, write()
		}
		if err != nil {
			return l, err
		}
		l.Rows++
		vals, errs := Coerce(row, from, pairs, to)
		if len(errs) > 0 {
			return l, &LoadError{Row: l.Rows, Err: errs[0], Undone: true}
		}
		v := make(map[string]any, len(pairs))
		for i, p := range pairs {
			v[p.To] = vals[i] // NULL too: the file said so
		}
		batch = append(batch, source.RowChange{Kind: source.ChangeInsert, Values: v})
		if len(batch) == size {
			if err := write(); err != nil {
				return l, err
			}
		}
	}
}
