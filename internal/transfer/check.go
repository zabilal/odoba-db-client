package transfer

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// A dry run: every row of a file made the table's values as an import
// would write them, and nothing written (FR-10.5, ADR-0048).

// Problem is a value in a file that would not go in.
type Problem struct {
	Row int64 // the row's place among the file's rows, from 1
	CellError
}

// Checked is what a dry run found, or how far it has got.
type Checked struct {
	Rows     int64     // the rows read
	Bad      int64     // of those, the rows with a value that would not go in
	Values   int64     // the values that would not go in
	Problems []Problem // the first of them, in the file's order
	Elapsed  time.Duration
}

// reportEvery is how many rows a dry run, or a load, reads between reports.
const reportEvery = 500

// Check reads every row of rs and makes it the table's values, as Coerce
// does, writing nothing. It keeps the first limit problems and counts the
// rest. progress, when not nil, is told now and then how far it has got,
// without the problems. A row the file cannot give ends it with an error,
// as ctx ending does, and what it found before.
func Check(ctx context.Context, rs model.RowStream, pairs []Pair, to map[string]model.ColumnDef, limit int, progress func(Checked)) (Checked, error) {
	start := time.Now()
	from := rs.Columns()
	var c Checked
	for {
		row, err := rs.Next(ctx) // each of the files' readers stops when ctx ends
		if err != nil {
			c.Elapsed = time.Since(start)
			if errors.Is(err, io.EOF) {
				return c, nil
			}
			return c, err
		}
		c.Rows++
		if _, errs := Coerce(row, from, pairs, to); len(errs) > 0 {
			c.Bad++
			c.Values += int64(len(errs))
			for _, e := range errs {
				if len(c.Problems) < limit {
					c.Problems = append(c.Problems, Problem{Row: c.Rows, CellError: e})
				}
			}
		}
		if progress != nil && c.Rows%reportEvery == 0 {
			progress(Checked{Rows: c.Rows, Bad: c.Bad, Values: c.Values, Elapsed: time.Since(start)})
		}
	}
}
