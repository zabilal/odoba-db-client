//go:build conformance

package cassandra

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

// Paging a table (T2.51), against a real cluster.

// rowsInPages is how many rows the paging fixture holds: enough for four
// pages of the grid's own size and a short one at the end, and more than the
// limit a statement carries when nobody asks for one — so that a walk past
// that limit is walked rather than cut short.
const rowsInPages = 1200

// pagesSeeded makes a table whose rows have an order of their own: one
// partition, clustered by a number, so the page after a page is the rows
// after its rows and nothing else.
func pagesSeeded(t *testing.T, src source.Source) {
	t.Helper()
	ctx := context.Background()
	s := src.(*cassandraSource)
	if err := s.session.Query(`CREATE TABLE IF NOT EXISTS ` + fixture + `.pages
		(bucket int, n int, name text, PRIMARY KEY ((bucket), n))`).WithContext(ctx).Exec(); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	var held int
	if err := s.session.Query(`SELECT count(*) FROM ` + fixture + `.pages WHERE bucket = 1`).
		WithContext(ctx).Scan(&held); err != nil {
		t.Fatalf("counting the fixture: %v", err)
	}
	if held == rowsInPages {
		return
	}
	for start := 0; start < rowsInPages; start += 100 {
		var batch strings.Builder
		batch.WriteString("BEGIN BATCH ")
		for n := start; n < start+100 && n < rowsInPages; n++ {
			fmt.Fprintf(&batch, "INSERT INTO %s.pages (bucket, n, name) VALUES (1, %d, 'row %d'); ", fixture, n, n)
		}
		batch.WriteString("APPLY BATCH")
		if err := s.session.Query(batch.String()).WithContext(ctx).Exec(); err != nil {
			t.Fatalf("filling the fixture: %v", err)
		}
	}
}

// page reads one window of a browse, as the grid's own fetch does.
func page(t *testing.T, src source.Source, ref model.ObjectRef, offset, limit int64) []model.Row {
	t.Helper()
	rows, err := pageOrError(src, ref, offset, limit)
	if err != nil {
		t.Fatalf("the page at %d: %v", offset, err)
	}
	return rows
}

func pageOrError(src source.Source, ref model.ObjectRef, offset, limit int64) ([]model.Row, error) {
	ctx := context.Background()
	rs, err := src.Browse(ctx, ref, source.BrowseOptions{Offset: offset, Limit: limit})
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []model.Row
	for {
		row, err := rs.Next(ctx)
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
}

// numbers are the n column of rows of the paging fixture.
func numbers(t *testing.T, src source.Source, rows []model.Row) []int64 {
	t.Helper()
	out := make([]int64, 0, len(rows))
	for _, r := range rows {
		n, ok := r[1].(int64)
		if !ok {
			t.Fatalf("a row reads %v", r)
		}
		out = append(out, n)
	}
	return out
}

func TestLivePagesForwardThroughATable(t *testing.T) {
	src := live(t, liveConfig(fixture))
	seeded(t, src)
	pagesSeeded(t, src)
	pages := model.NewRef(model.KindTable, fixture, "pages")

	// The first page, then the one after it: the second resumes where the
	// first stopped rather than reading it again.
	first := numbers(t, src, page(t, src, pages, 0, 256))
	if len(first) != 256 || first[0] != 0 || first[255] != 255 {
		t.Fatalf("the first page holds %d rows, %v…", len(first), first[:min(3, len(first))])
	}
	second := numbers(t, src, page(t, src, pages, 256, 256))
	if len(second) != 256 || second[0] != 256 || second[255] != 511 {
		t.Fatalf("the second page holds %d rows, %v…", len(second), second[:min(3, len(second))])
	}
	// The last page is short, which is how the grid learns where the rows end.
	last := numbers(t, src, page(t, src, pages, 1024, 256))
	if len(last) != rowsInPages-1024 || last[0] != 1024 {
		t.Errorf("the last page holds %d rows, %v…", len(last), last[:min(3, len(last))])
	}
	// A page already read is read again the same way: where it began is
	// remembered.
	again := numbers(t, src, page(t, src, pages, 256, 256))
	if len(again) != 256 || again[0] != 256 {
		t.Errorf("the second page, read again, holds %d rows from %d", len(again), again[0])
	}
	// And a page of another size begins where it was asked to.
	small := numbers(t, src, page(t, src, pages, 256, 10))
	if len(small) != 10 || small[0] != 256 || small[9] != 265 {
		t.Errorf("ten rows from 256 read as %v", small)
	}
	// A page that begins between two already read walks the few rows from
	// the nearer of them rather than from the beginning.
	between := numbers(t, src, page(t, src, pages, 300, 20))
	if len(between) != 20 || between[0] != 300 || between[19] != 319 {
		t.Errorf("twenty rows from 300 read as %v", between)
	}
	// A page read across two of the cluster's own remembers nothing about
	// where it ended — a paging state names the end of a page, not of a row —
	// so the page after it still begins where it was asked to.
	after := numbers(t, src, page(t, src, pages, 320, 20))
	if len(after) != 20 || after[0] != 320 || after[19] != 339 {
		t.Errorf("twenty rows from 320 read as %v", after)
	}
	// Where the pages ended is what was remembered, and why the reads above
	// were exact.
	if states := src.(*cassandraSource).states.known(); states == 0 {
		t.Error("nothing was remembered about where the pages ended")
	}
}

func TestLiveWalksToAPageNothingHasReachedAndRefusesAJump(t *testing.T) {
	src := live(t, liveConfig(fixture))
	seeded(t, src)
	pagesSeeded(t, src)
	pages := model.NewRef(model.KindTable, fixture, "pages")

	// Nothing has been read on this connection, so the page at 512 is walked
	// to: 512 rows is less than this driver will read to answer one page.
	walked := numbers(t, src, page(t, src, pages, 512, 256))
	if len(walked) != 256 || walked[0] != 512 {
		t.Errorf("the page walked to holds %d rows from %d", len(walked), walked[0])
	}

	// A walk longer than the limit a statement carries when nobody asks for
	// one: a page is bounded by the size the cluster is asked for, and a
	// paged read carries no limit of its own to be cut short by.
	far := live(t, liveConfig(fixture))
	long := numbers(t, far, page(t, far, pages, 1100, 20))
	if len(long) != 20 || long[0] != 1100 || long[19] != 1119 {
		t.Errorf("twenty rows from 1100 read as %v", long)
	}

	// A jump far past anything read is refused, in words that say what to do
	// instead: a Cassandra table is read forward.
	fresh := live(t, liveConfig(fixture))
	_, err := pageOrError(fresh, pages, 1_000_000, 256)
	if err == nil {
		t.Fatal("a page a million rows in was answered")
	}
	if !strings.Contains(err.Error(), "forward") {
		t.Errorf("the refusal says %q", err)
	}
}

func TestLiveOrdersRowsOnlyByWhatClustersThem(t *testing.T) {
	src := live(t, liveConfig(fixture))
	seeded(t, src)
	pagesSeeded(t, src)
	ctx := context.Background()
	pages := model.NewRef(model.KindTable, fixture, "pages")

	// A clustering column is what rows are ordered by within a partition.
	rs, err := src.Browse(ctx, pages, source.BrowseOptions{Limit: 5,
		Filters: []source.Filter{{Column: "bucket", Op: source.OpEqual, Values: []any{1}}},
		Sorts:   []source.Sort{{Column: "n", Descending: true}}})
	if err != nil {
		t.Fatalf("ordered by what clusters the rows: %v", err)
	}
	rs.Close()

	// The partition key is not: a cluster does not order whole tables.
	for _, sort := range []source.Sort{{Column: "bucket"}, {Column: "name"}} {
		if _, err := src.Browse(ctx, pages, source.BrowseOptions{Limit: 5,
			Sorts: []source.Sort{sort}}); err == nil {
			t.Errorf("rows were ordered by %s", sort.Column)
		} else if !strings.Contains(err.Error(), "partition") && !strings.Contains(err.Error(), "order") {
			t.Errorf("ordering by %s says %q", sort.Column, err)
		}
	}
}

func TestLiveARowIsAddressedByWhatAddressesIt(t *testing.T) {
	src := live(t, liveConfig(fixture))
	seeded(t, src)
	ctx := context.Background()
	rs, err := src.Browse(ctx, model.NewRef(model.KindTable, fixture, "people"), source.BrowseOptions{Limit: 1})
	if err != nil {
		t.Fatalf("browse: %v", err)
	}
	defer rs.Close()
	id := model.IdentityOf(rs)
	if !id.Editable() || strings.Join(id.Columns, ", ") != "country, id" {
		t.Errorf("a row is addressed by %+v", id)
	}
	// A column says what it is, as the tree said it.
	cols := rs.Columns()
	if len(cols) == 0 || cols[0].Name != "country" {
		t.Errorf("the columns read %+v", cols)
	}
}
