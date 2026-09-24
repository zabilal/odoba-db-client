package sqlite

import (
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// A flavour is what differs between a local file and the same SQL over a
// wire. Everything else in this package is shared, so what a flavour
// changes is worth saying out loud.

func TestTheFlavourSaysWhatTheConnectionIs(t *testing.T) {
	for name, f := range map[string]Flavour{
		"a file":   {Product: "SQLite", Where: "/tmp/x.db", WhereIs: "file", ResultOrigins: true},
		"a server": {Product: "libSQL", Where: "http://h:8080", WhereIs: "url"},
	} {
		t.Run(name, func(t *testing.T) {
			s := NewSource(nil, source.ConnectionConfig{}, f).(*sqliteSource)
			if got := s.Capabilities().Query.EditableResults; got != f.ResultOrigins {
				t.Errorf("it claims editable results is %v, and it can say where a column came from: %v",
					got, f.ResultOrigins)
			}
		})
	}
}

// A result whose columns cannot be traced to a table cannot be edited:
// there is nothing to write back to. Claiming otherwise would put an
// editable grid in front of somebody whose edits could not be applied.
func TestAConnectionThatCannotSayWhereAColumnCameFromDoesNotClaimEditableResults(t *testing.T) {
	s := NewSource(nil, source.ConnectionConfig{}, Flavour{ResultOrigins: false}).(*sqliteSource)
	q := s.Capabilities().Query
	if q.EditableResults {
		t.Error("it claims a query's rows can be edited without knowing what table they are from")
	}
	// What it still claims: none of the rest of querying depends on origins.
	if !q.Supported || !q.Transactions || !q.Explain || !q.Cancel {
		t.Errorf("it gave up more than editing: %+v", q)
	}
}

func TestAColumnWithNoTypeIsGivenWhatTheTableDeclares(t *testing.T) {
	decl := []model.Column{
		{Name: "id", Type: model.DataType{Class: model.TypeInteger, Native: "INTEGER"}},
		{Name: "name", Type: model.DataType{Class: model.TypeString, Native: "TEXT"}},
		{Name: "score", Type: model.DataType{Class: model.TypeFloat, Native: "REAL"}},
	}
	cols := []model.ColumnDef{
		{Name: "rowid"}, // nothing declares a rowid, and nothing may invent one
		{Name: "id"},
		{Name: "name", Type: model.DataType{Class: model.TypeJSON, Native: "JSON"}}, // already answered
		{Name: "score"},
		{Name: "total"}, // an expression the table knows nothing about
	}
	applyDeclared(cols, decl)
	for i, want := range []model.TypeClass{
		model.TypeUnknown, model.TypeInteger, model.TypeJSON, model.TypeFloat, model.TypeUnknown,
	} {
		if cols[i].Type.Class != want {
			t.Errorf("%s reads as %v, want %v", cols[i].Name, cols[i].Type.Class, want)
		}
	}
	if cols[1].Type.Native != "INTEGER" {
		t.Errorf("id took the class and not the type: %+v", cols[1].Type)
	}
}

// Asking the table costs a round trip, so it is asked only when the
// connection did not answer — which a local file always does.
func TestTheTableIsOnlyAskedWhenTheConnectionDidNotSay(t *testing.T) {
	answered := []model.ColumnDef{
		{Name: "id", Type: model.DataType{Class: model.TypeInteger}},
		{Name: "name", Type: model.DataType{Class: model.TypeString}},
	}
	if unknownTypes(answered) {
		t.Error("a result whose columns all have types would be asked again")
	}
	if !unknownTypes(append(answered, model.ColumnDef{Name: "note"})) {
		t.Error("a result with an untyped column would not be asked")
	}
	if unknownTypes(nil) {
		t.Error("a result with no columns at all would be asked")
	}
}

// The repair is not libSQL's alone. SQLite reports a type for a result
// column only where it is a table's column; a view's computed column comes
// back with nothing, and the view itself knows better — SQLite works out
// what a CAST produces and pragma_table_xinfo says so. Browsing such a view
// used to show its column as unknown.
func TestAViewsComputedColumnTakesTheTypeTheViewKnows(t *testing.T) {
	s := open(t, fixture(t), source.Guard{})
	ctx := t.Context()
	if _, err := s.db.ExecContext(ctx, `CREATE VIEW casts AS
		SELECT CAST(id AS TEXT) AS id_text, name || '!' AS shout, name FROM people`); err != nil {
		t.Fatal(err)
	}
	rs, err := s.Browse(ctx, model.NewRef(model.KindView, "main", "casts"), source.BrowseOptions{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	want := map[string]model.TypeClass{
		"id_text": model.TypeString,  // the view declares text, the connection said nothing
		"name":    model.TypeString,  // the connection said so itself
		"shout":   model.TypeUnknown, // nothing knows what concatenation produces
	}
	for _, c := range rs.Columns() {
		if w, ok := want[c.Name]; ok && c.Type.Class != w {
			t.Errorf("%s reads as %v, want %v", c.Name, c.Type.Class, w)
		}
		delete(want, c.Name)
	}
	if len(want) > 0 {
		t.Errorf("columns missing from the browse: %v", want)
	}
}

// And a table is not changed by any of it: a browse of one reports the
// declared types as it always has, through the connection.
func TestATableStillAnswersForItself(t *testing.T) {
	s := open(t, fixture(t), source.Guard{})
	rs, err := s.Browse(t.Context(), people, source.BrowseOptions{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	want := map[string]model.TypeClass{
		"id": model.TypeInteger, "name": model.TypeString, "score": model.TypeFloat,
		"born": model.TypeDate, "meta": model.TypeJSON, "pic": model.TypeBytes,
	}
	for _, c := range rs.Columns() {
		if w, ok := want[c.Name]; ok && c.Type.Class != w {
			t.Errorf("%s reads as %v, want %v", c.Name, c.Type.Class, w)
		}
		delete(want, c.Name)
	}
	if len(want) > 0 {
		t.Errorf("columns missing from the browse: %v", want)
	}
}
