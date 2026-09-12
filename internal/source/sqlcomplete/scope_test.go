package sqlcomplete

import (
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

// scopeAt reads the tables in scope at a cursor marked with a '|'.
func scopeAt(t *testing.T, text string) []Relation {
	t.Helper()
	i := strings.IndexByte(text, '|')
	if i < 0 {
		t.Fatalf("no cursor in %q", text)
	}
	return Scope(sqllex.PostgreSQL, text[:i]+text[i+1:], i)
}

// names renders relations as "name=database.schema.table" for comparison.
func names(rels []Relation) string {
	var b strings.Builder
	for i, r := range rels {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(r.Name + "=" + r.Database + "." + r.Schema + "." + r.Table)
	}
	return b.String()
}

func TestScopeReadsTheTablesAStatementNames(t *testing.T) {
	cases := []struct{ text, want string }{
		{"select | from orders", "orders=..orders"},
		{"select | from orders o", "o=..orders"},
		{"select | from orders as o", "o=..orders"},
		{"select | from public.orders o", "o=.public.orders"},
		{"select | from shop.public.orders o", "o=shop.public.orders"},
		{"select | from orders o, order_items i", "o=..orders i=..order_items"},
		{"select | from orders o join order_items i on i.order_id = o.id", "o=..orders i=..order_items"},
		{"select | from orders o left join order_items on true", "o=..orders order_items=..order_items"},
		{"update orders o set x = 1 where |", "o=..orders"},
		{"insert into orders (|", "orders=..orders"},
		{`select | from "My Table" "My O"`, "My O=..My Table"},
		{"select | from orders", "orders=..orders"},
		{"select 1 where |", ""},
	}
	for _, tc := range cases {
		if got := names(scopeAt(t, tc.text)); got != tc.want {
			t.Errorf("%q: scope %q, want %q", tc.text, got, tc.want)
		}
	}
}

func TestScopeKeepsAClauseWordOutOfAnAlias(t *testing.T) {
	// WHERE, JOIN and ORDER are what follows the table, not names for it.
	for _, text := range []string{
		"select * from orders where |",
		"select * from orders order by |",
		"select * from orders group by |",
		"select * from orders join order_items on |",
	} {
		rels := scopeAt(t, text)
		if len(rels) == 0 || rels[0].Name != "orders" {
			t.Errorf("%q: scope %q, want the table under its own name", text, names(rels))
		}
	}
}

func TestScopeOfASubquery(t *testing.T) {
	// Inside the brackets, both are read.
	got := names(scopeAt(t, "select * from orders o where id in (select | from order_items i)"))
	if got != "o=..orders i=..order_items" {
		t.Errorf("scope %q inside a subquery, want both tables", got)
	}
	// Outside them, only the statement's own.
	got = names(scopeAt(t, "select | from orders o where id in (select x from order_items i)"))
	if got != "o=..orders" {
		t.Errorf("scope %q outside a subquery, want the outer table alone", got)
	}
	// Two brackets deep.
	got = names(scopeAt(t, "select * from a where x in (select y from b where z in (select | from c))"))
	if got != "a=..a b=..b c=..c" {
		t.Errorf("scope %q two brackets deep, want all three", got)
	}
	// A sibling subquery is not in scope.
	// In the order the statement names them, which puts a subquery's table
	// before the outer FROM clause that follows it.
	got = names(scopeAt(t, "select (select 1 from b) x, (select | from c) y from a"))
	if got != "c=..c a=..a" {
		t.Errorf("scope %q, want this subquery's table and the statement's", got)
	}
}

func TestScopeOfADerivedTable(t *testing.T) {
	got := names(scopeAt(t, "select | from (select id from orders) d"))
	if got != "d=.." {
		t.Errorf("scope %q, want the derived table under its name with no table", got)
	}
	got = names(scopeAt(t, "select | from (select id from orders) d join order_items i on true"))
	if got != "d=.. i=..order_items" {
		t.Errorf("scope %q, want the derived table and the joined one", got)
	}
}

func TestScopeIsTheStatementTheCursorIsIn(t *testing.T) {
	got := names(scopeAt(t, "select 1 from a; select | from b"))
	if got != "b=..b" {
		t.Errorf("scope %q, want this statement's table alone", got)
	}
	got = names(scopeAt(t, "select | from b; select 1 from a"))
	if got != "b=..b" {
		t.Errorf("scope %q, want no table of the statement after", got)
	}
	// A semicolon inside brackets does not end a statement.
	got = names(scopeAt(t, "select 'a;b' as x, | from orders"))
	if got != "orders=..orders" {
		t.Errorf("scope %q, want the table: the semicolon is in a string", got)
	}
}
