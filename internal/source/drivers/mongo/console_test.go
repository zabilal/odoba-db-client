package mongo

import (
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// arg renders one of a command's arguments as extended JSON.
func arg(t *testing.T, c command, i int) string {
	t.Helper()
	if i >= len(c.args) {
		t.Fatalf("argument %d of %d", i+1, len(c.args))
	}
	if doc, ok := c.args[i].DocumentOK(); ok {
		var d bson.D
		if err := bson.Unmarshal(doc, &d); err != nil {
			t.Fatal(err)
		}
		return extJSON(d)
	}
	return c.args[i].String()
}

func TestACommandIsReadAsItIsTyped(t *testing.T) {
	// A comma inside a document is not a comma between arguments.
	c, err := parseCommand(`db.people.find({"score": {"$gt": 10}, "name": "Ada"}, {"name": 1})`)
	if err != nil {
		t.Fatal(err)
	}
	if c.kind != "find" || c.collection != "people" {
		t.Fatalf("read as %+v", c)
	}
	if len(c.args) != 2 {
		t.Fatalf("%d arguments, want two", len(c.args))
	}
	if got := arg(t, c, 0); got != `{"score":{"$gt":10},"name":"Ada"}` {
		t.Errorf("the filter is %s", got)
	}
	if got := arg(t, c, 1); got != `{"name":1}` {
		t.Errorf("the projection is %s", got)
	}
	// A bracket inside a string is text, not a bracket.
	one, err := parseCommand(`db.people.find({"a": "((("})`)
	if err != nil {
		t.Fatal(err)
	}
	if got := arg(t, one, 0); got != `{"a":"((("}` {
		t.Errorf("a bracket in a string read as %s", got)
	}
	// A call with nothing in it.
	if c, err := parseCommand("db.people.countDocuments()"); err != nil || c.kind != "countDocuments" || len(c.args) != 0 {
		t.Errorf("read as %+v (%v)", c, err)
	}
	// A trailing semicolon and spaces are a person typing.
	if c, err := parseCommand("  db.people.find() ;  "); err != nil || c.kind != "find" {
		t.Errorf("read as %+v (%v)", c, err)
	}
	// JavaScript's own quotes.
	c, err = parseCommand(`db.people.find({'name': 'Ada'})`)
	if err != nil {
		t.Fatal(err)
	}
	if got := arg(t, c, 0); got != `{"name":"Ada"}` {
		t.Errorf("single quotes read as %s", got)
	}
	// A name in brackets, which is how a collection with a dot is named.
	c, err = parseCommand(`db["my.people"].find({})`)
	if err != nil {
		t.Fatal(err)
	}
	if c.collection != "my.people" {
		t.Errorf("the collection is %q", c.collection)
	}
	// A command on the database itself.
	if c, err := parseCommand("db.getCollectionNames()"); err != nil || c.kind != "getCollectionNames" || c.collection != "" {
		t.Errorf("read as %+v (%v)", c, err)
	}
	// The shell's own words.
	if c, err := parseCommand("show collections"); err != nil || c.kind != "show" || c.word != "collections" {
		t.Errorf("read as %+v (%v)", c, err)
	}
	if c, err := parseCommand("use shop"); err != nil || c.kind != "use" || c.word != "shop" {
		t.Errorf("read as %+v (%v)", c, err)
	}
}

func TestWhatTheConsoleDoesNotReadItSaysSo(t *testing.T) {
	cases := []struct{ text, says string }{
		{"", "nothing to run"},
		{"   ", "nothing to run"},
		{"select * from people", "not a command this console knows"},
		{"show", "show what"},
		{"use", "which database"},
		{"db", "try db."},
		{"db.", "a name is expected"},
		{"db.people", "is not called"},
		{"db.people.find", "is not called"},
		{"db.people.find({", "has no )"},
		{`db["people".find({})`, "has no ]"},
		{"db.people.find({}).limit(5)", "one call at a time"},
		{"db.people.find({not json})", "not a value this console reads"},
		{"db.people.find(,)", "is empty"},
	}
	for _, tc := range cases {
		_, err := parseCommand(tc.text)
		if err == nil {
			t.Errorf("%q was read", tc.text)
			continue
		}
		if !strings.Contains(err.Error(), tc.says) {
			t.Errorf("%q is refused with %q, want it to say %q", tc.text, err, tc.says)
		}
	}
	// A word that begins like a keyword is not one.
	if _, err := parseCommand("shows collections"); err == nil {
		t.Error("shows was read as show")
	}
}

func TestACommandSaysWhatItWouldDo(t *testing.T) {
	reads := []string{
		"db.people.find({})", "db.people.findOne()", "db.people.countDocuments()",
		"db.people.aggregate([])", "show collections", "use shop", "db.people.getIndexes()",
	}
	for _, text := range reads {
		c, err := parseCommand(text)
		if err != nil {
			t.Fatalf("%s: %v", text, err)
		}
		if got := c.access(); got != source.AccessRead {
			t.Errorf("%s is %v, want a read", text, got)
		}
	}
	writes := []string{
		"db.people.insertOne({})", "db.people.updateMany({}, {})", "db.people.deleteOne({})",
	}
	for _, text := range writes {
		c, _ := parseCommand(text)
		if got := c.access(); got != source.AccessWrite {
			t.Errorf("%s is %v, want a write", text, got)
		}
	}
	ddl := []string{"db.people.drop()", "db.people.createIndex({})", "db.runCommand({})"}
	for _, text := range ddl {
		c, _ := parseCommand(text)
		if got := c.access(); got != source.AccessDDL {
			t.Errorf("%s is %v, want a structural change", text, got)
		}
	}
	// A command this console does not know is taken to change everything.
	if got := (command{kind: "somethingNew"}).access(); got != source.AccessDDL {
		t.Errorf("an unknown command is %v, want the careful answer", got)
	}
	// A pipeline that writes is a write, whatever the command looks like.
	c, err := parseCommand(`db.people.aggregate([{"$match": {}}, {"$out": "copies"}])`)
	if err != nil {
		t.Fatal(err)
	}
	if !c.writesInPipeline() {
		t.Error("an aggregate that writes is not seen to write")
	}
	c, _ = parseCommand(`db.people.aggregate([{"$match": {"$out": 1}}])`)
	if c.writesInPipeline() {
		t.Error("a field called $out is seen as a stage")
	}
	c, _ = parseCommand(`db.people.find({})`)
	if c.writesInPipeline() {
		t.Error("a find writes")
	}
}

func TestAScriptIsSplitIntoItsCommands(t *testing.T) {
	script := `db.people.find({})
db.people.countDocuments();
// a note on its own
db.people.aggregate([
  {"$match": {"a": 1}},
  {"$group": {"_id": "$b"}}
])`
	got := splitCommands(script)
	if len(got) != 3 {
		t.Fatalf("%d commands, want three: %+v", len(got), got)
	}
	if got[0].Text != `db.people.find({})` {
		t.Errorf("the first is %q", got[0].Text)
	}
	if got[1].Text != "db.people.countDocuments()" {
		t.Errorf("the second is %q, want its semicolon gone", got[1].Text)
	}
	// A bracket left open carries the command onto the next line, which is
	// how a pipeline is typed.
	if !strings.HasPrefix(got[2].Text, "db.people.aggregate([") || !strings.HasSuffix(got[2].Text, "])") {
		t.Errorf("the third is %q", got[2].Text)
	}
	// Each says where it begins, so an error can be shown where it happened.
	for i, want := range []string{"db.people.find", "db.people.countDocuments", "db.people.aggregate"} {
		if at := got[i].Offset; !strings.HasPrefix(script[at:], want) {
			t.Errorf("command %d begins at %d, which is %q", i+1, at, firstLine(script[at:]))
		}
	}
	// A semicolon or a newline inside a string is text.
	if got := splitCommands(`db.people.find({"a": "one;two"})`); len(got) != 1 {
		t.Errorf("%d commands, want one: %+v", len(got), got)
	}
	// Nothing but comments and space is nothing to run.
	if got := splitCommands("  \n// only a note\n\n"); len(got) != 0 {
		t.Errorf("%d commands from a note alone", len(got))
	}
}

func TestTheDialectSpeaksTheConsolesLanguage(t *testing.T) {
	s := &mongoSource{}
	// What a command does, for the guard.
	if got := s.Classify(`db.people.find({})`); got != source.AccessRead {
		t.Errorf("a find is %v", got)
	}
	if got := s.Classify(`db.people.deleteMany({})`); got != source.AccessWrite {
		t.Errorf("a delete is %v", got)
	}
	if got := s.Classify(`db.people.drop()`); got != source.AccessDDL {
		t.Errorf("a drop is %v", got)
	}
	if got := s.Classify(`db.people.aggregate([{"$out": "copies"}])`); got != source.AccessWrite {
		t.Errorf("an aggregate that writes is %v", got)
	}
	// What cannot be read changes everything, which is the safe direction.
	if got := s.Classify("SELECT 1"); got != source.AccessDDL {
		t.Errorf("what the console cannot read is %v", got)
	}
	// A name is written as the shell reads one.
	if got := s.QuoteIdentifier(`my "coll"`); got != `"my \"coll\""` {
		t.Errorf("a name is written %s", got)
	}
	if got := s.QualifyRef(model.NewRef(model.KindCollection, "shop", "people")); got != "db.people" {
		t.Errorf("a collection is addressed as %s", got)
	}
	if s.Placeholder(1) != "" {
		t.Error("a command binds nothing beside it")
	}
}

func TestABrowseIsWrittenAsTheCallItIs(t *testing.T) {
	s := &mongoSource{}
	ref := model.NewRef(model.KindCollection, "shop", "people")
	st, err := s.BuildBrowse(ref, source.BrowseOptions{
		Filters: []source.Filter{{Column: "score", Op: source.OpGreater, Values: []any{int32(10)}}},
		Sorts:   []source.Sort{{Column: "name"}},
		Offset:  20, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `db.people.find({"score":{"$gt":10}}).sort({"name":1}).skip(20).limit(10)`
	if st.SQL != want {
		t.Errorf("the browse is\n%s\nwant\n%s", st.SQL, want)
	}
	// With nothing asked of it, it is the plainest call there is.
	st, _ = s.BuildBrowse(ref, source.BrowseOptions{})
	if st.SQL != "db.people.find({})" {
		t.Errorf("a plain browse is %s", st.SQL)
	}
	// The columns asked for are in it.
	st, _ = s.BuildBrowse(ref, source.BrowseOptions{Columns: []string{"name"}})
	if !strings.Contains(st.SQL, `find({}, {"name":1,"_id":1})`) {
		t.Errorf("a projection is %s", st.SQL)
	}
	// Nothing else holds documents to browse, whatever its path.
	if _, err := s.BuildBrowse(model.NewRef(model.KindDatabase, "shop"), source.BrowseOptions{}); err == nil {
		t.Error("a database was browsed")
	}
	if _, err := s.BuildBrowse(model.NewRef(model.KindIndex, "shop", "people", "_id_"), source.BrowseOptions{}); err == nil {
		t.Error("an index was browsed as though it were its collection")
	}
}

// firstLine is a script from a point to the end of its line.
func firstLine(text string) string {
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		return text[:i]
	}
	return text
}

func TestAConditionIsOneFilterDocument(t *testing.T) {
	ok := []string{`{}`, `{"a": 1}`, `  {"a": {"$gt": 1}}  `}
	for _, text := range ok {
		if _, err := browseFilter(source.BrowseOptions{Where: text}); err != nil {
			t.Errorf("%s is refused: %v", text, err)
		}
	}
	refused := []struct{ text, says string }{
		{`{} {}`, "more than one"},
		{`{"a": 1} {"b": 2}`, "more than one"},
		{`[{"a": 1}]`, "not a list or a value"},
		{`"a string"`, "not a list or a value"},
		{`42`, "not a list or a value"},
		{`not json`, "filter document"},
		{`{`, "filter document"},
		{`score > 1`, "filter document"},
	}
	for _, tc := range refused {
		_, err := browseFilter(source.BrowseOptions{Where: tc.text})
		if err == nil {
			t.Errorf("%s was taken as a condition", tc.text)
			continue
		}
		if !strings.Contains(err.Error(), tc.says) {
			t.Errorf("%s is refused with %q, want it to say %q", tc.text, err, tc.says)
		}
	}
	// A condition is ANDed with the grid's own filters, not instead of them.
	got, err := browseFilter(source.BrowseOptions{Where: `{"a": 1}`,
		Filters: []source.Filter{{Column: "b", Op: source.OpEqual, Values: []any{2}}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(extJSON(got), `"$and"`) {
		t.Errorf("both together are %s", extJSON(got))
	}
}
