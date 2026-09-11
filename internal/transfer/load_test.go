package transfer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

var (
	itemsRef     = model.NewRef(model.KindTable, "db", "s", "items")
	errDuplicate = errors.New("duplicate key")
)

// fakeLoader keeps what a load gives it, committing a batch at a time as
// the drivers do, and refuses the row failAt says, or everything with err.
type fakeLoader struct {
	target  model.ObjectRef
	columns []string
	types   []model.ColumnDef
	opt     source.LoadOptions
	rows    []model.Row
	failAt  int64
	err     error
}

func (f *fakeLoader) LoadRows(ctx context.Context, target model.ObjectRef, columns []string, rows model.RowStream, opt source.LoadOptions) (int64, error) {
	f.target, f.columns, f.types, f.opt = target, columns, rows.Columns(), opt
	if f.err != nil {
		return 0, f.err
	}
	size := int64(max(opt.BatchSize, 1))
	committed := func() int64 {
		if opt.Truncate {
			return 0
		}
		return int64(len(f.rows)) / size * size
	}
	for {
		r, err := rows.Next(ctx)
		if errors.Is(err, io.EOF) {
			return int64(len(f.rows)), nil
		}
		if err != nil {
			return committed(), err
		}
		if int64(len(f.rows))+1 == f.failAt {
			return committed(), &source.LoadError{Row: f.failAt, Err: errDuplicate}
		}
		f.rows = append(f.rows, r)
	}
}

// itemsCSV is a file of ids and names, one line a row.
func itemsCSV(lines ...string) string { return "id,name\n" + strings.Join(lines, "\n") + "\n" }

var (
	itemsTo    = columns(col("id", model.TypeInteger, false), col("name", model.TypeString, true))
	itemsPairs = []Pair{{0, "id"}, {1, "name"}}
)

func load(t *testing.T, ctx context.Context, file string, f *fakeLoader, opt LoadOptions, progress func(Loaded)) (Loaded, error) {
	t.Helper()
	return Load(ctx, csvRows(t, file), itemsPairs, itemsTo, itemsRef, f, opt, progress)
}

func TestRowsAreMadeTheTablesAndLoaded(t *testing.T) {
	var lines []string
	for i := 1; i <= 2*reportEvery+3; i++ {
		lines = append(lines, fmt.Sprintf("%d,n%d", i, i))
	}
	lines[1] = "2," // a name left empty goes in as NULL
	f := &fakeLoader{}
	var told []Loaded
	l, err := load(t, context.Background(), itemsCSV(lines...), f, LoadOptions{Batch: 7, Confirmed: true}, func(p Loaded) { told = append(told, p) })
	if err != nil || l.Rows != 1003 || l.Written != 1003 || l.Elapsed <= 0 {
		t.Fatalf("%v %+v", err, l)
	}
	if !f.target.Equal(itemsRef) || !reflect.DeepEqual(f.columns, []string{"id", "name"}) || !reflect.DeepEqual(f.opt, source.LoadOptions{BatchSize: 7, Confirmed: true}) {
		t.Errorf("into the table, its columns named, as told: %v %v %+v", f.target, f.columns, f.opt)
	}
	if len(f.types) != 2 || f.types[0].Type.Class != model.TypeInteger || f.types[1].Name != "name" {
		t.Errorf("the rows are the table's columns: %+v", f.types)
	}
	if !reflect.DeepEqual(f.rows[0], model.Row{int64(1), "n1"}) || f.rows[1][1] != nil || f.rows[1002][0] != int64(1003) {
		t.Errorf("each row made the table's values, NULL too: %v %v", f.rows[0], f.rows[1])
	}
	if len(told) != 2 || told[0].Rows != 500 || told[1].Rows != 1000 || told[1].Elapsed <= 0 || told[1].Written != 0 {
		t.Errorf("told how many rows have been read, now and then: %+v", told)
	}
	f = &fakeLoader{}
	if _, err := load(t, context.Background(), itemsCSV("1,a"), f, LoadOptions{Replace: true, Confirmed: true}, nil); err != nil || !f.opt.Truncate {
		t.Errorf("replacing empties the table first: %v %+v", err, f.opt)
	}
	f = &fakeLoader{}
	if _, err := load(t, context.Background(), itemsCSV("1,a"), f, LoadOptions{Keys: []string{"id"}}, nil); err != nil || !reflect.DeepEqual(f.opt.Keys, []string{"id"}) {
		t.Errorf("a row whose key is taken updates it: %v %+v", err, f.opt)
	}
}

func TestALoadStopsAtAValueThatWouldNotGoIn(t *testing.T) {
	f := &fakeLoader{}
	l, err := load(t, context.Background(), itemsCSV("1,a", "2,b", "x,c", "4,d"), f, LoadOptions{Batch: 2}, nil)
	var le *LoadError
	var ce CellError
	if !errors.As(err, &le) || le.Row != 3 || !errors.As(err, &ce) || ce.Column != "id" || err.Error() != "row 3: id: not a whole number (x)" {
		t.Fatalf("stopped at the row, with why: %v", err)
	}
	if l.Rows != 3 || l.Written != 2 || len(f.rows) != 2 {
		t.Errorf("the batch before it written, and nothing after: %+v", l)
	}
}

func TestALoadStopsWhereTheServerRefusesARow(t *testing.T) {
	file := itemsCSV("1,a", "2,b", "3,c", "4,d", "5,e")
	f := &fakeLoader{failAt: 4}
	l, err := load(t, context.Background(), file, f, LoadOptions{Batch: 2}, nil)
	var le *LoadError
	if !errors.As(err, &le) || le.Row != 4 || !errors.Is(err, errDuplicate) || l.Written != 2 || l.Rows != 4 {
		t.Errorf("the row the server refused, and the batches before it: %v %+v", err, l)
	}
	f = &fakeLoader{failAt: 2}
	if l, err := load(t, context.Background(), file, f, LoadOptions{Replace: true, Confirmed: true}, nil); !errors.As(err, &le) || le.Row != 2 || l.Written != 0 {
		t.Errorf("a replace refused leaves the table as it was: %v %+v", err, l)
	}
}

func TestALoadTheSourceRefusesWritesNothing(t *testing.T) {
	file := itemsCSV("1,a")
	if l, err := load(t, context.Background(), file, &fakeLoader{err: source.ErrReadOnly}, LoadOptions{}, nil); !errors.Is(err, source.ErrReadOnly) || l.Written != 0 {
		t.Errorf("refused: %v %+v", err, l)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := &fakeLoader{}
	if _, err := load(t, ctx, file, f, LoadOptions{}, nil); !errors.Is(err, context.Canceled) || len(f.rows) != 0 {
		t.Errorf("stopped before it began: %v", err)
	}
	var many []string
	for i := range reportEvery + 1 {
		many = append(many, fmt.Sprint(i, ",n"))
	}
	if l, err := load(t, context.Background(), itemsCSV(many...), &fakeLoader{}, LoadOptions{}, nil); err != nil || l.Written != reportEvery+1 {
		t.Errorf("with no one to tell how far it has got: %v %+v", err, l)
	}
	var le *LoadError
	if l, err := load(t, context.Background(), itemsCSV("1,a", "2,b,c"), &fakeLoader{}, LoadOptions{}, nil); err == nil || errors.As(err, &le) || l.Rows != 1 {
		t.Errorf("a file that cannot be read further is said as it is: %v %+v", err, l)
	}
}
