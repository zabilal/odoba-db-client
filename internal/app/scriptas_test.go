package app

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// scripted answers Describe and a PostgreSQL-shaped dialect's names; the
// rest of Source and Dialect is nil.
type scripted struct {
	source.Source
	source.Dialect
	desc   any
	panics bool
}

func (s scripted) Describe(context.Context, model.ObjectRef) (any, error) {
	if s.panics {
		panic("the driver fell over")
	}
	return s.desc, nil
}
func (scripted) QuoteIdentifier(n string) string { return `"` + n + `"` }
func (scripted) QualifyRef(r model.ObjectRef) string {
	return `"` + r.Path[len(r.Path)-2] + `"."` + r.Name() + `"`
}
func (scripted) Placeholder(i int) string { return "$" + strconv.Itoa(i) }

// noDialect describes but has no query language.
type noDialect struct{ source.Source }

func (noDialect) Describe(context.Context, model.ObjectRef) (any, error) { return &model.Table{}, nil }

var people = &model.Table{Name: "people",
	Columns: []model.Column{
		{Name: "id", Identity: true},
		{Name: "name"},
		{Name: "initial", Generated: "left(name, 1)"},
	},
	PrimaryKey: &model.PrimaryKey{Columns: []string{"id"}},
}

func TestScriptAsWritesFromTheDescriptionAndTheDialect(t *testing.T) {
	ctx, ref := context.Background(), model.NewRef(model.KindTable, "db", "public", "people")
	src := scripted{desc: people}
	for _, c := range []struct {
		kind ScriptKind
		want string
	}{
		{ScriptSelect, "SELECT \"id\",\n       \"name\",\n       \"initial\"\nFROM \"public\".\"people\";\n"},
		{ScriptInsert, "INSERT INTO \"public\".\"people\" (\"name\")\nVALUES ($1);\n"},
		{ScriptUpdate, "UPDATE \"public\".\"people\"\nSET \"name\" = $1\nWHERE \"id\" = $2;\n"},
	} {
		if got, err := ScriptAs(ctx, src, ref, c.kind); err != nil || got != c.want {
			t.Errorf("kind %d:\n got %q, %v\nwant %q", c.kind, got, err, c.want)
		}
	}
}

func TestScriptAsRefusesWhatItCannotWrite(t *testing.T) {
	ctx, ref := context.Background(), model.NewRef(model.KindView, "db", "public", "v")
	view := scripted{desc: &model.View{Name: "v", Columns: []model.Column{{Name: "a"}}}}
	if _, err := ScriptAs(ctx, view, ref, ScriptSelect); err != nil {
		t.Errorf("a view as SELECT: %v", err)
	}
	if _, err := ScriptAs(ctx, view, ref, ScriptInsert); err == nil {
		t.Error("a view was written as INSERT")
	}
	if _, err := ScriptAs(ctx, noDialect{}, ref, ScriptSelect); err == nil || !strings.Contains(err.Error(), "no query language") {
		t.Errorf("a source with no query language should say so, got %v", err)
	}
	_, err := ScriptAs(ctx, scripted{panics: true}, ref, ScriptSelect)
	if err == nil || !strings.Contains(err.Error(), "the driver fell over") {
		t.Errorf("a panicking describe should come back as an error, got %v", err)
	}
}
