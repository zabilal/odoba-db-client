package conformance

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// checkLoader imports rows into the Writable table (FR-10.6, ADR-0050,
// ADR-0052): a batch a transaction, a row the server refuses stopping it
// with the batches before it kept, emptying the table first in one
// transaction whole, a row whose key is taken updating the row there, rows
// refused left out when told, and the guard asked first. It runs last, as it leaves rows behind.
func checkLoader(t *testing.T, target Target) {
	if target.Writable.IsZero() {
		t.Skip("no Writable target configured")
	}
	ctx := context.Background()
	src := target.Open(ctx, t)
	defer src.Close()
	l, ok := src.(source.BulkLoader)
	if !ok {
		t.Skip("source does not implement BulkLoader")
	}
	ref := target.Writable
	cols := []string{"id", "name", "n"}
	load := func(l source.BulkLoader, opt source.LoadOptions, rows ...model.Row) (int64, error) {
		return l.LoadRows(ctx, ref, cols, &loadRows{rows: rows}, opt)
	}
	row := func(id int64, name string) model.Row { return model.Row{id, name, nil} }
	var le *source.LoadError

	n, err := load(l, source.LoadOptions{Truncate: true, Confirmed: true, BatchSize: 2}, row(10, "ten"), row(11, "eleven"), row(12, "twelve"))
	if err != nil || n != 3 {
		t.Fatalf("a load that empties the table first: %v %d", err, n)
	}
	if got := tableRows(t, src, ref); got != "[10 ten <nil>] [11 eleven <nil>] [12 twelve <nil>]" {
		t.Fatalf("the table holds the rows loaded, and only them: %s", got)
	}

	n, err = load(l, source.LoadOptions{BatchSize: 2}, row(20, "a"), row(21, "b"), row(22, "c"), row(10, "again"))
	if !errors.As(err, &le) || le.Row != 4 || n != 2 {
		t.Errorf("a row the server refuses stops the load, the batch before it kept: %v %d", err, n)
	}
	want := "[10 ten <nil>] [11 eleven <nil>] [12 twelve <nil>] [20 a <nil>] [21 b <nil>]"
	if got := tableRows(t, src, ref); got != want {
		t.Errorf("after a load stopped at its fourth row: %s", got)
	}

	if _, err := load(l, source.LoadOptions{Truncate: true, Confirmed: true}, row(30, "x"), row(30, "twice")); !errors.As(err, &le) || le.Row != 2 {
		t.Errorf("a load emptying the table stops at a row refused: %v", err)
	}
	if _, err := load(l, source.LoadOptions{Truncate: true}, row(40, "x")); !errors.Is(err, source.ErrConfirmationRequired) {
		t.Errorf("emptying the table without consent: %v", err)
	}
	stopped := errors.New("the file could not be read further")
	if n, err := l.LoadRows(ctx, ref, cols, &loadRows{rows: []model.Row{row(70, "x")}, err: stopped}, source.LoadOptions{}); !errors.Is(err, stopped) || n != 0 {
		t.Errorf("rows that stop coming stop the load: %v %d", err, n)
	}
	if got := tableRows(t, src, ref); got != want {
		t.Errorf("a load emptying the table that fails, or whose rows stop coming, leaves the table as it was: %s", got)
	}

	// A row whose key is taken updates the row there (ADR-0052).
	if n, err := load(l, source.LoadOptions{Keys: []string{"id"}}, row(10, "TEN"), row(13, "thirteen")); err != nil || n != 2 {
		t.Errorf("an upsert: %v %d", err, n)
	}
	want = "[10 TEN <nil>] [11 eleven <nil>] [12 twelve <nil>] [13 thirteen <nil>] [20 a <nil>] [21 b <nil>]"
	if got := tableRows(t, src, ref); got != want {
		t.Errorf("after an upsert, the row with its key updated and the other added: %s", got)
	}

	// A row refused is left out when told, and the load goes on (ADR-0053).
	var left []int64
	skip := source.LoadOptions{BatchSize: 2, OnError: "skip", Skipped: func(e *source.LoadError) { left = append(left, e.Row) }}
	if n, err := load(l, skip, row(20, "again"), row(30, "thirty"), row(31, "thirty-one")); err != nil || n != 2 || len(left) != 1 || left[0] != 1 {
		t.Errorf("a row refused, left out: %v %d, left out %v", err, n, left)
	}
	n, err = load(l, source.LoadOptions{OnError: "collect", MaxErrors: 1}, row(40, "forty"), row(20, "again"), row(21, "again"))
	if !errors.As(err, &le) || le.Row != 3 || n != 0 {
		t.Errorf("collecting stops at the row after the most left out: %v %d", err, n)
	}
	want = "[10 TEN <nil>] [11 eleven <nil>] [12 twelve <nil>] [13 thirteen <nil>] [20 a <nil>] [21 b <nil>] [30 thirty <nil>] [31 thirty-one <nil>]"
	if got := tableRows(t, src, ref); got != want {
		t.Errorf("after rows left out, and a load that stopped: %s", got)
	}

	if target.OpenGuarded == nil {
		return
	}
	ro := target.OpenGuarded(ctx, t, source.Guard{ReadOnly: true})
	defer ro.Close()
	if _, err := load(ro.(source.BulkLoader), source.LoadOptions{Confirmed: true}, row(50, "ro")); !errors.Is(err, source.ErrReadOnly) {
		t.Errorf("a read-only connection loads nothing: %v", err)
	}
	prod := target.OpenGuarded(ctx, t, source.Guard{Environment: source.EnvProduction})
	defer prod.Close()
	if _, err := load(prod.(source.BulkLoader), source.LoadOptions{}, row(60, "prod")); !errors.Is(err, source.ErrConfirmationRequired) {
		t.Errorf("production without consent: %v", err)
	}
	if got := tableRows(t, src, ref); got != want {
		t.Errorf("after the loads refused: %s", got)
	}
}

// loadRows streams the rows a load is given, then err, or io.EOF.
type loadRows struct {
	rows []model.Row
	err  error
}

func (r *loadRows) Columns() []model.ColumnDef { return nil }
func (r *loadRows) Close() error               { return nil }
func (r *loadRows) Next(context.Context) (model.Row, error) {
	if len(r.rows) == 0 {
		if r.err != nil {
			return nil, r.err
		}
		return nil, io.EOF
	}
	row := r.rows[0]
	r.rows = r.rows[1:]
	return row, nil
}

// tableRows is a table's rows in order of id, as they read.
func tableRows(t *testing.T, src source.Source, ref model.ObjectRef) string {
	t.Helper()
	ctx := context.Background()
	rs, err := src.Browse(ctx, ref, source.BrowseOptions{Sorts: []source.Sort{{Column: "id"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	var out []string
	for {
		r, err := rs.Next(ctx)
		if errors.Is(err, io.EOF) {
			return strings.Join(out, " ")
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprint(r))
	}
}
