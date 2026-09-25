package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/export"
	"github.com/ikigai-db/ikigai-db/internal/model"
)

// Writing rows where a pipeline can read them (FR-16.1, FR-10.1).
//
// The formats are the application's own, and not a subset: a pipeline asking
// for the same file the window would write should get the same file. What is
// added here is only the naming — a word on a command line for each — and
// where it goes.

// formatNames are the words a command line uses for each format. Short and
// lower-case, as a flag value is; the window's own names are for reading.
var formatNames = map[string]export.Format{
	"csv":    export.CSV,
	"tsv":    export.TSV,
	"json":   export.JSON,
	"ndjson": export.NDJSON,
	"md":     export.Markdown,
	"xlsx":   export.XLSX,
	"sql":    export.SQLInsert,
	"html":   export.HTML,
	"xml":    export.XML,
}

// formatList is every name, for a flag's help and for a refusal.
func formatList() string {
	names := make([]string, 0, len(formatNames))
	for name := range formatNames {
		names = append(names, name)
	}
	// Sorted so that help and errors read the same every time.
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j] < names[j-1]; j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}
	return strings.Join(names, ", ")
}

// rowsOut is where rows go and how they are written.
type rowsOut struct {
	format string
	out    string
	header bool
}

func (r *rowsOut) flags(fs *flag.FlagSet, header bool) {
	fs.StringVar(&r.format, "format", "csv", "how to write the rows: "+formatList())
	fs.StringVar(&r.out, "out", "", "the file to write; standard output when absent")
	fs.BoolVar(&r.header, "header", header, "write the column names as the first row")
}

// options reads the flags as export options, refusing a format nobody has.
func (r *rowsOut) options(name string) (export.Options, error) {
	f, ok := formatNames[strings.ToLower(strings.TrimSpace(r.format))]
	if !ok {
		return export.Options{}, fmt.Errorf("%q is not a format this writes: %s", r.format, formatList())
	}
	if f == export.XLSX && r.out == "" {
		// A workbook is written by seeking about in it, which standard output
		// cannot do. Said here rather than failing halfway through a file.
		return export.Options{}, errors.New("an Excel workbook needs --out: it cannot be written to standard output")
	}
	return export.Options{Format: f, Header: r.header, Name: name}, nil
}

// writer is where the rows go, and what to do when they have gone.
//
// A file is written under a temporary name and moved into place at the end, so
// that a pipeline reading the output of a failed run finds nothing rather than
// half of something.
func (r *rowsOut) writer(e *env) (io.Writer, func(ok bool) error, error) {
	if strings.TrimSpace(r.out) == "" {
		return e.out, func(bool) error { return nil }, nil
	}
	tmp := r.out + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return nil, nil, err
	}
	return f, func(ok bool) error {
		if cerr := f.Close(); cerr != nil && ok {
			os.Remove(tmp)
			return cerr
		}
		if !ok {
			os.Remove(tmp)
			return nil
		}
		return os.Rename(tmp, r.out)
	}, nil
}

// copyRows writes a stream where the flags say, and answers how many rows went.
func (r *rowsOut) copyRows(ctx context.Context, e *env, rs model.RowStream, opt export.Options) (int64, error) {
	dst, done, err := r.writer(e)
	if err != nil {
		return 0, err
	}
	p, err := export.Copy(ctx, dst, rs, opt, nil)
	if cerr := done(err == nil); err == nil {
		err = cerr
	}
	return p.Rows, err
}

// nounRows is "1 row" or "7 rows", for a line somebody reads.
func nounRows(n int64) string {
	if n == 1 {
		return "1 row"
	}
	return fmt.Sprintf("%d rows", n)
}

// exportInserts is the SQL INSERT format, named here so that the one command
// that has to recognise it does not have to know the package's constant.
const exportInserts = export.SQLInsert

// firstRows is a stream cut short: --limit, which is a pipeline saying "enough
// to look at" rather than a filter. Cutting it here rather than in the browse
// keeps it exact whatever the source does with a limit of its own.
type firstRows struct {
	rs   model.RowStream
	left int64
}

func (f *firstRows) Columns() []model.ColumnDef { return f.rs.Columns() }
func (f *firstRows) Close() error               { return f.rs.Close() }

func (f *firstRows) Next(ctx context.Context) (model.Row, error) {
	if f.left <= 0 {
		return nil, io.EOF
	}
	row, err := f.rs.Next(ctx)
	if err != nil {
		return nil, err
	}
	f.left--
	return row, nil
}
