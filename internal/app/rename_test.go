package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/capability"
)

// Renaming an object, and saying first what it costs (FR-6.6).

var tableRef = model.NewRef(model.KindTable, "db", "public", "people")

func TestARenameIsRenderedByTheDriver(t *testing.T) {
	src := &renderer{ddl: true}
	stmts, err := PlanRename(src, tableRef, "folk")
	if err != nil {
		t.Fatal(err)
	}
	if len(stmts) != 1 || !strings.Contains(stmts[0].SQL, "RENAME TO folk") {
		t.Errorf("it rendered %+v", stmts)
	}
}

// Whether a rename is on offer is the driver's answer, not a list of kinds
// kept here that would have to be kept in step with seven of them.
func TestWhatCanBeRenamedIsAskedOfTheDriver(t *testing.T) {
	src := &renderer{ddl: true}
	if !CanRename(src, tableRef) {
		t.Error("a table cannot be renamed")
	}
	if CanRename(src, model.NewRef(model.KindView, "db", "public", "recent")) {
		t.Error("a view can be renamed by a driver that refuses views")
	}
	if CanRename(&noRenderer{}, tableRef) {
		t.Error("a source with no generator offers a rename")
	}
}

// Asking whether a rename is possible must not leave a trace: it renders
// with a name that is thrown away, and nothing is sent anywhere.
func TestAskingWhetherARenameIsPossibleChangesNothing(t *testing.T) {
	src := &renderer{ddl: true}
	CanRename(src, tableRef)
	if len(src.calls) != 1 || !strings.HasPrefix(src.calls[0], "rename object to ") {
		t.Fatalf("it called %q", src.calls)
	}
	// The probe has to differ from the name the object has, or the driver
	// answers "no change" and the command is never offered.
	if strings.HasSuffix(src.calls[0], " people") {
		t.Error("it probed with the name the object already has")
	}
}

func TestARenameOnASourceThatRendersNothing(t *testing.T) {
	if _, err := PlanRename(&noRenderer{}, tableRef, "folk"); !errors.Is(err, ErrNoDDL) {
		t.Errorf("it said %v", err)
	}
}

// depender says what depends on an object.
type depender struct {
	source.Source
	deps []model.Dependent
	err  error
}

func (*depender) Capabilities() capability.Capabilities { return capability.Capabilities{} }

func (d *depender) Dependents(context.Context, model.ObjectRef) ([]model.Dependent, error) {
	return d.deps, d.err
}

func TestWhatDependsOnAnObject(t *testing.T) {
	deps := []model.Dependent{
		{Label: "public.recent", Note: "a view"},
		{Label: "public.total()", Note: "a body", Breaks: true},
		{Label: "public.orders", Note: "a key"},
	}
	got, err := DependentsOf(context.Background(), &depender{deps: deps}, tableRef)
	if err != nil || len(got) != 3 {
		t.Fatalf("it said %+v, %v", got, err)
	}
	breaks := Breaking(got)
	if len(breaks) != 1 || breaks[0].Label != "public.total()" {
		t.Errorf("what breaks is %+v", breaks)
	}
}

// A source that cannot say is not a source that says nothing depends on it.
// The difference is the whole warning.
func TestASourceThatCannotSayWhatDependsSaysSo(t *testing.T) {
	_, err := DependentsOf(context.Background(), &renderer{ddl: true}, tableRef)
	if !errors.Is(err, ErrNoDependencies) {
		t.Errorf("it said %v", err)
	}
}

func TestNothingBreaksWhenNothingDepends(t *testing.T) {
	if got := Breaking(nil); got != nil {
		t.Errorf("it found %+v", got)
	}
}

// A driver that falls over reading dependencies is contained, like every
// other call into one (ADR-0017).
type fallingDepender struct{ depender }

func (*fallingDepender) Dependents(context.Context, model.ObjectRef) ([]model.Dependent, error) {
	panic("fakesql: reading dependencies fell over")
}

func TestADriverThatFallsOverReadingDependenciesIsContained(t *testing.T) {
	_, err := DependentsOf(context.Background(), &fallingDepender{}, tableRef)
	if err == nil || !strings.Contains(err.Error(), "what depends on an object") {
		t.Errorf("it said %v", err)
	}
}

// A driver that falls over rendering a rename is contained too.
type renamePanicker struct{ renderer }

func (*renamePanicker) RenameObject(model.ObjectRef, string) ([]source.Statement, error) {
	panic("fakesql: renaming fell over")
}

func TestADriverThatFallsOverRenamingIsContained(t *testing.T) {
	_, err := PlanRename(&renamePanicker{renderer{ddl: true}}, tableRef, "folk")
	if err == nil || !strings.Contains(err.Error(), "rendering a rename") {
		t.Errorf("it said %v", err)
	}
}
