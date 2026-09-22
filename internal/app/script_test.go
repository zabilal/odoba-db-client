package app

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/capability"
)

// Writing a whole schema as DDL (FR-6.7).

var schemaRef = model.NewRef(model.KindSchema, "db", "public")

// schemaSource is a schema with something of every kind in it, and a view
// that selects from another view.
type schemaSource struct {
	source.Source
	views   []string // in the order the source lists them
	selects map[string]string
	noDeps  bool
	keys    map[string][]model.ForeignKey
	read    []model.ObjectKind // the classes it was asked to list
}

func (*schemaSource) Capabilities() capability.Capabilities {
	return capability.Capabilities{Schema: capability.Schema{DDL: true}}
}

func (s *schemaSource) Children(_ context.Context, ref model.ObjectRef) ([]model.Node, error) {
	if ref.Kind == model.KindSchema {
		var out []model.Node
		for _, k := range []model.ObjectKind{
			model.KindTrigger, model.KindView, model.KindTable, model.KindRoutine, model.KindSequence,
		} {
			out = append(out, model.ClassNode(ref, k, 1))
		}
		// A class this does not script, to be ignored rather than guessed at.
		out = append(out, model.ClassNode(ref, model.KindUserType, 1))
		return out, nil
	}
	kind, ok := model.ClassOf(ref)
	if !ok {
		return nil, nil
	}
	s.read = append(s.read, kind)
	names := map[model.ObjectKind][]string{
		model.KindSequence: {"people_id_seq"},
		model.KindTable:    {"people", "orders"},
		model.KindView:     s.views,
		model.KindRoutine:  {"total()"},
		model.KindTrigger:  {"audit"},
		model.KindUserType: {"mood"},
	}[kind]
	var out []model.Node
	for _, n := range names {
		out = append(out, model.Node{Ref: model.NewRef(kind, "db", "public", n), Label: n})
	}
	return out, nil
}

func (s *schemaSource) Describe(_ context.Context, ref model.ObjectRef) (any, error) {
	switch ref.Kind {
	case model.KindTable:
		return &model.Table{Name: ref.Name(),
			Columns:     []model.Column{{Name: "id", Type: model.DataType{Native: "integer"}}},
			ForeignKeys: s.keys[ref.Name()]}, nil
	case model.KindView, model.KindMaterializedView:
		return &model.View{Name: ref.Name(), Definition: "SELECT 1"}, nil
	case model.KindRoutine:
		return &model.Routine{Name: ref.Name(), Definition: "CREATE FUNCTION " + ref.Name()}, nil
	case model.KindTrigger:
		return &model.Trigger{Name: ref.Name(), Definition: "CREATE TRIGGER " + ref.Name()}, nil
	case model.KindSequence:
		return &model.Sequence{Name: ref.Name(), Start: 1, Increment: 1}, nil
	}
	return nil, errors.New("nothing of that kind here")
}

func (s *schemaSource) Dependents(_ context.Context, ref model.ObjectRef) ([]model.Dependent, error) {
	if s.noDeps {
		return nil, ErrNoDependencies
	}
	var out []model.Dependent
	for on, from := range s.selects {
		if from == ref.Name() {
			out = append(out, model.Dependent{Ref: model.NewRef(model.KindView, "db", "public", on)})
		}
	}
	return out, nil
}

// The generator writes what it is given, so the test reads the order rather
// than the grammar.
func (*schemaSource) CreateObject(ref model.ObjectRef, obj any) ([]source.Statement, error) {
	if t, ok := obj.(*model.Table); ok {
		return []source.Statement{{SQL: "CREATE TABLE " + ref.Name() + " keys=" + keyNames(t.ForeignKeys)}}, nil
	}
	return []source.Statement{{SQL: "CREATE " + strings.ToUpper(string(ref.Kind)) + " " + ref.Name()}}, nil
}

func (*schemaSource) AlterObject(ref model.ObjectRef, from, to any) ([]source.Statement, error) {
	was, now := from.(*model.Table), to.(*model.Table)
	var out []source.Statement
	for _, f := range now.ForeignKeys {
		if !hasKey(was.ForeignKeys, f.Name) {
			out = append(out, source.Statement{SQL: "ALTER TABLE " + ref.Name() + " ADD CONSTRAINT " + f.Name})
		}
	}
	return out, nil
}

func (*schemaSource) RenameColumn(model.ObjectRef, string, string) ([]source.Statement, error) {
	return nil, nil
}
func (*schemaSource) DropObject(model.ObjectRef, bool) ([]source.Statement, error) { return nil, nil }
func (*schemaSource) RenameObject(model.ObjectRef, string) ([]source.Statement, error) {
	return nil, nil
}

func hasKey(keys []model.ForeignKey, name string) bool {
	for _, k := range keys {
		if k.Name == name {
			return true
		}
	}
	return false
}

func keyNames(keys []model.ForeignKey) string {
	var out []string
	for _, k := range keys {
		out = append(out, k.Name)
	}
	return strings.Join(out, ",")
}

func scriptLines(t *testing.T, src source.Source) []string {
	t.Helper()
	stmts, err := ScriptSchema(context.Background(), src, schemaRef)
	if err != nil {
		t.Fatalf("scripting: %v", err)
	}
	out := make([]string, len(stmts))
	for i, s := range stmts {
		out[i] = s.SQL
	}
	return out
}

func at(lines []string, prefix string) int {
	for i, l := range lines {
		if strings.HasPrefix(l, prefix) {
			return i
		}
	}
	return -1
}

// A schema comes out in an order it can be run in, whatever order the source
// happens to list its classes in.
func TestASchemaIsWrittenInAnOrderItCanBeRunIn(t *testing.T) {
	lines := scriptLines(t, &schemaSource{views: []string{"recent"}})
	for _, pair := range [][2]string{
		{"CREATE SEQUENCE", "CREATE TABLE"},
		{"CREATE TABLE", "CREATE VIEW"},
		{"CREATE VIEW", "CREATE ROUTINE"},
		{"CREATE ROUTINE", "CREATE TRIGGER"},
	} {
		if a, b := at(lines, pair[0]), at(lines, pair[1]); a < 0 || b < 0 || a > b {
			t.Errorf("%s is at %d and %s at %d, in:\n%s", pair[0], a, pair[1], b, strings.Join(lines, "\n"))
		}
	}
	// A kind with no place in the order is left out rather than guessed at.
	if i := at(lines, "CREATE USER_TYPE"); i >= 0 {
		t.Errorf("a kind this cannot order was written anyway, at %d", i)
	}
}

// Two tables that refer to each other cannot be put in an order at all, so
// the references are added once both tables exist.
func TestReferencesBetweenTablesComeAfterEveryTable(t *testing.T) {
	src := &schemaSource{views: []string{"recent"}, keys: map[string][]model.ForeignKey{
		"people": {{Name: "people_last_order_fkey", RefTable: "orders"}},
		"orders": {{Name: "orders_who_fkey", RefTable: "people"}},
	}}
	lines := scriptLines(t, src)
	lastTable, firstKey := -1, -1
	for i, l := range lines {
		if strings.HasPrefix(l, "CREATE TABLE") {
			lastTable = i
			if strings.Contains(l, "keys=people_last_order_fkey") || strings.Contains(l, "keys=orders_who_fkey") {
				t.Errorf("a table was written with its references inline: %q", l)
			}
		}
		if strings.Contains(l, "ADD CONSTRAINT") && firstKey < 0 {
			firstKey = i
		}
	}
	if firstKey < 0 || firstKey < lastTable {
		t.Errorf("the first reference is at %d and the last table at %d, in:\n%s",
			firstKey, lastTable, strings.Join(lines, "\n"))
	}
	if n := strings.Count(strings.Join(lines, "\n"), "ADD CONSTRAINT"); n != 2 {
		t.Errorf("%d references were written, want 2", n)
	}
}

// A view that selects from another view comes after it, whatever order the
// source listed them in.
func TestAViewComesAfterWhatItSelectsFrom(t *testing.T) {
	src := &schemaSource{
		views:   []string{"aa_on_zz", "zz_base"}, // listed the wrong way round
		selects: map[string]string{"aa_on_zz": "zz_base"},
	}
	lines := scriptLines(t, src)
	if base, on := at(lines, "CREATE VIEW zz_base"), at(lines, "CREATE VIEW aa_on_zz"); base < 0 || on < 0 || base > on {
		t.Errorf("zz_base is at %d and the view over it at %d, in:\n%s", base, on, strings.Join(lines, "\n"))
	}
}

// A source that cannot say what depends on what leaves the order as it was
// listed. Guessing and being wrong writes a script that fails halfway, which
// is worse than one whose order somebody has to fix themselves.
func TestAnUnaskableSourceLeavesTheViewsAsTheyWereListed(t *testing.T) {
	src := &schemaSource{views: []string{"aa_on_zz", "zz_base"}, noDeps: true}
	lines := scriptLines(t, src)
	if a, z := at(lines, "CREATE VIEW aa_on_zz"), at(lines, "CREATE VIEW zz_base"); a > z {
		t.Errorf("the order was changed on no information: %d then %d", a, z)
	}
}

// A cycle cannot be ordered, so nothing pretends it has been: the order goes
// back exactly as it came.
//
// The graph is a and b selecting from each other with c selecting from b,
// and the listed order is c, a, b. A two-view cycle would not do: following
// it round and leaving it alone both answer a, b, and a test that cannot
// tell those apart is not testing anything.
func TestViewsThatDependOnEachOtherAreLeftAlone(t *testing.T) {
	var refs []model.ObjectRef
	for _, n := range []string{"c", "a", "b"} {
		refs = append(refs, model.NewRef(model.KindView, "db", "public", n))
	}
	src := &schemaSource{selects: map[string]string{"a": "b", "b": "a", "c": "b"}}
	got := inDependencyOrder(context.Background(), src, refs)
	var names []string
	for _, r := range got {
		names = append(names, r.Name())
	}
	if strings.Join(names, ",") != "c,a,b" {
		t.Errorf("a cycle came back as %v, want the order it went in as", names)
	}
}

// What will not be written is not read. A class this cannot put in an order
// is skipped before its objects are listed, not after.
func TestAClassWithNoPlaceInTheOrderIsNotEvenRead(t *testing.T) {
	src := &schemaSource{views: []string{"recent"}}
	scriptLines(t, src)
	for _, k := range src.read {
		if !slices.Contains(buildOrder, k) {
			t.Errorf("it read the %ss it was never going to write", k)
		}
	}
	// Five of the six: this schema offers no materialized views.
	if len(src.read) != 5 {
		t.Errorf("it read %v", src.read)
	}
}

func TestOneObjectIsWrittenOnItsOwn(t *testing.T) {
	src := &schemaSource{}
	stmts, err := ScriptCreate(context.Background(), src, model.NewRef(model.KindTable, "db", "public", "people"))
	if err != nil || len(stmts) != 1 || !strings.HasPrefix(stmts[0].SQL, "CREATE TABLE people") {
		t.Errorf("it wrote %+v, %v", stmts, err)
	}
}

// One object on its own keeps its references, because the table it names is
// already there. Only a whole schema has to take them out.
func TestOneTableKeepsItsReferences(t *testing.T) {
	src := &schemaSource{keys: map[string][]model.ForeignKey{"people": {{Name: "people_who_fkey"}}}}
	stmts, err := ScriptCreate(context.Background(), src, model.NewRef(model.KindTable, "db", "public", "people"))
	if err != nil || len(stmts) != 1 || !strings.Contains(stmts[0].SQL, "keys=people_who_fkey") {
		t.Errorf("it wrote %+v, %v", stmts, err)
	}
}

func TestScriptingWhatCannotBeRendered(t *testing.T) {
	if _, err := ScriptCreate(context.Background(), &noRenderer{}, schemaRef); !errors.Is(err, ErrNoDDL) {
		t.Errorf("one object said %v", err)
	}
	if _, err := ScriptSchema(context.Background(), &noRenderer{}, schemaRef); !errors.Is(err, ErrNoDDL) {
		t.Errorf("a schema said %v", err)
	}
}

// A driver that falls over writing a script is contained (ADR-0017).
type scriptPanicker struct{ schemaSource }

func (*scriptPanicker) Children(context.Context, model.ObjectRef) ([]model.Node, error) {
	panic("fakesql: listing fell over")
}

func TestADriverThatFallsOverScriptingIsContained(t *testing.T) {
	_, err := ScriptSchema(context.Background(), &scriptPanicker{}, schemaRef)
	if err == nil || !strings.Contains(err.Error(), "writing a schema's DDL") {
		t.Errorf("it said %v", err)
	}
}
