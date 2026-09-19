package app

import (
	"context"
	"io"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/capability"
)

// Filtering what has been read (T2.67, FR-13.9).

// numbered is a source of rows numbered from zero, which a filter reads
// through. It counts what it was asked for, so that a test can say how much
// of the source a filter had to read.
type numbered struct {
	n       int64
	fetches int
}

func (numbered) Columns() []model.ColumnDef {
	return []model.ColumnDef{{Name: "n", Type: model.DataType{Class: model.TypeInteger}}}
}

func (s *numbered) Fetch(_ context.Context, offset, limit int64) ([]model.Row, error) {
	s.fetches++
	var out []model.Row
	for i := offset; i < offset+limit && i < s.n; i++ {
		out = append(out, model.Row{i})
	}
	return out, nil
}

func (s *numbered) Count(context.Context) (int64, error) { return s.n, nil }

// everyTenth matches 0, 10, 20 … so that a window of matches is spread over
// far more rows than the window holds.
func everyTenth() source.Filter {
	var vals []any
	for i := int64(0); i < 1000; i += 10 {
		vals = append(vals, i)
	}
	return source.Filter{Column: "n", Op: source.OpIn, Values: vals}
}

func filtered(t *testing.T, src *numbered, filters ...source.Filter) *FilteredRows {
	t.Helper()
	f, err := NewFilteredRows(src, filters)
	if err != nil {
		t.Fatalf("filtering: %v", err)
	}
	return f
}

func TestAFilteredWindowIsFilledRatherThanShortened(t *testing.T) {
	src := &numbered{n: 1000}
	f := filtered(t, src, everyTenth())

	// A grid takes a short window to mean the data ran out, so a filter that
	// drops nine rows in ten must read further rather than hand back three.
	rows, err := f.Fetch(context.Background(), 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 10 {
		t.Fatalf("a window of ten came back with %d", len(rows))
	}
	if got := rows[0][0]; got != int64(0) {
		t.Errorf("the window starts at %v", got)
	}
	if got := rows[9][0]; got != int64(90) {
		t.Errorf("the window ends at %v, and every tenth row matches", got)
	}

	// A filter sparse enough that one page of the source cannot fill a window
	// must read further, rather than hand back what the first page happened to
	// hold.
	sparse := &numbered{n: 1000}
	var every100th []any
	for i := int64(0); i < 1000; i += 100 {
		every100th = append(every100th, i)
	}
	g := filtered(t, sparse, source.Filter{Column: "n", Op: source.OpIn, Values: every100th})
	few, err := g.Fetch(context.Background(), 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(few) != 10 {
		t.Errorf("a window of ten came back with %d, and one page of the source holds five matches", len(few))
	}
	if sparse.fetches < 2 {
		t.Errorf("it filled the window in %d fetches, so it never read past the first page", sparse.fetches)
	}

	// The window after it carries on where that one stopped.
	rows, err = f.Fetch(context.Background(), 10, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 10 || rows[0][0] != int64(100) {
		t.Errorf("the second window is %v", rows)
	}
}

func TestAShortWindowStillMeansTheEnd(t *testing.T) {
	src := &numbered{n: 35}
	f := filtered(t, src, everyTenth()) // 0, 10, 20, 30

	rows, err := f.Fetch(context.Background(), 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 {
		t.Errorf("%d rows match, and four should", len(rows))
	}
	// Past the matches there is nothing, which is how the grid learns it has
	// them all.
	rows, err = f.Fetch(context.Background(), 4, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Errorf("past the last match there were %d rows", len(rows))
	}
}

func TestHowManyMatchIsUnknownUntilItHasBeenRead(t *testing.T) {
	src := &numbered{n: 35}
	f := filtered(t, src, everyTenth())

	// Before anything is read, how many match cannot be known — and a number
	// that is only what has been found so far would draw a scrollbar that lies.
	if n, _ := f.Count(context.Background()); n != -1 {
		t.Errorf("before reading, the count is %d", n)
	}
	if _, err := f.Fetch(context.Background(), 0, 10); err != nil {
		t.Fatal(err)
	}
	if n, _ := f.Count(context.Background()); n != 4 {
		t.Errorf("after reading to the end, the count is %d", n)
	}
	rows, atEnd, stopped := f.Read()
	if rows != 35 || !atEnd || stopped {
		t.Errorf("it read %d rows, at the end %v, stopped %v", rows, atEnd, stopped)
	}
}

func TestAFilterStopsRatherThanReadingForever(t *testing.T) {
	was := filterScan
	filterScan = 100
	t.Cleanup(func() { filterScan = was })

	// A log has no end worth reading to. A filter that matches nothing must
	// stop somewhere and say so, rather than read until the memory runs out.
	src := &numbered{n: 10_000}
	f := filtered(t, src, source.Filter{Column: "n", Op: source.OpEqual, Values: []any{int64(-1)}})
	rows, err := f.Fetch(context.Background(), 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Errorf("a filter matching nothing returned %d rows", len(rows))
	}
	read, atEnd, stopped := f.Read()
	if !stopped || atEnd {
		t.Errorf("it read %d rows, at the end %v, stopped %v", read, atEnd, stopped)
	}
	if read > filterScan+filterPage {
		t.Errorf("it read %d rows, past its bound of %d", read, filterScan)
	}
	// And it does not claim a count it never finished finding.
	if n, _ := f.Count(context.Background()); n != -1 {
		t.Errorf("a filter that stopped early counted %d", n)
	}
}

func TestAFilterThatCannotBeReadIsRefusedNow(t *testing.T) {
	// A pattern that is not a pattern is a mistake somebody can fix, not rows
	// that quietly never match.
	if _, err := NewFilteredRows(&numbered{n: 1},
		[]source.Filter{{Column: "n", Op: source.OpRegex, Values: []any{"("}}}); err == nil {
		t.Error("a broken regular expression was accepted")
	}
}

// filterSource is a source that says only what paradigm it is and whether the
// server filters for it: enough to ask where a filter belongs.
type filterSource struct {
	source.Source
	paradigm     model.Paradigm
	serverFilter bool
}

func (f *filterSource) Capabilities() capability.Capabilities {
	return capability.Capabilities{
		Paradigm: f.paradigm,
		Data:     capability.Data{ServerFilter: f.serverFilter},
		Objects:  map[model.ObjectKind]bool{model.KindTopic: true, model.KindTable: true},
	}
}

func (f *filterSource) Browse(context.Context, model.ObjectRef, source.BrowseOptions) (model.RowStream, error) {
	return &emptyRows{}, nil
}

type emptyRows struct{}

func (*emptyRows) Columns() []model.ColumnDef              { return nil }
func (*emptyRows) Next(context.Context) (model.Row, error) { return nil, io.EOF }
func (*emptyRows) Close() error                            { return nil }

func TestAFilterBelongsHereOnlyWhereTheServerWillNotDoIt(t *testing.T) {
	for _, c := range []struct {
		what         string
		paradigm     model.Paradigm
		serverFilter bool
		want         bool
	}{
		{"a log, which a broker will not filter", model.ParadigmStream, false, true},
		// The clause that matters: filtering twice would read every row here
		// to narrow what the server had already narrowed.
		{"a stream whose server does filter", model.ParadigmStream, true, false},
		{"a table, filtered where the data is", model.ParadigmRelational, false, false},
	} {
		b, err := NewBrowseSource(context.Background(),
			&filterSource{paradigm: c.paradigm, serverFilter: c.serverFilter}, orders, source.BrowseOptions{})
		if err != nil {
			t.Fatalf("%s: %v", c.what, err)
		}
		if got := b.FiltersHere(); got != c.want {
			t.Errorf("%s: filtered here %v, want %v", c.what, got, c.want)
		}
	}
}
