package guardcheck

import (
	"context"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// writerOnly implements source.Writer and nothing else, so that what the
// check notices can be checked itself.
type writerOnly struct{ guard source.Guard }

func (w *writerOnly) Plan(context.Context, source.Changeset) (*source.WritePlan, error) {
	return &source.WritePlan{}, nil
}
func (w *writerOnly) Apply(_ context.Context, plan *source.WritePlan) (*source.WriteOutcome, error) {
	for _, st := range plan.Statements {
		if err := w.guard.Allow(source.AccessWrite, st.Confirmed); err != nil {
			return nil, err
		}
	}
	return &source.WriteOutcome{}, nil
}

// A check that passes whatever the drivers do would be worse than no check,
// so it is held to a source that leaves a write path out.
func TestAWritePathLeftOutIsNoticed(t *testing.T) {
	spy := &testing.T{}
	Complete(spy, &writerOnly{}, map[string]func(*writerOnly, bool) error{})
	if !spy.Failed() {
		t.Fatal("a source with an unguarded Apply passed")
	}

	// And one that holds it passes.
	ok := &testing.T{}
	Complete(ok, &writerOnly{}, map[string]func(*writerOnly, bool) error{
		"Apply": func(*writerOnly, bool) error { return nil },
	})
	if ok.Failed() {
		t.Error("a source that holds every write path was failed")
	}
}

// Plan renders what would happen and sends nothing, so it is not a write
// path and need not be listed.
func TestPlanningIsNotAWritePath(t *testing.T) {
	for _, name := range []string{"Plan", "PlanIndex", "PlanDropIndex"} {
		if !Planning[name] {
			t.Errorf("%s is treated as a write path", name)
		}
	}
	if Planning["Apply"] || Planning["ApplyIndex"] || Planning["Produce"] {
		t.Error("something that writes is treated as planning")
	}
}

// The check runs what it is given, on both connections.
func TestTheCheckActuallyRunsWhatItIsGiven(t *testing.T) {
	ran := 0
	ro := &writerOnly{guard: source.Guard{ReadOnly: true}}
	prod := &writerOnly{guard: source.Guard{Environment: source.EnvProduction}}
	Check(t, ro, prod, map[string]func(*writerOnly, bool) error{
		"Apply": func(w *writerOnly, c bool) error {
			ran++
			_, err := w.Apply(context.Background(), &source.WritePlan{
				Statements: []source.Statement{{SQL: "DELETE FROM t", Confirmed: c}}})
			return err
		},
	})
	if ran != 3 {
		t.Errorf("it ran the operation %d times, want 3", ran)
	}
}

// Every interface on the list is one this project has.
func TestTheListNamesRealInterfaces(t *testing.T) {
	for _, iface := range Mutating {
		if iface.Kind().String() != "interface" {
			t.Errorf("%s is not an interface", iface)
		}
		if !strings.HasPrefix(iface.PkgPath(), "github.com/ikigai-db/ikigai-db/internal/source") {
			t.Errorf("%s is not this project's", iface)
		}
	}
	if len(Mutating) < 7 {
		t.Errorf("the list has %d interfaces on it", len(Mutating))
	}
}

// Check does the completeness check too, not only the running. Otherwise a
// driver could hold three of its four write paths and pass.
func TestCheckAlsoNoticesAWritePathLeftOut(t *testing.T) {
	spy := &testing.T{}
	ro := &writerOnly{guard: source.Guard{ReadOnly: true}}
	prod := &writerOnly{guard: source.Guard{Environment: source.EnvProduction}}
	Check(spy, ro, prod, map[string]func(*writerOnly, bool) error{})
	if !spy.Failed() {
		t.Fatal("Check passed a source whose Apply nothing holds to the guard")
	}
}
