package cassandra

import (
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Where a query's pages ended, remembered (T2.51). A paging state is opaque
// to everything but the cluster, so what is tested here is the bookkeeping:
// which state a page resumes from, and how far it will walk.

func statement(sql string, args ...any) source.Statement {
	return source.Statement{SQL: sql, Args: args}
}

func TestAPageResumesWhereTheOneBeforeItEnded(t *testing.T) {
	var p pageStates
	st := statement("SELECT * FROM t")

	// Nothing is remembered yet: the first page begins at the beginning, and
	// nothing is walked past.
	state, skip, err := p.resume(st, 0)
	if err != nil || state != nil || skip != 0 {
		t.Fatalf("the first page: %v %d %v", state, skip, err)
	}
	p.reached(st, 256, []byte("after-256"))
	p.reached(st, 512, []byte("after-512"))

	// A page that begins where one ended resumes from it exactly.
	state, skip, err = p.resume(st, 512)
	if err != nil || string(state) != "after-512" || skip != 0 {
		t.Errorf("the page at 512: %q %d %v", state, skip, err)
	}
	// One that begins between two walks from the nearer, and only that far.
	state, skip, err = p.resume(st, 600)
	if err != nil || string(state) != "after-512" || skip != 88 {
		t.Errorf("the page at 600: %q %d %v", state, skip, err)
	}
	// One before anything remembered is walked to from the beginning.
	state, skip, err = p.resume(st, 100)
	if err != nil || state != nil || skip != 100 {
		t.Errorf("the page at 100: %q %d %v", state, skip, err)
	}
}

func TestAJumpFurtherThanItWillWalkIsRefused(t *testing.T) {
	var p pageStates
	st := statement("SELECT * FROM t")

	// From nothing, a page within the walk is walked to.
	if _, skip, err := p.resume(st, walkLimit); err != nil || skip != walkLimit {
		t.Errorf("a page at the end of the walk: %d %v", skip, err)
	}
	// A row further is not: it would read a table to draw a screen.
	_, _, err := p.resume(st, walkLimit+1)
	if err == nil {
		t.Fatal("a page past the walk was answered")
	}
	if !strings.Contains(err.Error(), "forward") {
		t.Errorf("the refusal says %q", err)
	}
	// Reaching a page brings the ones after it back within reach.
	p.reached(st, walkLimit, []byte("far"))
	if state, skip, err := p.resume(st, walkLimit+1); err != nil || string(state) != "far" || skip != 1 {
		t.Errorf("the page after the furthest read: %q %d %v", state, skip, err)
	}
}

func TestAStateBelongsToTheQueryItCameFrom(t *testing.T) {
	var p pageStates
	one := statement("SELECT * FROM t WHERE a = ?", 1)
	another := statement("SELECT * FROM t WHERE a = ?", 2)
	p.reached(one, 256, []byte("one"))

	if state, _, _ := p.resume(one, 256); string(state) != "one" {
		t.Errorf("its own query resumes from %q", state)
	}
	// The same text with other values is another query, and knows nothing.
	if state, skip, _ := p.resume(another, 256); state != nil || skip != 256 {
		t.Errorf("another query resumes from %q, %d", state, skip)
	}
	if state, skip, _ := p.resume(statement("SELECT * FROM other"), 256); state != nil || skip != 256 {
		t.Errorf("another statement resumes from %q, %d", state, skip)
	}
}

func TestWhatIsRememberedIsBounded(t *testing.T) {
	var p pageStates
	st := statement("SELECT * FROM t")
	for i := int64(1); i <= statesKept+1; i++ {
		p.reached(st, i*256, []byte("state"))
	}
	if n := p.known(); n > statesKept {
		t.Errorf("%d states kept, and the bound is %d", n, statesKept)
	}
	// Nothing is remembered about a page that ended nowhere, or about the
	// beginning, which is not a resuming point.
	p.reached(st, 0, []byte("state"))
	p.reached(st, 900, nil)
	if state, skip, _ := p.resume(st, 900); state != nil || skip != 900 {
		t.Errorf("a page that ended nowhere resumes from %q, walking %d", state, skip)
	}
}

func TestRowsAreOrderedOnlyByWhatClustersThem(t *testing.T) {
	col := func(name, kind string) model.Column {
		return model.Column{Name: name, Attrs: map[string]string{"kind": kind}}
	}
	cols := []model.Column{
		col("country", partitionKey), col("id", clusteringKey), col("name", "regular"),
	}
	if err := orderable(nil, cols); err != nil {
		t.Errorf("no order at all: %v", err)
	}
	if err := orderable([]source.Sort{{Column: "id", Descending: true}}, cols); err != nil {
		t.Errorf("ordered by what clusters the rows: %v", err)
	}
	for _, name := range []string{"country", "name", "nothing"} {
		err := orderable([]source.Sort{{Column: name}}, cols)
		if err == nil {
			t.Errorf("rows were ordered by %s", name)
			continue
		}
		// The refusal says what they can be ordered by.
		if !strings.Contains(err.Error(), "id") {
			t.Errorf("ordering by %s says %q", name, err)
		}
	}
	// A table whose rows have no order of their own says that instead.
	flat := []model.Column{col("id", partitionKey), col("name", "regular")}
	if err := orderable([]source.Sort{{Column: "name"}}, flat); err == nil ||
		!strings.Contains(err.Error(), "no order") {
		t.Errorf("a table with nothing clustering its rows: %v", err)
	}
}

func TestARowIsAddressedByItsKey(t *testing.T) {
	ref := model.NewRef(model.KindTable, "shop", "people")
	col := func(name, kind string) model.Column {
		return model.Column{Name: name, Attrs: map[string]string{"kind": kind}}
	}
	id := identityOf(ref, []model.Column{
		col("country", partitionKey), col("id", clusteringKey), col("name", "regular")})
	if !id.Editable() || strings.Join(id.Columns, ", ") != "country, id" || !id.Target.Equal(ref) {
		t.Errorf("a row is addressed by %+v", id)
	}
	// A table with no key at all addresses nothing, which is what a view of
	// somebody else's rows can look like.
	if id := identityOf(ref, []model.Column{col("name", "regular")}); id.Editable() {
		t.Errorf("rows with no key are addressed by %+v", id)
	}
}
