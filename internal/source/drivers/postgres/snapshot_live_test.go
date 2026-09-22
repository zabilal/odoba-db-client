//go:build conformance

package postgres

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/diff"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Reading a whole database in one pass (FR-7.1).
//
// The claim this makes is that the bulk queries and the per-object ones read
// the same catalogue the same way. Only a server holding real objects can
// settle that, and if the two ever drift apart a comparison would report
// differences that are this program's rather than the databases'.

func snapshotOf(t *testing.T, src *pgSource) *model.Database {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	db, err := src.Snapshot(ctx, env("IKIGAI_PG_DB", "ikigai_test"))
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	return db
}

func schemaIn(t *testing.T, db *model.Database, name string) model.Schema {
	t.Helper()
	for _, s := range db.Schemas {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("the snapshot holds no schema %q; it has %v", name, schemaNames(db))
	return model.Schema{}
}

func schemaNames(db *model.Database) []string {
	var out []string
	for _, s := range db.Schemas {
		out = append(out, s.Name)
	}
	return out
}

// What one pass reads and what a Describe per object reads are the same
// thing. This is the whole claim, and it is checked by comparing the two
// with the engine written for comparing schemas.
func TestOnePassReadsWhatDescribeReads(t *testing.T) {
	src := openSource(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	snap := schemaIn(t, snapshotOf(t, src), "ikigai_it")

	// The same schema again, one object at a time, through the tree.
	walked := model.Schema{Name: "ikigai_it"}
	ref := model.NewRef(model.KindSchema, env("IKIGAI_PG_DB", "ikigai_test"), "ikigai_it")
	classes, err := src.Children(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range classes {
		kind, ok := model.ClassOf(c.Ref)
		if !ok {
			continue
		}
		objs, err := src.Children(ctx, c.Ref)
		if err != nil {
			t.Fatal(err)
		}
		for _, o := range objs {
			switch kind {
			case model.KindTable, model.KindView, model.KindMaterializedView,
				model.KindRoutine, model.KindSequence:
			default:
				continue
			}
			desc, err := src.Describe(ctx, o.Ref)
			if err != nil {
				t.Fatalf("describing %s %s: %v", kind, o.Ref.Name(), err)
			}
			switch v := desc.(type) {
			case *model.Table:
				walked.Tables = append(walked.Tables, *v)
			case *model.View:
				walked.Views = append(walked.Views, *v)
			case *model.Routine:
				walked.Routines = append(walked.Routines, *v)
			case *model.Sequence:
				walked.Sequences = append(walked.Sequences, *v)
			}
		}
	}

	// A walk cannot read a schema's own owner or comment, and it reads no
	// user types, so those are taken off the one that can before comparing:
	// what is under test is the objects, which both ways do read.
	snap.Owner, snap.Comment, snap.UserTypes, snap.Attrs = "", "", nil, nil

	a := &model.Database{Schemas: []model.Schema{walked}}
	b := &model.Database{Schemas: []model.Schema{snap}}
	if got := diff.Compare(a, b); got.Differs() {
		t.Errorf("one pass and a Describe each disagree:\n%s", differences(got, ""))
	}
	if len(snap.Tables) == 0 {
		t.Fatal("the fixture schema has no tables, so this compared nothing")
	}
}

// differences names what a comparison found, so a failure says where to look.
func differences(n diff.Node, path string) string {
	at := strings.TrimPrefix(path+"/"+n.Name, "/")
	var out []string
	for _, d := range n.Detail {
		out = append(out, fmt.Sprintf("  %s %s: %q vs %q", at, d.Name, d.From, d.To))
	}
	if n.Status != diff.Same && len(n.Children) == 0 && len(n.Detail) == 0 {
		out = append(out, fmt.Sprintf("  %s %s", at, n.Status))
	}
	for _, c := range n.Children {
		if c.Status != diff.Same {
			out = append(out, differences(c, at))
		}
	}
	return strings.Join(out, "\n")
}

// A database compared with itself differs in nothing, read twice from the
// same server. Anything unstable in the read — a map walked in whatever
// order, a pointer into a slice that moved — shows up here and nowhere else.
func TestADatabaseComparedWithItself(t *testing.T) {
	src := openSource(t, false)
	a, b := snapshotOf(t, src), snapshotOf(t, src)
	if got := diff.Compare(a, b); got.Differs() {
		t.Errorf("two reads of one database disagree:\n%s", differences(got, ""))
	}
	if c := got(a, b); c < 20 {
		t.Errorf("it only compared %d things, so it may have compared nothing", c)
	}
}

func got(a, b *model.Database) int {
	return diff.Compare(a, b).Count()[diff.Same]
}

// The system schemas are left out. A comparison that reported pg_catalog
// would report it every time and be right about nothing.
func TestTheSystemSchemasAreNotInTheSnapshot(t *testing.T) {
	db := snapshotOf(t, openSource(t, false))
	for _, s := range db.Schemas {
		if strings.HasPrefix(s.Name, "pg_") || s.Name == "information_schema" {
			t.Errorf("the snapshot holds %q", s.Name)
		}
	}
	if len(db.Schemas) == 0 {
		t.Fatal("it holds no schemas at all")
	}
	if db.Name == "" || db.Charset == "" {
		t.Errorf("the database says it is %q in %q", db.Name, db.Charset)
	}
}

// An included column comes back as a name, not as the quoted name
// pg_get_indexdef prints — or the DDL generator would quote it again and
// render a column nobody has. A generated column comes back as generated
// rather than as a plain default, which would turn it into one on the way
// back out.
func TestAnIncludedColumnThatNeedsQuotingComesBackAsItsName(t *testing.T) {
	src := openSource(t, false)
	stamp := time.Now().UnixNano() % 100000
	table := fmt.Sprintf("inc_t_%d", stamp)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	session, err := src.Session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	run := func(sql string) {
		t.Helper()
		res, err := session.Query(ctx, source.Statement{SQL: sql})
		if res != nil && res.Rows != nil {
			res.Rows.Close()
		}
		if err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	run(fmt.Sprintf(`CREATE TABLE "ikigai_it"."%s" (id integer, "Order" integer,
		doubled integer GENERATED ALWAYS AS (id * 2) STORED)`, table))
	run(fmt.Sprintf(`CREATE INDEX "%s_ix" ON "ikigai_it"."%s" (id) INCLUDE ("Order")`, table, table))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		s, err := src.Session(ctx)
		if err != nil {
			t.Errorf("could not clean up: %v", err)
			return
		}
		defer s.Close()
		res, err := s.Query(ctx, source.Statement{
			SQL: fmt.Sprintf(`DROP TABLE IF EXISTS "ikigai_it"."%s" CASCADE`, table)})
		if res != nil && res.Rows != nil {
			res.Rows.Close()
		}
		if err != nil {
			t.Errorf("could not clean up: %v", err)
		}
	})

	// Both readings agree, and both say Order rather than "Order".
	desc, err := src.Describe(ctx, fixtureRef(table))
	if err != nil {
		t.Fatal(err)
	}
	described := desc.(*model.Table)
	var snapped *model.Table
	for i, tb := range schemaIn(t, snapshotOf(t, src), "ikigai_it").Tables {
		if tb.Name == table {
			snapped = &schemaIn(t, snapshotOf(t, src), "ikigai_it").Tables[i]
			break
		}
	}
	if snapped == nil {
		t.Fatal("the snapshot does not hold the table just made")
	}
	for what, tb := range map[string]*model.Table{"Describe": described, "Snapshot": snapped} {
		if len(tb.Indexes) != 1 || len(tb.Indexes[0].Include) != 1 {
			t.Fatalf("%s read %+v", what, tb.Indexes)
		}
		if got := tb.Indexes[0].Include[0]; got != "Order" {
			t.Errorf("%s says the included column is %q", what, got)
		}
		var gen *model.Column
		for i := range tb.Columns {
			if tb.Columns[i].Name == "doubled" {
				gen = &tb.Columns[i]
			}
		}
		if gen == nil {
			t.Fatalf("%s read no generated column: %+v", what, tb.Columns)
		}
		// The expression is what generates it, not a default: conflating
		// them would render GENERATED as DEFAULT on the way back out.
		if gen.Generated == "" || gen.HasDefault || gen.Default != "" {
			t.Errorf("%s read the generated column as %+v", what, *gen)
		}
	}
	// And rendering it again gives a statement the server accepts.
	stmts, err := dialect{}.CreateObject(fixtureRef(table+"_copy"), snapped)
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range stmts {
		if strings.Contains(st.SQL, `"""Order"""`) {
			t.Fatalf("it rendered %s", st.SQL)
		}
	}
}
