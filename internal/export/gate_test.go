//go:build !race

package export

import (
	"bytes"
	"context"
	"io"
	"runtime"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/testutil/race"
)

// NFR-P7: exporting a million rows to CSV holds memory flat and manages at
// least a hundred thousand rows a second.
//
// The budget names local Postgres, and the server's share of it is the
// driver's business — the conformance suite reads from real servers. What
// this gate holds is the part this package decides: the rows it is given
// are written as fast as the budget says, and nothing accumulates while it
// does. A writer that buffered a million rows would pass a throughput test
// and fail the sentence the budget is in.

// millionRows is a stream of a million rows that allocates nothing per row
// beyond the row itself: what is measured is the export, not the source.
type millionRows struct {
	left int64
	row  model.Row
}

func (m *millionRows) Columns() []model.ColumnDef {
	return []model.ColumnDef{
		{Name: "id", Type: model.DataType{Class: model.TypeInteger, Native: "bigint"}},
		{Name: "name", Type: model.DataType{Class: model.TypeString, Native: "text"}},
		{Name: "total", Type: model.DataType{Class: model.TypeFloat, Native: "numeric"}},
	}
}

func (m *millionRows) Next(ctx context.Context) (model.Row, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if m.left <= 0 {
		return nil, io.EOF
	}
	m.left--
	return m.row, nil
}

func (*millionRows) Close() error { return nil }

func TestGateP7ExportAMillionRows(t *testing.T) {
	if testing.Short() {
		t.Skip("gate skipped in -short")
	}
	race.SkipTimingGate(t)

	const rows = 1_000_000
	src := &millionRows{left: rows, row: model.Row{int64(42), "a name with, a comma", 12.5}}

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	start := time.Now()
	p, err := Copy(context.Background(), io.Discard, src, Options{Format: CSV, Header: true}, nil)
	took := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if p.Rows != rows {
		t.Fatalf("it wrote %d rows", p.Rows)
	}

	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	// Throughput, against the budget's hundred thousand a second.
	perSecond := float64(rows) / took.Seconds()
	if perSecond < 100_000 {
		t.Errorf("exported %.0f rows/s; NFR-P7 asks for 100,000", perSecond)
	}

	// Flat: what is held afterwards is the buffer and the writer, not the
	// rows. A megabyte of headroom covers the 64KB buffer, the encoder and
	// the noise the test binary's own heap makes.
	grew := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	if grew > 1<<20 {
		t.Errorf("the heap grew by %d bytes over a million rows; NFR-P7 asks for flat", grew)
	}
	t.Logf("%.0f rows/s, heap %+d bytes", perSecond, grew)
}

// And the bytes are right: a gate that measured a writer which wrote
// nothing would be a fast export of an empty file.
func TestGateP7WritesWhatItSays(t *testing.T) {
	var buf bytes.Buffer
	src := &millionRows{left: 3, row: model.Row{int64(1), "x", 2.5}}
	if _, err := Copy(context.Background(), &buf, src, Options{Format: CSV, Header: true}, nil); err != nil {
		t.Fatal(err)
	}
	if got := bytes.Count(buf.Bytes(), []byte("\n")); got != 4 {
		t.Errorf("it wrote %d lines for a header and three rows:\n%s", got, buf.String())
	}
}
