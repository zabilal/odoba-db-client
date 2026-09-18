package conformance

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source/capability"
)

// The suite's own checks, against trees made to break them: every driver
// passes them, so without these a check that stopped checking would pass too.

var declared = capability.Capabilities{Objects: map[model.ObjectKind]bool{
	model.KindDatabase: true, model.KindFolder: true, model.KindTable: true, model.KindView: true,
}}

// fakeTree answers Children from a map, keeping which nodes were expanded.
type fakeTree struct {
	kids     map[string][]model.Node // by the parent's ref
	expanded []string
}

func (f *fakeTree) Children(_ context.Context, ref model.ObjectRef) ([]model.Node, error) {
	f.expanded = append(f.expanded, ref.String())
	return f.kids[ref.String()], nil
}

func TestAClassMustBeTheModelsAndDeclared(t *testing.T) {
	db := model.NewRef(model.KindDatabase, "d")
	good := model.ClassNode(db, model.KindTable, 1)
	misnamed := good
	misnamed.Label = "Relations"
	for name, c := range map[string]struct {
		n    model.Node
		want string
	}{
		"a class":             {good, ""},
		"an unknown folder":   {model.Node{Ref: model.NewRef(model.KindFolder, "d", "tables"), Label: "Tables"}, "no object class"},
		"an undeclared class": {model.ClassNode(db, model.KindIndex, 1), "does not declare"},
		"a misnamed class":    {misnamed, "is called"},
		"an undeclared kind":  {model.Node{Ref: model.NewRef(model.KindRoutine, "d", "f"), Label: "f"}, "not declared"},
		"no label":            {model.Node{Ref: model.NewRef(model.KindTable, "d", "t")}, "empty label"},
	} {
		got := strings.Join(nodeProblems(declared, c.n), "; ")
		if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
}

func TestTheWalkFindsAnotherKindInAClass(t *testing.T) {
	db := model.NewRef(model.KindDatabase, "d")
	tables := model.ClassNode(db, model.KindTable, 2)
	f := &fakeTree{kids: map[string][]model.Node{tables.Ref.String(): {
		{Ref: model.NewRef(model.KindTable, "d", "t"), Label: "t"},
		{Ref: model.NewRef(model.KindView, "d", "v"), Label: "v"},
	}}}
	got := walkTree(context.Background(), f, declared, []model.Node{tables}, 1)
	if len(got) != 1 || !strings.Contains(got[0], `of kind "view"`) {
		t.Errorf("problems %q; want the view in Tables, and only it", got)
	}
}

func TestTheWalkFindsANodeThatOpensOntoNothing(t *testing.T) {
	db := model.NewRef(model.KindDatabase, "d")
	tables := model.ClassNode(db, model.KindTable, 1)
	// A table that says it has columns and lists none: an expander that turns
	// and never opens. It is two levels down, where only the walk reaches.
	table := model.Node{Ref: model.NewRef(model.KindTable, "d", "t"), Label: "t", HasChildren: true}
	f := &fakeTree{kids: map[string][]model.Node{tables.Ref.String(): {table}}}
	got := walkTree(context.Background(), f, declared, []model.Node{tables}, 1)
	if len(got) != 1 || !strings.Contains(got[0], "claims HasChildren but returned none") {
		t.Errorf("problems %q; want the table that opens onto nothing", got)
	}
	// A node that claims none is not expanded, and is no problem.
	leaf := model.Node{Ref: model.NewRef(model.KindTable, "d", "t"), Label: "t"}
	quiet := &fakeTree{kids: map[string][]model.Node{tables.Ref.String(): {leaf}}}
	if got := walkTree(context.Background(), quiet, declared, []model.Node{tables}, 1); len(got) != 0 {
		t.Errorf("a leaf is %q", got)
	}
}

func TestTheWalkGoesFourLevelsDownAndFourNodesAcross(t *testing.T) {
	node := func(name string, branch bool) model.Node {
		return model.Node{Ref: model.NewRef(model.KindTable, "d", name), Label: name, HasChildren: branch}
	}
	deep := &fakeTree{kids: map[string][]model.Node{}}
	for i := 0; i < 5; i++ {
		deep.kids[node(strconv.Itoa(i), true).Ref.String()] = []model.Node{node(strconv.Itoa(i+1), true)}
	}
	walkTree(context.Background(), deep, declared, []model.Node{node("0", true)}, 1)
	if len(deep.expanded) != 3 {
		t.Errorf("expanded %q; the first three levels, and no deeper", deep.expanded)
	}
	wide := &fakeTree{}
	nodes := []model.Node{node("leaf", false)}
	for i := 0; i < 6; i++ {
		nodes = append(nodes, node(strconv.Itoa(i), true))
	}
	walkTree(context.Background(), wide, declared, nodes, 1)
	if len(wide.expanded) != 4 || slices.Contains(wide.expanded, node("leaf", false).Ref.String()) {
		t.Errorf("expanded %q; four of the six with children, and not the leaf", wide.expanded)
	}
}
