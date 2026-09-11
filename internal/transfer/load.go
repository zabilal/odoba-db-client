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
// table's rows, one transaction whole. A row that would not go in, or that
// the server refuses, stops the load, or is left out when told (ADR-0054).

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

	// OnError is what a row that would not go in, or that the server
	// refuses, does: "abort", or none, stops the load; "skip" leaves it out
	// and goes on; "collect" leaves out up to MaxErrors rows and stops at
	// the next.
	OnError   string
	MaxErrors int

	// Skipped, when given, is told of each row left out, by its place among
	// the file's rows.
	Skipped func(*LoadError)
}

// Loaded is how far a load has got.
type Loaded struct {
	Rows    int64 // the file's rows read
	Written int64 // of those, the rows written, once the load has ended
	Left    int64 // the rows left out
	Elapsed time.Duration
}

// LoadError is a load stopped, or a row left out, at one of the file's
// rows. Nothing of a stopped row's batch was written.
type LoadError struct {
	Row int64 // the row's place among the file's rows, from 1
	Err error
}

func (e *LoadError) Error() string { return fmt.Sprintf("row %d: %v", e.Row, e.Err) }
func (e *LoadError) Unwrap() error { return e.Err }

// Load writes every row of rs into target as new rows, through l, each made
// the table's values as Coerce does. It stops at the first row with a value
// that would not go in, and at the first the server refuses, saying which,
// unless told to leave such rows out; the batches before it stay written,
// unless the load was replacing the table's rows, which are then as they
// were. progress, when not nil, is told now and then how many rows have
// been read. ctx ending stops it, and the batch under way is undone.
func Load(ctx context.Context, rs model.RowStream, pairs []Pair, to map[string]model.ColumnDef, target model.ObjectRef,
	l Loader, opt LoadOptions, progress func(Loaded)) (Loaded, error) {
	skipping := opt.OnError == "skip" || opt.OnError == "collect"
	switch {
	case opt.OnError != "" && opt.OnError != "abort" && !skipping:
		return Loaded{}, fmt.Errorf("transfer: the %q error policy is not taken", opt.OnError)
	case opt.OnError == "collect" && opt.MaxErrors <= 0:
		return Loaded{}, errors.New("transfer: collecting rows left out needs the most to leave out")
	}
	c := &coerced{rs: rs, from: rs.Columns(), pairs: pairs, to: to, start: time.Now(), progress: progress, opt: opt, skipping: skipping}
	names := make([]string, len(pairs))
	for i, p := range pairs {
		names[i] = p.To
	}
	lo := source.LoadOptions{BatchSize: opt.Batch, Truncate: opt.Replace, Keys: opt.Keys, Confirmed: opt.Confirmed}
	if skipping {
		// The loader leaves out what it is refused; the most is counted
		// here, with the rows that would not go in.
		lo.OnError = "skip"
		lo.Skipped = func(e *source.LoadError) { c.leave(&LoadError{Row: c.fileRow(e.Row), Err: e.Err}) }
	}
	n, err := l.LoadRows(ctx, target, names, c, lo)
	var refused *source.LoadError
	if errors.As(err, &refused) {
		err = &LoadError{Row: refused.Row, Err: refused.Err} // nothing is left out before it, as nothing is left out
	}
	return Loaded{Rows: c.read, Written: n, Left: c.left, Elapsed: time.Since(c.start)}, err
}

// coerced is a file's rows made a table's values, as a load asks for them,
// with the rows that would not go in left out where the load is told to.
type coerced struct {
	rs       model.RowStream
	from     []model.ColumnDef
	pairs    []Pair
	to       map[string]model.ColumnDef
	start    time.Time
	progress func(Loaded)
	opt      LoadOptions
	skipping bool

	read    int64 // the file's rows read
	left    int64 // the rows left out
	dropped int64 // of those, the rows left out here, before the loader was given them
	over    error // a row past the most to leave out, which ends the load
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
	for {
		if c.over != nil {
			return nil, c.over
		}
		row, err := c.rs.Next(ctx) // each of the files' readers stops when ctx ends
		if err != nil {
			return nil, err
		}
		c.read++
		if c.progress != nil && c.read%reportEvery == 0 {
			c.progress(Loaded{Rows: c.read, Elapsed: time.Since(c.start)})
		}
		vals, errs := Coerce(row, c.from, c.pairs, c.to)
		if len(errs) == 0 {
			return vals, nil
		}
		e := &LoadError{Row: c.read, Err: errs[0]}
		if !c.skipping {
			return nil, e
		}
		c.dropped++
		c.leave(e)
	}
}

// fileRow is the place among the file's rows of the loader's k'th row: k,
// and one for each row left out here before it. A loader tells of a row it
// refuses as it refuses it, before it asks for the next (LoadOptions.Skipped).
func (c *coerced) fileRow(k int64) int64 { return k + c.dropped }

// leave leaves a row out and tells of it, unless it is one past the most a
// collect leaves out, which then ends the load.
func (c *coerced) leave(e *LoadError) {
	if c.opt.OnError == "collect" && c.left >= int64(c.opt.MaxErrors) {
		c.over = e // the load ends at it: nothing more is read
		return
	}
	c.left++
	if c.opt.Skipped != nil {
		c.opt.Skipped(e)
	}
}
