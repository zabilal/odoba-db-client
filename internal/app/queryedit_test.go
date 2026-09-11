package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// keyedSource is a scriptSource that writes rows. Its sessions answer a
// statement run alone with rows known by a key, a row a millisecond until
// they are closed, and note what they were asked.
type keyedSource struct {
	*scriptSource
	seen    sync.Mutex
	queried []source.Statement
	streams []*keyedStream
	plans   []*source.WritePlan
}

func (k *keyedSource) Session(context.Context) (source.Session, error) {
	return &keyedSession{scriptSession: scriptSession{src: k.scriptSource}, k: k}, nil
}

func (k *keyedSource) Plan(_ context.Context, cs source.Changeset) (*source.WritePlan, error) {
	return &source.WritePlan{Target: cs.Target}, nil
}

func (k *keyedSource) Apply(_ context.Context, plan *source.WritePlan) (*source.WriteOutcome, error) {
	k.seen.Lock()
	defer k.seen.Unlock()
	k.plans = append(k.plans, plan)
	return &source.WriteOutcome{FailedAt: -1}, nil
}

func (k *keyedSource) asked() []source.Statement {
	k.seen.Lock()
	defer k.seen.Unlock()
	return append([]source.Statement(nil), k.queried...)
}

type keyedSession struct {
	scriptSession
	k *keyedSource
}

// Query answers "none" with no rows, and anything else with rows known by a
// key.
func (s *keyedSession) Query(_ context.Context, st source.Statement) (*source.Result, error) {
	s.k.seen.Lock()
	defer s.k.seen.Unlock()
	s.k.queried = append(s.k.queried, st)
	if st.SQL == "none" {
		return &source.Result{}, nil
	}
	ks := &keyedStream{countStream: countStream{n: 1 << 30, slow: true}}
	s.k.streams = append(s.k.streams, ks)
	return &source.Result{Rows: ks, Affected: -1}, nil
}

type keyedStream struct{ countStream }

func (*keyedStream) Identity() model.RowIdentity {
	return model.RowIdentity{Kind: model.IdentityPrimaryKey, Columns: []string{"n"}, Target: model.NewRef(model.KindTable, "t")}
}

func TestAResultIsEditedWhereItsSourceWritesItsStatementReadsAndItHasAKey(t *testing.T) {
	ctx := context.Background()
	k := &keyedSource{scriptSource: &scriptSource{}}
	qs, err := newQuerySession(ctx, k, "c1", QueryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	keyed := newResultSet(ctx, &keyedStream{countStream: countStream{n: 1}}, 10)
	plain := newResultSet(ctx, &countStream{n: 1}, 10)
	defer keyed.Close()
	defer plain.Close()
	if !qs.Editable(keyed, "select n from t") {
		t.Error("a result known by a key, of a statement that reads, on a source that writes, is edited")
	}
	if qs.Editable(plain, "select n from t") {
		t.Error("a result known by nothing is not")
	}
	if qs.Editable(keyed, "update t set n = 2 returning n") {
		t.Error("nor one of a statement that writes: reading it again would write again")
	}
	other, _ := newQuerySession(ctx, &scriptSource{}, "c1", QueryOptions{})
	if other.Editable(keyed, "select n from t") {
		t.Error("nor one on a source that does not write")
	}
	if _, err := other.Plan(ctx, source.Changeset{}); !errors.Is(err, errNoWrites) {
		t.Errorf("Plan on a source that does not write: %v", err)
	}
	if _, err := other.Apply(ctx, &source.WritePlan{}); !errors.Is(err, errNoWrites) {
		t.Errorf("Apply on a source that does not write: %v", err)
	}
	table := model.NewRef(model.KindTable, "t")
	plan, err := qs.Plan(ctx, source.Changeset{Target: table})
	if err != nil || !plan.Target.Equal(table) {
		t.Fatalf("Plan plans through the source: %v %v", plan, err)
	}
	if out, err := qs.Apply(ctx, plan); err != nil || out.FailedAt != -1 || len(k.plans) != 1 {
		t.Errorf("Apply writes through the source: %v %v", out, err)
	}
}

func TestRereadRunsAStatementThatReadsAloneOnTheSession(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	k := &keyedSource{scriptSource: &scriptSource{}}
	qs, err := newQuerySession(ctx, k, "c1", QueryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	rs, err := qs.Reread(ctx, "select n from t where n > :a", map[string]any{"a": int64(1)})
	if err != nil {
		t.Fatal(err)
	}
	if !rs.Identity().Editable() {
		t.Error("the rows read again are known by their key")
	}
	if got := k.asked(); len(got) != 1 || got[0].SQL != "select n from t where n > :a" || got[0].Named["a"] != int64(1) {
		t.Errorf("run alone, with the values it ran with: %+v", got)
	}
	if _, err := qs.Reread(ctx, "update t set n = 2", nil); err == nil || len(k.asked()) != 1 {
		t.Error("a statement that writes is never run again")
	}
	if _, err := qs.Reread(ctx, "none", nil); err == nil {
		t.Error("a statement that no longer returns rows says so")
	}
	ch, err := qs.Run(ctx, "rows 1", source.ScriptOptions{})
	if err != nil {
		t.Fatal(err)
	}
	collect(t, ch)
	for deadline := time.Now().Add(5 * time.Second); !k.streams[0].closed.Load(); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the next run ends the rows read again, as it ends its own results")
		}
	}
}
