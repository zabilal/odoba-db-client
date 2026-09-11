package sqlscript

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// rowsOf streams rows, then err, or io.EOF.
type rowsOf struct {
	rows []model.Row
	err  error
}

func (r *rowsOf) Columns() []model.ColumnDef { return nil }
func (r *rowsOf) Close() error               { return nil }
func (r *rowsOf) Next(context.Context) (model.Row, error) {
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

// people is n rows of ids from 1, and names.
func people(n int) *rowsOf {
	r := &rowsOf{}
	for i := 1; i <= n; i++ {
		r.rows = append(r.rows, model.Row{int64(i), fmt.Sprint("p", i)})
	}
	return r
}

// txLog records what a load does with its transactions, and fails as told.
type txLog struct {
	events    []string
	fail      string // an Exec whose statement reads so fails
	affected  int64  // rows each Exec says it changed; 0 is 1
	beginErr  error
	failBegin int // the begin, from 1, that fails with beginErr; 0 is the first
	begins    int
	commitErr error
}

var errDuplicate = errors.New("duplicate key")

func (l *txLog) begin() (Tx, error) {
	if l.begins++; l.beginErr != nil && l.begins == max(l.failBegin, 1) {
		return Tx{}, l.beginErr
	}
	l.events = append(l.events, "begin")
	return Tx{
		Exec: func(st source.Statement) (int64, error) {
			said := fmt.Sprintf("%s %v", st.SQL, st.Args)
			l.events = append(l.events, said)
			if l.fail != "" && said == l.fail {
				return 0, errDuplicate
			}
			return max(l.affected, 1), nil
		},
		Commit: func() error {
			l.events = append(l.events, "commit")
			return l.commitErr
		},
		Rollback: func() error {
			l.events = append(l.events, "rollback")
			return nil
		},
	}, nil
}

func (l *txLog) load(t *testing.T, rows model.RowStream, opt source.LoadOptions, guard source.Guard) (int64, error) {
	t.Helper()
	return LoadWith(context.Background(), pgLike{}, guard, peopleRef, []string{"id", "name"}, rows, opt, l.begin)
}

const insertPerson = `INSERT INTO "s"."people" ("id", "name") VALUES ($1, $2)`

func TestRowsAreLoadedABatchATransaction(t *testing.T) {
	l := &txLog{}
	rows := people(4)
	rows.rows = append(rows.rows, model.Row{int64(5)}) // short: its name is NULL
	n, err := l.load(t, rows, source.LoadOptions{BatchSize: 2}, source.Guard{})
	want := []string{"begin", insertPerson + " [1 p1]", insertPerson + " [2 p2]", "commit",
		"begin", insertPerson + " [3 p3]", insertPerson + " [4 p4]", "commit",
		"begin", insertPerson + " [5 <nil>]", "commit"}
	if err != nil || n != 5 || strings.Join(l.events, "\n") != strings.Join(want, "\n") {
		t.Errorf("%v %d:\n%s", err, n, strings.Join(l.events, "\n"))
	}
	l = &txLog{}
	if n, err := l.load(t, people(DefaultLoadBatch+1), source.LoadOptions{}, source.Guard{}); err != nil || n != DefaultLoadBatch+1 ||
		strings.Count(strings.Join(l.events, "\n"), "commit") != 2 || slices.Index(l.events, "commit") != DefaultLoadBatch+1 {
		t.Errorf("%d rows a transaction unless told: %v %d", DefaultLoadBatch, err, n)
	}
}

func TestALoadThatEmptiesTheTableIsOneTransaction(t *testing.T) {
	l := &txLog{}
	n, err := l.load(t, people(3), source.LoadOptions{BatchSize: 2, Truncate: true, Confirmed: true}, source.Guard{})
	want := []string{"begin", `DELETE FROM "s"."people" []`, insertPerson + " [1 p1]", insertPerson + " [2 p2]", insertPerson + " [3 p3]", "commit"}
	if err != nil || n != 3 || strings.Join(l.events, "\n") != strings.Join(want, "\n") {
		t.Errorf("%v %d:\n%s", err, n, strings.Join(l.events, "\n"))
	}
	l = &txLog{fail: insertPerson + " [2 p2]"}
	n, err = l.load(t, people(3), source.LoadOptions{Truncate: true, Confirmed: true}, source.Guard{})
	var le *source.LoadError
	if !errors.As(err, &le) || le.Row != 2 || n != 0 || l.events[len(l.events)-1] != "rollback" || strings.Contains(strings.Join(l.events, " "), "commit") {
		t.Errorf("a failure leaves the table as it was: %v %d %v", err, n, l.events)
	}
	l = &txLog{fail: `DELETE FROM "s"."people" []`}
	if n, err := l.load(t, people(1), source.LoadOptions{Truncate: true, Confirmed: true}, source.Guard{}); err == nil || n != 0 || l.events[len(l.events)-1] != "rollback" {
		t.Errorf("a table that cannot be emptied: %v %v", err, l.events)
	}
}

func TestARowTheServerRefusesStopsTheLoad(t *testing.T) {
	l := &txLog{fail: insertPerson + " [4 p4]"}
	n, err := l.load(t, people(5), source.LoadOptions{BatchSize: 2}, source.Guard{})
	var le *source.LoadError
	if !errors.As(err, &le) || le.Row != 4 || err.Error() != "row 4: duplicate key" || !errors.Is(err, errDuplicate) || n != 2 {
		t.Fatalf("stopped at the row, the batches before it kept: %v %d", err, n)
	}
	if last := l.events[len(l.events)-1]; last != "rollback" || strings.Count(strings.Join(l.events, " "), "commit") != 1 {
		t.Errorf("its transaction rolled back: %v", l.events)
	}
	l = &txLog{affected: 2}
	if _, err := l.load(t, people(1), source.LoadOptions{}, source.Guard{}); !errors.As(err, &le) || le.Err.Error() != "2 rows added, where the row was one" {
		t.Errorf("a row that added two: %v", err)
	}
}

func TestALoadIsGuardedAndSaysWhatStoppedIt(t *testing.T) {
	for name, c := range map[string]struct {
		opt   source.LoadOptions
		guard source.Guard
		want  error
	}{
		"read-only":              {source.LoadOptions{Confirmed: true}, source.Guard{ReadOnly: true}, source.ErrReadOnly},
		"production, no consent": {source.LoadOptions{}, source.Guard{Environment: source.EnvProduction}, source.ErrConfirmationRequired},
		"emptying, no consent":   {source.LoadOptions{Truncate: true}, source.Guard{}, source.ErrConfirmationRequired},
	} {
		l := &txLog{}
		if _, err := l.load(t, people(1), c.opt, c.guard); !errors.Is(err, c.want) || len(l.events) != 0 {
			t.Errorf("%s: %v, %v", name, err, l.events)
		}
	}
	l := &txLog{}
	if n, err := l.load(t, people(1), source.LoadOptions{Confirmed: true}, source.Guard{Environment: source.EnvProduction}); err != nil || n != 1 {
		t.Errorf("production with consent: %v", err)
	}
	if _, err := l.load(t, people(1), source.LoadOptions{OnError: "skip"}, source.Guard{}); err == nil {
		t.Error("an error policy not taken is refused")
	}
	if _, err := LoadWith(context.Background(), pgLike{}, source.Guard{}, peopleRef, nil, people(1), source.LoadOptions{}, l.begin); err == nil {
		t.Error("a load of no columns is refused")
	}
	gone := errors.New("the server went away")
	l = &txLog{beginErr: gone}
	if _, err := l.load(t, people(1), source.LoadOptions{}, source.Guard{}); !errors.Is(err, gone) {
		t.Errorf("begin: %v", err)
	}
	l = &txLog{beginErr: gone, failBegin: 2}
	if n, err := l.load(t, people(3), source.LoadOptions{BatchSize: 2}, source.Guard{}); !errors.Is(err, gone) || n != 2 {
		t.Errorf("a second transaction not begun: %v %d", err, n)
	}
	l = &txLog{commitErr: gone}
	if n, err := l.load(t, people(3), source.LoadOptions{BatchSize: 2}, source.Guard{}); !errors.Is(err, gone) || !strings.Contains(err.Error(), "committing them failed") || n != 0 {
		t.Errorf("commit: %v %d", err, n)
	}
	l = &txLog{commitErr: gone}
	if n, err := l.load(t, people(1), source.LoadOptions{}, source.Guard{}); !errors.Is(err, gone) || !strings.Contains(err.Error(), "committing them failed") || n != 0 {
		t.Errorf("the last commit: %v %d", err, n)
	}
	l = &txLog{}
	if n, err := l.load(t, &rowsOf{rows: people(3).rows, err: gone}, source.LoadOptions{BatchSize: 2}, source.Guard{}); !errors.Is(err, gone) || n != 2 || l.events[len(l.events)-1] != "rollback" {
		t.Errorf("rows that stop coming: %v %d %v", err, n, l.events)
	}
}

// noUpsert is a dialect that cannot write a row over another.
type noUpsert struct{ source.Dialect }

func TestAnUpsertUpdatesTheRowWhoseKeyIsTaken(t *testing.T) {
	l := &txLog{affected: 2} // as MySQL says of a row updated
	n, err := l.load(t, people(2), source.LoadOptions{Keys: []string{"id"}}, source.Guard{})
	upsert := insertPerson + ` ON CONFLICT ("id") DO UPDATE SET "name" = EXCLUDED."name"`
	want := []string{"begin", upsert + " [1 p1]", upsert + " [2 p2]", "commit"}
	if err != nil || n != 2 || strings.Join(l.events, "\n") != strings.Join(want, "\n") {
		t.Errorf("%v %d:\n%s", err, n, strings.Join(l.events, "\n"))
	}
	if got := OnConflict(pgLike{}, []string{"a", "b"}, []string{"a", "c", "b", "d"}); got != ` ON CONFLICT ("a", "b") DO UPDATE SET "c" = EXCLUDED."c", "d" = EXCLUDED."d"` {
		t.Errorf("the other columns updated, in order: %s", got)
	}
	if got := OnConflict(pgLike{}, []string{"id"}, []string{"id"}); got != ` ON CONFLICT ("id") DO NOTHING` {
		t.Errorf("a row of its key alone left as it is: %s", got)
	}
	for name, c := range map[string]struct {
		d   source.Dialect
		opt source.LoadOptions
	}{
		"emptying the table too": {pgLike{}, source.LoadOptions{Keys: []string{"id"}, Truncate: true, Confirmed: true}},
		"a key not loaded":       {pgLike{}, source.LoadOptions{Keys: []string{"email"}}},
		"a source that cannot":   {noUpsert{pgLike{}}, source.LoadOptions{Keys: []string{"id"}}},
	} {
		l := &txLog{}
		if _, err := LoadWith(context.Background(), c.d, source.Guard{}, peopleRef, []string{"id", "name"}, people(1), c.opt, l.begin); err == nil || len(l.events) != 0 {
			t.Errorf("%s: %v %v", name, err, l.events)
		}
	}
}
