package cassandra

import (
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

func TestANameIsWrittenAsCQLReadsOne(t *testing.T) {
	var d dialect
	for name, want := range map[string]string{
		"people":     `"people"`,
		"People":     `"People"`,
		`odd"name`:   `"odd""name"`,
		"with space": `"with space"`,
		"":           `""`,
		"select":     `"select"`,
	} {
		if got := d.QuoteIdentifier(name); got != want {
			t.Errorf("%q is written %s, want %s", name, got, want)
		}
	}
	// An object is named with its keyspace, and one that has no keyspace is
	// named by itself.
	if got := d.QualifyRef(model.NewRef(model.KindTable, "shop", "people")); got != `"shop"."people"` {
		t.Errorf("a table is addressed as %s", got)
	}
	if got := d.QualifyRef(model.NewRef(model.KindDatabase, "shop")); got != `"shop"` {
		t.Errorf("a keyspace is addressed as %s", got)
	}
	if got := d.Placeholder(1); got != "?" {
		t.Errorf("a value is bound as %s", got)
	}
}

func TestAScriptIsSplitWhereItsStatementsEnd(t *testing.T) {
	var d dialect
	texts := func(script string) []string {
		var out []string
		for _, s := range d.SplitScript(script) {
			out = append(out, s.Text)
		}
		return out
	}
	got := texts("SELECT * FROM t; INSERT INTO t (a) VALUES (1)")
	if len(got) != 2 {
		t.Errorf("two statements read as %q", got)
	}
	// A batch's own semicolons do not end it: it is one statement to send.
	batch := texts("BEGIN BATCH INSERT INTO t (a) VALUES (1); DELETE FROM t WHERE a = 2; APPLY BATCH; SELECT * FROM t")
	if len(batch) != 2 || !strings.HasSuffix(batch[0], "APPLY BATCH") {
		t.Errorf("a batch and a select read as %q", batch)
	}
	// A semicolon inside a string is part of it.
	if got := texts("INSERT INTO t (a) VALUES ('one; two')"); len(got) != 1 {
		t.Errorf("a string holding a semicolon read as %q", got)
	}
}

func TestWhatAStatementDoesIsReadFromIt(t *testing.T) {
	var d dialect
	for stmt, want := range map[string]source.Access{
		"SELECT * FROM people":                     source.AccessRead,
		"select count(*) from people where id = 1": source.AccessRead,
		"USE shop":                           source.AccessRead,
		"DESCRIBE TABLES":                    source.AccessRead,
		"LIST ROLES":                         source.AccessRead,
		"INSERT INTO people (id) VALUES (1)": source.AccessWrite,
		"UPDATE people SET name = 'Ada' WHERE id = 1":           source.AccessWrite,
		"DELETE FROM people WHERE id = 1":                       source.AccessWrite,
		"BEGIN BATCH INSERT INTO t (a) VALUES (1); APPLY BATCH": source.AccessWrite,
		"TRUNCATE people":                         source.AccessDDL,
		"CREATE TABLE t (a int PRIMARY KEY)":      source.AccessDDL,
		"ALTER TABLE t ADD b int":                 source.AccessDDL,
		"DROP KEYSPACE shop":                      source.AccessDDL,
		"CREATE ROLE ada WITH PASSWORD = 'x'":     source.AccessAdmin,
		"DROP USER ada":                           source.AccessAdmin,
		"GRANT SELECT ON KEYSPACE shop TO ada":    source.AccessAdmin,
		"REVOKE MODIFY ON ALL KEYSPACES FROM ada": source.AccessAdmin,
		// A verb nobody listed writes, and nothing at all reads.
		"MUTATE people":     source.AccessWrite,
		"":                  source.AccessRead,
		"-- only a comment": source.AccessRead,
	} {
		if got := d.Classify(stmt); got != want {
			t.Errorf("%q is %s, want %s", stmt, got, want)
		}
	}
	// A script is what its worst statement does.
	if got := d.Classify("SELECT * FROM t; DROP TABLE t"); got != source.AccessDDL {
		t.Errorf("a script that drops a table is %s", got)
	}
}

func TestABrowseIsTheStatementItWouldSend(t *testing.T) {
	var d dialect
	people := model.NewRef(model.KindTable, "shop", "people")
	st, err := d.BuildBrowse(people, source.BrowseOptions{})
	if err != nil || st.SQL != `SELECT * FROM "shop"."people" LIMIT ?` {
		t.Fatalf("a browse of everything: %q %v", st.SQL, err)
	}
	if len(st.Args) != 1 || st.Args[0] != int64(DefaultPageSize) {
		t.Errorf("the page is %v", st.Args)
	}
	st, err = d.BuildBrowse(people, source.BrowseOptions{
		Columns: []string{"id", "name"},
		Filters: []source.Filter{
			{Column: "country", Op: source.OpEqual, Values: []any{"GB"}},
			{Column: "id", Op: source.OpIn, Values: []any{1, 2}},
			{Column: "score", Op: source.OpGreaterEqual, Values: []any{7}},
		},
		Sorts: []source.Sort{{Column: "id", Descending: true}},
		Limit: 10,
	})
	want := `SELECT "id", "name" FROM "shop"."people" WHERE "country" = ? AND "id" IN (?, ?) AND "score" >= ? ORDER BY "id" DESC LIMIT ?`
	if err != nil || st.SQL != want {
		t.Fatalf("a narrowed browse: %q %v", st.SQL, err)
	}
	if len(st.Args) != 5 || st.Args[0] != "GB" || st.Args[4] != int64(10) {
		t.Errorf("the values bound are %v", st.Args)
	}
	// A condition a person typed is ANDed with the filters, and may only read.
	st, err = d.BuildBrowse(people, source.BrowseOptions{Where: "score > 7", Limit: 5})
	if err != nil || !strings.Contains(st.SQL, "score > 7") || !strings.Contains(st.SQL, "WHERE (") {
		t.Errorf("a typed condition: %q %v", st.SQL, err)
	}
	// And ANDed with the filters rather than replacing them.
	st, err = d.BuildBrowse(people, source.BrowseOptions{
		Filters: []source.Filter{{Column: "country", Op: source.OpEqual, Values: []any{"GB"}}},
		Where:   "score > 7", Limit: 5})
	if err != nil || !strings.Contains(st.SQL, `WHERE "country" = ? AND (`) {
		t.Errorf("a filter and a typed condition: %q %v", st.SQL, err)
	}
	if _, err := d.BuildBrowse(people, source.BrowseOptions{Where: "1 = 1; DROP TABLE people"}); err == nil {
		t.Error("a condition carrying a second statement was accepted")
	}
}

func TestABrowseRefusesWhatCQLCannotDo(t *testing.T) {
	var d dialect
	people := model.NewRef(model.KindTable, "shop", "people")
	for what, opt := range map[string]source.BrowseOptions{
		"an offset":                 {Offset: 10},
		"where the nulls go":        {Sorts: []source.Sort{{Column: "id", NullsFirst: true}}},
		"a pattern":                 {Filters: []source.Filter{{Column: "name", Op: source.OpLike, Values: []any{"A%"}}}},
		"text inside a value":       {Filters: []source.Filter{{Column: "name", Op: source.OpContains, Values: []any{"da"}}}},
		"a regular expression":      {Filters: []source.Filter{{Column: "name", Op: source.OpRegex, Values: []any{"^A"}}}},
		"a range":                   {Filters: []source.Filter{{Column: "id", Op: source.OpBetween, Values: []any{1, 9}}}},
		"nothing at all":            {Filters: []source.Filter{{Column: "name", Op: source.OpIsNull}}},
		"inequality":                {Filters: []source.Filter{{Column: "id", Op: source.OpNotEqual, Values: []any{1}}}},
		"a condition negated":       {Filters: []source.Filter{{Column: "id", Op: source.OpEqual, Values: []any{1}, Negate: true}}},
		"a comparison with nothing": {Filters: []source.Filter{{Column: "id", Op: source.OpEqual, Values: []any{nil}}}},
		"one value too few":         {Filters: []source.Filter{{Column: "id", Op: source.OpEqual, Values: nil}}},
		"nothing to be in":          {Filters: []source.Filter{{Column: "id", Op: source.OpIn}}},
		"a place in a stream":       {Seek: &source.Seek{}},
		"a stream to follow":        {Follow: true},
	} {
		if st, err := d.BuildBrowse(people, opt); err == nil {
			t.Errorf("%s was accepted: %q", what, st.SQL)
		}
	}
	// A keyspace holds no rows of its own.
	if _, err := d.BuildBrowse(model.NewRef(model.KindDatabase, "shop"), source.BrowseOptions{}); err == nil {
		t.Error("a keyspace was browsed")
	}
	// A materialized view is read as the table it is written from.
	view := model.NewRef(model.KindMaterializedView, "shop", "people_by_score")
	if _, err := d.BuildBrowse(view, source.BrowseOptions{}); err != nil {
		t.Errorf("a view: %v", err)
	}
}

// ALLOW FILTERING reads every partition on every node. Nothing here adds it:
// that is a person's decision, not a driver's to make quietly.
func TestNothingAsksTheClusterToScanItself(t *testing.T) {
	var d dialect
	st, err := d.BuildBrowse(model.NewRef(model.KindTable, "shop", "people"), source.BrowseOptions{
		Filters: []source.Filter{{Column: "name", Op: source.OpEqual, Values: []any{"Ada"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToUpper(st.SQL), "ALLOW FILTERING") {
		t.Errorf("a browse asked the cluster to scan itself: %s", st.SQL)
	}
}

// CQL wants a primary key on a DELETE or UPDATE and would refuse most of
// these itself. It is asked here anyway, rather than leaving a server to be
// the guardrail (FR-4.9).
func TestCQLNoticesAStatementThatChangesEveryRow(t *testing.T) {
	for _, c := range []struct{ stmt, target string }{
		{"DELETE FROM orders", "orders"},
		{"UPDATE orders SET paid = true", "orders"},
		{"DELETE FROM shop.orders", "orders"},
	} {
		u := unboundedIn(c.stmt)
		if u == nil {
			t.Errorf("%q was not noticed", c.stmt)
			continue
		}
		if u.Target != c.target {
			t.Errorf("%q names %q, want %q", c.stmt, u.Target, c.target)
		}
	}
	for _, stmt := range []string{
		"DELETE FROM orders WHERE id = 1",
		"UPDATE orders SET paid = true WHERE id = 1",
		"SELECT * FROM orders",
		"TRUNCATE orders",
	} {
		if u := unboundedIn(stmt); u != nil {
			t.Errorf("%q was taken for one: %v", stmt, u)
		}
	}
}
