//go:build duckdb

package duckdb

import (
	"database/sql"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// The driver describes itself well enough for the connection form to be
// built from it (FR-1.1).
func TestTheDriverDescribesItself(t *testing.T) {
	desc := Driver{}.Describe()
	if desc.ID != driverID || desc.Paradigm != model.ParadigmRelational {
		t.Fatalf("the driver describes itself as %+v", desc)
	}
	// A file, not a server: no host, no port, no login.
	if desc.DefaultPort != 0 || len(desc.URLSchemes) != 0 {
		t.Errorf("a file-based engine claims %d and %q", desc.DefaultPort, desc.URLSchemes)
	}
	if len(desc.Fields) != 1 || desc.Fields[0].Key != "database" {
		t.Fatalf("the form asks for %+v", desc.Fields)
	}
	if desc.Fields[0].Kind != source.FieldFile || !desc.Fields[0].Required {
		t.Errorf("the file field reads %+v", desc.Fields[0])
	}
}

// A read-only connection holds the file read-only as well, which is the
// second of two defences and the one no statement can undo (NFR-S4).
func TestAReadOnlyConnectionHoldsTheFileReadOnly(t *testing.T) {
	if got := dsn("/tmp/x.duckdb", true); got != "/tmp/x.duckdb?access_mode=READ_ONLY" {
		t.Errorf("a read-only connection reads %q", got)
	}
	// And a writable one says nothing, rather than saying READ_WRITE: a
	// file opened with settings of its own is a different configuration
	// from one opened plainly, and this engine will not hold a file two
	// ways at once (ADR-0146).
	if got := dsn("/tmp/x.duckdb", false); got != "/tmp/x.duckdb" {
		t.Errorf("a writable connection reads %q", got)
	}
}

// A connection that fails says why in terms somebody can act on (FR-1.4).
func TestAFailedConnectionSaysWhy(t *testing.T) {
	for _, c := range []struct {
		name string
		err  error
		want source.ConnectKind
	}{
		{"held another way", errors.New("Can't open a connection to same database file with a different configuration than existing connections"), source.ConnectRefused},
		{"held by another program", errors.New("IO Error: Could not set lock on file"), source.ConnectRefused},
		{"not a database", errors.New("IO Error: The file is not a valid DuckDB database file"), source.ConnectConfig},
		{"not readable", errors.New("IO Error: permission denied"), source.ConnectAuth},
		{"something else", errors.New("who knows"), source.ConnectUnknown},
	} {
		t.Run(c.name, func(t *testing.T) {
			var ce *source.ConnectError
			if !errors.As(classifyConnectError(c.err), &ce) {
				t.Fatalf("%v was not classified at all", c.err)
			}
			if ce.Kind != c.want {
				t.Errorf("%v classified as %v, want %v", c.err, ce.Kind, c.want)
			}
			if ce.Hint == "" {
				t.Error("nothing was said about what to do")
			}
		})
	}
	// One already classified is not classified again.
	given := &source.ConnectError{Kind: source.ConnectConfig, Hint: "Choose a database file."}
	if got := classifyConnectError(given); got != given {
		t.Errorf("a classified failure was classified again: %v", got)
	}
}

// An error says what kind it was, which is the nearest thing this engine
// has to the code every other one gives (FR-5.10).
func TestAnErrorSaysWhatKindItWas(t *testing.T) {
	for _, c := range []struct{ in, code, text string }{
		{"Binder Error: No function matches the given name\nCandidate functions:\n+(TINYINT)",
			"Binder Error", "Binder Error: No function matches the given name"},
		{"Catalog Error: Table with name x does not exist!", "Catalog Error",
			"Catalog Error: Table with name x does not exist!"},
		{"Constraint Error: Duplicate key", "Constraint Error", "Constraint Error: Duplicate key"},
		{"something went wrong", "", "something went wrong"},
		{"a: b", "", "a: b"},
	} {
		err := errors.New(c.in)
		if got := errorCode(err); got != c.code {
			t.Errorf("errorCode(%q) = %q, want %q", c.in, got, c.code)
		}
		if got := firstLine(c.in); got != c.text {
			t.Errorf("firstLine(%q) = %q, want %q", c.in, got, c.text)
		}
	}
	// The lines under a message are the picture it draws of the offending
	// word, which is wider than the place a message is shown.
	err := statementError(errors.New("Binder Error: no\nCandidate functions:\n+(TINYINT)"), nil)
	var se *source.StatementError
	if !errors.As(err, &se) {
		t.Fatalf("a failure reads as %v", err)
	}
	if strings.Contains(se.Message.Text, "Candidate") {
		t.Errorf("the message reads %q", se.Message.Text)
	}
	if se.Message.Code != "Binder Error" {
		t.Errorf("the code reads %q", se.Message.Code)
	}
	if statementError(nil, nil) != nil {
		t.Error("nothing was made into a failure")
	}
}

// Every capability claimed has the interface behind it, and the language
// is one the editor knows how to read.
func TestNothingIsClaimedWithoutSomethingBehindIt(t *testing.T) {
	s := &duckSource{}
	caps := s.Capabilities()
	if caps.Query.Cancel {
		if _, ok := any(s).(source.Killer); !ok {
			t.Error("Cancel is claimed without a Killer")
		}
	}
	if caps.Data.DistinctValues {
		if _, ok := any(s).(source.DistinctLister); !ok {
			t.Error("DistinctValues is claimed without a DistinctLister")
		}
	}
	if caps.Data.Insert && func() bool { _, ok := any(s).(source.Writer); return !ok }() {
		t.Error("writing is claimed without a Writer")
	}
	// A capability with no interface behind it is worse than none.
	if caps.Query.Transactions {
		t.Error("Query.Transactions is claimed and there is no Transactor")
	}
	if caps.Query.Explain || caps.Query.ExplainAnalyze {
		t.Error("Explain is claimed, and this engine answers a drawn box rather than a plan")
	}
	if caps.Query.EditableResults {
		t.Error("EditableResults is claimed and nothing tells a result's columns where they came from")
	}
	if caps.Data.BulkLoad {
		t.Error("BulkLoad is claimed and there is no BulkLoader")
	}
	if caps.Data.ColumnStats {
		t.Error("ColumnStats is claimed and there is no Statistician")
	}
	if caps.Schema.DDL || caps.Schema.Diff {
		t.Error("DDL or Diff is claimed and there is nothing to render or snapshot")
	}
	// A connection holds the file it was opened on and any other attached
	// to it, and each holds schemas.
	if !caps.Structure.MultipleDatabases || !caps.Structure.Schemas {
		t.Errorf("the structure reads %+v", caps.Structure)
	}
}

// Closing twice is closing once (source.Source).
func TestClosingTwiceIsClosingOnce(t *testing.T) {
	db, err := sql.Open(driverID, "")
	if err != nil {
		t.Fatal(err)
	}
	s := &duckSource{db: db}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("the second close said %v", err)
	}
	if _, err := s.conn(); !errors.Is(err, errClosed) {
		t.Errorf("a closed source handed out a connection: %v", err)
	}
}

// A badge is the estimate the engine keeps, and nothing where it keeps
// none. Unlike the engines that cannot tell an unmeasured table from an
// empty one, this one says nothing rather than zero (FR-2.5).
func TestABadgeIsAnEstimateOrNothing(t *testing.T) {
	if _, ok := estimateBadge(sql.NullInt64{}); ok {
		t.Error("a table the engine has not measured was given a badge")
	}
	if _, ok := estimateBadge(sql.NullInt64{Int64: -1, Valid: true}); ok {
		t.Error("a negative estimate was given a badge")
	}
	empty, ok := estimateBadge(sql.NullInt64{Int64: 0, Valid: true})
	if !ok || empty.Text != "0" {
		t.Errorf("an empty table's badge is %+v (present %v)", empty, ok)
	}
	b, ok := estimateBadge(sql.NullInt64{Int64: 1234, Valid: true})
	if !ok || b.Exact || b.Text != "1.2K" {
		t.Errorf("a measured table's badge is %+v (present %v)", b, ok)
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

// Paging ends its sort with the key, so that a page is a page.
func TestPagingEndsItsSortWithTheKey(t *testing.T) {
	got := tiebreak([]source.Sort{{Column: "status"}}, []string{"id"})
	if !slices.Equal(got, []source.Sort{{Column: "status"}, {Column: "id"}}) {
		t.Errorf("the sort reads %+v", got)
	}
	got = tiebreak([]source.Sort{{Column: "id", Descending: true}}, []string{"id"})
	if !slices.Equal(got, []source.Sort{{Column: "id", Descending: true}}) {
		t.Errorf("the key was added again: %+v", got)
	}
	got = tiebreak(nil, []string{"b", "a"})
	if !slices.Equal(got, []source.Sort{{Column: "b"}, {Column: "a"}}) {
		t.Errorf("a key of two columns reads %+v", got)
	}
	if got := tiebreak([]source.Sort{{Column: "a"}}, nil); !slices.Equal(got,
		[]source.Sort{{Column: "a"}}) {
		t.Errorf("a sort without a key reads %+v", got)
	}
}

// A row can be written only when every column that addresses it is on the
// screen (FR-4.7).
func TestARowIsEditableOnlyWhenItIsAddressed(t *testing.T) {
	tbl := model.NewRef(model.KindTable, "shop", "main", "orders")
	keyed := &tableInfo{key: []string{"id"}}
	addressed := &tableInfo{key: []string{rowIDName}, rowID: true}

	if got := identityFor(tbl, keyed, nil); got.Kind != model.IdentityPrimaryKey {
		t.Errorf("a table with a key is addressed as %v", got.Kind)
	}
	// A table with no key of its own is addressed by where its rows are,
	// and says so: it is a position, not a key, and it is good while the
	// grid holds the row it read.
	if got := identityFor(tbl, addressed, nil); got.Kind != model.IdentityRowID {
		t.Errorf("a table with no key is addressed as %v", got.Kind)
	}
	if got := identityFor(tbl, keyed, []string{"id", "total"}); got.Kind != model.IdentityPrimaryKey {
		t.Errorf("a projection holding the key is addressed as %v", got.Kind)
	}
	if got := identityFor(tbl, keyed, []string{"total"}); got.Kind != model.IdentityNone {
		t.Errorf("a projection without the key is addressed as %v", got.Kind)
	}
	pair := &tableInfo{key: []string{"b", "a"}}
	if got := identityFor(tbl, pair, []string{"b", "c"}); got.Kind != model.IdentityNone {
		t.Errorf("half a key addressed a row: %v", got.Kind)
	}
	view := model.NewRef(model.KindView, "shop", "main", "adults")
	if got := identityFor(view, keyed, nil); got.Kind != model.IdentityNone {
		t.Errorf("a view's rows are addressed as %v", got.Kind)
	}
	if got := identityFor(tbl, &tableInfo{}, nil); got.Kind != model.IdentityNone {
		t.Errorf("a table with no key at all is addressed as %v", got.Kind)
	}
}

// A row whose key is taken over the row there is written as ON CONFLICT
// (ADR-0052).
func TestAnUpsertNamesTheKeyItTakesOver(t *testing.T) {
	got := d.UpsertClause([]string{"id"}, []string{"id", "name"})
	if !strings.Contains(got, `ON CONFLICT ("id") DO UPDATE`) {
		t.Errorf("an upsert reads %s", got)
	}
	if !strings.Contains(got, `"name" = EXCLUDED."name"`) {
		t.Errorf("an upsert does not write the other columns: %s", got)
	}
}
