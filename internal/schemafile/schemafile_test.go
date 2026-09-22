package schemafile

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/diff"
	"github.com/ikigai-db/ikigai-db/internal/model"
)

// A model saved and read back is the model that was saved. Anything this
// loses is a difference a comparison would report against a database nobody
// had changed, which is the failure this package exists to avoid.

// rich is a database with something of every kind in it.
func rich() *model.Database {
	zero := int64(0)
	return &model.Database{
		Name: "sales", Charset: "UTF8", Collate: "C", Comment: "the one",
		Schemas: []model.Schema{{
			Name: "public", Owner: "ada", Comment: "the public one",
			Attrs: map[string]string{"replication": "1"},
			Tables: []model.Table{{
				Name: "orders", Comment: "what was bought", RowsEstimate: 12000,
				Columns: []model.Column{
					{Name: "id", Position: 1, Identity: true,
						Type: model.DataType{Class: model.TypeInteger, Native: "integer", Length: -1}},
					{Name: "total", Position: 2, HasDefault: true, Default: "0",
						Type:  model.DataType{Class: model.TypeDecimal, Native: "numeric(10,2)", Nullable: true, Length: -1, Precision: 10, Scale: 2},
						Attrs: map[string]string{"storage": "main"}},
					{Name: "doubled", Position: 3, Generated: "(total * 2)",
						Type: model.DataType{Class: model.TypeDecimal, Native: "numeric", Length: -1}},
				},
				PrimaryKey: &model.PrimaryKey{Name: "orders_pkey", Columns: []string{"id"}},
				Uniques:    []model.UniqueConstraint{{Name: "orders_ref_key", Columns: []string{"total"}}},
				Checks:     []model.CheckConstraint{{Name: "orders_positive", Expression: "total >= 0"}},
				ForeignKeys: []model.ForeignKey{{Name: "orders_who_fkey", Columns: []string{"id"},
					RefSchema: "public", RefTable: "people", RefColumns: []string{"id"},
					OnDelete: model.ActionCascade, OnUpdate: model.ActionNoAction}},
				Indexes: []model.Index{{Name: "orders_total_ix", Method: "btree", Unique: true,
					Columns: []model.IndexColumn{{Name: "total", Descending: true}, {Expression: "lower(note)"}},
					Include: []string{"id"}, Predicate: "total > 0",
					Attrs: map[string]string{"fillfactor": "70"}}},
				Triggers: []model.Trigger{{Name: "audit", Timing: "BEFORE", Events: []string{"INSERT", "UPDATE"},
					ForEachRow: true, Condition: "new.total > 0", Definition: "CREATE TRIGGER audit"}},
				Attrs: map[string]string{"fillfactor": "70"},
			}},
			Views: []model.View{{Name: "recent", Definition: "SELECT 1", Comment: "lately",
				Columns: []model.Column{{Name: "id", Position: 1,
					Type: model.DataType{Class: model.TypeInteger, Native: "integer", Length: -1}}}},
				{Name: "totals", Materialized: true, Definition: "SELECT 2",
					Indexes: []model.Index{{Name: "totals_ix", Columns: []model.IndexColumn{{Name: "id"}}}}}},
			Routines: []model.Routine{{Name: "total", Kind: model.RoutineFunction, Language: "plpgsql",
				Parameters: []model.Parameter{{Name: "a", Mode: "IN", Type: model.DataType{Native: "integer"}}},
				Returns:    &model.DataType{Native: "bigint"}, Definition: "BEGIN RETURN 1; END", Comment: "adds up"}},
			Sequences: []model.Sequence{{Name: "orders_id_seq", DataType: "bigint", Start: 1,
				Increment: 1, MinValue: &zero, Cycle: false, Comment: "the ids"}},
			UserTypes: []model.UserType{{Name: "mood", Category: "enum",
				EnumValues: []string{"ok", "sad"}, Comment: "how it went"}},
		}},
	}
}

func saved(t *testing.T, db *model.Database) (string, *model.Database) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "model")
	if err := Write(dir, db); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	return dir, got
}

// The whole of it goes out and comes back, judged by the thing that will
// judge it in earnest.
func TestAModelSavedAndReadBackIsTheSameModel(t *testing.T) {
	want := rich()
	_, got := saved(t, want)

	// A file does not know how many rows a table has, and does not pretend
	// to: that is the one thing deliberately not saved.
	want.Schemas[0].Tables[0].RowsEstimate = -1

	if d := diff.Compare(want, got); d.Differs() {
		t.Errorf("it came back different:\n%s", changed(d, ""))
	}
	if c := diff.Compare(want, got).Count(); c[diff.Same] < 15 {
		t.Errorf("it compared only %d things", c[diff.Same])
	}
}

func changed(n diff.Node, path string) string {
	at := strings.TrimPrefix(path+"/"+n.Name, "/")
	var out []string
	for _, d := range n.Detail {
		out = append(out, "  "+at+" "+d.Name+": "+d.From+" -> "+d.To)
	}
	if n.Status != diff.Same && len(n.Children) == 0 && len(n.Detail) == 0 {
		out = append(out, "  "+at+" "+string(n.Status))
	}
	for _, c := range n.Children {
		if c.Status != diff.Same {
			out = append(out, changed(c, at))
		}
	}
	return strings.Join(out, "\n")
}

// One file per object is the point: a commit shows the objects that changed.
func TestOneFileForEachObject(t *testing.T) {
	dir, _ := saved(t, rich())
	for _, want := range []string{
		"database.json",
		"public/schema.json",
		"public/tables/orders.json",
		"public/views/recent.json",
		"public/views/totals.json",
		"public/routines/total.json",
		"public/sequences/orders_id_seq.json",
		"public/types/mood.json",
	} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(want))); err != nil {
			t.Errorf("no %s: %v", want, err)
		}
	}
}

// Saving the same schema twice writes the same bytes, or version control
// shows a change where nothing changed.
func TestSavingTwiceWritesTheSameBytes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "model")
	if err := Write(dir, rich()); err != nil {
		t.Fatal(err)
	}
	first := treeOf(t, dir)
	if err := Write(dir, rich()); err != nil {
		t.Fatal(err)
	}
	if second := treeOf(t, dir); !slices.Equal(first, second) {
		t.Errorf("the second save wrote:\n%v\nafter:\n%v", second, first)
	}
}

// treeOf is every file under dir with its contents, as one sorted list.
func treeOf(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		out = append(out, rel+"\n"+string(data))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(out)
	return out
}

// An object dropped from the database is gone from the model, rather than
// left behind to compare as one somebody should put back.
func TestAnObjectDroppedIsGoneFromTheSavedModel(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "model")
	if err := Write(dir, rich()); err != nil {
		t.Fatal(err)
	}
	fewer := rich()
	fewer.Schemas[0].Views = fewer.Schemas[0].Views[:1]
	if err := Write(dir, fewer); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "public", "views", "totals.json")); !os.IsNotExist(err) {
		t.Errorf("the dropped view is still there: %v", err)
	}
	got, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(got.Schemas[0].Views); n != 1 {
		t.Errorf("the model holds %d views", n)
	}
}

// A name is a label on a file and the real name is inside it, so a table
// called something a filesystem will not take is saved under a name it will
// and read back as itself.
func TestNamesAFilesystemWillNotTake(t *testing.T) {
	db := &model.Database{Name: "sales", Schemas: []model.Schema{{Name: "public",
		Tables: []model.Table{
			{Name: "order/totals", RowsEstimate: -1},
			{Name: "con", RowsEstimate: -1},
			{Name: `a "quoted" name.`, RowsEstimate: -1},
			{Name: "...", RowsEstimate: -1},
		}}}}
	dir, got := saved(t, db)

	var names []string
	for _, t := range got.Schemas[0].Tables {
		names = append(names, t.Name)
	}
	slices.Sort(names)
	if want := []string{"...", `a "quoted" name.`, "con", "order/totals"}; !slices.Equal(names, want) {
		t.Errorf("they came back as %q, want %q", names, want)
	}
	// And none of them left a separator or a device name in a path.
	entries, err := os.ReadDir(filepath.Join(dir, "public", "tables"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		stem := strings.TrimSuffix(e.Name(), ".json")
		switch {
		case strings.ContainsAny(e.Name(), `/\:*?"<>|`):
			t.Errorf("it wrote a file called %q", e.Name())
		case reserved[strings.ToLower(stem)]:
			t.Errorf("it wrote a file called %q, which Windows keeps for a device", e.Name())
		case strings.HasSuffix(stem, ".") || strings.HasSuffix(stem, " "):
			// Windows will not give a file a name ending in a dot or a
			// space; it silently takes them off, and then two names are one.
			t.Errorf("it wrote a file called %q", e.Name())
		}
	}
}

// Reading a model twice gives the same model, objects included, in the order
// their file names sort.
func TestReadingAModelTwiceGivesTheSameOrder(t *testing.T) {
	db := &model.Database{Name: "sales", Schemas: []model.Schema{{Name: "public",
		Tables: []model.Table{
			{Name: "zebra", RowsEstimate: -1}, {Name: "apple", RowsEstimate: -1},
			{Name: "mango", RowsEstimate: -1},
		}}}}
	dir, first := saved(t, db)
	second, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	var a, b []string
	for i := range first.Schemas[0].Tables {
		a = append(a, first.Schemas[0].Tables[i].Name)
		b = append(b, second.Schemas[0].Tables[i].Name)
	}
	if !slices.Equal(a, b) || !slices.IsSorted(a) {
		t.Errorf("it read %v then %v", a, b)
	}
}

// A row count is not saved, and a table read back says it does not know how
// many rows it has. Saving it would put a diff in the repository every day
// for nothing; claiming to know would be a file pretending to be a server.
func TestARowCountIsNotSavedAndIsNotInvented(t *testing.T) {
	dir, got := saved(t, rich())
	data, err := os.ReadFile(filepath.Join(dir, "public", "tables", "orders.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "12000") {
		t.Errorf("the file holds the row count:\n%s", data)
	}
	if n := got.Schemas[0].Tables[0].RowsEstimate; n != -1 {
		t.Errorf("a table read from a file says it has %d rows; it cannot know", n)
	}
}

// Two names that would be one file are refused, and say which two. Writing
// one over the other would lose an object silently, and a comparison would
// then report it as one somebody had dropped.
func TestTwoNamesThatWouldBeOneFileAreRefused(t *testing.T) {
	db := &model.Database{Name: "sales", Schemas: []model.Schema{{Name: "public",
		Tables: []model.Table{{Name: "Orders", RowsEstimate: -1}, {Name: "orders", RowsEstimate: -1}}}}}
	dir := filepath.Join(t.TempDir(), "model")
	err := Write(dir, db)
	if err == nil {
		t.Fatal("it wrote both to one file")
	}
	if !strings.Contains(err.Error(), "Orders") || !strings.Contains(err.Error(), "orders") {
		t.Errorf("it said %v", err)
	}
	// And it refused before writing anything.
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("it left a directory behind: %v", err)
	}
}

// Saving a model replaces everything under the directory, so one holding
// anything else is refused rather than emptied.
func TestADirectoryHoldingSomethingElseIsRefused(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := Write(dir, rich())
	if !errors.Is(err, ErrNotAModel) {
		t.Fatalf("it said %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "notes.txt")); err != nil {
		t.Errorf("it removed what was there: %v", err)
	}
	// An empty one is fine, and so is one that already holds a model.
	empty := filepath.Join(t.TempDir(), "empty")
	if err := os.MkdirAll(empty, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Write(empty, rich()); err != nil {
		t.Errorf("an empty directory was refused: %v", err)
	}
	if err := Write(empty, rich()); err != nil {
		t.Errorf("a directory holding a model was refused: %v", err)
	}
}

// Reading something that is not a model says so, rather than answering an
// empty database that compares as everything having been dropped.
func TestReadingSomethingThatIsNotAModel(t *testing.T) {
	if _, err := Read(t.TempDir()); !errors.Is(err, ErrNotAModel) {
		t.Errorf("an empty directory said %v", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, dbFile),
		[]byte(`{"format":"ikigai-schema/99","name":"sales"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Read(dir)
	if err == nil || !strings.Contains(err.Error(), "ikigai-schema/99") {
		t.Errorf("a later format said %v", err)
	}
}

// A model with nothing in it is a model, not a failure: an empty schema is a
// thing somebody may have.
func TestAnEmptyModel(t *testing.T) {
	_, got := saved(t, &model.Database{Name: "sales", Schemas: []model.Schema{{Name: "public"}}})
	if len(got.Schemas) != 1 || got.Schemas[0].Name != "public" {
		t.Errorf("it read %+v", got)
	}
	if len(got.Schemas[0].Tables) != 0 {
		t.Errorf("it invented %d tables", len(got.Schemas[0].Tables))
	}
}

func TestSavingNothingAtAll(t *testing.T) {
	if err := Write(filepath.Join(t.TempDir(), "m"), nil); err == nil {
		t.Error("it saved nothing as something")
	}
}

// What a comparison leaves out travels with the model (FR-7.5).

func TestRulesTravelWithTheModel(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "model")
	if err := Write(dir, rich()); err != nil {
		t.Fatal(err)
	}
	if got, err := Ignore(dir); err != nil || got.Any() {
		t.Fatalf("a new model said %+v, %v", got, err)
	}

	want := diff.Options{Schemas: []string{"audit"}, Names: []string{"*_tmp"},
		Whitespace: true, Collation: true, Comments: true}
	if err := SetIgnore(dir, want); err != nil {
		t.Fatal(err)
	}
	got, err := Ignore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Describe() != want.Describe() {
		t.Errorf("it kept %q, want %q", got.Describe(), want.Describe())
	}

	// And they are in the root file, where a commit that changes them
	// touches one file and says so.
	data, err := os.ReadFile(filepath.Join(dir, dbFile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "audit") {
		t.Errorf("the root file holds %q", data)
	}
	if _, err := os.Stat(filepath.Join(dir, "public", "tables", "orders.json")); err != nil {
		t.Errorf("keeping a rule disturbed the objects: %v", err)
	}
}

// Saving the model again keeps the rules agreed about it: reading the
// database again is no reason to throw away an agreement about it.
func TestSavingAgainKeepsTheRules(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "model")
	if err := Write(dir, rich()); err != nil {
		t.Fatal(err)
	}
	if err := SetIgnore(dir, diff.Options{Comments: true}); err != nil {
		t.Fatal(err)
	}
	if err := Write(dir, rich()); err != nil {
		t.Fatal(err)
	}
	got, err := Ignore(dir)
	if err != nil || !got.Comments {
		t.Errorf("after saving again it says %+v, %v", got, err)
	}
}

// Rules that say nothing are not written at all, so a model with none has a
// root file with none in it.
func TestNoRulesAreNotWrittenDown(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "model")
	if err := Write(dir, rich()); err != nil {
		t.Fatal(err)
	}
	if err := SetIgnore(dir, diff.Options{Comments: true}); err != nil {
		t.Fatal(err)
	}
	if err := SetIgnore(dir, diff.Options{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, dbFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "ignore") {
		t.Errorf("the root file still holds %q", data)
	}
}

// A rule that is not one is refused before it is written down.
func TestARuleThatIsNotOneIsNotKept(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "model")
	if err := Write(dir, rich()); err != nil {
		t.Fatal(err)
	}
	if err := SetIgnore(dir, diff.Options{Names: []string{"[unclosed"}}); err == nil {
		t.Error("a pattern that will not parse was kept")
	}
	if got, _ := Ignore(dir); got.Any() {
		t.Errorf("it kept %+v", got)
	}
}

// A directory with no model in it says so rather than answering no rules,
// which would read as a model that ignores nothing.
func TestAskingAboutAModelThatIsNotThere(t *testing.T) {
	if _, err := Ignore(t.TempDir()); !errors.Is(err, ErrNotAModel) {
		t.Errorf("it said %v", err)
	}
	if err := SetIgnore(t.TempDir(), diff.Options{Comments: true}); !errors.Is(err, ErrNotAModel) {
		t.Errorf("it said %v", err)
	}
}
