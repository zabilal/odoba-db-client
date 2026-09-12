package sqlcomplete

import (
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

func testCatalog() *Static {
	c := NewStatic()
	c.AddObject("shop", "public", Object{Name: "orders"},
		Column{Name: "id", Type: "integer"},
		Column{Name: "order_date", Type: "date"},
		Column{Name: "Customer Id", Type: "integer"},
	)
	c.AddObject("shop", "public", Object{Name: "order_items"},
		Column{Name: "id", Type: "integer"},
		Column{Name: "order_id", Type: "integer"},
		Column{Name: "qty", Type: "integer"},
	)
	c.AddObject("shop", "public", Object{Name: "open_orders", Kind: model.KindView})
	c.AddObject("shop", "audit", Object{Name: "changes"}, Column{Name: "at", Type: "timestamptz"})
	c.AddObject("warehouse", "public", Object{Name: "pallets"})
	c.AddRoutine("shop", "public", "order_total")
	return c
}

// complete runs a request whose cursor is marked with a '|'.
func complete(t *testing.T, e *Engine, text string, limit int) source.CompletionResult {
	t.Helper()
	i := strings.IndexByte(text, '|')
	if i < 0 {
		t.Fatalf("no cursor in %q", text)
	}
	return e.Complete(source.CompletionRequest{
		Text:     text[:i] + text[i+1:],
		Cursor:   len([]rune(text[:i])),
		Database: "shop",
		Schema:   "public",
		Limit:    limit,
	})
}

func labels(res source.CompletionResult) []string {
	out := make([]string, 0, len(res.Candidates))
	for _, c := range res.Candidates {
		out = append(out, c.Label)
	}
	return out
}

func find(res source.CompletionResult, label string) (source.Completion, bool) {
	for _, c := range res.Candidates {
		if c.Label == label {
			return c, true
		}
	}
	return source.Completion{}, false
}

func rankOf(res source.CompletionResult, label string) int {
	for i, c := range res.Candidates {
		if c.Label == label {
			return i
		}
	}
	return -1
}

func TestCompleteOffersTablesOfTheSessionSchema(t *testing.T) {
	e := New(sqllex.PostgreSQL, testCatalog(), nil)
	res := complete(t, e, "select * from ord|", 0)
	got := labels(res)
	if len(got) < 2 || got[0] != "orders" {
		t.Fatalf("candidates %v, want orders first", got)
	}
	if _, ok := find(res, "order_items"); !ok {
		t.Errorf("candidates %v, want order_items among them", got)
	}
	// A table of another schema is not offered unqualified.
	if _, ok := find(res, "changes"); ok {
		t.Errorf("candidates %v include another schema's table", got)
	}
	// Nor a table of another database.
	if _, ok := find(res, "pallets"); ok {
		t.Errorf("candidates %v include another database's table", got)
	}
	if c, _ := find(res, "orders"); c.Kind != source.CompletionTable || c.Detail != "public" {
		t.Errorf("orders is %v detail %q, want a table detailed public", c.Kind, c.Detail)
	}
}

func TestCompleteOffersViewsApartFromTables(t *testing.T) {
	e := New(sqllex.PostgreSQL, testCatalog(), nil)
	res := complete(t, e, "select * from o|", 0)
	c, ok := find(res, "open_orders")
	if !ok {
		t.Fatalf("candidates %v, want the view among them", labels(res))
	}
	if c.Kind != source.CompletionView {
		t.Errorf("open_orders is %v, want a view", c.Kind)
	}
	if rankOf(res, "orders") > rankOf(res, "open_orders") {
		t.Errorf("candidates %v, want a table above a view", labels(res))
	}
}

func TestCompleteOffersSchemasWhereATableGoes(t *testing.T) {
	e := New(sqllex.PostgreSQL, testCatalog(), nil)
	res := complete(t, e, "select * from au|", 0)
	c, ok := find(res, "audit")
	if !ok {
		t.Fatalf("candidates %v, want the schema among them", labels(res))
	}
	if c.Kind != source.CompletionSchema {
		t.Errorf("audit is %v, want a schema", c.Kind)
	}
}

func TestCompleteQualifiedByASchema(t *testing.T) {
	e := New(sqllex.PostgreSQL, testCatalog(), nil)
	res := complete(t, e, "select * from audit.|", 0)
	if got := labels(res); len(got) != 1 || got[0] != "changes" {
		t.Errorf("candidates %v, want the named schema's table alone", got)
	}
	res = complete(t, e, "select * from warehouse.public.|", 0)
	if got := labels(res); len(got) != 1 || got[0] != "pallets" {
		t.Errorf("candidates %v, want the named database and schema's table", got)
	}
}

func TestCompleteColumnsOfAQualifiedTable(t *testing.T) {
	e := New(sqllex.PostgreSQL, testCatalog(), nil)
	res := complete(t, e, "select orders.| from orders", 0)
	if rankOf(res, "id") < 0 || rankOf(res, "order_date") < 0 {
		t.Fatalf("candidates %v, want the table's columns", labels(res))
	}
	c, _ := find(res, "order_date")
	if c.Kind != source.CompletionColumn || c.Detail != "date" {
		t.Errorf("order_date is %v detail %q, want a column detailed date", c.Kind, c.Detail)
	}
	// Qualified by schema and table as well.
	res = complete(t, e, "select public.orders.| from public.orders", 0)
	if rankOf(res, "id") < 0 {
		t.Errorf("candidates %v, want the columns of public.orders", labels(res))
	}
}

func TestCompleteOffersKeywordsAndTheirCase(t *testing.T) {
	e := New(sqllex.PostgreSQL, testCatalog(), nil)
	res := complete(t, e, "sel|", 0)
	c, ok := find(res, "SELECT")
	if !ok {
		t.Fatalf("candidates %v, want SELECT", labels(res))
	}
	if c.Kind != source.CompletionKeyword {
		t.Errorf("SELECT is %v, want a keyword", c.Kind)
	}
	if c.Insert != "select" {
		t.Errorf("insert %q after typing lower case, want %q", c.Insert, "select")
	}
	if res.Candidates[0].Label != "SELECT" {
		t.Errorf("candidates %v, want the word typed first", labels(res))
	}
	// Typed in upper case, it is written in upper case.
	if c, _ := find(complete(t, e, "SEL|", 0), "SELECT"); c.Insert != "SELECT" {
		t.Errorf("insert %q after typing upper case, want %q", c.Insert, "SELECT")
	}
	// With nothing typed, the case is the keyword's own.
	if c, _ := find(complete(t, e, "|", 2000), "SELECT"); c.Insert != "SELECT" {
		t.Errorf("insert %q with nothing typed, want %q", c.Insert, "SELECT")
	}
}

func TestCompleteOffersAWordOnce(t *testing.T) {
	// SET is a keyword and a type; EXISTS a keyword and a function.
	e := New(sqllex.PostgreSQL, testCatalog(), nil)
	res := complete(t, e, "select * from orders where |", 2000)
	for _, want := range []string{"SET", "EXISTS"} {
		n := 0
		for _, c := range res.Candidates {
			if c.Label == want {
				n++
			}
		}
		if n != 1 {
			t.Errorf("%s offered %d times, want once", want, n)
		}
	}
}

func TestCompleteOffersFunctions(t *testing.T) {
	e := New(sqllex.PostgreSQL, testCatalog(), nil)
	res := complete(t, e, "select coun|", 0)
	c, ok := find(res, "COUNT")
	if !ok {
		t.Fatalf("candidates %v, want COUNT", labels(res))
	}
	if c.Kind != source.CompletionFunction {
		t.Errorf("COUNT is %v, want a function", c.Kind)
	}
	// A schema's own routine is offered above the dialect's words.
	res = complete(t, e, "select order_t|", 0)
	if got := labels(res); len(got) == 0 || got[0] != "order_total" {
		t.Errorf("candidates %v, want the schema's routine first", got)
	}
	if c, _ := find(res, "order_total"); c.Kind != source.CompletionFunction {
		t.Errorf("order_total is %v, want a function", c.Kind)
	}
}

func TestCompleteOffersDatabases(t *testing.T) {
	e := New(sqllex.PostgreSQL, testCatalog(), nil)
	res := complete(t, e, "use ware|", 0)
	if got := labels(res); len(got) != 1 || got[0] != "warehouse" {
		t.Fatalf("candidates %v, want the database", got)
	}
	if c, _ := find(res, "warehouse"); c.Kind != source.CompletionDatabase {
		t.Errorf("warehouse is %v, want a database", c.Kind)
	}
}

func TestCompleteQuotesWhatMustBeQuoted(t *testing.T) {
	e := New(sqllex.PostgreSQL, testCatalog(), nil)
	res := complete(t, e, "select orders.cu| from orders", 0)
	c, ok := find(res, "Customer Id")
	if !ok {
		t.Fatalf("candidates %v, want the column", labels(res))
	}
	if c.Insert != `"Customer Id"` {
		t.Errorf("insert %q, want it quoted", c.Insert)
	}
	// A plain name is written plainly.
	plain := complete(t, e, "select orders.i| from orders", 0)
	if c, _ := find(plain, "id"); c.Insert != "id" {
		t.Errorf("insert %q, want %q unquoted", c.Insert, "id")
	}
	// The source's own quoter is used where there is one.
	e = New(sqllex.PostgreSQL, testCatalog(), func(s string) string { return "«" + s + "»" })
	res = complete(t, e, "select orders.cu| from orders", 0)
	if c, _ := find(res, "Customer Id"); c.Insert != "«Customer Id»" {
		t.Errorf("insert %q, want the source's quoting", c.Insert)
	}
}

func TestCompleteQuotesANameTheServerWouldReadAsAWord(t *testing.T) {
	cat := NewStatic()
	cat.AddObject("shop", "public", Object{Name: "order"})
	e := New(sqllex.PostgreSQL, cat, nil)
	res := complete(t, e, "select * from ord|", 0)
	if c, _ := find(res, "order"); c.Insert != `"order"` {
		t.Errorf("insert %q, want a keyword name quoted", c.Insert)
	}
}

func TestCompleteKeepsAnOpeningQuote(t *testing.T) {
	e := New(sqllex.PostgreSQL, testCatalog(), nil)
	res := complete(t, e, `select * from "ord|`, 0)
	c, ok := find(res, "orders")
	if !ok {
		t.Fatalf("candidates %v, want the table", labels(res))
	}
	if c.Insert != `"orders"` {
		t.Errorf("insert %q, want it quoted as it was begun", c.Insert)
	}
	if res.Start != 14 {
		t.Errorf("replaces from %d, want 14 (the opening quote)", res.Start)
	}
}

func TestCompleteReportsWhatItReplacesInRunes(t *testing.T) {
	e := New(sqllex.PostgreSQL, testCatalog(), nil)
	// The comment holds six two-byte runes. A byte offset taken for a rune
	// one lands six characters back, in another word altogether.
	const text = "-- naïve ünicode wörds ärë höre\nselect * from ord"
	res := complete(t, e, text+"|", 0)
	if got := string([]rune(text)[res.Start:res.End]); got != "ord" {
		t.Errorf("replaces %q, want %q", got, "ord")
	}
	if _, ok := find(res, "orders"); !ok {
		t.Errorf("candidates %v, want the table", labels(res))
	}
}

func TestCompleteRanksColumnsAboveKeywords(t *testing.T) {
	e := New(sqllex.PostgreSQL, testCatalog(), nil)
	res := complete(t, e, "select * from orders where orders.o|", 0)
	if got := labels(res); len(got) == 0 || got[0] != "order_date" {
		t.Errorf("candidates %v, want the column first", got)
	}
}

func TestCompleteRanksANameBeingTypedFromItsStartFirst(t *testing.T) {
	// "is_deleted" scores better letter by letter — both letters of "id"
	// start a word in it — but "id" is not how it begins, and someone
	// typing "id" means the column of that name.
	cat := NewStatic()
	cat.AddObject("shop", "public", Object{Name: "orders"},
		Column{Name: "is_deleted", Type: "boolean"},
		Column{Name: "id", Type: "integer"},
	)
	e := New(sqllex.PostgreSQL, cat, nil)
	got := labels(complete(t, e, "select orders.id| from orders", 0))
	if len(got) != 2 || got[0] != "id" {
		t.Errorf("candidates %v, want the name begun that way first", got)
	}
}

func TestCompleteOrdersEqualCandidatesByName(t *testing.T) {
	// With nothing typed nothing can be scored, so the order is the names',
	// which is at least the order it was in a moment ago. A map's own order
	// would shuffle the popup between keystrokes.
	e := New(sqllex.PostgreSQL, testCatalog(), nil)
	got := labels(complete(t, e, "select * from |", 0))
	if len(got) < 2 || got[0] != "order_items" || got[1] != "orders" {
		t.Errorf("candidates %v, want the tables in name order first", got)
	}
}

func TestCompleteOffersNothingInsideAComment(t *testing.T) {
	e := New(sqllex.PostgreSQL, testCatalog(), nil)
	res := complete(t, e, "select * from orders -- ord|", 0)
	if len(res.Candidates) != 0 {
		t.Errorf("candidates %v, want none in a comment", labels(res))
	}
	if res.Start != res.End {
		t.Errorf("replaces [%d,%d), want nothing", res.Start, res.End)
	}
}

func TestCompleteLimits(t *testing.T) {
	e := New(sqllex.PostgreSQL, testCatalog(), nil)
	if got := len(complete(t, e, "|", 0).Candidates); got != DefaultLimit {
		t.Errorf("%d candidates with no limit asked, want %d", got, DefaultLimit)
	}
	if got := len(complete(t, e, "|", 3).Candidates); got != 3 {
		t.Errorf("%d candidates, want 3", got)
	}
}

func TestCompleteWithoutACatalogOffersTheDialectsWords(t *testing.T) {
	e := New(nil, nil, nil)
	res := complete(t, e, "sel|", 0)
	if _, ok := find(res, "SELECT"); !ok {
		t.Errorf("candidates %v, want SELECT with no catalog", labels(res))
	}
	if len(complete(t, e, "select * from |", 0).Candidates) != 0 {
		t.Error("want no table with no catalog")
	}
}

func TestCompleteUsesTheDialectsQuoteAndWords(t *testing.T) {
	cat := NewStatic()
	cat.AddObject("shop", "public", Object{Name: "Orders"})
	e := New(sqllex.MySQL, cat, nil)
	res := complete(t, e, "select * from Ord|", 0)
	if c, _ := find(res, "Orders"); c.Insert != "`Orders`" {
		t.Errorf("insert %q, want MySQL's quoting", c.Insert)
	}
	// A MySQL word PostgreSQL does not have.
	if _, ok := find(complete(t, e, "auto_inc|", 0), "AUTO_INCREMENT"); !ok {
		t.Error("want MySQL's own keywords")
	}
}

func TestCompleteIsStableAcrossRuns(t *testing.T) {
	// The dialect's words come out of a map, whose order changes per run.
	e := New(sqllex.PostgreSQL, testCatalog(), nil)
	first := labels(complete(t, e, "select * from orders where |", 0))
	for i := 0; i < 20; i++ {
		got := labels(complete(t, e, "select * from orders where |", 0))
		if len(got) != len(first) {
			t.Fatalf("run %d: %d candidates, want %d", i, len(got), len(first))
		}
		for j := range got {
			if got[j] != first[j] {
				t.Fatalf("run %d: candidate %d is %q, want %q", i, j, got[j], first[j])
			}
		}
	}
}

func TestCompleteColumnsOfAnAliasedTable(t *testing.T) {
	e := New(sqllex.PostgreSQL, testCatalog(), nil)
	for _, text := range []string{
		"select o.| from orders o",
		"select o.| from orders as o",
		"select * from orders o where o.|",
		"update orders o set o.| = 1",
	} {
		res := complete(t, e, text, 0)
		if rankOf(res, "order_date") < 0 || rankOf(res, "id") < 0 {
			t.Errorf("%q: candidates %v, want the aliased table's columns", text, labels(res))
		}
		if rankOf(res, "qty") >= 0 {
			t.Errorf("%q: candidates %v include another table's column", text, labels(res))
		}
	}
	// The name is matched whatever its case, as the server matches it.
	if res := complete(t, e, "select O.| from orders o", 0); rankOf(res, "order_date") < 0 {
		t.Errorf("candidates %v, want the table's columns", labels(res))
	}
	// An alias hides a table of the same name: o is orders here, not the
	// table named o.
	cat := testCatalog()
	cat.AddObject("shop", "public", Object{Name: "o"}, Column{Name: "not_ordered", Type: "text"})
	res := complete(t, New(sqllex.PostgreSQL, cat, nil), "select o.| from orders o", 0)
	if rankOf(res, "not_ordered") >= 0 {
		t.Errorf("candidates %v, want the alias to win over a table of its name", labels(res))
	}
}

func TestCompleteColumnsOfATableNamedWhereItIs(t *testing.T) {
	e := New(sqllex.PostgreSQL, testCatalog(), nil)
	// The statement says the schema, not the session.
	res := complete(t, e, "select c.| from audit.changes c", 0)
	if rankOf(res, "at") < 0 {
		t.Errorf("candidates %v, want the columns of audit.changes", labels(res))
	}
	res = complete(t, e, "select p.| from warehouse.public.pallets p", 0)
	if len(res.Candidates) != 0 {
		t.Errorf("candidates %v, want none: that table has no columns recorded", labels(res))
	}
}

func TestCompleteColumnsOfEveryTableInScope(t *testing.T) {
	e := New(sqllex.PostgreSQL, testCatalog(), nil)
	res := complete(t, e, "select | from orders o join order_items i on i.order_id = o.id", 0)
	for _, want := range []string{"order_date", "qty"} {
		if rankOf(res, want) < 0 {
			t.Errorf("candidates %v, want %s among them", labels(res), want)
		}
	}
	// Which table a column comes from is said, since two are read.
	if c, _ := find(res, "qty"); c.Detail != "i · integer" {
		t.Errorf("qty is detailed %q, want %q", c.Detail, "i · integer")
	}
	// The names the statement reads its tables under are offered too.
	c, ok := find(res, "i")
	if !ok || c.Kind != source.CompletionAlias {
		t.Errorf("candidates %v, want the alias i", labels(res))
	}
	if c.Detail != "order_items" {
		t.Errorf("the alias i is detailed %q, want the table it stands for", c.Detail)
	}
	// A column both tables have cannot be written bare.
	if c, _ := find(res, "id"); c.Insert != "o.id" {
		t.Errorf("id is written %q, want it qualified: both tables have one", c.Insert)
	}
	// One only one table has is written as it is.
	if c, _ := find(res, "qty"); c.Insert != "qty" {
		t.Errorf("qty is written %q, want it bare", c.Insert)
	}
}

func TestCompleteColumnsOfOneTableAreNotSaidTwice(t *testing.T) {
	e := New(sqllex.PostgreSQL, testCatalog(), nil)
	res := complete(t, e, "select | from orders", 0)
	if c, _ := find(res, "order_date"); c.Detail != "date" {
		t.Errorf("order_date is detailed %q, want its type alone", c.Detail)
	}
	if c, _ := find(res, "id"); c.Insert != "id" {
		t.Errorf("id is written %q, want it bare where only one table has one", c.Insert)
	}
	// The table's own name stands for it where there is no alias.
	if c, ok := find(res, "orders"); !ok || c.Kind != source.CompletionAlias {
		t.Errorf("candidates %v, want the table's own name to qualify with", labels(res))
	}
}

func TestCompleteColumnsWhereAColumnIsTypedBeforeItsTable(t *testing.T) {
	// The FROM clause comes after the cursor, and is still what says where
	// the column is from.
	e := New(sqllex.PostgreSQL, testCatalog(), nil)
	res := complete(t, e, "select order_d| from orders", 0)
	if got := labels(res); len(got) == 0 || got[0] != "order_date" {
		t.Errorf("candidates %v, want the column first", got)
	}
}

func TestCompleteColumnsInsideASubquery(t *testing.T) {
	e := New(sqllex.PostgreSQL, testCatalog(), nil)
	// Inside, both the subquery's table and the statement's own are read.
	res := complete(t, e, "select * from orders o where o.id in (select | from order_items i)", 0)
	if rankOf(res, "qty") < 0 || rankOf(res, "order_date") < 0 {
		t.Errorf("candidates %v, want both tables' columns inside a subquery", labels(res))
	}
	// Outside, the subquery's table is not in scope.
	res = complete(t, e, "select | from orders o where o.id in (select order_id from order_items i)", 0)
	if rankOf(res, "qty") >= 0 {
		t.Errorf("candidates %v, want no column of a table read only inside a subquery", labels(res))
	}
	if rankOf(res, "order_date") < 0 {
		t.Errorf("candidates %v, want the statement's own columns", labels(res))
	}
}

func TestCompleteDerivedTableOffersItsNameAndNoColumns(t *testing.T) {
	e := New(sqllex.PostgreSQL, testCatalog(), nil)
	res := complete(t, e, "select d.| from (select id from orders) d", 0)
	if len(res.Candidates) != 0 {
		t.Errorf("candidates %v, want none: a subquery's columns are not the catalog's", labels(res))
	}
	// A table of that name is not read in its place.
	cat := testCatalog()
	cat.AddObject("shop", "public", Object{Name: "d"}, Column{Name: "wrong", Type: "text"})
	res = complete(t, New(sqllex.PostgreSQL, cat, nil), "select d.| from (select id from orders) d", 0)
	if rankOf(res, "wrong") >= 0 {
		t.Errorf("candidates %v, want no table read in a derived table's place", labels(res))
	}
	// Its name is still offered, so it can be typed.
	res = complete(t, e, "select | from (select id from orders) d", 0)
	if c, ok := find(res, "d"); !ok || c.Kind != source.CompletionAlias {
		t.Errorf("candidates %v, want the derived table's name", labels(res))
	}
}

func TestCompleteColumnsOfTheStatementTheCursorIsIn(t *testing.T) {
	e := New(sqllex.PostgreSQL, testCatalog(), nil)
	res := complete(t, e, "select at from audit.changes;\nselect | from orders", 0)
	if rankOf(res, "at") >= 0 {
		t.Errorf("candidates %v, want no column of the statement before", labels(res))
	}
	if rankOf(res, "order_date") < 0 {
		t.Errorf("candidates %v, want this statement's columns", labels(res))
	}
	// And nothing from the statement after it either.
	res = complete(t, e, "select | from orders;\nselect at from audit.changes", 0)
	if rankOf(res, "at") >= 0 {
		t.Errorf("candidates %v, want no column of the statement after", labels(res))
	}
}

func TestCompleteColumnsWhereRowsAreWritten(t *testing.T) {
	e := New(sqllex.PostgreSQL, testCatalog(), nil)
	if res := complete(t, e, "insert into orders (|", 0); rankOf(res, "order_date") < 0 {
		t.Errorf("candidates %v, want the table's columns in an insert's list", labels(res))
	}
	if res := complete(t, e, "update orders set |", 0); rankOf(res, "order_date") < 0 {
		t.Errorf("candidates %v, want the table's columns in an update", labels(res))
	}
}
