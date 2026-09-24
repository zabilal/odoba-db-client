package app

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/capability"
)

// Copying a table's rows into another table (FR-10.9).

// copySource is a table of rows that can also be loaded into.
type copySource struct {
	source.Source
	name    string
	cols    []model.ColumnDef
	rows    []model.Row
	noLoad  bool // it has no bulk loader
	loaded  []model.Row
	target  model.ObjectRef
	columns []string // the columns the load named
	opt     source.LoadOptions
	failAt  int64 // the row the server refuses, from 1
}

func (*copySource) Capabilities() capability.Capabilities {
	return capability.Capabilities{Paradigm: model.ParadigmRelational,
		Data: capability.Data{ExactCount: true}}
}

func (c *copySource) Browse(context.Context, model.ObjectRef, source.BrowseOptions) (model.RowStream, error) {
	return &copyRows{cols: c.cols, rows: c.rows}, nil
}

func (c *copySource) LoadRows(ctx context.Context, target model.ObjectRef, columns []string,
	rows model.RowStream, opt source.LoadOptions) (int64, error) {
	if c.noLoad {
		return 0, errors.New("copySource: no loader")
	}
	c.target, c.columns, c.opt = target, columns, opt
	var n int64
	for {
		row, err := rows.Next(ctx)
		if errors.Is(err, io.EOF) {
			return n, nil
		}
		if err != nil {
			return n, err
		}
		n++
		if c.failAt == n {
			return n - 1, &source.LoadError{Row: n, Err: errors.New("the server refused it")}
		}
		c.loaded = append(c.loaded, row)
	}
}

// loaderless is a table with rows and no way to load any in: an engine
// with no bulk loader written for it.
type loaderless struct {
	source.Source
	cols []model.ColumnDef
}

func (*loaderless) Capabilities() capability.Capabilities {
	return capability.Capabilities{Paradigm: model.ParadigmRelational}
}

func (l *loaderless) Browse(context.Context, model.ObjectRef, source.BrowseOptions) (model.RowStream, error) {
	return &copyRows{cols: l.cols}, nil
}

type copyRows struct {
	cols []model.ColumnDef
	rows []model.Row
	i    int
}

func (s *copyRows) Columns() []model.ColumnDef { return s.cols }
func (s *copyRows) Close() error               { return nil }
func (s *copyRows) Next(ctx context.Context) (model.Row, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.i >= len(s.rows) {
		return nil, io.EOF
	}
	s.i++
	return s.rows[s.i-1], nil
}

func copyCol(name string, class model.TypeClass, native string) model.ColumnDef {
	return model.ColumnDef{Name: name, Type: model.DataType{Class: class, Native: native, Nullable: true}}
}

// browsing opens a BrowseSource over one of these tables.
func browsing(t *testing.T, c *copySource) *BrowseSource {
	t.Helper()
	b, err := NewBrowseSource(context.Background(), c, model.NewRef(model.KindTable, "db", c.name), source.BrowseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func copyOrders() *copySource {
	return &copySource{name: "orders",
		cols: []model.ColumnDef{copyCol("id", model.TypeInteger, "integer"), copyCol("total", model.TypeFloat, "numeric"),
			copyCol("note", model.TypeString, "text")},
		rows: []model.Row{{int64(1), 9.5, "first"}, {int64(2), 8.25, "second"}}}
}

// Rows go from one table to another, made the destination's values, with
// the columns paired by name.
func TestCopyingRowsBetweenTables(t *testing.T) {
	from, to := copyOrders(), &copySource{name: "archive",
		cols: []model.ColumnDef{copyCol("total", model.TypeFloat, "numeric"), copyCol("id", model.TypeInteger, "integer")}}
	got, err := CopyRows(context.Background(), browsing(t, from), browsing(t, to), CopyOptions{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Rows != 2 || got.Written != 2 {
		t.Errorf("it read %d rows and wrote %d", got.Rows, got.Written)
	}
	// The destination's columns, in the destination's order, and only the
	// ones it has.
	if len(to.columns) != 2 || to.columns[0] != "id" || to.columns[1] != "total" {
		t.Errorf("it wrote the columns %v", to.columns)
	}
	if len(to.loaded) != 2 || to.loaded[0][0] != int64(1) {
		t.Errorf("it wrote %+v", to.loaded)
	}
	if to.target.Name() != "archive" {
		t.Errorf("it wrote into %q", to.target.Name())
	}
}

// A value goes into a column of another type made that type, because the
// columns the rows are made into are the destination's.
func TestACopiedValueIsMadeTheDestinationsType(t *testing.T) {
	from, to := copyOrders(), &copySource{name: "archive",
		cols: []model.ColumnDef{copyCol("id", model.TypeString, "text")}}
	if _, err := CopyRows(context.Background(), browsing(t, from), browsing(t, to), CopyOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	if len(to.loaded) != 2 {
		t.Fatalf("it wrote %+v", to.loaded)
	}
	if _, ok := to.loaded[0][0].(string); !ok {
		t.Errorf("the id arrived as %T, and the column it went into holds text", to.loaded[0][0])
	}
}

// What the destination has nowhere to put is said before anything is
// copied, rather than found out halfway.
func TestWhatIsLeftBehindIsSaidFirst(t *testing.T) {
	from, to := copyOrders(), &copySource{name: "archive",
		cols: []model.ColumnDef{copyCol("id", model.TypeInteger, "integer")}}
	if got := CopyLeftBehind(browsing(t, from), browsing(t, to)); len(got) != 2 ||
		got[0] != "total" || got[1] != "note" {
		t.Errorf("it leaves behind %v", got)
	}
	if got := CopyLeftBehind(browsing(t, from), browsing(t, copyOrders())); len(got) != 0 {
		t.Errorf("a copy into the same shape leaves behind %v", got)
	}
}

// A destination with no column in common is refused, rather than writing
// nothing and calling it a copy.
func TestACopyNeedsAColumnInCommon(t *testing.T) {
	from, to := copyOrders(), &copySource{name: "elsewhere",
		cols: []model.ColumnDef{copyCol("name", model.TypeString, "text")}}
	_, err := CopyRows(context.Background(), browsing(t, from), browsing(t, to), CopyOptions{}, nil)
	if err == nil || !strings.Contains(err.Error(), "matches") {
		t.Errorf("it said %v", err)
	}
	if len(to.loaded) != 0 {
		t.Errorf("it wrote %+v", to.loaded)
	}
}

// A connection that cannot be written to in bulk says so, and is not
// offered as somewhere to copy into.
func TestACopyNeedsALoader(t *testing.T) {
	to := &loaderless{cols: []model.ColumnDef{copyCol("id", model.TypeInteger, "integer")}}
	if CanCopyInto(to) {
		t.Error("a source with no bulk loader says it can be copied into")
	}
	b, err := NewBrowseSource(context.Background(), to, model.NewRef(model.KindTable, "db", "archive"), source.BrowseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CopyRows(context.Background(), browsing(t, copyOrders()), b, CopyOptions{}, nil); !errors.Is(err, ErrNoLoader) {
		t.Errorf("it said %v", err)
	}
}

// What the copy is told about replacing and about rows that will not go
// in reaches the loader: they are the import's options because they are
// the same question.
func TestTheCopyPassesOnHowToWrite(t *testing.T) {
	from, to := copyOrders(), &copySource{name: "archive",
		cols: []model.ColumnDef{copyCol("id", model.TypeInteger, "integer")}}
	if _, err := CopyRows(context.Background(), browsing(t, from), browsing(t, to),
		CopyOptions{Replace: true, Confirmed: true, Batch: 500}, nil); err != nil {
		t.Fatal(err)
	}
	if !to.opt.Truncate || !to.opt.Confirmed || to.opt.BatchSize != 500 {
		t.Errorf("the loader was told %+v", to.opt)
	}
}

// A row the server refuses stops the copy and says which row it was.
func TestARefusedRowStopsTheCopy(t *testing.T) {
	from, to := copyOrders(), &copySource{name: "archive", failAt: 2,
		cols: []model.ColumnDef{copyCol("id", model.TypeInteger, "integer")}}
	got, err := CopyRows(context.Background(), browsing(t, from), browsing(t, to), CopyOptions{}, nil)
	if err == nil || !strings.Contains(err.Error(), "row 2") {
		t.Errorf("it said %v", err)
	}
	if got.Written != 1 {
		t.Errorf("it wrote %d rows", got.Written)
	}
}

// Cancelling stops it.
func TestCancellingStopsTheCopy(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	from, to := copyOrders(), &copySource{name: "archive",
		cols: []model.ColumnDef{copyCol("id", model.TypeInteger, "integer")}}
	if _, err := CopyRows(ctx, browsing(t, from), browsing(t, to), CopyOptions{}, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("it said %v", err)
	}
}

// The tables a connection has are listed for choosing where rows go:
// everything with rows of its own, under every database and schema.
func TestListingTheTablesToCopyInto(t *testing.T) {
	got, all, err := Tables(context.Background(), &searchable{}, TableLimit)
	if err != nil || !all {
		t.Fatalf("it listed %d (all %v): %v", len(got), all, err)
	}
	var names []string
	for _, n := range got {
		names = append(names, n.Ref.Name())
	}
	// The table is browsable; the view and the routine in that fake are
	// not, and nor is any database, schema or class folder.
	if len(names) != 1 || names[0] != "orders" {
		t.Errorf("it listed %v", names)
	}
}

// A list longer than anybody picks from stops, and says it stopped: a
// destination missing from a list reads as one that cannot be written to.
func TestListingTablesStopsSomewhere(t *testing.T) {
	got, all, err := Tables(context.Background(), &searchable{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 || all {
		t.Errorf("it listed %d (all %v)", len(got), all)
	}
}

// A tree that cannot be read says so rather than answering with a short
// list, which would read as a connection with few tables.
func TestListingTablesSaysWhenItCannotRead(t *testing.T) {
	_, _, err := Tables(context.Background(), &searchable{children: errors.New("the server hung up")}, TableLimit)
	if err == nil || !strings.Contains(err.Error(), "hung up") {
		t.Errorf("it said %v", err)
	}
}
