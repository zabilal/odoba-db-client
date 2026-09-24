package cockroach

import (
	"slices"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Paging ends its sort with the key, so that a page is a page: LIMIT and
// OFFSET over an incompletely ordered result may return the same row twice
// and skip another entirely.
func TestPagingEndsItsSortWithTheKey(t *testing.T) {
	got := tiebreak([]source.Sort{{Column: "status"}}, []string{"id"})
	if !slices.Equal(got, []source.Sort{{Column: "status"}, {Column: "id"}}) {
		t.Errorf("the sort reads %+v", got)
	}
	// A key column somebody is already sorting by is not added twice: the
	// order they asked for is the order they get.
	got = tiebreak([]source.Sort{{Column: "id", Descending: true}}, []string{"id"})
	if !slices.Equal(got, []source.Sort{{Column: "id", Descending: true}}) {
		t.Errorf("the key was added again: %+v", got)
	}
	// A key of two columns adds both, in the order it was declared.
	got = tiebreak(nil, []string{"b", "a"})
	if !slices.Equal(got, []source.Sort{{Column: "b"}, {Column: "a"}}) {
		t.Errorf("a key of two columns reads %+v", got)
	}
	// Nothing to add is nothing added: a view has no key.
	if got := tiebreak([]source.Sort{{Column: "a"}}, nil); !slices.Equal(got,
		[]source.Sort{{Column: "a"}}) {
		t.Errorf("a sort without a key reads %+v", got)
	}
}

// A row can be written only when every column that addresses it is on the
// screen (FR-4.7).
func TestARowIsEditableOnlyWhenItIsAddressed(t *testing.T) {
	tbl := model.NewRef(model.KindTable, "shop", "public", "orders")
	key := tableKey{columns: []string{"id"}}

	id := identityFor(tbl, key, nil)
	if id.Kind != model.IdentityPrimaryKey || !slices.Equal(id.Columns, []string{"id"}) {
		t.Fatalf("a table with a key is addressed as %+v", id)
	}
	if id.Target.String() != tbl.String() {
		t.Errorf("the identity points at %s", id.Target)
	}
	// The key is in the projection, so the rows can be written.
	if got := identityFor(tbl, key, []string{"id", "total"}); got.Kind != model.IdentityPrimaryKey {
		t.Errorf("a projection holding the key is addressed as %v", got.Kind)
	}
	// It is not, so they cannot.
	if got := identityFor(tbl, key, []string{"total"}); got.Kind != model.IdentityNone {
		t.Errorf("a projection without the key is addressed as %v", got.Kind)
	}
	// A key of two columns needs both.
	pair := tableKey{columns: []string{"b", "a"}}
	if got := identityFor(tbl, pair, []string{"b", "c"}); got.Kind != model.IdentityNone {
		t.Errorf("half a key addressed a row: %v", got.Kind)
	}
	// A view has no key, so its rows are not written back.
	view := model.NewRef(model.KindView, "shop", "public", "adults")
	if got := identityFor(view, key, nil); got.Kind != model.IdentityNone {
		t.Errorf("a view's rows are addressed as %v", got.Kind)
	}
	if got := identityFor(tbl, tableKey{}, nil); got.Kind != model.IdentityNone {
		t.Errorf("a table with no key at all is addressed as %v", got.Kind)
	}
}

// A badge is an estimate the engine gathered, or nothing at all.
//
// Nothing when the estimate is zero: the engine says zero both for a table
// it has never measured and for one that is empty, and a table of a
// million rows badged "0" would be an untruth in the tree (FR-2.5).
func TestABadgeIsAnEstimateOrNothing(t *testing.T) {
	if _, ok := estimateBadge(0); ok {
		t.Error("a table nobody measured was given a badge")
	}
	if _, ok := estimateBadge(-1); ok {
		t.Error("a negative estimate was given a badge")
	}
	b, ok := estimateBadge(1234)
	if !ok {
		t.Fatal("a measured table has no badge")
	}
	if b.Exact {
		t.Error("an estimate says it is exact")
	}
	if b.Text != "1.2K" {
		t.Errorf("the badge reads %q", b.Text)
	}
}

// A count on a badge is short enough to sit beside a name.
func TestACountIsAbbreviated(t *testing.T) {
	for n, want := range map[int64]string{
		0: "0", 1: "1", 999: "999",
		1000: "1K", 1234: "1.2K", 999999: "1000K",
		1_000_000: "1M", 1_500_000: "1.5M",
		1_000_000_000: "1B", 2_400_000_000: "2.4B",
	} {
		if got := humanCount(n); got != want {
			t.Errorf("humanCount(%d) = %q, want %q", n, got, want)
		}
	}
}
