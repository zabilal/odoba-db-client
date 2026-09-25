package sqlscript

import (
	"context"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// A row written over the row already there with its key (FR-10.6, ADR-0052).
//
// Most engines say it as a clause after the INSERT. Firebird says it by
// changing the first two words — UPDATE OR INSERT … MATCHING (keys) — so a
// dialect may say what goes before as well as after, and the two ways are
// held here rather than in either driver.

// fbLike is a dialect whose upsert goes around the INSERT rather than after
// it, as Firebird's does.
type fbLike struct{ pgLike }

func (d fbLike) UpsertClause(keys, cols []string) string { panic("this dialect rewrites instead") }

func (d fbLike) UpsertAround(keys, _ []string) (before, after string) {
	quoted := make([]string, len(keys))
	for i, k := range keys {
		quoted[i] = d.QuoteIdentifier(k)
	}
	return "UPDATE OR ", " MATCHING (" + strings.Join(quoted, ", ") + ")"
}

func TestAnUpsertGoesWhereTheDialectPutsIt(t *testing.T) {
	l := &txLog{}
	n, err := LoadWith(context.Background(), fbLike{}, source.Guard{}, peopleRef,
		[]string{"id", "name"}, people(1), source.LoadOptions{Keys: []string{"id"}}, l.begin)
	if err != nil || n != 1 {
		t.Fatalf("%v %d", err, n)
	}
	want := `UPDATE OR ` + insertPerson + ` MATCHING ("id") [1 p1]`
	if got := strings.Join(l.events, "\n"); !strings.Contains(got, want) {
		t.Errorf("it wrote\n%s\nwhich lacks\n%s", got, want)
	}
}

// A dialect that says it as a clause is unaffected: it is asked only for the
// clause, and nothing goes in front of its INSERT.
func TestAClauseDialectStillGetsAClause(t *testing.T) {
	up, err := upsertParts(pgLike{}, []string{"id", "name"}, []string{"id"})
	if err != nil {
		t.Fatal(err)
	}
	if up.before != "" {
		t.Errorf("something was put in front of the insert: %q", up.before)
	}
	if !strings.HasPrefix(up.after, " ON CONFLICT") {
		t.Errorf("its clause is %q", up.after)
	}
	if up.none() {
		t.Error("it reports no upsert at all")
	}
}

func TestNoKeysMeansNoUpsert(t *testing.T) {
	up, err := upsertParts(pgLike{}, []string{"id"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !up.none() {
		t.Errorf("it would write %q … %q", up.before, up.after)
	}
}

func TestADialectWithNeitherWayIsRefused(t *testing.T) {
	if _, err := upsertParts(noUpsert{pgLike{}}, []string{"id"}, []string{"id"}); err == nil {
		t.Error("a dialect that cannot write one was accepted")
	}
}

// A key that is not among the columns loaded addresses nothing, whichever way
// the dialect writes it.
func TestAKeyNotLoadedIsRefusedEitherWay(t *testing.T) {
	for name, d := range map[string]source.Dialect{"a clause": pgLike{}, "a rewrite": fbLike{}} {
		t.Run(name, func(t *testing.T) {
			if _, err := upsertParts(d, []string{"id", "name"}, []string{"email"}); err == nil {
				t.Error("it was accepted")
			}
		})
	}
}
