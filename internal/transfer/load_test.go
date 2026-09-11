package transfer

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

var itemsRef = model.NewRef(model.KindTable, "db", "s", "items")

// fakeWriter keeps each changeset it is given, and fails as it is told.
type fakeWriter struct {
	sets      []source.Changeset
	planErr   error
	applyErr  error
	failBatch int  // the batch, from 1, whose outcome fails
	failAt    int  // the change of it that fails; -1 for the commit
	undone    bool // whether the failed batch was rolled back
}

func (w *fakeWriter) Plan(_ context.Context, cs source.Changeset) (*source.WritePlan, error) {
	if w.planErr != nil {
		return nil, w.planErr
	}
	w.sets = append(w.sets, cs)
	return &source.WritePlan{Target: cs.Target, Statements: make([]source.Statement, len(cs.Changes))}, nil
}

func (w *fakeWriter) Apply(_ context.Context, p *source.WritePlan) (*source.WriteOutcome, error) {
	if w.applyErr != nil {
		return nil, w.applyErr
	}
	if len(w.sets) == w.failBatch {
		return &source.WriteOutcome{Applied: max(w.failAt, 0), FailedAt: w.failAt, RolledBack: w.undone, Err: errors.New("duplicate key")}, nil
	}
	return &source.WriteOutcome{Applied: len(p.Statements), FailedAt: -1}, nil
}

// itemsCSV is a file of ids and names, one line a row.
func itemsCSV(lines ...string) string { return "id,name\n" + strings.Join(lines, "\n") + "\n" }

var (
	itemsTo    = columns(col("id", model.TypeInteger, false), col("name", model.TypeString, true))
	itemsPairs = []Pair{{0, "id"}, {1, "name"}}
)

func TestRowsAreWrittenABatchATransaction(t *testing.T) {
	var lines []string
	for i := 1; i <= DefaultBatch*2+3; i++ {
		lines = append(lines, fmt.Sprintf("%d,n%d", i, i))
	}
	lines[1] = "2," // a name left empty goes in as NULL
	w := &fakeWriter{}
	var told []Loaded
	l, err := Load(context.Background(), csvRows(t, itemsCSV(lines...)), itemsPairs, itemsTo, itemsRef, w,
		LoadOptions{Confirmed: true}, func(p Loaded) { told = append(told, p) })
	if err != nil || l.Rows != 1003 || l.Written != 1003 || l.Elapsed <= 0 {
		t.Fatalf("%v %+v", err, l)
	}
	var sizes []int
	for _, cs := range w.sets {
		sizes = append(sizes, len(cs.Changes))
		if !cs.Target.Equal(itemsRef) || !cs.Confirmed || cs.Identity.Editable() {
			t.Errorf("a changeset of new rows, to the table, with the consent given: %+v", cs)
		}
	}
	if !reflect.DeepEqual(sizes, []int{500, 500, 3}) {
		t.Errorf("rows a transaction: %v", sizes)
	}
	first, second := w.sets[0].Changes[0], w.sets[0].Changes[1]
	if first.Kind != source.ChangeInsert || !reflect.DeepEqual(first.Values, map[string]any{"id": int64(1), "name": "n1"}) {
		t.Errorf("each row made the table's values: %+v", first)
	}
	if v, ok := second.Values["name"]; !ok || v != nil {
		t.Errorf("NULL is written as NULL: %+v", second)
	}
	if last := w.sets[2].Changes[2].Values["id"]; last != int64(1003) {
		t.Errorf("a batch is not written over by the next: %v", last)
	}
	if len(told) != 3 || told[0].Written != 500 || told[0].Rows != 500 || told[2].Written != 1003 || told[2].Elapsed <= 0 {
		t.Errorf("told after each batch: %+v", told)
	}
}

func TestALoadStopsAtAValueThatWouldNotGoIn(t *testing.T) {
	w := &fakeWriter{}
	l, err := Load(context.Background(), csvRows(t, itemsCSV("1,a", "2,b", "x,c", "4,d")), itemsPairs, itemsTo, itemsRef, w,
		LoadOptions{Batch: 2}, nil)
	var le *LoadError
	var ce CellError
	if !errors.As(err, &le) || le.Row != 3 || !le.Undone || !errors.As(err, &ce) || ce.Column != "id" {
		t.Fatalf("stopped at the row, with why: %v", err)
	}
	if err.Error() != "row 3: id: not a whole number (x)" {
		t.Errorf("%q", err)
	}
	if l.Rows != 3 || l.Written != 2 || len(w.sets) != 1 {
		t.Errorf("the batch before it written, and nothing after: %+v, %d batches", l, len(w.sets))
	}
}

func TestALoadStopsWhereTheServerRefusesARow(t *testing.T) {
	file := itemsCSV("1,a", "2,b", "3,c", "4,d", "5,e")
	w := &fakeWriter{failBatch: 2, failAt: 1, undone: true}
	l, err := Load(context.Background(), csvRows(t, file), itemsPairs, itemsTo, itemsRef, w, LoadOptions{Batch: 2}, nil)
	var le *LoadError
	if !errors.As(err, &le) || le.Row != 4 || !le.Undone || le.Err.Error() != "duplicate key" || l.Written != 2 {
		t.Errorf("the row the server refused, its batch undone: %v %+v", err, l)
	}
	w = &fakeWriter{failBatch: 1, failAt: 0}
	if _, err := Load(context.Background(), csvRows(t, file), itemsPairs, itemsTo, itemsRef, w, LoadOptions{Batch: 2}, nil); !errors.As(err, &le) || le.Undone || le.Row != 1 {
		t.Errorf("a batch the server could not undo: %v", err)
	}
	w = &fakeWriter{failBatch: 3, failAt: -1}
	if l, err := Load(context.Background(), csvRows(t, file), itemsPairs, itemsTo, itemsRef, w, LoadOptions{Batch: 2}, nil); err == nil || errors.As(err, &le) || l.Written != 4 {
		t.Errorf("a commit that failed is said as it is: %v %+v", err, l)
	}
}

func TestALoadTheWriterRefusesWritesNothing(t *testing.T) {
	file := itemsCSV("1,a")
	w := &fakeWriter{applyErr: source.ErrReadOnly}
	if l, err := Load(context.Background(), csvRows(t, file), itemsPairs, itemsTo, itemsRef, w, LoadOptions{}, nil); !errors.Is(err, source.ErrReadOnly) || l.Written != 0 {
		t.Errorf("refused: %v %+v", err, l)
	}
	w = &fakeWriter{planErr: errors.New("cannot plan")}
	if _, err := Load(context.Background(), csvRows(t, file), itemsPairs, itemsTo, itemsRef, w, LoadOptions{}, nil); err == nil || err.Error() != "cannot plan" {
		t.Errorf("not planned: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w = &fakeWriter{}
	if _, err := Load(ctx, csvRows(t, file), itemsPairs, itemsTo, itemsRef, w, LoadOptions{}, nil); !errors.Is(err, context.Canceled) || len(w.sets) != 0 {
		t.Errorf("stopped before it began: %v", err)
	}
	if l, err := Load(context.Background(), csvRows(t, itemsCSV()[:len("id,name\n")]), itemsPairs, itemsTo, itemsRef, w, LoadOptions{}, nil); err != nil || l.Rows != 0 || len(w.sets) != 0 {
		t.Errorf("a file with no rows writes nothing: %v %+v", err, l)
	}
}
