//go:build conformance

package postgres

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// The DDL this renders has to run. Everything else about it is this
// program's opinion of PostgreSQL's grammar; a server is the only thing that
// can say whether the opinion is right (FR-6.4).

// runDDL sends statements through a session, as the preview does, and fails
// on the first one the server refuses.
func runDDL(t *testing.T, src *pgSource, stmts []source.Statement) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	session, err := src.Session(ctx)
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	defer session.Close()
	for i, st := range stmts {
		res, err := session.Query(ctx, st)
		if res != nil && res.Rows != nil {
			res.Rows.Close()
		}
		if err != nil {
			t.Fatalf("statement %d of %d:\n\t%s\n%v", i+1, len(stmts), st.SQL, err)
		}
	}
}

// designedTable makes a table from rendered DDL and answers what the server
// then says it is.
func designedTable(t *testing.T, src *pgSource, tbl *model.Table) *model.Table {
	t.Helper()
	stmts, err := dialect{}.CreateObject(fixtureRef(tbl.Name), tbl)
	if err != nil {
		t.Fatalf("rendering CREATE: %v", err)
	}
	runDDL(t, src, stmts)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		session, err := src.Session(ctx)
		if err != nil {
			return
		}
		defer session.Close()
		session.Query(ctx, source.Statement{SQL: `DROP TABLE IF EXISTS "ikigai_it"."` + tbl.Name + `" CASCADE`})
	})
	return described(t, src, tbl.Name)
}

func described(t *testing.T, src *pgSource, name string) *model.Table {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	desc, err := src.Describe(ctx, fixtureRef(name))
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	tbl, ok := desc.(*model.Table)
	if !ok {
		t.Fatalf("described as %T", desc)
	}
	return tbl
}

// A table this rendered is a table PostgreSQL builds, and describes back as
// what was asked for.
func TestRenderedDDLBuildsTheTable(t *testing.T) {
	src := openSource(t, false)
	name := fmt.Sprintf("ddl_%d", time.Now().UnixNano()%100000)
	asked := &model.Table{
		Name: name,
		Columns: []model.Column{
			{Name: "id", Type: model.DataType{Class: model.TypeInteger, Native: "integer"}, Identity: true},
			{Name: "name", Type: model.DataType{Class: model.TypeString, Native: "text"}},
			{Name: "note", Type: model.DataType{Class: model.TypeString, Native: "varchar(40)", Nullable: true}},
		},
		PrimaryKey: &model.PrimaryKey{Name: name + "_pkey", Columns: []string{"id"}},
		Uniques:    []model.UniqueConstraint{{Name: name + "_name_key", Columns: []string{"name"}}},
		Checks:     []model.CheckConstraint{{Name: name + "_name_len", Expression: "length(name) > 0"}},
		Comment:    "built from rendered DDL",
	}
	asked.Columns[1].Comment = "what they are called"

	got := designedTable(t, src, asked)
	if len(got.Columns) != 3 {
		t.Fatalf("it has %d columns", len(got.Columns))
	}
	// PostgreSQL says the type back in its own words: varchar(40) is
	// character varying(40), which is the same type spelt as the server
	// spells it.
	if !strings.Contains(got.Columns[2].Type.Native, "(40)") {
		t.Errorf("its third column is %q", got.Columns[2].Type.Native)
	}
	if got.Columns[1].Type.Nullable {
		t.Error("a column asked for NOT NULL came back nullable")
	}
	if got.PrimaryKey == nil || got.PrimaryKey.Columns[0] != "id" {
		t.Errorf("its key is %+v", got.PrimaryKey)
	}
	if got.Comment != "built from rendered DDL" {
		t.Errorf("its comment is %q", got.Comment)
	}
	if got.Columns[1].Comment != "what they are called" {
		t.Errorf("its column's comment is %q", got.Columns[1].Comment)
	}
}

// And the DDL for a change runs, in the order it comes back in — which is
// what the ordering is for.
func TestRenderedDDLMakesTheChange(t *testing.T) {
	src := openSource(t, false)
	name := fmt.Sprintf("ddl_alt_%d", time.Now().UnixNano()%100000)
	was := designedTable(t, src, &model.Table{
		Name: name,
		Columns: []model.Column{
			{Name: "id", Type: model.DataType{Class: model.TypeInteger, Native: "integer"}, Identity: true},
			{Name: "name", Type: model.DataType{Class: model.TypeString, Native: "text"}},
			{Name: "note", Type: model.DataType{Class: model.TypeString, Native: "varchar(40)", Nullable: true}},
		},
		PrimaryKey: &model.PrimaryKey{Name: name + "_pkey", Columns: []string{"id"}},
		Uniques:    []model.UniqueConstraint{{Name: name + "_note_key", Columns: []string{"note"}}},
	})

	// Widen a type, drop the column a unique is on — which means the unique
	// must go first — add one, and comment on it.
	now := &model.Table{Name: name}
	now.Columns = append(now.Columns, was.Columns[0], was.Columns[1])
	now.Columns[1].Type.Native = "varchar(200)"
	now.Columns = append(now.Columns, model.Column{Name: "email",
		Type:    model.DataType{Class: model.TypeString, Native: "text", Nullable: true},
		Comment: "where to write"})
	now.PrimaryKey = was.PrimaryKey

	stmts, err := dialect{}.AlterObject(fixtureRef(name), was, now)
	if err != nil {
		t.Fatalf("rendering ALTER: %v", err)
	}
	if len(stmts) == 0 {
		t.Fatal("it rendered nothing for a change")
	}
	runDDL(t, src, stmts)

	got := described(t, src, name)
	if len(got.Columns) != 3 {
		t.Fatalf("it now has %d columns: %+v", len(got.Columns), got.Columns)
	}
	if !strings.Contains(got.Columns[1].Type.Native, "(200)") {
		t.Errorf("the widened column is %q", got.Columns[1].Type.Native)
	}
	if got.Columns[2].Name != "email" || got.Columns[2].Comment != "where to write" {
		t.Errorf("the new column is %+v", got.Columns[2])
	}
	if len(got.Uniques) != 0 {
		t.Errorf("the unique on the dropped column is still there: %+v", got.Uniques)
	}
}

// A rename runs, and the column keeps what was in it — which is the whole
// reason a rename is not a drop and an add.
func TestARenameRunsAndKeepsWhatWasThere(t *testing.T) {
	src := openSource(t, false)
	name := fmt.Sprintf("ddl_ren_%d", time.Now().UnixNano()%100000)
	designedTable(t, src, &model.Table{
		Name: name,
		Columns: []model.Column{
			{Name: "id", Type: model.DataType{Class: model.TypeInteger, Native: "integer"}},
			{Name: "note", Type: model.DataType{Class: model.TypeString, Native: "text", Nullable: true}},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	session, err := src.Session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if _, err := session.Query(ctx, source.Statement{
		SQL: `INSERT INTO "ikigai_it"."` + name + `" (id, note) VALUES (1, 'kept')`}); err != nil {
		t.Fatal(err)
	}

	stmts, err := dialect{}.RenameColumn(fixtureRef(name), "note", "comment")
	if err != nil {
		t.Fatal(err)
	}
	runDDL(t, src, stmts)

	res, err := session.Query(ctx, source.Statement{
		SQL: `SELECT comment FROM "ikigai_it"."` + name + `" WHERE id = 1`})
	if err != nil {
		t.Fatalf("reading the renamed column: %v", err)
	}
	rows := drain(t, res.Rows)
	if len(rows) != 1 || fmt.Sprint(rows[0][0]) != "kept" {
		t.Errorf("the renamed column holds %v", rows)
	}
}

// fixtureRef names a table in the schema these tests build in, which is
// what tells the renderer where to send its statements.
func fixtureRef(name string) model.ObjectRef {
	return model.NewRef(model.KindTable, env("IKIGAI_PG_DB", "ikigai_test"), "ikigai_it", name)
}

// The objects whose structure is their source (FR-6.5). These queries are
// catalogue queries, and a catalogue query is only right if the catalogue
// agrees.

func TestDescribingASourceObject(t *testing.T) {
	src := openSource(t, false)
	stamp := time.Now().UnixNano() % 100000
	table := fmt.Sprintf("src_t_%d", stamp)
	designedTable(t, src, &model.Table{
		Name: table,
		Columns: []model.Column{
			{Name: "id", Type: model.DataType{Class: model.TypeInteger, Native: "integer"}},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	session, err := src.Session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	run := func(sql string) {
		t.Helper()
		if _, err := session.Query(ctx, source.Statement{SQL: sql}); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}

	fn := fmt.Sprintf("src_f_%d", stamp)
	view := fmt.Sprintf("src_v_%d", stamp)
	seq := fmt.Sprintf("src_s_%d", stamp)
	trg := fmt.Sprintf("src_g_%d", stamp)
	run(fmt.Sprintf(`CREATE FUNCTION "ikigai_it"."%s"(a integer) RETURNS integer
		LANGUAGE sql AS $$ SELECT a + 1 $$`, fn))
	run(fmt.Sprintf(`CREATE VIEW "ikigai_it"."%s" AS SELECT id FROM "ikigai_it"."%s"`, view, table))
	run(fmt.Sprintf(`CREATE SEQUENCE "ikigai_it"."%s" START WITH 7 INCREMENT BY 3`, seq))
	run(fmt.Sprintf(`CREATE FUNCTION "ikigai_it"."%s_fn"() RETURNS trigger
		LANGUAGE plpgsql AS $$ BEGIN RETURN NEW; END $$`, trg))
	run(fmt.Sprintf(`CREATE TRIGGER "%s" BEFORE INSERT ON "ikigai_it"."%s"
		FOR EACH ROW EXECUTE FUNCTION "ikigai_it"."%s_fn"()`, trg, table, trg))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		s, err := src.Session(ctx)
		if err != nil {
			return
		}
		defer s.Close()
		for _, sql := range []string{
			fmt.Sprintf(`DROP VIEW IF EXISTS "ikigai_it"."%s"`, view),
			fmt.Sprintf(`DROP SEQUENCE IF EXISTS "ikigai_it"."%s"`, seq),
			fmt.Sprintf(`DROP FUNCTION IF EXISTS "ikigai_it"."%s"(integer)`, fn),
			fmt.Sprintf(`DROP FUNCTION IF EXISTS "ikigai_it"."%s_fn"() CASCADE`, trg),
		} {
			s.Query(ctx, source.Statement{SQL: sql})
		}
	})

	// A routine comes back as the statement PostgreSQL would write, which is
	// what makes editing it editing text.
	desc, err := src.Describe(ctx, model.NewRef(model.KindRoutine,
		env("IKIGAI_PG_DB", "ikigai_test"), "ikigai_it", fn+"(a integer)"))
	if err != nil {
		t.Fatalf("describing a function: %v", err)
	}
	routine, ok := desc.(*model.Routine)
	if !ok {
		t.Fatalf("a function described as %T", desc)
	}
	if !strings.Contains(routine.Definition, "CREATE OR REPLACE FUNCTION") {
		t.Errorf("its definition reads %q", routine.Definition)
	}
	if routine.Language != "sql" || routine.Kind != model.RoutineFunction {
		t.Errorf("it is a %s %s", routine.Language, routine.Kind)
	}
	// And what it renders is what it was given.
	fnStmts, err := dialect{}.CreateObject(model.NewRef(model.KindRoutine,
		env("IKIGAI_PG_DB", "ikigai_test"), "ikigai_it", fn+"(a integer)"), routine)
	if err != nil {
		t.Fatal(err)
	}
	runDDL(t, src, fnStmts)

	// A view comes back as its body, and is replaced in place.
	desc, err = src.Describe(ctx, model.NewRef(model.KindView,
		env("IKIGAI_PG_DB", "ikigai_test"), "ikigai_it", view))
	if err != nil {
		t.Fatalf("describing a view: %v", err)
	}
	v, ok := desc.(*model.View)
	if !ok || strings.TrimSpace(v.Definition) == "" {
		t.Fatalf("a view described as %T with %+v", desc, v)
	}
	viewStmts, err := dialect{}.CreateObject(model.NewRef(model.KindView,
		env("IKIGAI_PG_DB", "ikigai_test"), "ikigai_it", view), v)
	if err != nil {
		t.Fatal(err)
	}
	runDDL(t, src, viewStmts)

	// A sequence comes back as its numbers.
	desc, err = src.Describe(ctx, model.NewRef(model.KindSequence,
		env("IKIGAI_PG_DB", "ikigai_test"), "ikigai_it", seq))
	if err != nil {
		t.Fatalf("describing a sequence: %v", err)
	}
	q, ok := desc.(*model.Sequence)
	if !ok {
		t.Fatalf("a sequence described as %T", desc)
	}
	if q.Start != 7 || q.Increment != 3 {
		t.Errorf("it starts at %d by %d", q.Start, q.Increment)
	}
	seqStmts, err := dialect{}.CreateObject(model.NewRef(model.KindSequence,
		env("IKIGAI_PG_DB", "ikigai_test"), "ikigai_it", seq), q)
	if err != nil {
		t.Fatal(err)
	}
	runDDL(t, src, seqStmts)

	// A trigger comes back as the statement that made it.
	desc, err = src.Describe(ctx, model.NewRef(model.KindTrigger,
		env("IKIGAI_PG_DB", "ikigai_test"), "ikigai_it", table, trg))
	if err != nil {
		t.Fatalf("describing a trigger: %v", err)
	}
	tr, ok := desc.(*model.Trigger)
	if !ok || !strings.Contains(tr.Definition, "CREATE TRIGGER") {
		t.Fatalf("a trigger described as %T with %+v", desc, tr)
	}
	trgStmts, err := dialect{}.CreateObject(model.NewRef(model.KindTrigger,
		env("IKIGAI_PG_DB", "ikigai_test"), "ikigai_it", table, trg), tr)
	if err != nil {
		t.Fatal(err)
	}
	runDDL(t, src, trgStmts)
}
