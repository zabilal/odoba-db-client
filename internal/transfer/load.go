package transfer

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Writing a file's rows into a table (FR-10.6, ADR-0049, ADR-0051): each row
// made the table's values and handed to the source's bulk loader as it asks
// for it, so memory stays flat; a batch a transaction, or, replacing the
// table's rows, one transaction whole.

// Loader loads rows in bulk, as a source's BulkLoader does.
type Loader interface {
	LoadRows(ctx context.Context, target model.ObjectRef, columns []string, rows model.RowStream, opt source.LoadOptions) (int64, error)
}

// LoadOptions say how rows are written.
type LoadOptions struct {
	Batch     int      // rows a transaction; 0 is the source's own
	Confirmed bool     // consent to write to production, or to replace (FR-4.9)
	Replace   bool     // empty the table first, all in one transaction
	Keys      []string // update the row whose key, in these columns, is taken
}

// Loaded is how far a load has got.
type Loaded struct {
	Rows    int64 // the file's rows read
	Written int64 // of those, the rows written, once the load has ended
	Elapsed time.Duration
}

// LoadError is a load stopped at one of the file's rows. Nothing of the
// row's batch was written.
type LoadError struct {
	Row int64 // the row's place among the file's rows, from 1
	Err error
}

func (e *LoadError) Error() string { return fmt.Sprintf("row %d: %v", e.Row, e.Err) }
func (e *LoadError) Unwrap() error { return e.Err }

// Load writes every row of rs into target as new rows, through l, each made
// the table's values as Coerce does. It stops at the first row with a value
// that would not go in, and at the first the server refuses, saying which;
// the batches before it stay written, unless the load was replacing the
// table's rows, which are then as they were. progress, when not nil, is
// told now and then how many rows have been read. ctx ending stops it, and
// the batch under way is undone.
func Load(ctx context.Context, rs model.RowStream, pairs []Pair, to map[string]model.ColumnDef, target model.ObjectRef,
	l Loader, opt LoadOptions, progress func(Loaded)) (Loaded, error) {
	c := &coerced{rs: rs, from: rs.Columns(), pairs: pairs, to: to, start: time.Now(), progress: progress}
	names := make([]string, len(pairs))
	for i, p := range pairs {
		names[i] = p.To
	}
	n, err := l.LoadRows(ctx, target, names, c, source.LoadOptions{BatchSize: opt.Batch, Truncate: opt.Replace, Keys: opt.Keys, Confirmed: opt.Confirmed})
	var refused *source.LoadError
	if errors.As(err, &refused) {
		err = &LoadError{Row: refused.Row, Err: refused.Err} // the loader counts the rows as the file does
	}
	return Loaded{Rows: c.read, Written: n, Elapsed: time.Since(c.start)}, err
}

// coerced is a file's rows made a table's values, as a load asks for them.
type coerced struct {
	rs       model.RowStream
	from     []model.ColumnDef
	pairs    []Pair
	to       map[string]model.ColumnDef
	start    time.Time
	progress func(Loaded)
	read     int64
}

func (c *coerced) Columns() []model.ColumnDef {
	out := make([]model.ColumnDef, len(c.pairs))
	for i, p := range c.pairs {
		out[i] = c.to[p.To]
	}
	return out
}

// Close leaves the file's rows to whoever opened them.
func (c *coerced) Close() error { return nil }

func (c *coerced) Next(ctx context.Context) (model.Row, error) {
	row, err := c.rs.Next(ctx) // each of the files' readers stops when ctx ends
	if err != nil {
		return nil, err
	}
	c.read++
	vals, errs := Coerce(row, c.from, c.pairs, c.to)
	if len(errs) > 0 {
		return nil, &LoadError{Row: c.read, Err: errs[0]}
	}
	if c.progress != nil && c.read%reportEvery == 0 {
		c.progress(Loaded{Rows: c.read, Elapsed: time.Since(c.start)})
	}
	return vals, nil
}
