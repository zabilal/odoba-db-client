// Package transfer moves rows between files and sources (FR-10). It reads
// the files an import takes as streams of rows, so that memory does not grow
// with the file (FR-10.3). It holds no UI (ARCH-1).
package transfer

import (
	"bufio"
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// Format is a file's format.
type Format int

const (
	CSV    Format = iota
	TSV           // quoted as CSV is
	JSON          // an array of objects
	NDJSON        // an object a line
	XLSX          // an Excel workbook
)

func (f Format) String() string {
	switch f {
	case CSV:
		return "CSV"
	case TSV:
		return "TSV"
	case JSON:
		return "JSON"
	case NDJSON:
		return "NDJSON"
	case XLSX:
		return "Excel"
	}
	return fmt.Sprintf("Format(%d)", int(f))
}

// Options say how a file is read. Here they are given; finding them from
// the file itself is T2.18's.
type Options struct {
	Format Format
	// Header takes a delimited file's, or a sheet's, first row as the
	// columns' names. Without it the columns are numbered.
	Header bool
	// Sheet names the sheet of a workbook to read; empty is the first.
	Sheet string
}

// Open reads a file of size bytes as rows (FR-10.4, ADR-0044). The stream
// reads the file as its rows are asked for, and must be closed.
func Open(r io.ReaderAt, size int64, opt Options) (model.RowStream, error) {
	switch opt.Format {
	case CSV, TSV:
		return openDelimited(io.NewSectionReader(r, 0, size), opt)
	case JSON, NDJSON:
		return openRecords(io.NewSectionReader(r, 0, size), opt.Format == JSON)
	case XLSX:
		return openSheet(r, size, opt)
	}
	return nil, fmt.Errorf("transfer: cannot read %v", opt.Format)
}

// delimited reads CSV, or TSV quoted as CSV is, as export writes them. A
// field left empty is NULL, as export writes NULL. The first record's width
// is every row's: a shorter row is padded with NULL, and a longer one is an
// error naming its line.
type delimited struct {
	r     *csv.Reader
	cols  []model.ColumnDef
	first []string // the first record, when it is a row rather than names
}

func openDelimited(r io.Reader, opt Options) (*delimited, error) {
	br := bufio.NewReader(r)
	if b, _ := br.Peek(3); bytes.Equal(b, []byte{0xEF, 0xBB, 0xBF}) {
		_, _ = br.Discard(3) // a byte-order mark is not text
	}
	c := csv.NewReader(br)
	c.LazyQuotes, c.FieldsPerRecord = true, -1
	if opt.Format == TSV {
		c.Comma = '\t'
	}
	d := &delimited{r: c}
	rec, err := c.Read()
	switch {
	case err == io.EOF:
		return d, nil // an empty file: no columns, no rows
	case err != nil:
		return nil, err
	case opt.Header:
		d.cols = named(rec)
	default:
		d.cols, d.first = named(make([]string, len(rec))), slices.Clone(rec)
	}
	return d, nil
}

func (d *delimited) Columns() []model.ColumnDef { return d.cols }

func (d *delimited) Next(ctx context.Context) (model.Row, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rec := d.first
	d.first = nil
	if rec == nil {
		var err error
		if rec, err = d.r.Read(); err != nil {
			return nil, err // io.EOF ends the rows
		}
	}
	if len(rec) > len(d.cols) {
		line, _ := d.r.FieldPos(0)
		return nil, fmt.Errorf("transfer: line %d has %d fields, the first had %d", line, len(rec), len(d.cols))
	}
	row := make(model.Row, len(d.cols))
	for i, f := range rec {
		if f != "" {
			row[i] = f
		}
	}
	return row, nil
}

func (d *delimited) Close() error { return nil }

// named makes names the columns', each of text that may be NULL: an empty
// name is its column's number, and a repeated one is numbered after its
// first.
func named(names []string) []model.ColumnDef {
	seen := map[string]int{}
	cols := make([]model.ColumnDef, len(names))
	for i, n := range names {
		if n = strings.TrimSpace(n); n == "" {
			n = fmt.Sprintf("column %d", i+1)
		}
		if seen[n]++; seen[n] > 1 {
			n = fmt.Sprintf("%s %d", n, seen[n])
		}
		cols[i] = model.ColumnDef{Name: n, Type: model.DataType{Class: model.TypeString, Nullable: true}}
	}
	return cols
}
