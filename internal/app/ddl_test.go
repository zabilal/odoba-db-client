package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/capability"
)

// renderer is a source that renders DDL by writing down what it was asked,
// so that a test can see which call came first and what it was given.
type renderer struct {
	source.Source
	ddl   bool
	calls []string
	from  *model.Table
	to    *model.Table
}

func (r *renderer) Capabilities() capability.Capabilities {
	return capability.Capabilities{Schema: capability.Schema{DDL: r.ddl}}
}

func (r *renderer) CreateObject(model.ObjectRef, any) ([]source.Statement, error) { return nil, nil }

func (r *renderer) DropObject(model.ObjectRef, bool) ([]source.Statement, error) { return nil, nil }

func (r *renderer) RenameObject(ref model.ObjectRef, to string) ([]source.Statement, error) {
	if ref.Kind != model.KindTable {
		return nil, fmt.Errorf("renderer: a %s cannot be renamed", ref.Kind)
	}
	if to == ref.Name() {
		return nil, nil // as the contract says: renaming it to what it is called is no change
	}
	r.calls = append(r.calls, "rename object to "+to)
	return []source.Statement{{SQL: "ALTER TABLE " + ref.Name() + " RENAME TO " + to}}, nil
}

func (r *renderer) RenameColumn(_ model.ObjectRef, from, to string) ([]source.Statement, error) {
	r.calls = append(r.calls, "rename "+from+" to "+to)
	return []source.Statement{{SQL: "RENAME " + from + " TO " + to}}, nil
}

func (r *renderer) AlterObject(_ model.ObjectRef, from, to any) ([]source.Statement, error) {
	r.calls = append(r.calls, "alter")
	r.from, _ = from.(*model.Table)
	r.to, _ = to.(*model.Table)
	return []source.Statement{{SQL: "ALTER"}}, nil
}

func TestADesignIsRenderedRenamesFirst(t *testing.T) {
	d := designed(0)
	renamed := d.Columns()[1]
	renamed.Name = "full_name"
	if err := d.ChangeColumn("name", renamed); err != nil {
		t.Fatal(err)
	}

	src := &renderer{ddl: true}
	got, err := PlanDDL(src, d)
	if err != nil {
		t.Fatalf("rendering: %v", err)
	}
	if len(got) != 2 || got[0].SQL != "RENAME name TO full_name" || got[1].SQL != "ALTER" {
		t.Fatalf("it rendered %+v", got)
	}
	if len(src.calls) != 2 || src.calls[0] != "rename name to full_name" {
		t.Errorf("it asked %v", src.calls)
	}

	// What the alteration is rendered against already uses the new name, so
	// nothing after the rename looks for a column that is no longer there.
	if src.from == nil || src.from.Columns[1].Name != "full_name" {
		t.Errorf("the table it altered from calls it %q", src.from.Columns[1].Name)
	}
	if src.to.Columns[1].Name != "full_name" {
		t.Errorf("the table it altered into calls it %q", src.to.Columns[1].Name)
	}
}

// Renaming must not change the design, or reading the preview twice would
// render something different the second time.
func TestRenderingADesignTwiceRendersTheSame(t *testing.T) {
	d := designed(0)
	renamed := d.Columns()[1]
	renamed.Name = "full_name"
	if err := d.ChangeColumn("name", renamed); err != nil {
		t.Fatal(err)
	}
	first, err := PlanDDL(&renderer{ddl: true}, d)
	if err != nil {
		t.Fatal(err)
	}
	second, err := PlanDDL(&renderer{ddl: true}, d)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != len(second) || first[0].SQL != second[0].SQL {
		t.Errorf("it rendered %+v then %+v", first, second)
	}
	if got := d.Original().Columns[1].Name; got != "name" {
		t.Errorf("rendering changed what the design was read as: %q", got)
	}
}

// A source that cannot render says so, rather than a designer offering a
// preview that would be empty.
func TestASourceThatCannotRenderSaysSo(t *testing.T) {
	for _, c := range []struct {
		what string
		src  source.Source
	}{
		{"one with no generator at all", &noRenderer{}},
		{"one that has one and does not claim it", &renderer{ddl: false}},
	} {
		t.Run(c.what, func(t *testing.T) {
			if CanRenderDDL(c.src) {
				t.Error("it says it can render")
			}
			_, err := PlanDDL(c.src, designed(0))
			if !errors.Is(err, ErrNoDDL) {
				t.Errorf("it said %v", err)
			}
		})
	}
	if !CanRenderDDL(&renderer{ddl: true}) {
		t.Error("a source that renders says it cannot")
	}
}

// noRenderer has no DDL generator on it at all.
type noRenderer struct{ source.Source }

func (*noRenderer) Capabilities() capability.Capabilities {
	return capability.Capabilities{Schema: capability.Schema{DDL: true}}
}

func TestRenderingNothingAtAll(t *testing.T) {
	if _, err := PlanDDL(&renderer{ddl: true}, nil); err == nil ||
		!strings.Contains(err.Error(), "no design") {
		t.Errorf("it said %v", err)
	}
}

// A driver that falls over while rendering is contained, like every other
// call into one (ADR-0017).
type panicker struct{ renderer }

func (*panicker) AlterObject(model.ObjectRef, any, any) ([]source.Statement, error) {
	panic("fakesql: rendering fell over")
}

func TestADriverThatFallsOverRenderingIsContained(t *testing.T) {
	src := &panicker{renderer{ddl: true}}
	_, err := PlanDDL(src, designed(0))
	if err == nil || !strings.Contains(err.Error(), "rendering a structural change") {
		t.Errorf("it said %v", err)
	}
	_ = context.Background()
}
