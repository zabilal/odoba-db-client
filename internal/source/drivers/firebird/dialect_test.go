package firebird

import (
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// What this driver writes, decided without a server. Every identifier here
// goes through QuoteIdentifier and every value through a placeholder
// (NFR-S6), so these tests are as much about what is not in a statement as
// about what is.

var d dialect

func people() model.ObjectRef { return model.NewRef(model.KindTable, "ikigai.fdb", "PEOPLE") }

func TestANameIsAlwaysQuoted(t *testing.T) {
	for in, want := range map[string]string{
		"PEOPLE":      `"PEOPLE"`,
		"people":      `"people"`, // a lower-case name is a different table, and is kept
		`odd"name`:    `"odd""name"`,
		"WITH SPACES": `"WITH SPACES"`,
		"":            `""`,
	} {
		if got := d.QuoteIdentifier(in); got != want {
			t.Errorf("%q quotes as %s, want %s", in, got, want)
		}
	}
}

// An object is named by itself. Firebird has no schemas, and no way to
// qualify by the database: the database in a ref is the tree's bookkeeping
// and must not reach a statement.
func TestAnObjectIsNamedWithoutItsDatabase(t *testing.T) {
	for _, ref := range []model.ObjectRef{
		model.NewRef(model.KindTable, "ikigai.fdb", "PEOPLE"),
		model.NewRef(model.KindTable, "PEOPLE"),
		model.NewRef(model.KindView, "somewhere else", "PEOPLE"),
	} {
		if got := d.QualifyRef(ref); got != `"PEOPLE"` {
			t.Errorf("%v is named %s", ref.Path, got)
		}
	}
}

func TestEveryPlaceholderIsAQuestionMark(t *testing.T) {
	for i := 0; i < 4; i++ {
		if got := d.Placeholder(i); got != "?" {
			t.Errorf("the %dth placeholder is %q", i, got)
		}
	}
}

func TestABrowseReadsAPageInOrder(t *testing.T) {
	st, err := d.BuildBrowse(people(), source.BrowseOptions{
		Columns: []string{"ID", "NAME"},
		Sorts:   []source.Sort{{Column: "NAME", Descending: true}, {Column: "ID", NullsFirst: true}},
		Offset:  40, Limit: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `SELECT "ID", "NAME" FROM "PEOPLE" ORDER BY "NAME" DESC NULLS LAST, "ID" NULLS FIRST` +
		` OFFSET ? ROWS FETCH NEXT ? ROWS ONLY`
	if st.SQL != want {
		t.Errorf("it reads\n%s\nwant\n%s", st.SQL, want)
	}
	if len(st.Args) != 2 || st.Args[0] != int64(40) || st.Args[1] != int64(20) {
		t.Errorf("the window is bound as %v", st.Args)
	}
}

// Every column, and the driver's own page size, when nothing was asked for.
// OFFSET is left out at zero, and FETCH is not: an unbounded read is what
// NFR-P11 forbids.
func TestABrowseWithNothingAskedForIsStillBounded(t *testing.T) {
	st, err := d.BuildBrowse(people(), source.BrowseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if st.SQL != `SELECT * FROM "PEOPLE" FETCH NEXT ? ROWS ONLY` {
		t.Errorf("it reads %s", st.SQL)
	}
	if len(st.Args) != 1 || st.Args[0] != int64(DefaultPageSize) {
		t.Errorf("its window is %v", st.Args)
	}
}

func TestWhatCannotBeBrowsedIsRefused(t *testing.T) {
	for _, kind := range []model.ObjectKind{model.KindIndex, model.KindRoutine, model.KindSequence, model.KindTrigger} {
		if _, err := d.BuildBrowse(model.NewRef(kind, "db", "X"), source.BrowseOptions{}); err == nil {
			t.Errorf("it would browse a %s", kind)
		}
	}
	// Seeking and following are a log's, and Firebird holds no log.
	for name, opt := range map[string]source.BrowseOptions{
		"a seek":   {Seek: &source.Seek{}},
		"a follow": {Follow: true},
	} {
		if _, err := d.BuildBrowse(people(), opt); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestASortNeedsAColumn(t *testing.T) {
	if _, err := d.BuildBrowse(people(), source.BrowseOptions{Sorts: []source.Sort{{}}}); err == nil {
		t.Error("it sorted by nothing")
	}
}

func TestEveryFilterBindsItsValues(t *testing.T) {
	for name, c := range map[string]struct {
		f    source.Filter
		want string
		args []any
	}{
		"equal": {source.Filter{Column: "ID", Op: source.OpEqual, Values: []any{int64(7)}},
			`"ID" = ?`, []any{int64(7)}},
		"equal to nothing": {source.Filter{Column: "ID", Op: source.OpEqual, Values: []any{nil}},
			`"ID" IS NULL`, nil},
		"not equal to nothing": {source.Filter{Column: "ID", Op: source.OpNotEqual, Values: []any{nil}},
			`"ID" IS NOT NULL`, nil},
		"less": {source.Filter{Column: "N", Op: source.OpLess, Values: []any{int64(3)}},
			`"N" < ?`, []any{int64(3)}},
		"like": {source.Filter{Column: "NAME", Op: source.OpLike, Values: []any{"a%"}},
			`CAST("NAME" AS VARCHAR(8191)) LIKE ?`, []any{"a%"}},
		"not like": {source.Filter{Column: "NAME", Op: source.OpNotLike, Values: []any{"a%"}},
			`CAST("NAME" AS VARCHAR(8191)) NOT LIKE ?`, []any{"a%"}},
		"contains": {source.Filter{Column: "NAME", Op: source.OpContains, Values: []any{"50%"}},
			`CAST("NAME" AS VARCHAR(8191)) CONTAINING ?`, []any{"50%"}},
		"is null": {source.Filter{Column: "BORN", Op: source.OpIsNull},
			`"BORN" IS NULL`, nil},
		"is not null": {source.Filter{Column: "BORN", Op: source.OpIsNotNull},
			`"BORN" IS NOT NULL`, nil},
		"between": {source.Filter{Column: "ID", Op: source.OpBetween, Values: []any{int64(1), int64(9)}},
			`"ID" BETWEEN ? AND ?`, []any{int64(1), int64(9)}},
		"in": {source.Filter{Column: "ID", Op: source.OpIn, Values: []any{int64(1), int64(2)}},
			`"ID" IN (?, ?)`, []any{int64(1), int64(2)}},
		"in, with nothing among them": {source.Filter{Column: "ID", Op: source.OpIn, Values: []any{int64(1), nil}},
			`("ID" IN (?) OR "ID" IS NULL)`, []any{int64(1)}},
		"in nothing at all": {source.Filter{Column: "ID", Op: source.OpIn},
			`1 = 0`, nil},
		"in only nothing": {source.Filter{Column: "ID", Op: source.OpIn, Values: []any{nil}},
			`"ID" IS NULL`, nil},
		"not in": {source.Filter{Column: "ID", Op: source.OpNotIn, Values: []any{int64(1)}},
			`("ID" NOT IN (?) OR "ID" IS NULL)`, []any{int64(1)}},
		"not in, nothing among them": {source.Filter{Column: "ID", Op: source.OpNotIn, Values: []any{int64(1), nil}},
			`"ID" NOT IN (?)`, []any{int64(1)}},
		"not in nothing at all": {source.Filter{Column: "ID", Op: source.OpNotIn},
			`1 = 1`, nil},
		"not in only nothing": {source.Filter{Column: "ID", Op: source.OpNotIn, Values: []any{nil}},
			`"ID" IS NOT NULL`, nil},
		"negated": {source.Filter{Column: "ID", Op: source.OpEqual, Values: []any{int64(1)}, Negate: true},
			`NOT ("ID" = ?)`, []any{int64(1)}},
	} {
		t.Run(name, func(t *testing.T) {
			b := &builder{d: d}
			got, err := b.filter(c.f)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("it reads %s, want %s", got, c.want)
			}
			if len(b.args) != len(c.args) {
				t.Fatalf("it binds %v, want %v", b.args, c.args)
			}
			for i := range c.args {
				if b.args[i] != c.args[i] {
					t.Errorf("it binds %v, want %v", b.args, c.args)
				}
			}
			if strings.ContainsAny(got, "'") {
				t.Errorf("a value reached the statement: %s", got)
			}
		})
	}
}

func TestAFilterThatCannotBeWrittenIsRefused(t *testing.T) {
	for name, f := range map[string]source.Filter{
		"a regular expression":    {Column: "NAME", Op: source.OpRegex, Values: []any{"^a"}},
		"an operator nobody has":  {Column: "NAME", Op: source.FilterOp("~~")},
		"equal to two things":     {Column: "ID", Op: source.OpEqual, Values: []any{1, 2}},
		"between one thing":       {Column: "ID", Op: source.OpBetween, Values: []any{1}},
		"null with a value":       {Column: "ID", Op: source.OpIsNull, Values: []any{1}},
		"contains nothing given":  {Column: "NAME", Op: source.OpContains},
		"ordered against nothing": {Column: "ID", Op: source.OpLess, Values: []any{nil}},
		"like nothing given":      {Column: "NAME", Op: source.OpLike},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := (&builder{d: d}).filter(f); err == nil {
				t.Error("it was accepted")
			}
		})
	}
}

// A condition somebody typed is one condition, and only a condition.
func TestATypedConditionMayOnlyRead(t *testing.T) {
	for name, where := range map[string]string{
		"a second statement": `1=1; DELETE FROM "PEOPLE"`,
		"a delete":           `DELETE FROM "PEOPLE"`,
		"an update":          `UPDATE "PEOPLE" SET "NAME" = 'x'`,
		"a procedure":        `EXECUTE PROCEDURE DO_HARM`,
	} {
		t.Run(name, func(t *testing.T) {
			if st, err := d.BuildBrowse(people(), source.BrowseOptions{Where: where}); err == nil {
				t.Errorf("it built %s", st.SQL)
			}
		})
	}
	st, err := d.BuildBrowse(people(), source.BrowseOptions{Where: `"ID" > 10`})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st.SQL, `"ID" > 10`) || !strings.Contains(st.SQL, "WHERE (") {
		t.Errorf("a condition that reads was not kept: %s", st.SQL)
	}
}

func TestCountingAndListingAndMeasuringAreTheSameRows(t *testing.T) {
	opt := source.BrowseOptions{
		Filters: []source.Filter{{Column: "ID", Op: source.OpGreater, Values: []any{int64(5)}}},
		// Paging and ordering are a page's, and none of these three is a page.
		Sorts: []source.Sort{{Column: "NAME"}}, Offset: 10, Limit: 5,
	}
	count, err := d.buildCount(people(), opt)
	if err != nil {
		t.Fatal(err)
	}
	if count.SQL != `SELECT COUNT(*) FROM "PEOPLE" WHERE "ID" > ?` {
		t.Errorf("counting reads %s", count.SQL)
	}
	list, err := d.buildDistinct(people(), "NAME", opt, 50)
	if err != nil {
		t.Fatal(err)
	}
	want := `SELECT "NAME", COUNT(*) FROM "PEOPLE" WHERE "ID" > ? GROUP BY "NAME"` +
		` ORDER BY COUNT(*) DESC, "NAME" FETCH NEXT ? ROWS ONLY`
	if list.SQL != want {
		t.Errorf("listing reads\n%s\nwant\n%s", list.SQL, want)
	}
	if len(list.Args) != 2 || list.Args[1] != int64(50) {
		t.Errorf("the list's own limit is %v", list.Args)
	}
	stats, _, err := d.buildStats(people(), model.ColumnDef{Name: "SCORE",
		Type: model.DataType{Class: model.TypeFloat}}, opt)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(stats.SQL, `FROM "PEOPLE" WHERE "ID" > ?`) {
		t.Errorf("measuring reads %s", stats.SQL)
	}
}

func TestListingNeedsALimit(t *testing.T) {
	for _, limit := range []int{0, -1} {
		if _, err := d.buildDistinct(people(), "NAME", source.BrowseOptions{}, limit); err == nil {
			t.Errorf("a list of %d values was accepted", limit)
		}
	}
}

func TestOnlyABrowsableObjectIsCountedListedOrMeasured(t *testing.T) {
	ix := model.NewRef(model.KindIndex, "db", "X")
	if _, err := d.buildCount(ix, source.BrowseOptions{}); err == nil {
		t.Error("an index was counted")
	}
	if _, err := d.buildDistinct(ix, "A", source.BrowseOptions{}, 10); err == nil {
		t.Error("an index's values were listed")
	}
	if _, _, err := d.buildStats(ix, model.ColumnDef{Name: "A"}, source.BrowseOptions{}); err == nil {
		t.Error("an index's column was measured")
	}
}

// A row loaded over the row there with its key is Firebird's own form, which
// is words before the INSERT as well as after it.
func TestAnUpsertIsWrittenAroundTheInsert(t *testing.T) {
	before, after := d.UpsertAround([]string{"ID", "N"}, []string{"ID", "NAME", "N"})
	if before != "UPDATE OR " {
		t.Errorf("it begins %q", before)
	}
	if after != ` MATCHING ("ID", "N")` {
		t.Errorf("it ends %q", after)
	}
	whole := before + `INSERT INTO "T" ("ID", "NAME", "N") VALUES (?, ?, ?)` + after
	if !strings.HasPrefix(whole, "UPDATE OR INSERT INTO") {
		t.Errorf("together they read %s", whole)
	}
}
