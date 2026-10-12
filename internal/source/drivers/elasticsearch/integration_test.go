//go:build conformance

package elasticsearch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/conformance"
)

// The live suite, against OpenSearch with its security plugin off (T5.15):
//
//	docker run -d --name ikigai-opensearch -p 59200:9200 \
//	  -e discovery.type=single-node -e DISABLE_SECURITY_PLUGIN=true \
//	  -e DISABLE_INSTALL_DEMO_CONFIG=true \
//	  -e OPENSEARCH_JAVA_OPTS="-Xms512m -Xmx512m" opensearchproject/opensearch:2
//
// IKIGAI_REQUIRE_OPENSEARCH=1 turns a skip into a failure, which is what the
// gate sets. What it cannot prove is anything about Elasticsearch itself, or
// about a server that asks who is calling: this one takes anybody. A refused
// password, and a server that is something else, are held by tests over a
// server made for the purpose (source_test.go), and are said to be that in
// ADR-0173.

const people = "ikigai_people"

func required() bool { return os.Getenv("IKIGAI_REQUIRE_OPENSEARCH") != "" }

func port() int {
	if v, err := strconv.Atoi(os.Getenv("IKIGAI_OPENSEARCH_PORT")); err == nil {
		return v
	}
	return 59200
}

func config() source.ConnectionConfig {
	return source.ConnectionConfig{DriverID: driverID, Host: "127.0.0.1", Port: port(),
		TLS: source.TLSConfig{Mode: "disable"}}
}

// open connects as the application would.
func open(t *testing.T) *esSource {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	src, err := Driver{}.Open(ctx, config())
	if err != nil {
		if required() {
			t.Fatalf("OpenSearch required but unavailable: %v", err)
		}
		t.Skipf("no OpenSearch on port %d: %v", port(), err)
	}
	t.Cleanup(func() { _ = src.Close() })
	return src.(*esSource)
}

func ref(name string) model.ObjectRef { return model.NewRef(model.KindCollection, name) }

// person is one seeded document, kept so that every filter can be checked
// against the same question asked here.
type person struct {
	id     int64
	name   string
	bio    string
	city   string
	score  float64
	born   time.Time
	active bool
	tags   []string // on every third
}

var cities = []string{"Lagos", "Kano", "Ibadan", "Abuja"}

func seeded() []person {
	out := make([]person, 0, 100)
	for i := int64(1); i <= 100; i++ {
		p := person{
			id: i, name: fmt.Sprintf("Person %d", i), city: cities[i%4],
			bio:   fmt.Sprintf("writes about rivers and bridge number %d", i),
			score: float64(i) * 1.5, active: i%2 == 0,
			born: time.Date(1950+int(i)%50, time.Month(1+i%12), 1+int(i)%28, 12, 0, 0, 0, time.UTC),
		}
		if i%3 == 0 {
			p.tags = []string{"a", "b"}
		}
		if i%10 == 0 {
			p.bio = "keeps bees"
		}
		out = append(out, p)
	}
	return out
}

// seed makes the indices the suite reads. Deleted and made again rather than
// made fresh: an index's mapping cannot be changed once it has one.
//
// The mapping is written out rather than left to the engine, because what a
// field is decides what a filter on it becomes, and the suite has to know.
func seed(t *testing.T) *esSource {
	t.Helper()
	s := open(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	must := func(method, path string, body any) {
		t.Helper()
		if err := s.c.ask(ctx, method, path, nil, body, nil); err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
	}
	for _, name := range []string{people, "ikigai_empty"} {
		if err := s.c.ask(ctx, http.MethodDelete, "/"+name, nil, nil, nil); err != nil && !notFound(err) {
			t.Fatalf("removing %s: %v", name, err)
		}
	}
	type m = map[string]any
	must(http.MethodPut, "/ikigai_empty", m{"settings": m{"number_of_shards": 1, "number_of_replicas": 0}})
	must(http.MethodPut, "/"+people, m{
		"settings": m{"number_of_shards": 1, "number_of_replicas": 0},
		"mappings": m{"properties": m{
			"id":     m{"type": "long"},
			"name":   m{"type": "text", "fields": m{"keyword": m{"type": "keyword"}}},
			"bio":    m{"type": "text"},
			"city":   m{"type": "keyword"},
			"score":  m{"type": "double"},
			"born":   m{"type": "date"},
			"active": m{"type": "boolean"},
			"big":    m{"type": "unsigned_long"},
			"tags":   m{"type": "keyword"},
			"address": m{"properties": m{
				"street": m{"type": "keyword"},
				"geo":    m{"type": "geo_point"},
			}},
			"notes": m{"type": "nested", "properties": m{
				"by": m{"type": "keyword"},
			}},
		}},
	})
	for _, p := range seeded() {
		doc := m{
			"id": p.id, "name": p.name, "bio": p.bio, "city": p.city, "score": p.score,
			"born": p.born.Format(time.RFC3339), "active": p.active,
			"address": m{"street": fmt.Sprintf("%d River Road", p.id), "geo": m{"lat": 6.5, "lon": 3.4}},
		}
		if p.tags != nil {
			doc["tags"] = p.tags
			doc["notes"] = []m{{"by": "ada"}, {"by": "bola"}}
		}
		if p.id == 1 {
			// More than an int64 holds, which is what the type is for.
			doc["big"] = jsonNumber("18446744073709551615")
		}
		must(http.MethodPut, "/"+people+"/_doc/p"+strconv.FormatInt(p.id, 10), doc)
	}
	must(http.MethodPost, "/"+people+"/_refresh", nil)
	return s
}

func TestConformance(t *testing.T) {
	seed(t)
	conformance.Run(t, conformance.Target{
		Name:      "opensearch",
		Open:      func(_ context.Context, t *testing.T) source.Source { return open(t) },
		Browsable: ref(people),
		// No Writable: this reads and does not write (ADR-0173). No
		// Conditions: the WHERE checks need a query language, and this
		// source has claimed none, so they skip; that a typed condition is
		// refused rather than ignored is held by a test of this driver's own.
	})
}

// read is every row a browse answers, by column name.
func read(t *testing.T, s *esSource, opt source.BrowseOptions) []map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := s.Browse(ctx, ref(people), opt)
	if err != nil {
		t.Fatalf("Browse(%+v): %v", opt, err)
	}
	defer st.Close()
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

func ids(rows []map[string]any) []int64 {
	out := make([]int64, 0, len(rows))
	for _, r := range rows {
		id, _ := r["id"].(int64)
		out = append(out, id)
	}
	return out
}

// A value comes back as what it is: an integer whole, a date as a time, a
// number too long for an integer as its digits, several values as several, and
// a field a document has not got as nothing.
func TestLiveAValueIsWhatItIs(t *testing.T) {
	s := seed(t)
	rows := read(t, s, source.BrowseOptions{Limit: 200, Sorts: []source.Sort{{Column: "id"}}})
	if len(rows) != 100 {
		t.Fatalf("read %d documents", len(rows))
	}
	one, three := rows[0], rows[2]
	want := seeded()[0]
	for name, v := range map[string]any{
		"_id": "p1", "id": int64(1), "name": want.name, "bio": want.bio, "city": want.city,
		"score": 1.5, "born": want.born, "active": false,
		"big":            model.Decimal("18446744073709551615"),
		"address.street": "1 River Road",
		"tags":           nil, "notes.by": nil,
	} {
		if got := one[name]; !reflect.DeepEqual(got, v) {
			t.Errorf("%s is %#v (%T), want %#v (%T)", name, got, got, v, v)
		}
	}
	if got := three["tags"]; !reflect.DeepEqual(got, []any{"a", "b"}) {
		t.Errorf("tags are %#v", got)
	}
	if got := three["notes.by"]; !reflect.DeepEqual(got, []any{"ada", "bola"}) {
		t.Errorf("a field of a nested object is %#v, want every object's", got)
	}
	if got, _ := one["address.geo"].(map[string]any); !reflect.DeepEqual(got, map[string]any{"lat": 6.5, "lon": 3.4}) {
		t.Errorf("a place is %#v", one["address.geo"])
	}
	doc, _ := one["_source"].(map[string]any)
	if doc["id"] != int64(1) || doc["name"] != want.name {
		t.Errorf("the document itself is %#v", one["_source"])
	}
	if three["big"] != nil {
		t.Errorf("a field a document has not got is %#v", three["big"])
	}
}

// One page follows another: every document once, none twice, in the order
// asked for and in no order at all.
func TestLivePagingDoesNotRepeatItself(t *testing.T) {
	s := seed(t)
	for name, sorts := range map[string][]source.Sort{
		"unsorted": nil,
		"by city":  {{Column: "city"}}, // which four documents in five tie on
	} {
		seen := map[int64]int{}
		for off := int64(0); off < 120; off += 30 {
			for _, id := range ids(read(t, s, source.BrowseOptions{Offset: off, Limit: 30, Sorts: sorts})) {
				seen[id]++
			}
		}
		if len(seen) != 100 {
			t.Errorf("%s: %d different documents over the pages, want 100", name, len(seen))
		}
		for id, n := range seen {
			if n != 1 {
				t.Errorf("%s: document %d was read %d times", name, id, n)
			}
		}
	}
}

// A sort is the engine's, on the whole index and not on the page.
func TestLiveASortIsTheServers(t *testing.T) {
	s := seed(t)
	desc := ids(read(t, s, source.BrowseOptions{Limit: 3, Sorts: []source.Sort{{Column: "score", Descending: true}}}))
	if !reflect.DeepEqual(desc, []int64{100, 99, 98}) {
		t.Errorf("highest score first: %v", desc)
	}
	// name is analysed text with a keyword beside it, and is sorted through
	// the keyword: as text, so "Person 10" follows "Person 1".
	byName := ids(read(t, s, source.BrowseOptions{Limit: 3, Sorts: []source.Sort{{Column: "name"}}}))
	if !reflect.DeepEqual(byName, []int64{1, 10, 100}) {
		t.Errorf("by name: %v", byName)
	}
	two := ids(read(t, s, source.BrowseOptions{Limit: 3,
		Sorts: []source.Sort{{Column: "city"}, {Column: "id", Descending: true}}}))
	if !reflect.DeepEqual(two, []int64{99, 95, 91}) { // Abuja is first, and is every fourth from 3
		t.Errorf("by city, then id downwards: %v", two)
	}
	// A document without the field goes last unless it is asked for first.
	last := read(t, s, source.BrowseOptions{Limit: 1, Sorts: []source.Sort{{Column: "tags"}}})
	first := read(t, s, source.BrowseOptions{Limit: 1, Sorts: []source.Sort{{Column: "tags", NullsFirst: true}}})
	if last[0]["tags"] == nil || first[0]["tags"] != nil {
		t.Errorf("without NullsFirst %#v came first, with it %#v", last[0]["tags"], first[0]["tags"])
	}
}

// What cannot be sorted by is refused, with the reason, before the engine is
// asked: a grid under a sort arrow it did not get is worse than one refused.
func TestLiveWhatHasNoOrderIsNotSortedBy(t *testing.T) {
	s := seed(t)
	for col, says := range map[string]string{
		"bio": "analysed text", "_id": "_id", "notes.by": "nested", "nowhere": "not a field", "_source": "whole document",
	} {
		_, err := s.Browse(context.Background(), ref(people), source.BrowseOptions{Limit: 1, Sorts: []source.Sort{{Column: col}}})
		if err == nil || !strings.Contains(err.Error(), says) {
			t.Errorf("a sort by %s: %v, want a refusal mentioning %q", col, err, says)
		}
	}
}

// Every filter the grid can build selects exactly the documents the same
// question selects when it is asked here, of the documents as they were
// written. That is the whole claim of filtering on the server (REQ-DRV-3):
// not that a filter is accepted, but that it means what it says.
func TestLiveEveryFilterMeansWhatItSays(t *testing.T) {
	s := seed(t)
	all := seeded()
	cutoff := time.Date(1975, 1, 1, 0, 0, 0, 0, time.UTC)
	lower := strings.ToLower
	cases := []struct {
		name string
		f    source.Filter
		want func(person) bool
	}{
		{"a keyword equal", source.Filter{Column: "city", Op: source.OpEqual, Values: []any{"Kano"}},
			func(p person) bool { return p.city == "Kano" }},
		{"a keyword is its case", source.Filter{Column: "city", Op: source.OpEqual, Values: []any{"kano"}},
			func(person) bool { return false }},
		{"not equal", source.Filter{Column: "city", Op: source.OpNotEqual, Values: []any{"Kano"}},
			func(p person) bool { return p.city != "Kano" }},
		{"a number equal", source.Filter{Column: "id", Op: source.OpEqual, Values: []any{int64(7)}},
			func(p person) bool { return p.id == 7 }},
		{"a boolean equal", source.Filter{Column: "active", Op: source.OpEqual, Values: []any{true}},
			func(p person) bool { return p.active }},
		{"text with a keyword beside it, equal to the whole", source.Filter{Column: "name", Op: source.OpEqual, Values: []any{"Person 1"}},
			func(p person) bool { return p.name == "Person 1" }},
		{"analysed text, equal to its words", source.Filter{Column: "bio", Op: source.OpEqual, Values: []any{"Keeps BEES"}},
			func(p person) bool { return p.bio == "keeps bees" }},
		{"less", source.Filter{Column: "score", Op: source.OpLess, Values: []any{15.0}},
			func(p person) bool { return p.score < 15 }},
		{"less or equal", source.Filter{Column: "score", Op: source.OpLessEqual, Values: []any{15.0}},
			func(p person) bool { return p.score <= 15 }},
		{"greater", source.Filter{Column: "id", Op: source.OpGreater, Values: []any{int64(90)}},
			func(p person) bool { return p.id > 90 }},
		{"greater or equal", source.Filter{Column: "id", Op: source.OpGreaterEqual, Values: []any{int64(90)}},
			func(p person) bool { return p.id >= 90 }},
		{"a time compared", source.Filter{Column: "born", Op: source.OpLess, Values: []any{cutoff}},
			func(p person) bool { return p.born.Before(cutoff) }},
		{"between, both ends in", source.Filter{Column: "id", Op: source.OpBetween, Values: []any{int64(10), int64(20)}},
			func(p person) bool { return p.id >= 10 && p.id <= 20 }},
		{"one of", source.Filter{Column: "city", Op: source.OpIn, Values: []any{"Kano", "Lagos"}},
			func(p person) bool { return p.city == "Kano" || p.city == "Lagos" }},
		{"none of", source.Filter{Column: "city", Op: source.OpNotIn, Values: []any{"Kano", "Lagos"}},
			func(p person) bool { return p.city != "Kano" && p.city != "Lagos" }},
		{"has none", source.Filter{Column: "tags", Op: source.OpIsNull},
			func(p person) bool { return p.tags == nil }},
		{"has one", source.Filter{Column: "tags", Op: source.OpIsNotNull},
			func(p person) bool { return p.tags != nil }},
		{"equal to nothing", source.Filter{Column: "tags", Op: source.OpEqual, Values: []any{nil}},
			func(p person) bool { return p.tags == nil }},
		{"not equal to nothing", source.Filter{Column: "tags", Op: source.OpNotEqual, Values: []any{nil}},
			func(p person) bool { return p.tags != nil }},
		{"any of several values equal", source.Filter{Column: "tags", Op: source.OpEqual, Values: []any{"b"}},
			func(p person) bool { return p.tags != nil }},
		{"contains, whatever the case", source.Filter{Column: "name", Op: source.OpContains, Values: []any{"SON 1"}},
			func(p person) bool { return strings.Contains(lower(p.name), "son 1") }},
		{"contains a wildcard's own character", source.Filter{Column: "name", Op: source.OpContains, Values: []any{"*"}},
			func(person) bool { return false }},
		{"analysed text contains its words", source.Filter{Column: "bio", Op: source.OpContains, Values: []any{"rivers and bridge"}},
			func(p person) bool { return strings.Contains(p.bio, "rivers and bridge") }},
		{"analysed text does not contain part of a word", source.Filter{Column: "bio", Op: source.OpContains, Values: []any{"river"}},
			func(person) bool { return false }},
		{"like, a prefix", source.Filter{Column: "name", Op: source.OpLike, Values: []any{"Person 1%"}},
			func(p person) bool { return strings.HasPrefix(p.name, "Person 1") }},
		{"like, one character", source.Filter{Column: "name", Op: source.OpLike, Values: []any{"Person _"}},
			func(p person) bool { return len(p.name) == len("Person 1") }},
		{"like is its case", source.Filter{Column: "name", Op: source.OpLike, Values: []any{"person 1%"}},
			func(person) bool { return false }},
		{"not like", source.Filter{Column: "name", Op: source.OpNotLike, Values: []any{"Person 1%"}},
			func(p person) bool { return !strings.HasPrefix(p.name, "Person 1") }},
		{"a regular expression", source.Filter{Column: "city", Op: source.OpRegex, Values: []any{"(Kano|Abu.*)"}},
			func(p person) bool { return p.city == "Kano" || p.city == "Abuja" }},
		{"negated", source.Filter{Column: "id", Op: source.OpBetween, Values: []any{int64(10), int64(20)}, Negate: true},
			func(p person) bool { return p.id < 10 || p.id > 20 }},
		{"by identity", source.Filter{Column: "_id", Op: source.OpEqual, Values: []any{"p42"}},
			func(p person) bool { return p.id == 42 }},
		{"a field inside an object", source.Filter{Column: "address.street", Op: source.OpEqual, Values: []any{"5 River Road"}},
			func(p person) bool { return p.id == 5 }},
	}
	ctx := context.Background()
	for _, c := range cases {
		var want []int64
		for _, p := range all {
			if c.want(p) {
				want = append(want, p.id)
			}
		}
		opt := source.BrowseOptions{Limit: 200, Filters: []source.Filter{c.f}, Sorts: []source.Sort{{Column: "id"}}}
		got := ids(read(t, s, opt))
		if len(got)+len(want) > 0 && !reflect.DeepEqual(got, want) {
			t.Errorf("%s: selected %v, want %v", c.name, got, want)
		}
		// And the count is of the same documents.
		if n, err := s.Count(ctx, ref(people), opt); err != nil || n != int64(len(want)) {
			t.Errorf("%s: counted %d, %v; want %d", c.name, n, err, len(want))
		}
	}

	// Two filters are both.
	both := ids(read(t, s, source.BrowseOptions{Limit: 200, Sorts: []source.Sort{{Column: "id"}}, Filters: []source.Filter{
		{Column: "city", Op: source.OpEqual, Values: []any{"Kano"}},
		{Column: "id", Op: source.OpLess, Values: []any{int64(10)}},
	}}))
	if !reflect.DeepEqual(both, []int64{1, 5, 9}) {
		t.Errorf("two filters selected %v", both)
	}
}

// What a filter cannot mean here is refused rather than meaning something
// near it.
func TestLiveAFilterThatCannotMeanItIsRefused(t *testing.T) {
	s := seed(t)
	ctx := context.Background()
	for name, c := range map[string]struct {
		opt  source.BrowseOptions
		says string
	}{
		"ordering analysed text": {source.BrowseOptions{Filters: []source.Filter{
			{Column: "bio", Op: source.OpGreater, Values: []any{"a"}}}}, "analysed text"},
		"a pattern across analysed text": {source.BrowseOptions{Filters: []source.Filter{
			{Column: "bio", Op: source.OpLike, Values: []any{"keeps%"}}}}, "analysed text"},
		"inside a nested object": {source.BrowseOptions{Filters: []source.Filter{
			{Column: "notes.by", Op: source.OpEqual, Values: []any{"ada"}}}}, "nested"},
		"a place compared": {source.BrowseOptions{Filters: []source.Filter{
			{Column: "address.geo", Op: source.OpEqual, Values: []any{"x"}}}}, "cannot be compared"},
		"the whole document": {source.BrowseOptions{Filters: []source.Filter{
			{Column: "_source", Op: source.OpContains, Values: []any{"x"}}}}, "whole document"},
		"a field that is not one": {source.BrowseOptions{Filters: []source.Filter{
			{Column: "nowhere", Op: source.OpEqual, Values: []any{"x"}}}}, "not a field"},
		"a column that is not one": {source.BrowseOptions{Columns: []string{"id", "nowhere"}}, "not a field"},
		"a typed condition":        {source.BrowseOptions{Where: "city:Kano"}, "typed condition"},
		"following":                {source.BrowseOptions{Follow: true}, "cannot be followed"},
	} {
		c.opt.Limit = 5
		if st, err := s.Browse(ctx, ref(people), c.opt); err == nil {
			st.Close()
			t.Errorf("%s was accepted", name)
		} else if !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: %v, want it to mention %q", name, err, c.says)
		}
		if len(c.opt.Filters) > 0 || c.opt.Where != "" {
			if n, err := s.Count(ctx, ref(people), c.opt); err == nil {
				t.Errorf("%s was counted: %d", name, n)
			}
		}
	}
}

// Only the columns asked for, in the order asked.
func TestLiveOnlyTheColumnsAskedFor(t *testing.T) {
	s := seed(t)
	ctx := context.Background()
	st, err := s.Browse(ctx, ref(people), source.BrowseOptions{Limit: 1, Columns: []string{"city", "_id", "id"},
		Sorts: []source.Sort{{Column: "id"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var names []string
	for _, c := range st.Columns() {
		names = append(names, c.Name)
	}
	row, err := st.Next(ctx)
	if err != nil || !reflect.DeepEqual(names, []string{"city", "_id", "id"}) ||
		!reflect.DeepEqual(row, model.Row{"Kano", "p1", int64(1)}) {
		t.Errorf("columns %v, row %#v, %v", names, row, err)
	}
}

// The engine will not page past its result window, and what it says about that
// is about a setting. What this says is about what was asked.
func TestLiveDeepPagingIsRefusedInItsOwnTerms(t *testing.T) {
	s := seed(t)
	_, err := s.Browse(context.Background(), ref(people), source.BrowseOptions{Offset: 10000, Limit: 10})
	if err == nil || !strings.Contains(err.Error(), "documents 10001 to 10010") ||
		!strings.Contains(err.Error(), "result window") {
		t.Errorf("a page past the window: %v", err)
	}
	// And the last page inside it is read, there being nothing on it.
	if rows := read(t, s, source.BrowseOptions{Offset: 9990, Limit: 10}); len(rows) != 0 {
		t.Errorf("the last page inside the window held %d documents", len(rows))
	}
}

func TestLiveTheTree(t *testing.T) {
	s := seed(t)
	ctx := context.Background()
	roots, err := s.Root(ctx)
	if err != nil || len(roots) != 1 {
		t.Fatalf("Root: %v, %v", roots, err)
	}
	class := roots[0]
	if k, ok := model.ClassOf(class.Ref); !ok || k != model.KindCollection || !class.HasChildren {
		t.Fatalf("the root is %+v", class)
	}
	kids, err := s.Children(ctx, class.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := strconv.Atoi(class.Badge.Text); n != len(kids) {
		t.Errorf("the class says %s and holds %d", class.Badge.Text, len(kids))
	}
	var names []string
	byName := map[string]model.Node{}
	for _, k := range kids {
		names = append(names, k.Label)
		byName[k.Label] = k
		if strings.HasPrefix(k.Label, ".") {
			t.Errorf("%s is the engine's own and is shown", k.Label)
		}
	}
	if !sort.StringsAreSorted(names) {
		t.Errorf("the indices are not in order: %v", names)
	}
	p, ok := byName[people]
	if !ok || !p.Browsable || !p.Describable || p.HasChildren || p.Ref.String() != ref(people).String() {
		t.Fatalf("%s is %+v", people, p)
	}
	if p.Badge != nil {
		// What the engine's own listing says is 166: it counts the objects
		// in a nested field as documents.
		t.Errorf("it is badged %+v before anything has counted", p.Badge)
	}
	// An index has nothing under it.
	if under, err := s.Children(ctx, p.Ref); err != nil || len(under) != 0 {
		t.Errorf("under an index: %v, %v", under, err)
	}
	b, ok, err := s.Badge(ctx, p.Ref)
	if err != nil || !ok || b.Text != "100" || !b.Exact {
		t.Errorf("counted: %+v, %v, %v", b, ok, err)
	}
	if _, ok, err := s.Badge(ctx, class.Ref); ok || err != nil {
		t.Errorf("a class was counted: %v, %v", ok, err)
	}
}

// An index's structure is its mapping, and how many documents hold each field
// is counted over all of them rather than guessed from some.
func TestLiveAnIndexReportsItsStructure(t *testing.T) {
	s := seed(t)
	got, err := s.Describe(context.Background(), ref(people))
	if err != nil {
		t.Fatal(err)
	}
	c, ok := got.(*model.Collection)
	if !ok || c.Name != people || c.DocumentsEstimate != 100 || c.Shape == nil || c.Shape.Sampled != 100 {
		t.Fatalf("described as %#v", got)
	}
	type seen struct {
		native   string
		class    model.TypeClass
		presence float64
	}
	fields := map[string]seen{}
	var order []string
	for _, f := range c.Shape.Fields {
		order = append(order, f.Name)
		fields[f.Name] = seen{f.Types[0].Type.Native, f.Types[0].Type.Class, f.Presence}
		// How many hold it is said as a number too, and the two agree.
		if want := int64(f.Presence*100 + 0.5); f.Types[0].Count != want {
			t.Errorf("%s is in %d documents and in %v of a hundred", f.Name, f.Types[0].Count, f.Presence)
		}
	}
	if !sort.StringsAreSorted(order) {
		t.Errorf("the fields are not in order: %v", order)
	}
	for name, want := range map[string]seen{
		"id":             {"long", model.TypeInteger, 1},
		"name":           {"text", model.TypeString, 1},
		"score":          {"double", model.TypeFloat, 1},
		"born":           {"date", model.TypeTimestamp, 1},
		"active":         {"boolean", model.TypeBool, 1},
		"big":            {"unsigned_long", model.TypeInteger, 0.01},
		"tags":           {"keyword", model.TypeString, 0.33},
		"address.street": {"keyword", model.TypeString, 1},
		"address.geo":    {"geo_point", model.TypeJSON, 1},
		"notes.by":       {"keyword", model.TypeString, 0.33},
	} {
		if got, ok := fields[name]; !ok || got != want {
			t.Errorf("%s is %+v, want %+v", name, got, want)
		}
	}
	if _, ok := fields["name.keyword"]; ok {
		t.Error("the keyword beside a text is a field of its own")
	}
	for k, want := range map[string]string{"number_of_shards": "1", "number_of_replicas": "0", "health": "green", "status": "open"} {
		if c.Attrs[k] != want {
			t.Errorf("%s is %q, want %q", k, c.Attrs[k], want)
		}
	}
	if c.Attrs["size"] == "" {
		t.Error("how much room it takes is not said")
	}

	// An index nothing was written to has no fields, and is still described.
	empty, err := s.Describe(context.Background(), ref("ikigai_empty"))
	if e, _ := empty.(*model.Collection); err != nil || e == nil || len(e.Shape.Fields) != 0 || e.DocumentsEstimate != 0 {
		t.Errorf("an empty index: %#v, %v", empty, err)
	}
	if rows, err := s.Browse(context.Background(), ref("ikigai_empty"), source.BrowseOptions{Limit: 5}); err != nil {
		t.Errorf("an empty index is not read: %v", err)
	} else {
		if cols := rows.Columns(); len(cols) != 2 || cols[0].Name != idColumn || cols[1].Name != sourceColumn {
			t.Errorf("its columns are %v", cols)
		}
		rows.Close()
	}
}

func TestLiveInfoSaysWhatItIs(t *testing.T) {
	s := open(t)
	info, err := s.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Product != "OpenSearch" || !strings.HasPrefix(info.Version, "2.") || info.Latency <= 0 {
		t.Errorf("it says it is %+v", info)
	}
	if info.Attrs["cluster"] == "" || info.Attrs["node"] == "" || info.Attrs["health"] == "" || info.Attrs["nodes"] != "1" {
		t.Errorf("about itself: %v", info.Attrs)
	}
	if err := s.Ping(context.Background()); err != nil {
		t.Errorf("Ping: %v", err)
	}
}

func TestLiveNoSuchIndex(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	gone := ref("ikigai_no_such_index")
	if _, err := s.Browse(ctx, gone, source.BrowseOptions{Limit: 1}); err == nil || !strings.Contains(err.Error(), "index not found") {
		t.Errorf("reading it: %v", err)
	}
	if _, err := s.Describe(ctx, gone); err == nil || !strings.Contains(err.Error(), "index not found") {
		t.Errorf("describing it: %v", err)
	}
	if _, err := s.Count(ctx, gone, source.BrowseOptions{}); err == nil {
		t.Error("it was counted")
	}
	// And something that is not an index at all is not asked about.
	for _, bad := range []model.ObjectRef{model.NewRef(model.KindTable, people), model.NewRef(model.KindCollection, "a", "b"), {}} {
		if _, err := s.Browse(ctx, bad, source.BrowseOptions{Limit: 1}); err == nil || !strings.Contains(err.Error(), "not an index") {
			t.Errorf("reading %v: %v", bad, err)
		}
		if _, err := s.Describe(ctx, bad); err == nil {
			t.Errorf("describing %v was answered", bad)
		}
		if _, err := s.Count(ctx, bad, source.BrowseOptions{}); err == nil {
			t.Errorf("counting %v was answered", bad)
		}
	}
}

func TestLiveAWrongAddressSaysSo(t *testing.T) {
	cfg := config()
	cfg.Port = 59 // nothing listens there
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := Driver{}.Open(ctx, cfg)
	var ce *source.ConnectError
	if !errors.As(err, &ce) || ce.Kind != source.ConnectUnreachable {
		t.Errorf("a wrong port: %v", err)
	}
	// Told to encrypt, against a server that does not: about encryption, not
	// about the address.
	cfg = config()
	cfg.TLS.Mode = "require"
	_, err = Driver{}.Open(ctx, cfg)
	if !errors.As(err, &ce) || ce.Kind != source.ConnectTLS {
		t.Errorf("encrypting to a server that does not: %v", err)
	}
}

// jsonNumber is a number written into a document as its digits.
type jsonNumber string

func (n jsonNumber) MarshalJSON() ([]byte, error) { return []byte(n), nil }
