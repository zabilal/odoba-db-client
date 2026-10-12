//go:build conformance

package trino

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/conformance"
)

// The live suite, against one Trino node with no authentication (T5.15):
//
//	docker run -d --name ikigai-trino -p 58085:8080 --memory 1536m \
//	  -v .../blackhole.properties:/etc/trino/catalog/blackhole.properties:ro \
//	  trinodb/trino:latest
//
// where blackhole.properties is the one line "connector.name=blackhole". The
// image brings tpch, which is the data this reads; memory, which is where it
// makes what it needs; and system. Blackhole is added because it is the only
// thing in Trino that can wait without working, which is what stopping a
// statement has to be proved against.
//
// IKIGAI_REQUIRE_TRINO=1 turns a skip into a failure, which is what the gate
// sets. What it cannot prove is anything about a server that asks who is
// calling, or one reached over TLS: those are held by tests of what the
// settings become (source_test.go).

func required() bool { return os.Getenv("IKIGAI_REQUIRE_TRINO") != "" }

func port() int {
	if v, err := strconv.Atoi(os.Getenv("IKIGAI_TRINO_PORT")); err == nil {
		return v
	}
	return 58085
}

func settings(catalog string, g source.Guard) source.ConnectionConfig {
	return source.ConnectionConfig{DriverID: driverID, Host: "127.0.0.1", Port: port(), User: "ikigai",
		Database: catalog, TLS: source.TLSConfig{Mode: "disable"}, Guard: g}
}

// open connects as the application would.
func open(t *testing.T, g source.Guard) *trinoSource {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	src, err := Driver{}.Open(ctx, settings("", g))
	if err != nil {
		if required() {
			t.Fatalf("Trino required but unavailable: %v", err)
		}
		t.Skipf("no Trino on port %d: %v", port(), err)
	}
	t.Cleanup(func() { _ = src.Close() })
	return src.(*trinoSource)
}

var customer = table("tpch", "tiny", "customer")

// seed makes what the suite needs that tpch does not have: a table of every
// kind of value, a view, an empty table, an empty schema, and a table that
// takes its time.
func seed(t *testing.T) *trinoSource {
	t.Helper()
	s := open(t, source.Guard{})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	for _, stmt := range []string{
		`CREATE SCHEMA IF NOT EXISTS memory.ikigai`,
		`CREATE SCHEMA IF NOT EXISTS memory.ikigai_empty`,
		`DROP VIEW IF EXISTS memory.ikigai.far_nations`,
		`DROP TABLE IF EXISTS memory.ikigai.kinds`,
		`DROP TABLE IF EXISTS memory.ikigai.writes`,
		`CREATE TABLE memory.ikigai.kinds AS
		 SELECT * FROM (VALUES
		  (1, true, tinyint '1', smallint '2', 3, bigint '9007199254740993', real '1.5', 2.5e0,
		   decimal '12.340', 'text', cast('ab' as char(4)), varbinary 'ab',
		   date '2026-03-04', time '01:02:03.456', timestamp '2026-03-04 05:06:07.123456',
		   timestamp '2026-03-04 05:06:07.123 +01:00', interval '2' day,
		   uuid '12151fd2-7586-11e9-8f9e-2a86e4085a59', json '{"a":1}', ipaddress '10.0.0.1',
		   array[1, 2], array['x', null], map(array['k'], array[1]), cast(row(1, 'a') as row(n integer, s varchar))),
		  (2, null, null, null, null, null, null, null, null, null, null, null, null, null, null, null, null,
		   null, null, null, null, null, null, null)
		 ) AS t (id, b, ti, si, i, bi, r, d, dec, v, ch, vb, dt, tm, ts, tstz, iv, u, j, ip, ints, texts, m, rw)`,
		`CREATE TABLE memory.ikigai.writes (id bigint, name varchar)`,
		`CREATE VIEW memory.ikigai.far_nations AS SELECT nationkey, name FROM tpch.tiny.nation WHERE nationkey > 20`,
		`COMMENT ON TABLE memory.ikigai.kinds IS 'one of everything'`,
		`CREATE TABLE IF NOT EXISTS blackhole.default.slow (x bigint)
		 WITH (split_count = 1, pages_per_split = 20, rows_per_page = 1, page_processing_delay = '2s')`,
	} {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("seeding with %q: %v", stmt, err)
		}
	}
	// A fresh source, so that nothing remembered from before the seed is
	// taken for what the server holds now.
	return open(t, source.Guard{})
}

func TestConformance(t *testing.T) {
	seed(t)
	conformance.Run(t, conformance.Target{
		Name: "trino",
		Open: func(_ context.Context, t *testing.T) source.Source { return open(t, source.Guard{}) },
		OpenGuarded: func(_ context.Context, t *testing.T, g source.Guard) source.Source {
			return open(t, g)
		},
		// Fifteen hundred customers in twenty-five nations and five market
		// segments, which is what the picklist's checks need: columns that
		// repeat, and repeat unevenly.
		Browsable: customer,
		// No Writable: rows here are addressed by nothing, so this source
		// writes none from a grid (ADR-0174).
	})
}

// read is every row a browse answers, by column name.
func read(t *testing.T, s *trinoSource, ref model.ObjectRef, opt source.BrowseOptions) []map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	st, err := s.Browse(ctx, ref, opt)
	if err != nil {
		t.Fatalf("Browse(%s, %+v): %v", ref, opt, err)
	}
	defer st.Close()
	return drain(t, ctx, st)
}

func drain(t *testing.T, ctx context.Context, st model.RowStream) []map[string]any {
	t.Helper()
	cols := st.Columns()
	var out []map[string]any
	for {
		row, err := st.Next(ctx)
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		held := map[string]any{}
		for i, c := range cols {
			held[c.Name] = row[i]
		}
		out = append(out, held)
	}
}

func keys(rows []map[string]any, col string) []int64 {
	out := make([]int64, 0, len(rows))
	for _, r := range rows {
		k, _ := r[col].(int64)
		out = append(out, k)
	}
	return out
}

// A value comes back as what it is: a whole number whole, an exact one as its
// digits, a time as a time in no zone of this machine's, and a thing with
// parts as its parts.
func TestLiveAValueIsWhatItIs(t *testing.T) {
	s := seed(t)
	rows := read(t, s, table("memory", "ikigai", "kinds"), source.BrowseOptions{Sorts: []source.Sort{{Column: "id"}}})
	if len(rows) != 2 {
		t.Fatalf("read %d rows", len(rows))
	}
	for name, want := range map[string]any{
		"id": int64(1), "b": true, "ti": int64(1), "si": int64(2), "i": int64(3),
		"bi": int64(9007199254740993), // which a float64 cannot hold
		"r":  1.5, "d": 2.5,
		"dec": model.Decimal("12.340"), // its zeros kept: it is the number the row holds
		"v":   "text", "ch": "ab  ", "vb": []byte("ab"),
		"dt":   time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC),
		"tm":   "01:02:03.456",
		"ts":   time.Date(2026, 3, 4, 5, 6, 7, 123456000, time.UTC),
		"tstz": time.Date(2026, 3, 4, 4, 6, 7, 123000000, time.UTC), // the moment, wherever it was written
		"iv":   "2 00:00:00.000",
		"u":    "12151fd2-7586-11e9-8f9e-2a86e4085a59",
		"j":    model.JSON(`{"a":1}`),
		"ip":   "10.0.0.1",
		"ints": []any{int64(1), int64(2)}, "texts": []any{"x", nil},
		"m":  map[string]any{"k": int64(1)},
		"rw": map[string]any{"n": int64(1), "s": "a"},
	} {
		if got := rows[0][name]; !reflect.DeepEqual(got, want) {
			t.Errorf("%s is %#v (%T), want %#v (%T)", name, got, got, want, want)
		}
	}
	for name, v := range rows[1] {
		if name != "id" && v != nil {
			t.Errorf("%s of the empty row is %#v", name, v)
		}
	}
}

// What a result says its columns are.
func TestLiveAColumnSaysWhatItIs(t *testing.T) {
	s := seed(t)
	ctx := context.Background()
	st, err := s.Browse(ctx, table("memory", "ikigai", "kinds"), source.BrowseOptions{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	got := map[string]model.DataType{}
	for _, c := range st.Columns() {
		got[c.Name] = c.Type
		if c.Origin.String() != table("memory", "ikigai", "kinds").String() {
			t.Errorf("%s says it comes from %s", c.Name, c.Origin)
		}
	}
	for name, want := range map[string]model.TypeClass{
		"b": model.TypeBool, "ti": model.TypeInteger, "bi": model.TypeInteger, "r": model.TypeFloat,
		"dec": model.TypeDecimal, "v": model.TypeString, "ch": model.TypeString, "vb": model.TypeBytes,
		"dt": model.TypeDate, "tm": model.TypeTime, "ts": model.TypeTimestamp, "tstz": model.TypeTimestamp,
		"iv": model.TypeInterval, "u": model.TypeUUID, "j": model.TypeJSON, "ip": model.TypeNetwork,
		"ints": model.TypeArray, "m": model.TypeStruct, "rw": model.TypeStruct,
	} {
		if got[name].Class != want {
			t.Errorf("%s is a %v (%s), want %v", name, got[name].Class, got[name].Native, want)
		}
	}
	if d := got["dec"]; d.Precision != 5 || d.Scale != 3 {
		t.Errorf("the decimal is (%d, %d)", d.Precision, d.Scale)
	}
	if got["ch"].Length != 4 || !got["tstz"].TimeZone || got["ts"].TimeZone {
		t.Errorf("char(%d); zones %v and %v", got["ch"].Length, got["tstz"].TimeZone, got["ts"].TimeZone)
	}
	if el := got["ints"].Element; el == nil || el.Class != model.TypeInteger {
		t.Errorf("an array of %+v", el)
	}
	if id, ok := st.(model.Identified); !ok || id.Identity().Kind != model.IdentityNone {
		t.Error("the rows say they can be addressed")
	}
}

// One page follows another: every row once, none twice, whatever the order
// asked for and where none was.
func TestLivePagingDoesNotRepeatItself(t *testing.T) {
	s := seed(t)
	for name, sorts := range map[string][]source.Sort{
		"unsorted":      nil,
		"by nation":     {{Column: "nationkey"}}, // which sixty rows in each tie on
		"by segment, z": {{Column: "mktsegment", Descending: true}},
	} {
		seen := map[int64]int{}
		for off := int64(0); off < 1600; off += 400 {
			for _, k := range keys(read(t, s, customer, source.BrowseOptions{Offset: off, Limit: 400, Sorts: sorts}), "custkey") {
				seen[k]++
			}
		}
		if len(seen) != 1500 {
			t.Errorf("%s: %d different rows over the pages, want 1500", name, len(seen))
		}
		for k, n := range seen {
			if n != 1 {
				t.Errorf("%s: customer %d was read %d times", name, k, n)
				break
			}
		}
	}
	// Nobody saying how many is not everything (NFR-P11).
	if rows := read(t, s, customer, source.BrowseOptions{}); len(rows) != DefaultPageSize {
		t.Errorf("with no limit, %d rows", len(rows))
	}
}

// A sort asked for comes first, and what breaks its ties comes after.
func TestLiveASortAskedForComesFirst(t *testing.T) {
	s := seed(t)
	desc := keys(read(t, s, customer, source.BrowseOptions{Limit: 3, Sorts: []source.Sort{{Column: "custkey", Descending: true}}}), "custkey")
	if !reflect.DeepEqual(desc, []int64{1500, 1499, 1498}) {
		t.Errorf("highest first: %v", desc)
	}
	rows := read(t, s, customer, source.BrowseOptions{Limit: 200,
		Sorts: []source.Sort{{Column: "nationkey", Descending: true}}})
	nations, custs := keys(rows, "nationkey"), keys(rows, "custkey")
	for i := 1; i < len(rows); i++ {
		if nations[i] > nations[i-1] || (nations[i] == nations[i-1] && custs[i] < custs[i-1]) {
			t.Fatalf("row %d: nation %d customer %d after nation %d customer %d", i, nations[i], custs[i], nations[i-1], custs[i-1])
		}
	}
	// Nothing goes last unless it is asked for first.
	kinds := table("memory", "ikigai", "kinds")
	last := read(t, s, kinds, source.BrowseOptions{Sorts: []source.Sort{{Column: "v"}}})
	first := read(t, s, kinds, source.BrowseOptions{Sorts: []source.Sort{{Column: "v", NullsFirst: true}}})
	if last[0]["v"] == nil || first[0]["v"] != nil {
		t.Errorf("without NullsFirst %#v came first, with it %#v", last[0]["v"], first[0]["v"])
	}
	// A table whose columns are not all orderable is still read: what has no
	// order is left out of what breaks a tie.
	if len(last) != 2 {
		t.Errorf("read %d rows of a table with a map in it", len(last))
	}
}

// Every filter the grid can build selects exactly the rows the same question
// selects when it is asked here, of the rows as they are (REQ-DRV-3).
func TestLiveEveryFilterMeansWhatItSays(t *testing.T) {
	s := seed(t)
	all := read(t, s, customer, source.BrowseOptions{Limit: 5000, Sorts: []source.Sort{{Column: "custkey"}}})
	if len(all) != 1500 {
		t.Fatalf("read %d customers", len(all))
	}
	str := func(r map[string]any, c string) string { v, _ := r[c].(string); return v }
	num := func(r map[string]any, c string) int64 { v, _ := r[c].(int64); return v }
	phone := regexp.MustCompile(`^1[0-3]-`)
	cases := []struct {
		name string
		f    source.Filter
		want func(map[string]any) bool
	}{
		{"equal", source.Filter{Column: "mktsegment", Op: source.OpEqual, Values: []any{"BUILDING"}},
			func(r map[string]any) bool { return str(r, "mktsegment") == "BUILDING" }},
		{"equal is its case", source.Filter{Column: "mktsegment", Op: source.OpEqual, Values: []any{"building"}},
			func(map[string]any) bool { return false }},
		{"not equal", source.Filter{Column: "mktsegment", Op: source.OpNotEqual, Values: []any{"BUILDING"}},
			func(r map[string]any) bool { return str(r, "mktsegment") != "BUILDING" }},
		{"a number equal", source.Filter{Column: "custkey", Op: source.OpEqual, Values: []any{int64(7)}},
			func(r map[string]any) bool { return num(r, "custkey") == 7 }},
		{"less", source.Filter{Column: "custkey", Op: source.OpLess, Values: []any{int64(10)}},
			func(r map[string]any) bool { return num(r, "custkey") < 10 }},
		{"less or equal", source.Filter{Column: "custkey", Op: source.OpLessEqual, Values: []any{int64(10)}},
			func(r map[string]any) bool { return num(r, "custkey") <= 10 }},
		{"greater", source.Filter{Column: "custkey", Op: source.OpGreater, Values: []any{int64(1490)}},
			func(r map[string]any) bool { return num(r, "custkey") > 1490 }},
		{"greater or equal", source.Filter{Column: "custkey", Op: source.OpGreaterEqual, Values: []any{int64(1490)}},
			func(r map[string]any) bool { return num(r, "custkey") >= 1490 }},
		{"between, both ends in", source.Filter{Column: "custkey", Op: source.OpBetween, Values: []any{int64(10), int64(20)}},
			func(r map[string]any) bool { return num(r, "custkey") >= 10 && num(r, "custkey") <= 20 }},
		{"one of", source.Filter{Column: "nationkey", Op: source.OpIn, Values: []any{int64(3), int64(7)}},
			func(r map[string]any) bool { return num(r, "nationkey") == 3 || num(r, "nationkey") == 7 }},
		{"none of", source.Filter{Column: "nationkey", Op: source.OpNotIn, Values: []any{int64(3), int64(7)}},
			func(r map[string]any) bool { return num(r, "nationkey") != 3 && num(r, "nationkey") != 7 }},
		{"one of nothing", source.Filter{Column: "nationkey", Op: source.OpIn},
			func(map[string]any) bool { return false }},
		{"none of nothing", source.Filter{Column: "nationkey", Op: source.OpNotIn},
			func(map[string]any) bool { return true }},
		{"contains, whatever the case", source.Filter{Column: "mktsegment", Op: source.OpContains, Values: []any{"uild"}},
			func(r map[string]any) bool { return strings.Contains(str(r, "mktsegment"), "UILD") }},
		{"contains a pattern's own character", source.Filter{Column: "name", Op: source.OpContains, Values: []any{"%"}},
			func(map[string]any) bool { return false }},
		{"contains, of a number", source.Filter{Column: "custkey", Op: source.OpContains, Values: []any{"149"}},
			func(r map[string]any) bool { return strings.Contains(strconv.FormatInt(num(r, "custkey"), 10), "149") }},
		{"like, a prefix", source.Filter{Column: "name", Op: source.OpLike, Values: []any{"Customer#00000001%"}},
			func(r map[string]any) bool { return strings.HasPrefix(str(r, "name"), "Customer#00000001") }},
		{"like, one character", source.Filter{Column: "name", Op: source.OpLike, Values: []any{"Customer#00000000_"}},
			func(r map[string]any) bool { return num(r, "custkey") < 10 }},
		{"like is its case", source.Filter{Column: "name", Op: source.OpLike, Values: []any{"customer#%"}},
			func(map[string]any) bool { return false }},
		{"like, an escaped underscore", source.Filter{Column: "name", Op: source.OpLike, Values: []any{`Customer\_%`}},
			func(map[string]any) bool { return false }},
		{"not like", source.Filter{Column: "name", Op: source.OpNotLike, Values: []any{"Customer#00000001%"}},
			func(r map[string]any) bool { return !strings.HasPrefix(str(r, "name"), "Customer#00000001") }},
		{"a regular expression", source.Filter{Column: "phone", Op: source.OpRegex, Values: []any{`^1[0-3]-`}},
			func(r map[string]any) bool { return phone.MatchString(str(r, "phone")) }},
		{"has one", source.Filter{Column: "comment", Op: source.OpIsNotNull},
			func(map[string]any) bool { return true }},
		{"has none", source.Filter{Column: "comment", Op: source.OpIsNull},
			func(map[string]any) bool { return false }},
		{"negated", source.Filter{Column: "custkey", Op: source.OpBetween, Values: []any{int64(10), int64(1490)}, Negate: true},
			func(r map[string]any) bool { return num(r, "custkey") < 10 || num(r, "custkey") > 1490 }},
	}
	for _, c := range cases {
		var want []int64
		for _, r := range all {
			if c.want(r) {
				want = append(want, num(r, "custkey"))
			}
		}
		got := keys(read(t, s, customer, source.BrowseOptions{Limit: 5000,
			Filters: []source.Filter{c.f}, Sorts: []source.Sort{{Column: "custkey"}}}), "custkey")
		if len(got)+len(want) > 0 && !reflect.DeepEqual(got, want) {
			t.Errorf("%s: selected %d rows %v…, want %d %v…", c.name, len(got), head(got), len(want), head(want))
		}
	}

	// Two filters are both, and a typed condition is one more.
	both := keys(read(t, s, customer, source.BrowseOptions{Sorts: []source.Sort{{Column: "custkey"}},
		Filters: []source.Filter{
			{Column: "nationkey", Op: source.OpEqual, Values: []any{int64(3)}},
			{Column: "custkey", Op: source.OpLess, Values: []any{int64(200)}},
		}, Where: "mktsegment = 'BUILDING'"}), "custkey")
	var want []int64
	for _, r := range all {
		if num(r, "nationkey") == 3 && num(r, "custkey") < 200 && str(r, "mktsegment") == "BUILDING" {
			want = append(want, num(r, "custkey"))
		}
	}
	if len(want) == 0 || !reflect.DeepEqual(both, want) {
		t.Errorf("two filters and a condition selected %v, want %v", both, want)
	}
}

func head(k []int64) []int64 { return k[:min(len(k), 5)] }

// What is nothing is matched as nothing, in a list as well as on its own; and
// a value of each kind filters by itself.
func TestLiveNothingAndEveryKindOfValueFilter(t *testing.T) {
	s := seed(t)
	kinds := table("memory", "ikigai", "kinds")
	sel := func(f source.Filter) []int64 {
		return keys(read(t, s, kinds, source.BrowseOptions{Filters: []source.Filter{f}, Sorts: []source.Sort{{Column: "id"}}}), "id")
	}
	for name, c := range map[string]struct {
		f    source.Filter
		want []int64
	}{
		"is null":              {source.Filter{Column: "v", Op: source.OpIsNull}, []int64{2}},
		"is not null":          {source.Filter{Column: "v", Op: source.OpIsNotNull}, []int64{1}},
		"equal to nothing":     {source.Filter{Column: "v", Op: source.OpEqual, Values: []any{nil}}, []int64{2}},
		"not equal to nothing": {source.Filter{Column: "v", Op: source.OpNotEqual, Values: []any{nil}}, []int64{1}},
		"in, with nothing":     {source.Filter{Column: "v", Op: source.OpIn, Values: []any{"text", nil}}, []int64{1, 2}},
		"in, only nothing":     {source.Filter{Column: "v", Op: source.OpIn, Values: []any{nil}}, []int64{2}},
		"not in":               {source.Filter{Column: "v", Op: source.OpNotIn, Values: []any{"other"}}, []int64{1, 2}},
		"not in, with nothing": {source.Filter{Column: "v", Op: source.OpNotIn, Values: []any{"other", nil}}, []int64{1}},
		"not in, only nothing": {source.Filter{Column: "v", Op: source.OpNotIn, Values: []any{nil}}, []int64{1}},
		// Each as the grid would hand back the value it showed.
		"an exact number": {source.Filter{Column: "dec", Op: source.OpEqual, Values: []any{model.Decimal("12.340")}}, []int64{1}},
		"a date":          {source.Filter{Column: "dt", Op: source.OpEqual, Values: []any{time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC)}}, []int64{1}},
		"a timestamp with no zone": {source.Filter{Column: "ts", Op: source.OpEqual,
			Values: []any{time.Date(2026, 3, 4, 5, 6, 7, 123456000, time.UTC)}}, []int64{1}},
		"a timestamp with one": {source.Filter{Column: "tstz", Op: source.OpEqual,
			Values: []any{time.Date(2026, 3, 4, 4, 6, 7, 123000000, time.UTC)}}, []int64{1}},
		"a boolean": {source.Filter{Column: "b", Op: source.OpEqual, Values: []any{true}}, []int64{1}},
		"a float":   {source.Filter{Column: "d", Op: source.OpGreater, Values: []any{2.4}}, []int64{1}},
		"bytes":     {source.Filter{Column: "vb", Op: source.OpEqual, Values: []any{[]byte("ab")}}, []int64{1}},
	} {
		if got := sel(c.f); len(got)+len(c.want) > 0 && !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s selected %v, want %v", name, got, c.want)
		}
	}
}

func TestLiveWhatCannotBeBrowsedIsRefused(t *testing.T) {
	s := seed(t)
	ctx := context.Background()
	for name, c := range map[string]struct {
		ref  model.ObjectRef
		opt  source.BrowseOptions
		says string
	}{
		"a schema":                  {model.NewRef(model.KindSchema, "tpch", "tiny"), source.BrowseOptions{}, "not browsable"},
		"a table with no catalog":   {table("tiny", "customer"), source.BrowseOptions{}, "not browsable"},
		"a table that is not there": {table("tpch", "tiny", "nosuch"), source.BrowseOptions{}, "does not exist"},
		"following":                 {customer, source.BrowseOptions{Follow: true}, "stream sources"},
		"a filter with no column":   {customer, source.BrowseOptions{Filters: []source.Filter{{Op: source.OpIsNull}}}, "needs a column"},
		"two conditions":            {customer, source.BrowseOptions{Where: "1 = 1; DROP TABLE x"}, "trino:"},
		"an operator nobody knows":  {customer, source.BrowseOptions{Filters: []source.Filter{{Column: "name", Op: "sounds like", Values: []any{"x"}}}}, "unsupported filter operator"},
		"ordered against nothing":   {customer, source.BrowseOptions{Filters: []source.Filter{{Column: "name", Op: source.OpLess, Values: []any{nil}}}}, "NULL"},
		"between one":               {customer, source.BrowseOptions{Filters: []source.Filter{{Column: "custkey", Op: source.OpBetween, Values: []any{1}}}}, "takes 2 value(s), got 1"},
	} {
		st, err := s.Browse(ctx, c.ref, c.opt)
		if err == nil {
			st.Close()
			t.Errorf("%s was browsed", name)
		} else if !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: %v, want it to mention %q", name, err, c.says)
		}
	}
}

func TestLiveTheExplorerWalksTheServer(t *testing.T) {
	seed(t)
	ctx := context.Background()
	src, err := Driver{}.Open(ctx, settings("memory", source.Guard{}))
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	s := src.(*trinoSource)

	roots, err := s.Root(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byName := func(nodes []model.Node) (map[string]model.Node, []string) {
		out, names := map[string]model.Node{}, []string{}
		for _, n := range nodes {
			out[n.Label] = n
			names = append(names, n.Label)
		}
		return out, names
	}
	cats, names := byName(roots)
	if !sort.StringsAreSorted(names) {
		t.Errorf("the catalogs are not in order: %v", names)
	}
	for _, want := range []string{"blackhole", "memory", "system", "tpch"} {
		if n, ok := cats[want]; !ok || n.Ref.Kind != model.KindDatabase || !n.HasChildren {
			t.Errorf("catalog %s is %+v", want, n)
		}
	}
	if cats["system"].Attrs["system"] != "true" || cats["memory"].Attrs["current"] != "true" || len(cats["tpch"].Attrs) != 0 {
		t.Errorf("marked: system %v, memory %v, tpch %v", cats["system"].Attrs, cats["memory"].Attrs, cats["tpch"].Attrs)
	}

	schemas, err := s.Children(ctx, cats["tpch"].Ref)
	if err != nil {
		t.Fatal(err)
	}
	sch, names := byName(schemas)
	if !sort.StringsAreSorted(names) || sch["tiny"].Ref.String() != model.NewRef(model.KindSchema, "tpch", "tiny").String() ||
		!sch["tiny"].HasChildren || sch["information_schema"].Attrs["system"] != "true" || len(sch["tiny"].Attrs) != 0 {
		t.Errorf("tpch's schemas: %v, tiny %+v", names, sch["tiny"])
	}

	classes, err := s.Children(ctx, sch["tiny"].Ref)
	if err != nil || len(classes) != 1 {
		t.Fatalf("tiny's classes: %v, %v", classes, err)
	}
	if k, _ := model.ClassOf(classes[0].Ref); k != model.KindTable || classes[0].Badge.Text != "8" {
		t.Errorf("tiny holds %+v", classes[0])
	}
	tables, err := s.Children(ctx, classes[0].Ref)
	if err != nil {
		t.Fatal(err)
	}
	tabs, names := byName(tables)
	if !reflect.DeepEqual(names, []string{"customer", "lineitem", "nation", "orders", "part", "partsupp", "region", "supplier"}) {
		t.Errorf("tiny's tables: %v", names)
	}
	if c := tabs["customer"]; c.Ref.String() != customer.String() || !c.Browsable || !c.Describable || !c.HasChildren {
		t.Errorf("customer is %+v", c)
	}
	cols, err := s.Children(ctx, customer)
	if err != nil || len(cols) != 8 {
		t.Fatalf("customer's columns: %v, %v", cols, err)
	}
	if c := cols[1]; c.Label != "name" || c.Ref.String() != model.NewRef(model.KindColumn, "tpch", "tiny", "customer", "name").String() ||
		c.Attrs["type"] != "varchar(25)" || c.Attrs["nullable"] != "false" || c.HasChildren {
		t.Errorf("the second column is %+v", c)
	}

	// A schema with a table and a view has a class for each; one with nothing
	// still has somewhere to open.
	classes, err = s.Children(ctx, model.NewRef(model.KindSchema, "memory", "ikigai"))
	if err != nil || len(classes) != 2 {
		t.Fatalf("the seeded schema's classes: %v, %v", classes, err)
	}
	for _, c := range classes {
		k, _ := model.ClassOf(c.Ref)
		kids, err := s.Children(ctx, c.Ref)
		if err != nil {
			t.Fatal(err)
		}
		_, names := byName(kids)
		switch k {
		case model.KindTable:
			if !reflect.DeepEqual(names, []string{"kinds", "writes"}) || c.Badge.Text != "2" {
				t.Errorf("its tables are %v, counted %s", names, c.Badge.Text)
			}
		case model.KindView:
			if !reflect.DeepEqual(names, []string{"far_nations"}) || kids[0].Ref.Kind != model.KindView || !kids[0].Browsable {
				t.Errorf("its views are %v", kids)
			}
		default:
			t.Errorf("a class of %v", k)
		}
	}
	empty, err := s.Children(ctx, model.NewRef(model.KindSchema, "memory", "ikigai_empty"))
	if err != nil || len(empty) != 1 || empty[0].HasChildren || empty[0].Badge.Text != "0" {
		t.Errorf("an empty schema: %+v, %v", empty, err)
	}

	for _, bad := range []model.ObjectRef{
		model.NewRef(model.KindDatabase), model.NewRef(model.KindSchema, "tpch"), table("tpch", "customer"),
		model.NewRef(model.KindDatabase, "tpch", "tiny"), table("tpch", "tiny", "nosuch"),
		model.NewRef(model.KindFolder, "tpch"), model.ClassRef(model.NewRef(model.KindSchema, "tpch", "tiny"), model.KindRoutine),
	} {
		if kids, err := s.Children(ctx, bad); err == nil {
			t.Errorf("under %s: %v", bad, kids)
		}
	}
	if kids, err := s.Children(ctx, model.NewRef(model.KindColumn, "tpch", "tiny", "customer", "name")); err != nil || kids != nil {
		t.Errorf("under a column: %v, %v", kids, err)
	}
}

func TestLiveATableAndAViewAreDescribed(t *testing.T) {
	s := seed(t)
	ctx := context.Background()
	got, err := s.Describe(ctx, customer)
	if err != nil {
		t.Fatal(err)
	}
	tb, ok := got.(*model.Table)
	if !ok || tb.Name != "customer" || len(tb.Columns) != 8 || tb.RowsEstimate != 1500 {
		t.Fatalf("customer is described as %#v", got)
	}
	if c := tb.Columns[0]; c.Name != "custkey" || c.Position != 1 || c.Type.Class != model.TypeInteger ||
		c.Type.Native != "bigint" || c.Type.Nullable || c.HasDefault {
		t.Errorf("its first column is %+v", c)
	}
	if c := tb.Columns[1]; c.Type.Length != 25 || c.Position != 2 {
		t.Errorf("its second column is %+v", c)
	}

	got, err = s.Describe(ctx, table("memory", "ikigai", "kinds"))
	if err != nil {
		t.Fatal(err)
	}
	kinds := got.(*model.Table)
	if kinds.Comment != "one of everything" {
		t.Errorf("what was written about it: %q", kinds.Comment)
	}
	byName := map[string]model.Column{}
	for _, c := range kinds.Columns {
		byName[c.Name] = c
	}
	if c := byName["v"]; !c.Type.Nullable {
		t.Errorf("a column that may be empty says it may not: %+v", c)
	}
	if c := byName["tstz"]; c.Type.Native != "timestamp(3) with time zone" || !c.Type.TimeZone || c.Type.Precision != 3 {
		t.Errorf("the zoned timestamp is %+v", c.Type)
	}
	if c := byName["dec"]; c.Type.Precision != 5 || c.Type.Scale != 3 {
		t.Errorf("the decimal is %+v", c.Type)
	}

	got, err = s.Describe(ctx, model.NewRef(model.KindView, "memory", "ikigai", "far_nations"))
	if err != nil {
		t.Fatal(err)
	}
	v, ok := got.(*model.View)
	if !ok || v.Name != "far_nations" || len(v.Columns) != 2 || !strings.Contains(v.Definition, "nationkey > 20") {
		t.Errorf("the view is described as %#v", got)
	}
	// And it is read like a table.
	if rows := read(t, s, model.NewRef(model.KindView, "memory", "ikigai", "far_nations"), source.BrowseOptions{}); len(rows) != 4 {
		t.Errorf("the view has %d rows", len(rows))
	}

	for _, bad := range []model.ObjectRef{model.NewRef(model.KindSchema, "tpch", "tiny"), table("tpch", "tiny"), table("tpch", "tiny", "nosuch")} {
		if d, err := s.Describe(ctx, bad); err == nil {
			t.Errorf("%s was described: %#v", bad, d)
		}
	}
}

// An estimate beside a table, where the catalog keeps one, and never dressed
// as a count.
func TestLiveABadgeIsAnEstimate(t *testing.T) {
	s := seed(t)
	ctx := context.Background()
	b, ok, err := s.Badge(ctx, customer)
	if err != nil || !ok || b.Text != "1.5K" || b.Exact {
		t.Errorf("customer: %+v, %v, %v", b, ok, err)
	}
	b, ok, err = s.Badge(ctx, table("tpch", "tiny", "nation"))
	if err != nil || !ok || b.Text != "25" || b.Exact {
		t.Errorf("nation: %+v, %v, %v", b, ok, err)
	}
	for _, none := range []model.ObjectRef{
		model.NewRef(model.KindView, "memory", "ikigai", "far_nations"),
		model.NewRef(model.KindSchema, "tpch", "tiny"), table("tpch", "tiny"),
	} {
		if b, ok, err := s.Badge(ctx, none); ok || err != nil {
			t.Errorf("%s: %+v, %v, %v", none, b, ok, err)
		}
	}
	for n, want := range map[int64]string{0: "0", 999: "999", 1000: "1K", 1234: "1.2K", 1_500_000: "1.5M", 2_000_000_000: "2B"} {
		if got := humanCount(n); got != want {
			t.Errorf("%d is written %q, want %q", n, got, want)
		}
	}
}

// pinned is a session, closed with the test.
func pinned(t *testing.T, s *trinoSource) source.Session {
	t.Helper()
	ss, err := s.Session(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	return ss
}

// one runs a statement and reads everything it answers.
func one(t *testing.T, q source.Queryer, sql string, args ...any) []model.Row {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	res, err := q.Query(ctx, source.Statement{SQL: sql, Args: args, Confirmed: true})
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	if res.Rows == nil {
		return nil
	}
	defer res.Rows.Close()
	var out []model.Row
	for {
		row, err := res.Rows.Next(ctx)
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		out = append(out, row)
	}
}

// A session keeps what a statement left: which catalog a bare name means, and
// what was prepared.
func TestLiveASessionKeepsWhatAStatementLeft(t *testing.T) {
	s := seed(t)
	ss := pinned(t, s)
	one(t, ss, `USE tpch.tiny`)
	if rows := one(t, ss, `SELECT count(*) FROM nation`); len(rows) != 1 || rows[0][0] != int64(25) {
		t.Errorf("after USE, a bare name gave %v", rows)
	}
	if rows := one(t, ss, `SELECT current_catalog, current_schema`); len(rows) != 1 || rows[0][0] != "tpch" || rows[0][1] != "tiny" {
		t.Errorf("where the session is: %v", rows)
	}
	// The columns of a table are asked for once and remembered, and a table
	// that is not there is not remembered as one with none.
	if _, err := s.columns(context.Background(), customer); err != nil {
		t.Fatal(err)
	}
	if _, err := s.columns(context.Background(), table("tpch", "tiny", "nosuch")); err == nil {
		t.Error("a table that is not there has columns")
	}
	s.mu.Lock()
	remembered := len(s.cols)
	s.mu.Unlock()
	if remembered != 1 {
		t.Errorf("%d tables' columns are remembered", remembered)
	}
	// And another session is somewhere else.
	other := pinned(t, s)
	if _, err := other.Query(context.Background(), source.Statement{SQL: `SELECT count(*) FROM nation`}); err == nil {
		t.Error("a second session found a bare name the first one's USE was for")
	}
	// A statement with no rows says nothing about rows, and leaves the
	// session usable.
	res, err := ss.Query(context.Background(), source.Statement{SQL: `SET SESSION query_max_run_time = '10m'`})
	if err != nil || res.Rows != nil || res.Affected != -1 {
		t.Errorf("a SET answered %+v, %v", res, err)
	}
	if rows := one(t, ss, `SHOW SESSION LIKE 'query_max_run_time'`); len(rows) != 1 || rows[0][1] != "10m" {
		t.Errorf("the property the session set: %v", rows)
	}
}

// A session that is closed runs nothing, and says that is why.
func TestLiveAClosedSessionSaysSo(t *testing.T) {
	s := seed(t)
	ss, err := s.Session(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := ss.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ss.Query(context.Background(), source.Statement{SQL: `SELECT 1`}); !errors.Is(err, errClosed) {
		t.Errorf("a statement on a closed session: %v", err)
	}
	if err := ss.Close(); err != nil {
		t.Errorf("closing it twice: %v", err)
	}
}

func TestLiveParametersAreBound(t *testing.T) {
	s := seed(t)
	rows := one(t, s, `SELECT name FROM tpch.tiny.nation WHERE nationkey = ? AND name LIKE ?`, int64(3), "C%")
	if len(rows) != 1 || rows[0][0] != "CANADA" {
		t.Errorf("positional: %v", rows)
	}
	// A value that is a statement stays a value.
	rows = one(t, s, `SELECT count(*) FROM tpch.tiny.nation WHERE name = ?`, `'; DROP TABLE memory.ikigai.kinds --`)
	if len(rows) != 1 || rows[0][0] != int64(0) {
		t.Errorf("a value shaped like a statement: %v", rows)
	}
	// An exact number is compared as one.
	rows = one(t, s, `SELECT id FROM memory.ikigai.kinds WHERE dec = ?`, model.Decimal("12.34"))
	if len(rows) != 1 || rows[0][0] != int64(1) {
		t.Errorf("an exact number: %v", rows)
	}
	ctx := context.Background()
	res, err := s.Query(ctx, source.Statement{
		SQL:   `SELECT name FROM tpch.tiny.nation WHERE nationkey = :k OR nationkey = :k + 1 ORDER BY 1`,
		Named: map[string]any{"k": int64(3)}})
	if err != nil {
		t.Fatal(err)
	}
	got := drain(t, ctx, res.Rows)
	res.Rows.Close()
	if len(got) != 2 || got[0]["name"] != "CANADA" || got[1]["name"] != "EGYPT" {
		t.Errorf("named, used twice: %v", got)
	}
	if _, err := s.Query(ctx, source.Statement{SQL: `SELECT :k, ?`, Args: []any{1}, Named: map[string]any{"k": 1}}); err == nil ||
		!strings.Contains(err.Error(), "cannot mix") {
		t.Errorf("both kinds at once: %v", err)
	}
	if _, err := s.Query(ctx, source.Statement{SQL: `SELECT :missing`, Named: map[string]any{"k": 1}}); err == nil {
		t.Error("a name with no value was run")
	}
	// Nothing run on a connection of its own keeps it: not a statement that
	// failed, not one with no rows, and not one whose rows were read.
	one(t, s, `USE tpch.tiny`)
	if _, err := s.Query(ctx, source.Statement{SQL: `SELECT nothing FROM tpch.tiny.nation`}); err == nil {
		t.Error("a column that is not there was read")
	}
	if n := s.db.Stats().InUse; n != 0 {
		t.Errorf("%d connections are still held", n)
	}
}

func TestLiveAScriptRunsItsStatements(t *testing.T) {
	s := seed(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	ch, err := s.QueryMulti(ctx, "USE tpch.tiny;\nSELECT count(*) FROM region;\n\nSELECT name FROM region ORDER BY 1", source.ScriptOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var got []source.ScriptResult
	for r := range ch {
		if r.Err != nil {
			t.Fatalf("statement %d (%s): %v", r.Index, r.Statement, r.Err)
		}
		got = append(got, r)
	}
	if len(got) != 3 || got[0].Result.Rows != nil || got[1].Offset != 15 || got[2].Statement != "SELECT name FROM region ORDER BY 1" {
		t.Fatalf("the script answered %+v", got)
	}
	// An earlier result is held while the rest runs; the last one streams.
	if _, held := got[1].Result.Rows.(*sliceStream); !held {
		t.Errorf("an earlier result is a %T", got[1].Result.Rows)
	}
	if _, held := got[2].Result.Rows.(*sliceStream); held {
		t.Error("the last result was read into memory before anybody asked for it")
	}
	first := drain(t, ctx, got[1].Result.Rows)
	last := drain(t, ctx, got[2].Result.Rows)
	got[2].Result.Rows.Close()
	if len(first) != 1 || first[0]["_col0"] != int64(5) || len(last) != 5 || last[0]["name"] != "AFRICA" {
		t.Errorf("results %v and %v", first, last)
	}

	// A script's statements each bind the names they use.
	ch, err = s.QueryMulti(ctx, "SELECT :k + 1; SELECT name FROM tpch.tiny.nation WHERE nationkey = :k",
		source.ScriptOptions{Named: map[string]any{"k": int64(3)}})
	if err != nil {
		t.Fatal(err)
	}
	var named []any
	for r := range ch {
		if r.Err != nil {
			t.Fatalf("statement %d: %v", r.Index, r.Err)
		}
		rows := drain(t, ctx, r.Result.Rows)
		r.Result.Rows.Close()
		for _, v := range rows[0] {
			named = append(named, v)
		}
	}
	if !reflect.DeepEqual(named, []any{int64(4), "CANADA"}) {
		t.Errorf("a script with a name in it answered %v", named)
	}

	// A failure ends it there, and says which statement.
	ch, err = s.QueryMulti(ctx, "SELECT 1; SELECT nothing FROM tpch.tiny.region; SELECT 2", source.ScriptOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var results []source.ScriptResult
	for r := range ch {
		results = append(results, r)
	}
	if len(results) != 2 || results[0].Err != nil || results[1].Err == nil || results[1].Index != 1 {
		t.Errorf("a script with a failure in it answered %+v", results)
	}
}

// A failure says what the server said, in its first line, and where.
func TestLiveAFailureSaysWhatTheServerSaid(t *testing.T) {
	s := seed(t)
	ctx := context.Background()
	_, err := s.Query(ctx, source.Statement{SQL: "SELECT 1\nFROM tpch.tiny.nosuch"})
	var se *source.StatementError
	if !errors.As(err, &se) || se.Message.Code != "TABLE_NOT_FOUND" || !strings.Contains(se.Message.Text, "does not exist") {
		t.Fatalf("a table that is not there: %v", err)
	}
	if se.Message.Position != len("SELECT 1\n")+6 { // line 2, column 6
		t.Errorf("it is said to be at %d", se.Message.Position)
	}
	_, err = s.Query(ctx, source.Statement{SQL: "SELEC 1"})
	if !errors.As(err, &se) || se.Message.Code != "SYNTAX_ERROR" || se.Message.Position != 1 ||
		strings.Contains(se.Message.Text, "Expecting") || !strings.Contains(se.Message.Text, "SELEC") {
		t.Errorf("a misspelling: %v (%+v)", err, se)
	}
}

// A statement somebody stops is stopped, promptly, and at the server too: a
// query engine that carried on with a query nobody is waiting for would be
// spending somebody's cluster.
func TestLiveAStatementIsStoppedAtTheServer(t *testing.T) {
	s := seed(t)
	ss := pinned(t, s)
	tag := fmt.Sprintf("ikigai-stop-%d", time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	stop, halt := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		// Forty seconds of waiting, and no work: blackhole sleeps between
		// the pages it makes up (never a scan; see the memory of what a wide
		// one did to this machine).
		res, err := ss.Query(stop, source.Statement{SQL: `SELECT count(*) AS "` + tag + `" FROM blackhole.default.slow`})
		if err == nil {
			_, err = res.Rows.Next(stop)
		}
		done <- err
	}()
	// Wait until the server has it, then stop it.
	running := func() string {
		// By how it begins: the question that asks this has the name in it
		// too, and would otherwise find itself.
		rows := one(t, s, `SELECT state FROM system.runtime.queries WHERE query LIKE ?`, `SELECT count(*) AS "`+tag+`%`)
		if len(rows) == 0 {
			return ""
		}
		state, _ := rows[0][0].(string)
		return state
	}
	for i := 0; i < 100 && running() != "RUNNING"; i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if got := running(); got != "RUNNING" {
		t.Fatalf("the statement never started: %q", got)
	}
	began := time.Now()
	halt()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("a stopped statement answered %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the statement did not stop when it was told to")
	}
	if took := time.Since(began); took > 3*time.Second {
		t.Errorf("stopping took %v", took)
	}
	state := ""
	for i := 0; i < 50; i++ {
		if state = running(); state == "FAILED" || state == "FINISHED" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if state != "FAILED" {
		t.Errorf("at the server the statement is %q, five seconds after it was stopped", state)
	}
	// And the tab it ran in still runs things.
	if rows := one(t, ss, `SELECT 7`); len(rows) != 1 || rows[0][0] != int64(7) {
		t.Errorf("after a stop, the session gave %v", rows)
	}
}

func TestLiveReadOnlyRefusesAWriteHere(t *testing.T) {
	seed(t)
	src := open(t, source.Guard{ReadOnly: true})
	ss := pinned(t, src)
	for _, sql := range []string{
		`INSERT INTO memory.ikigai.writes VALUES (1, 'a')`,
		`DELETE FROM memory.ikigai.writes WHERE id = 1`,
		`DROP TABLE memory.ikigai.writes`,
		`CALL system.runtime.kill_query(query_id => 'x', message => 'y')`,
		`EXPLAIN ANALYZE INSERT INTO memory.ikigai.writes VALUES (1, 'a')`,
		`EXECUTE something`,
	} {
		if _, err := ss.Query(context.Background(), source.Statement{SQL: sql, Confirmed: true}); !errors.Is(err, source.ErrReadOnly) {
			t.Errorf("%q was refused with %v, want it refused as a write", sql, err)
		}
	}
	if rows := one(t, ss, `SELECT count(*) FROM memory.ikigai.writes`); len(rows) != 1 || rows[0][0] != int64(0) {
		t.Errorf("a read on a read-only connection gave %v", rows)
	}
	// A plan is a read, and so is a plan that is run of something that reads.
	for _, sql := range []string{`EXPLAIN INSERT INTO memory.ikigai.writes VALUES (1, 'a')`, `EXPLAIN ANALYZE SELECT 1`} {
		if rows := one(t, ss, sql); len(rows) == 0 {
			t.Errorf("%q answered nothing", sql)
		}
	}
	// A script is refused whole, before any of it runs.
	_, err := src.QueryMulti(context.Background(),
		"SELECT 1; INSERT INTO memory.ikigai.writes VALUES (1, 'a')", source.ScriptOptions{Confirmed: true})
	if !errors.Is(err, source.ErrReadOnly) {
		t.Fatalf("the script was refused with %v", err)
	}
}

// A production connection asks before it changes anything (FR-4.9), and a
// write that is allowed says how many rows it wrote.
func TestLiveProductionAsksFirst(t *testing.T) {
	seed(t)
	src := open(t, source.Guard{Environment: source.EnvProduction})
	ss := pinned(t, src)
	ctx := context.Background()
	write := `INSERT INTO memory.ikigai.writes VALUES (1, 'a'), (2, 'b')`
	if _, err := ss.Query(ctx, source.Statement{SQL: write}); !errors.Is(err, source.ErrConfirmationRequired) {
		t.Errorf("a write on a production connection: %v", err)
	}
	res, err := ss.Query(ctx, source.Statement{SQL: write, Confirmed: true})
	if err != nil || res.Rows != nil || res.Affected != 2 {
		t.Errorf("a write with consent: %+v, %v", res, err)
	}
	plain := pinned(t, open(t, source.Guard{}))
	var unbounded *source.UnboundedError
	if _, err := plain.Query(ctx, source.Statement{SQL: `DELETE FROM memory.ikigai.writes`}); !errors.As(err, &unbounded) {
		t.Errorf("a delete of every row: %v", err)
	}
	if rows := one(t, plain, `SELECT count(*) FROM memory.ikigai.writes`); rows[0][0] != int64(2) {
		t.Errorf("after all that the table holds %v", rows)
	}
}

func TestLiveInfoSaysWhatItIs(t *testing.T) {
	s := open(t, source.Guard{})
	info, err := s.Info(context.Background())
	if err != nil || info.Product != "Trino" || info.Latency <= 0 {
		t.Fatalf("%+v, %v", info, err)
	}
	if n, err := strconv.Atoi(info.Version); err != nil || n < 400 {
		t.Errorf("it says it is version %q", info.Version)
	}
	if err := s.Ping(context.Background()); err != nil {
		t.Errorf("Ping: %v", err)
	}
	// A ping asks something, so a connection that cannot ask says so.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Ping(context.Background()); err == nil {
		t.Error("a closed connection answered a ping")
	}
	if _, err := s.Info(context.Background()); err == nil {
		t.Error("a closed connection said what it is")
	}
}

func TestLiveAWrongAddressSaysSo(t *testing.T) {
	open(t, source.Guard{}) // or skip
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cfg := settings("", source.Guard{})
	cfg.Port = 59
	if _, err := (Driver{}).Open(ctx, cfg); kindOf(err) != source.ConnectUnreachable {
		t.Errorf("a wrong port: %v", err)
	}
	cfg = settings("", source.Guard{})
	cfg.Host = "no-such-host.invalid"
	if _, err := (Driver{}).Open(ctx, cfg); kindOf(err) != source.ConnectUnreachable {
		t.Errorf("no such host: %v", err)
	}
	// Told to encrypt, against a server that does not: about encryption, not
	// about the address.
	cfg = settings("", source.Guard{})
	cfg.TLS.Mode = "require"
	if _, err := (Driver{}).Open(ctx, cfg); kindOf(err) != source.ConnectTLS {
		t.Errorf("encrypting to a server that does not: %v", err)
	}
}
