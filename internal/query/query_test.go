package query

import (
	"errors"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// A design, and the SQL it becomes. Everything here is decided without a
// connection, which is the point of the package being data and a function.

// pgLike is a dialect with PostgreSQL's quoting and a LIMIT clause. Enough of
// one to render a design, and no more: what a real dialect does with a design
// is the same thing, and the drivers' own tests hold their quoting.
type pgLike struct{ mutating bool }

func (pgLike) QuoteIdentifier(n string) string {
	return `"` + strings.ReplaceAll(n, `"`, `""`) + `"`
}

func (d pgLike) QualifyRef(r model.ObjectRef) string {
	parts := make([]string, 0, 2)
	if len(r.Path) >= 3 {
		parts = append(parts, d.QuoteIdentifier(r.Path[len(r.Path)-2]))
	}
	return strings.Join(append(parts, d.QuoteIdentifier(r.Name())), ".")
}

func (pgLike) Placeholder(i int) string { return "$1" }

func (d pgLike) Classify(string) source.Access {
	if d.mutating {
		return source.AccessWrite
	}
	return source.AccessRead
}

func (pgLike) SplitScript(string) []source.ScriptStatement { return nil }

func (d pgLike) BuildBrowse(ref model.ObjectRef, opt source.BrowseOptions) (source.Statement, error) {
	return source.Statement{SQL: "SELECT * FROM " + d.QualifyRef(ref) + " LIMIT $1",
		Args: []any{opt.Limit}}, nil
}

func ref(schema, name string) model.ObjectRef {
	return model.NewRef(model.KindTable, "db", schema, name)
}

// people and orders, with a foreign key between them.
func schema() *model.Database {
	return &model.Database{Name: "db", Schemas: []model.Schema{{
		Name: "public",
		Tables: []model.Table{
			{Name: "people", Columns: []model.Column{{Name: "id"}, {Name: "name"}}},
			{Name: "orders", Columns: []model.Column{{Name: "id"}, {Name: "person_id"}, {Name: "total"}},
				ForeignKeys: []model.ForeignKey{{Name: "orders_person", Columns: []string{"person_id"},
					RefSchema: "public", RefTable: "people", RefColumns: []string{"id"}}}},
			{Name: "notes", Columns: []model.Column{{Name: "id"}, {Name: "body"}}},
		},
	}}}
}

func render(t *testing.T, d *Design) string {
	t.Helper()
	got, err := Render(d, pgLike{})
	if err != nil {
		t.Fatalf("rendering: %v", err)
	}
	return got
}

// One table and nothing chosen is every column of it: a canvas with a table on
// it and nothing picked is a query for the table.
func TestOneTableAndNothingChosen(t *testing.T) {
	d := &Design{}
	d.Add(ref("public", "people"))
	if got := render(t, d); got != `SELECT "people".*`+"\n"+`FROM "public"."people" "people"` {
		t.Errorf("it reads\n%s", got)
	}
}

// A table is always named, and the columns always qualified by that name: a
// bare column would change meaning the moment a second table arrived.
func TestEveryColumnIsQualified(t *testing.T) {
	d := &Design{}
	p := d.Add(ref("public", "people"))
	d.Outputs = []Output{{Column: Column{Table: p, Name: "name"}}}
	got := render(t, d)
	if !strings.Contains(got, `SELECT "people"."name"`) {
		t.Errorf("it reads\n%s", got)
	}
}

// A name with a quote in it is a name, not a syntax error.
func TestAnAwkwardNameIsStillAName(t *testing.T) {
	d := &Design{}
	at := d.Add(model.NewRef(model.KindTable, "db", "public", `odd"table`))
	d.Outputs = []Output{{Column: Column{Table: at, Name: `odd"column`}}}
	got := render(t, d)
	if !strings.Contains(got, `"odd""table"`) || !strings.Contains(got, `"odd""column"`) {
		t.Errorf("it reads\n%s", got)
	}
}

// The same table twice is two tables, told apart by their names: a self-join is
// the commonest thing a designer is used for.
func TestTheSameTableTwiceIsTwoTables(t *testing.T) {
	d := &Design{}
	a := d.Add(ref("public", "people"))
	b := d.Add(ref("public", "people"))
	if d.Tables[a].Alias != "people" || d.Tables[b].Alias != "people2" {
		t.Fatalf("they are called %q and %q", d.Tables[a].Alias, d.Tables[b].Alias)
	}
	d.Joins = []Join{{Kind: JoinInner, Left: a, Right: b, On: []Pair{{Left: "id", Right: "id"}}}}
	got := render(t, d)
	want := `INNER JOIN "public"."people" "people2" ON "people"."id" = "people2"."id"`
	if !strings.Contains(got, want) {
		t.Errorf("it reads\n%s\nwhich lacks\n%s", got, want)
	}
}

func TestANameThatIsTakenGetsANumber(t *testing.T) {
	for name, c := range map[string]struct {
		taken []string
		want  string
	}{
		"free":        {nil, "people"},
		"taken once":  {[]string{"people"}, "people2"},
		"taken twice": {[]string{"people", "people2"}, "people3"},
		"a gap":       {[]string{"people", "people3"}, "people2"},
		"nameless":    {[]string{"t"}, "t2"},
	} {
		t.Run(name, func(t *testing.T) {
			asked := "people"
			if name == "nameless" {
				asked = ""
			}
			if got := AliasFor(c.taken, asked); got != c.want {
				t.Errorf("it is called %q, want %q", got, c.want)
			}
		})
	}
}

// A join is written after the table it joins from is named, whichever order
// the joins were made in: SQL will not take a join to a table it has not met.
func TestJoinsAreWrittenInTheOrderTheTablesAreReached(t *testing.T) {
	d := &Design{}
	people := d.Add(ref("public", "people"))
	orders := d.Add(ref("public", "orders"))
	notes := d.Add(ref("public", "notes"))
	// Made in the wrong order on purpose: notes hangs off orders, and orders
	// off people, but the notes join was drawn first.
	d.Joins = []Join{
		{Kind: JoinLeft, Left: orders, Right: notes, On: []Pair{{Left: "id", Right: "id"}}},
		{Kind: JoinInner, Left: people, Right: orders, On: []Pair{{Left: "id", Right: "person_id"}}},
	}
	got := render(t, d)
	if strings.Index(got, `"orders"`) > strings.Index(got, `"notes"`) {
		t.Errorf("notes is named before orders:\n%s", got)
	}
	lines := strings.Split(got, "\n")
	if len(lines) != 4 {
		t.Fatalf("it reads\n%s", got)
	}
	if !strings.HasPrefix(lines[2], "INNER JOIN") || !strings.HasPrefix(lines[3], "LEFT JOIN") {
		t.Errorf("it reads\n%s", got)
	}
}

// A join reaching backwards — drawn from the new table to one already on the
// canvas — is written the other way round, so the SQL still names the reached
// table first.
func TestAJoinDrawnBackwardsIsWrittenForwards(t *testing.T) {
	d := &Design{}
	people := d.Add(ref("public", "people"))
	orders := d.Add(ref("public", "orders"))
	d.Joins = []Join{{Kind: JoinInner, Left: orders, Right: people,
		On: []Pair{{Left: "person_id", Right: "id"}}}}
	got := render(t, d)
	want := `INNER JOIN "public"."orders" "orders" ON "people"."id" = "orders"."person_id"`
	if !strings.Contains(got, want) {
		t.Errorf("it reads\n%s\nwhich lacks\n%s", got, want)
	}
}

// A second join between two tables already reached is a further condition, and
// is written rather than dropped.
func TestASecondJoinBetweenTheSameTablesIsKept(t *testing.T) {
	d := &Design{}
	people := d.Add(ref("public", "people"))
	orders := d.Add(ref("public", "orders"))
	d.Joins = []Join{
		{Kind: JoinInner, Left: people, Right: orders, On: []Pair{{Left: "id", Right: "person_id"}}},
		{Kind: JoinInner, Left: people, Right: orders, On: []Pair{{Left: "name", Right: "total"}}},
	}
	got := render(t, d)
	if strings.Count(got, "INNER JOIN") != 2 {
		t.Errorf("it reads\n%s", got)
	}
}

// A cross join takes no columns, and every other kind needs some.
func TestWhatEachKindOfJoinNeeds(t *testing.T) {
	base := func() (*Design, int, int) {
		d := &Design{}
		return d, d.Add(ref("public", "people")), d.Add(ref("public", "orders"))
	}
	d, a, b := base()
	d.Joins = []Join{{Kind: JoinCross, Left: a, Right: b}}
	if got := render(t, d); !strings.Contains(got, `CROSS JOIN "public"."orders" "orders"`) {
		t.Errorf("a cross join reads\n%s", got)
	}
	if strings.Contains(render(t, d), " ON ") {
		t.Error("a cross join was given an ON")
	}

	for name, c := range map[string]struct {
		join Join
		want error
	}{
		"a cross join with columns": {Join{Kind: JoinCross, Left: 0, Right: 1,
			On: []Pair{{Left: "id", Right: "id"}}}, ErrCrossOn},
		"an inner join with none":   {Join{Kind: JoinInner, Left: 0, Right: 1}, ErrJoinEmpty},
		"a join to itself":          {Join{Kind: JoinInner, Left: 0, Right: 0, On: []Pair{{Left: "id", Right: "id"}}}, ErrJoinSelf},
		"a kind nobody has":         {Join{Kind: "SIDEWAYS JOIN", Left: 0, Right: 1, On: []Pair{{Left: "id", Right: "id"}}}, ErrUnknownJoin},
		"a table not on the canvas": {Join{Kind: JoinInner, Left: 0, Right: 9, On: []Pair{{Left: "id", Right: "id"}}}, ErrBadTable},
		"a pair with no column":     {Join{Kind: JoinInner, Left: 0, Right: 1, On: []Pair{{Left: "id"}}}, ErrNoColumn},
	} {
		t.Run(name, func(t *testing.T) {
			d, _, _ := base()
			d.Joins = []Join{c.join}
			if _, err := Render(d, pgLike{}); !errors.Is(err, c.want) {
				t.Errorf("it said %v, want %v", err, c.want)
			}
		})
	}
}

// A table nothing joins is a cross product with everything before it, which is
// never what somebody designed: it is refused, naming the table.
func TestATableNothingReachesIsRefused(t *testing.T) {
	d := &Design{}
	d.Add(ref("public", "people"))
	d.Add(ref("public", "notes"))
	_, err := Render(d, pgLike{})
	if !errors.Is(err, ErrUnreached) {
		t.Fatalf("it said %v", err)
	}
	if !strings.Contains(err.Error(), "notes") {
		t.Errorf("it says %q, which does not name the table", err)
	}
}

func TestTheRestOfASelect(t *testing.T) {
	d := &Design{Distinct: true, Limit: 50}
	people := d.Add(ref("public", "people"))
	orders := d.Add(ref("public", "orders"))
	d.Joins = []Join{{Kind: JoinInner, Left: people, Right: orders,
		On: []Pair{{Left: "id", Right: "person_id"}}}}
	name := Column{Table: people, Name: "name"}
	total := Column{Table: orders, Name: "total"}
	d.Outputs = []Output{
		{Column: name},
		{Column: total, Aggregate: AggregateSum, Alias: "spent"},
	}
	d.Where = []Condition{{Column: total, Op: source.OpGreater, Value: "10"}}
	d.Group = []Column{name}
	d.Having = []Condition{{Column: total, Op: source.OpGreater, Aggregate: AggregateSum, Value: "100"}}
	d.Order = []Sort{{Column: total, Aggregate: AggregateSum, Descending: true}}
	got := render(t, d)
	want := `SELECT DISTINCT "people"."name", SUM("orders"."total") AS "spent"
FROM "public"."people" "people"
INNER JOIN "public"."orders" "orders" ON "people"."id" = "orders"."person_id"
WHERE "orders"."total" > 10
GROUP BY "people"."name"
HAVING SUM("orders"."total") > 100
ORDER BY SUM("orders"."total") DESC
LIMIT 50`
	if got != want {
		t.Errorf("it reads\n%s\nwant\n%s", got, want)
	}
}

func TestEveryKindOfCondition(t *testing.T) {
	for name, c := range map[string]struct {
		cond Condition
		want string
	}{
		"equal":                 {Condition{Op: source.OpEqual, Value: "'x'"}, `"people"."name" = 'x'`},
		"not equal":             {Condition{Op: source.OpNotEqual, Value: "'x'"}, `"people"."name" <> 'x'`},
		"less":                  {Condition{Op: source.OpLess, Value: "3"}, `"people"."name" < 3`},
		"at most":               {Condition{Op: source.OpLessEqual, Value: "3"}, `"people"."name" <= 3`},
		"more":                  {Condition{Op: source.OpGreater, Value: "3"}, `"people"."name" > 3`},
		"at least":              {Condition{Op: source.OpGreaterEqual, Value: "3"}, `"people"."name" >= 3`},
		"like":                  {Condition{Op: source.OpLike, Value: "'a%'"}, `"people"."name" LIKE 'a%'`},
		"not like":              {Condition{Op: source.OpNotLike, Value: "'a%'"}, `"people"."name" NOT LIKE 'a%'`},
		"is null":               {Condition{Op: source.OpIsNull}, `"people"."name" IS NULL`},
		"is not null":           {Condition{Op: source.OpIsNotNull}, `"people"."name" IS NOT NULL`},
		"between":               {Condition{Op: source.OpBetween, Value: "1", Value2: "9"}, `"people"."name" BETWEEN 1 AND 9`},
		"in":                    {Condition{Op: source.OpIn, Value: "1, 2"}, `"people"."name" IN (1, 2)`},
		"in, bracketed already": {Condition{Op: source.OpIn, Value: "(1, 2)"}, `"people"."name" IN (1, 2)`},
		"not in":                {Condition{Op: source.OpNotIn, Value: "1"}, `"people"."name" NOT IN (1)`},
		// An expression, which is half of what somebody designing a query
		// means by a value, and is why the value is their text.
		"an expression": {Condition{Op: source.OpGreater, Value: "CURRENT_DATE - 7"},
			`"people"."name" > CURRENT_DATE - 7`},
	} {
		t.Run(name, func(t *testing.T) {
			d := &Design{}
			at := d.Add(ref("public", "people"))
			c.cond.Column = Column{Table: at, Name: "name"}
			d.Where = []Condition{c.cond}
			got := render(t, d)
			if !strings.Contains(got, "WHERE "+c.want) {
				t.Errorf("it reads\n%s\nwhich lacks\nWHERE %s", got, c.want)
			}
		})
	}
}

func TestConditionsAreJoinedByAnd(t *testing.T) {
	d := &Design{}
	at := d.Add(ref("public", "people"))
	d.Where = []Condition{
		{Column: Column{Table: at, Name: "id"}, Op: source.OpGreater, Value: "1"},
		{Column: Column{Table: at, Name: "name"}, Op: source.OpIsNotNull},
	}
	if got := render(t, d); !strings.Contains(got, `WHERE "people"."id" > 1 AND "people"."name" IS NOT NULL`) {
		t.Errorf("it reads\n%s", got)
	}
}

// A condition on groups may use an aggregate, and a condition on rows may not:
// that is not legal SQL anywhere, and the server's answer would be about a
// statement the person did not write.
func TestAnAggregateBelongsInAConditionOnGroups(t *testing.T) {
	d := &Design{}
	at := d.Add(ref("public", "people"))
	id := Column{Table: at, Name: "id"}
	d.Outputs = []Output{{Column: id, Aggregate: AggregateCount}}
	d.Having = []Condition{{Column: id, Op: source.OpGreater, Aggregate: AggregateCount, Value: "2"}}
	if got := render(t, d); !strings.Contains(got, `HAVING COUNT("people"."id") > 2`) {
		t.Errorf("it reads\n%s", got)
	}
	d.Having = nil
	d.Where = []Condition{{Column: id, Op: source.OpGreater, Aggregate: AggregateCount, Value: "2"}}
	_, err := Render(d, pgLike{})
	if !errors.Is(err, ErrAggregateInWhere) {
		t.Fatalf("it said %v", err)
	}
	if !strings.Contains(err.Error(), "COUNT(people.id)") {
		t.Errorf("it says %q, which does not name what is wrong", err)
	}
	// An aggregate nobody has is refused wherever it is.
	d.Where = []Condition{{Column: id, Op: source.OpGreater, Aggregate: Aggregate("MEDIAN"), Value: "2"}}
	if _, err := Render(d, pgLike{}); !errors.Is(err, ErrUnknownAgg) {
		t.Errorf("it said %v", err)
	}
}

func TestAConditionThatCannotBeWrittenIsRefused(t *testing.T) {
	for name, c := range map[string]struct {
		cond Condition
		want error
	}{
		"an operator nobody has":  {Condition{Op: source.FilterOp("~~"), Value: "1"}, ErrUnknownOp},
		"a value that is missing": {Condition{Op: source.OpEqual}, ErrNoValue},
		"only spaces":             {Condition{Op: source.OpEqual, Value: "   "}, ErrNoValue},
		"a value on a null test":  {Condition{Op: source.OpIsNull, Value: "1"}, ErrValueGiven},
		"half a between":          {Condition{Op: source.OpBetween, Value: "1"}, ErrNoUpperBound},
		"a second value given":    {Condition{Op: source.OpEqual, Value: "1", Value2: "2"}, ErrValueGiven},
		"no column":               {Condition{Op: source.OpEqual, Value: "1"}, ErrNoColumn},
	} {
		t.Run(name, func(t *testing.T) {
			d := &Design{}
			at := d.Add(ref("public", "people"))
			if c.want != ErrNoColumn {
				c.cond.Column = Column{Table: at, Name: "name"}
			}
			d.Where = []Condition{c.cond}
			_, err := Render(d, pgLike{})
			if !errors.Is(err, c.want) {
				t.Errorf("it said %v, want %v", err, c.want)
			}
		})
	}
}

// A query with an aggregate groups by everything that is not one. The server
// would say so; saying it here says which column, about a statement the person
// did not write.
func TestAnAggregateNeedsItsGrouping(t *testing.T) {
	d := &Design{}
	at := d.Add(ref("public", "people"))
	name := Column{Table: at, Name: "name"}
	d.Outputs = []Output{{Column: name}, {Column: Column{Table: at, Name: "id"}, Aggregate: AggregateCount}}
	_, err := Render(d, pgLike{})
	if !errors.Is(err, ErrGroupNeeded) {
		t.Fatalf("it said %v", err)
	}
	if !strings.Contains(err.Error(), "people.name") {
		t.Errorf("it says %q, which does not name the column", err)
	}
	// Grouped, it renders.
	d.Group = []Column{name}
	if got := render(t, d); !strings.Contains(got, `GROUP BY "people"."name"`) {
		t.Errorf("it reads\n%s", got)
	}
	// Every column of a table cannot be grouped by, and saying so is better
	// than a GROUP BY t.* nothing takes.
	d.Group = nil
	d.Outputs[0] = Output{All: true, Column: Column{Table: at}}
	if _, err := Render(d, pgLike{}); !errors.Is(err, ErrGroupNeeded) {
		t.Errorf("selecting everything beside an aggregate said %v", err)
	}
}

// An aggregate on its own needs no grouping: COUNT(*) over a table is a query.
func TestAnAggregateAloneNeedsNoGrouping(t *testing.T) {
	d := &Design{}
	at := d.Add(ref("public", "people"))
	d.Outputs = []Output{{Column: Column{Table: at, Name: "id"}, Aggregate: AggregateCount, Alias: "how many"}}
	got := render(t, d)
	if !strings.Contains(got, `COUNT("people"."id") AS "how many"`) {
		t.Errorf("it reads\n%s", got)
	}
	if strings.Contains(got, "GROUP BY") {
		t.Errorf("it grouped by nothing:\n%s", got)
	}
}

func TestEveryAggregate(t *testing.T) {
	for _, agg := range []Aggregate{AggregateCount, AggregateSum, AggregateMin, AggregateMax, AggregateAvg} {
		d := &Design{}
		at := d.Add(ref("public", "people"))
		d.Outputs = []Output{{Column: Column{Table: at, Name: "id"}, Aggregate: agg}}
		if got := render(t, d); !strings.Contains(got, string(agg)+`("people"."id")`) {
			t.Errorf("%s reads\n%s", agg, got)
		}
	}
	d := &Design{}
	at := d.Add(ref("public", "people"))
	d.Outputs = []Output{{Column: Column{Table: at, Name: "id"}, Aggregate: Aggregate("MEDIAN")}}
	if _, err := Render(d, pgLike{}); !errors.Is(err, ErrUnknownAgg) {
		t.Errorf("an aggregate nobody has said %v", err)
	}
}

// Every column of a table, which is what a table with nothing picked from it
// means when other tables have columns picked.
func TestEveryColumnOfOneTable(t *testing.T) {
	d := &Design{}
	people := d.Add(ref("public", "people"))
	orders := d.Add(ref("public", "orders"))
	d.Joins = []Join{{Kind: JoinInner, Left: people, Right: orders,
		On: []Pair{{Left: "id", Right: "person_id"}}}}
	d.Outputs = []Output{
		{All: true, Column: Column{Table: people}},
		{Column: Column{Table: orders, Name: "total"}},
	}
	if got := render(t, d); !strings.Contains(got, `SELECT "people".*, "orders"."total"`) {
		t.Errorf("it reads\n%s", got)
	}
}

// The limit is the engine's own clause, read out of a browse rather than
// written here: LIMIT, FETCH and TOP are three different things and a package
// with no business knowing engine names must not choose between them.
func TestTheLimitIsTheEnginesOwnClause(t *testing.T) {
	d := &Design{Limit: 25}
	d.Add(ref("public", "people"))
	if got := render(t, d); !strings.HasSuffix(got, "\nLIMIT 25") {
		t.Errorf("it reads\n%s", got)
	}
	d.Limit = 0
	if got := render(t, d); strings.Contains(got, "LIMIT") {
		t.Errorf("no limit rendered one:\n%s", got)
	}
	d.Limit = -1
	if _, err := Render(d, pgLike{}); !errors.Is(err, ErrNegativeLimit) {
		t.Errorf("a negative limit said %v", err)
	}
}

func TestWhatALimitClauseIsReadOutOf(t *testing.T) {
	for name, c := range map[string]struct {
		sql  string
		want string
	}{
		"postgres": {`SELECT * FROM "t" LIMIT $1`, "LIMIT 25"},
		// The last bound is the one that bounds the statement: a browse of a
		// view whose own definition is bounded would otherwise be read as
		// bounding the browse.
		"a bound inside a bound": {`SELECT * FROM (SELECT * FROM "t" LIMIT 1) "v" LIMIT $1`, "LIMIT 25"},
		"sqlite":                 {`SELECT * FROM "t" LIMIT ? OFFSET ?`, ""}, // two marks: not one clause
		"firebird":               {`SELECT * FROM "T" FETCH NEXT ? ROWS ONLY`, "FETCH NEXT 25 ROWS ONLY"},
		"on a line":              {"SELECT * FROM \"t\"\nLIMIT $1", "LIMIT 25"},
		"a literal":              {`SELECT * FROM "t" LIMIT 25`, "LIMIT 25"},
		// TOP comes before the select list, so there is no tail to read: the
		// designer says so rather than writing a clause that does nothing.
		"sqlserver": {`SELECT TOP (@p1) * FROM "t"`, ""},
		"nothing":   {`SELECT * FROM "t"`, ""},
	} {
		t.Run(name, func(t *testing.T) {
			got, ok := limitClause(c.sql, 25)
			if c.want == "" {
				if ok {
					t.Errorf("it read %q out of %s", got, c.sql)
				}
				return
			}
			if !ok || got != c.want {
				t.Errorf("it read %q (%v), want %q", got, ok, c.want)
			}
		})
	}
}

// A design a dialect reads as anything but a read is refused. Nothing in a
// Design can write one — there is no clause for it — which is exactly why it is
// checked: the values are the person's own text.
func TestADesignThatWouldWriteIsRefused(t *testing.T) {
	d := &Design{}
	at := d.Add(ref("public", "people"))
	d.Where = []Condition{{Column: Column{Table: at, Name: "id"}, Op: source.OpEqual,
		Value: "1; DROP TABLE people"}}
	if _, err := Render(d, pgLike{mutating: true}); !errors.Is(err, ErrMutating) {
		t.Errorf("it said %v", err)
	}
}

func TestADesignWithNothingInItIsRefused(t *testing.T) {
	if _, err := Render(&Design{}, pgLike{}); !errors.Is(err, ErrNoTables) {
		t.Errorf("it said %v", err)
	}
	d := &Design{}
	d.Add(ref("public", "people"))
	if _, err := Render(d, nil); err == nil {
		t.Error("a source with no SQL rendered some")
	}
}

// A column off the canvas is refused wherever it is named: rendering one would
// name a table the query has not got, or fail looking for it.
func TestAColumnOffTheCanvasIsRefusedEverywhere(t *testing.T) {
	nowhere := Column{Table: 9, Name: "x"}
	for name, change := range map[string]func(*Design){
		"selected":       func(d *Design) { d.Outputs = []Output{{Column: nowhere}} },
		"selected whole": func(d *Design) { d.Outputs = []Output{{All: true, Column: nowhere}} },
		"filtered":       func(d *Design) { d.Where = []Condition{{Column: nowhere, Op: source.OpEqual, Value: "1"}} },
		"grouped":        func(d *Design) { d.Group = []Column{nowhere} },
		"ordered":        func(d *Design) { d.Order = []Sort{{Column: nowhere}} },
		"in a having": func(d *Design) {
			d.Having = []Condition{{Column: nowhere, Op: source.OpEqual, Value: "1"}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			d := &Design{}
			d.Add(ref("public", "people"))
			change(d)
			if _, err := Render(d, pgLike{}); !errors.Is(err, ErrBadTable) {
				t.Errorf("it said %v", err)
			}
		})
	}
	// And an aggregate nobody has, wherever it is named.
	d := &Design{}
	at := d.Add(ref("public", "people"))
	d.Order = []Sort{{Column: Column{Table: at, Name: "id"}, Aggregate: Aggregate("MEDIAN")}}
	if _, err := Render(d, pgLike{}); !errors.Is(err, ErrUnknownAgg) {
		t.Errorf("an ordering by an aggregate nobody has said %v", err)
	}
}

func TestATableNeedsAName(t *testing.T) {
	d := &Design{Tables: []Table{{Ref: ref("public", "people")}}}
	if _, err := Render(d, pgLike{}); !errors.Is(err, ErrNoAlias) {
		t.Errorf("it said %v", err)
	}
	d = &Design{Tables: []Table{
		{Ref: ref("public", "people"), Alias: "t"},
		{Ref: ref("public", "orders"), Alias: "t"},
	}}
	if _, err := Render(d, pgLike{}); !errors.Is(err, ErrSameAlias) {
		t.Errorf("two of one name said %v", err)
	}
}
