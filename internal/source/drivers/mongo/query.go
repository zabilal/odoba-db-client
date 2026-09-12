package mongo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	mongodriver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Running console commands (FR-5.4, FR-12.1, T2.37).
//
// Every command is classified and put to the guard before any of them runs,
// as a SQL script's statements are: refusing the fourth after three have run
// would leave a person somewhere they did not choose (NFR-S4).

var (
	_ source.Queryer   = (*mongoSource)(nil)
	_ source.Sessioner = (*mongoSource)(nil)
)

// consoleLimit is how many documents one command reads. A console answers a
// person, and a person reads a screen; the grid pages a collection.
const consoleLimit = 200

// Session pins a console's own state: the database its commands are on,
// which "use" changes.
func (s *mongoSource) Session(context.Context) (source.Session, error) {
	return &console{src: s, db: s.currentDatabase()}, nil
}

// currentDatabase is where a console begins: the connection's own database,
// or admin, which every server has.
func (s *mongoSource) currentDatabase() string {
	if db := strings.TrimSpace(s.cfg.Database); db != "" {
		return db
	}
	return "admin"
}

// Query runs one command (source.Queryer).
func (s *mongoSource) Query(ctx context.Context, stmt source.Statement) (*source.Result, error) {
	c := &console{src: s, db: s.currentDatabase()}
	return c.Query(ctx, stmt)
}

// QueryMulti runs a script of them, a result at a time (FR-5.4).
func (s *mongoSource) QueryMulti(ctx context.Context, script string, opts source.ScriptOptions) (<-chan source.ScriptResult, error) {
	c := &console{src: s, db: s.currentDatabase()}
	return c.Run(ctx, script, opts)
}

// console is one query tab's connection: a database to be on, and the
// commands run against it.
type console struct {
	src *mongoSource

	mu     sync.Mutex
	db     string
	closed bool
}

var _ source.Session = (*console)(nil)

// Handle names the session for a cancel. MongoDB cancels by cancelling the
// context, and the driver does that itself, so there is nothing to name.
func (c *console) Handle() string { return "" }

// QueryMulti runs a script on this console (source.Queryer).
func (c *console) QueryMulti(ctx context.Context, script string, opts source.ScriptOptions) (<-chan source.ScriptResult, error) {
	return c.Run(ctx, script, opts)
}

func (c *console) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

// Query runs one command.
func (c *console) Query(ctx context.Context, stmt source.Statement) (_ *source.Result, err error) {
	defer panics.Recover(&err, "running a command")
	cmd, err := parseCommand(stmt.SQL)
	if err != nil {
		return nil, err
	}
	if err := c.allow(cmd, stmt.Confirmed); err != nil {
		return nil, err
	}
	return c.run(ctx, cmd)
}

// Run runs a script, delivering each command's result as it finishes. Every
// command is read and put to the guard before the first one runs.
func (c *console) Run(ctx context.Context, script string, opts source.ScriptOptions) (<-chan source.ScriptResult, error) {
	stmts := splitCommands(script)
	if len(stmts) == 0 {
		return nil, errors.New("mongodb: there is nothing to run")
	}
	cmds := make([]command, 0, len(stmts))
	for _, st := range stmts {
		cmd, err := parseCommand(st.Text)
		if err != nil {
			return nil, err
		}
		if err := c.allow(cmd, opts.Confirmed); err != nil {
			return nil, err
		}
		cmds = append(cmds, cmd)
	}
	out := make(chan source.ScriptResult, len(cmds))
	go func() {
		defer close(out)
		for i, cmd := range cmds {
			res, err := c.run(ctx, cmd)
			out <- source.ScriptResult{Index: i, Offset: stmts[i].Offset, Statement: stmts[i].Text, Result: res, Err: err}
			if err != nil || ctx.Err() != nil {
				return // a script stops at its first failure, as SQL's does
			}
		}
	}()
	return out, nil
}

// allow puts a command to the guard, by what it does.
func (c *console) allow(cmd command, confirmed bool) error {
	access := cmd.access()
	if cmd.writesInPipeline() && access == source.AccessRead {
		access = source.AccessWrite
	}
	return c.src.cfg.Guard.Allow(access, confirmed)
}

// database is the database the console is on.
func (c *console) database() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.db
}

// run does what a command says.
func (c *console) run(ctx context.Context, cmd command) (_ *source.Result, err error) {
	defer panics.Recover(&err, "running a command")
	start := time.Now()
	db := c.src.client.Database(c.database())
	coll := db.Collection(cmd.collection)

	switch cmd.kind {
	case "use":
		c.mu.Lock()
		c.db = cmd.word
		c.mu.Unlock()
		return said(start, "Now on "+cmd.word), nil

	case "show":
		switch cmd.word {
		case "collections", "tables":
			specs, err := c.src.collectionSpecs(ctx, c.database())
			if err != nil {
				return nil, err
			}
			names := make([]string, 0, len(specs))
			for _, s := range specs {
				names = append(names, s.Name)
			}
			return listed(start, "collection", names), nil
		case "dbs", "databases":
			names, err := c.src.client.ListDatabaseNames(ctx, bson.D{})
			if err != nil {
				return nil, err
			}
			return listed(start, "database", names), nil
		}
		return nil, fmt.Errorf("mongodb: show %s is not something this console shows; try collections or dbs", cmd.word)

	case "getCollectionNames":
		specs, err := c.src.collectionSpecs(ctx, c.database())
		if err != nil {
			return nil, err
		}
		names := make([]string, 0, len(specs))
		for _, s := range specs {
			names = append(names, s.Name)
		}
		return listed(start, "collection", names), nil
	}

	if cmd.collection == "" {
		if cmd.kind == "runCommand" {
			doc, err := documentArg(cmd, 0)
			if err != nil {
				return nil, err
			}
			var res bson.Raw
			if err := db.RunCommand(ctx, doc).Decode(&res); err != nil {
				return nil, err
			}
			return docsResult(start, []bson.Raw{res}), nil
		}
		return nil, fmt.Errorf("mongodb: db.%s() is not a command this console knows", cmd.kind)
	}

	switch cmd.kind {
	case "find":
		filter, err := optionalDocument(cmd, 0)
		if err != nil {
			return nil, err
		}
		find := options.Find().SetLimit(consoleLimit)
		if len(cmd.args) > 1 {
			projection, err := documentArg(cmd, 1)
			if err != nil {
				return nil, err
			}
			find.SetProjection(projection)
		}
		cur, err := coll.Find(ctx, filter, find)
		if err != nil {
			return nil, err
		}
		return cursorResult(ctx, start, cur)

	case "findOne":
		filter, err := optionalDocument(cmd, 0)
		if err != nil {
			return nil, err
		}
		cur, err := coll.Find(ctx, filter, options.Find().SetLimit(1))
		if err != nil {
			return nil, err
		}
		return cursorResult(ctx, start, cur)

	case "aggregate":
		if len(cmd.args) == 0 {
			return nil, errors.New("mongodb: an aggregate takes an array of stages")
		}
		stages, err := stagesOf(cmd.args[0])
		if err != nil {
			return nil, err
		}
		if !writes(stages) {
			stages = append(stages, bson.D{{Key: "$limit", Value: int64(consoleLimit)}})
		}
		cur, err := coll.Aggregate(ctx, stages)
		if err != nil {
			return nil, err
		}
		return cursorResult(ctx, start, cur)

	case "count", "countDocuments":
		filter, err := optionalDocument(cmd, 0)
		if err != nil {
			return nil, err
		}
		n, err := coll.CountDocuments(ctx, filter)
		if err != nil {
			return nil, err
		}
		return counted(start, n), nil

	case "estimatedDocumentCount":
		n, err := coll.EstimatedDocumentCount(ctx)
		if err != nil {
			return nil, err
		}
		return counted(start, n), nil

	case "distinct":
		if len(cmd.args) == 0 {
			return nil, errors.New("mongodb: distinct takes a field's name")
		}
		field, ok := cmd.args[0].StringValueOK()
		if !ok {
			return nil, errors.New("mongodb: distinct takes a field's name")
		}
		filter, err := optionalDocument(cmd, 1)
		if err != nil {
			return nil, err
		}
		res := coll.Distinct(ctx, field, filter)
		var vals []bson.RawValue
		if err := res.Decode(&vals); err != nil {
			return nil, err
		}
		return values(start, field, vals), nil

	case "insertOne":
		doc, err := documentArg(cmd, 0)
		if err != nil {
			return nil, err
		}
		res, err := coll.InsertOne(ctx, doc)
		if err != nil {
			return nil, err
		}
		return affected(start, 1, fmt.Sprintf("One document added, its _id %v", res.InsertedID)), nil

	case "insertMany":
		docs, err := arrayArg(cmd, 0)
		if err != nil {
			return nil, err
		}
		res, err := coll.InsertMany(ctx, docs)
		if err != nil {
			return nil, err
		}
		n := int64(len(res.InsertedIDs))
		return affected(start, n, fmt.Sprintf("%d documents added", n)), nil

	case "updateOne", "updateMany":
		filter, err := documentArg(cmd, 0)
		if err != nil {
			return nil, err
		}
		update, err := documentArg(cmd, 1)
		if err != nil {
			return nil, err
		}
		var res *mongodriver.UpdateResult
		if cmd.kind == "updateOne" {
			res, err = coll.UpdateOne(ctx, filter, update)
		} else {
			res, err = coll.UpdateMany(ctx, filter, update)
		}
		if err != nil {
			return nil, err
		}
		return affected(start, res.ModifiedCount,
			fmt.Sprintf("%d documents matched, %d changed", res.MatchedCount, res.ModifiedCount)), nil

	case "replaceOne":
		filter, err := documentArg(cmd, 0)
		if err != nil {
			return nil, err
		}
		doc, err := documentArg(cmd, 1)
		if err != nil {
			return nil, err
		}
		res, err := coll.ReplaceOne(ctx, filter, doc)
		if err != nil {
			return nil, err
		}
		return affected(start, res.ModifiedCount,
			fmt.Sprintf("%d documents matched, %d replaced", res.MatchedCount, res.ModifiedCount)), nil

	case "deleteOne", "deleteMany":
		filter, err := documentArg(cmd, 0)
		if err != nil {
			return nil, err
		}
		var res *mongodriver.DeleteResult
		if cmd.kind == "deleteOne" {
			res, err = coll.DeleteOne(ctx, filter)
		} else {
			res, err = coll.DeleteMany(ctx, filter)
		}
		if err != nil {
			return nil, err
		}
		return affected(start, res.DeletedCount, fmt.Sprintf("%d documents deleted", res.DeletedCount)), nil

	case "drop":
		if err := coll.Drop(ctx); err != nil {
			return nil, err
		}
		return said(start, "The collection "+cmd.collection+" is gone"), nil

	case "getIndexes":
		specs, err := c.src.indexSpecs(ctx, c.database(), cmd.collection)
		if err != nil {
			return nil, err
		}
		names := make([]string, 0, len(specs))
		for _, s := range specs {
			names = append(names, s.Name+": "+indexSummary(indexOf(s)))
		}
		return listed(start, "index", names), nil

	case "createIndex":
		keys, err := documentArg(cmd, 0)
		if err != nil {
			return nil, err
		}
		name, err := coll.Indexes().CreateOne(ctx, mongodriver.IndexModel{Keys: keys})
		if err != nil {
			return nil, err
		}
		return said(start, "The index "+name+" is made"), nil

	case "dropIndex":
		if len(cmd.args) == 0 {
			return nil, errors.New("mongodb: dropIndex takes an index's name")
		}
		name, ok := cmd.args[0].StringValueOK()
		if !ok {
			return nil, errors.New("mongodb: dropIndex takes an index's name")
		}
		if err := coll.Indexes().DropOne(ctx, name); err != nil {
			return nil, err
		}
		return said(start, "The index "+name+" is gone"), nil
	}
	return nil, fmt.Errorf("mongodb: db.%s.%s() is not a command this console knows", cmd.collection, cmd.kind)
}

// documentArg is one argument as a document.
func documentArg(cmd command, i int) (bson.D, error) {
	if i >= len(cmd.args) {
		return nil, fmt.Errorf("mongodb: %s wants a document as its argument %d", cmd.kind, i+1)
	}
	raw, ok := cmd.args[i].DocumentOK()
	if !ok {
		return nil, fmt.Errorf("mongodb: argument %d of %s is not a document", i+1, cmd.kind)
	}
	var doc bson.D
	if err := bson.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	return doc, nil
}

// optionalDocument is one argument as a document, or an empty one where the
// command was called without it: find() is find({}).
func optionalDocument(cmd command, i int) (bson.D, error) {
	if i >= len(cmd.args) {
		return bson.D{}, nil
	}
	return documentArg(cmd, i)
}

// arrayArg is one argument as a list of documents.
func arrayArg(cmd command, i int) ([]any, error) {
	if i >= len(cmd.args) {
		return nil, fmt.Errorf("mongodb: %s wants a list as its argument %d", cmd.kind, i+1)
	}
	arr, ok := cmd.args[i].ArrayOK()
	if !ok {
		return nil, fmt.Errorf("mongodb: argument %d of %s is not a list", i+1, cmd.kind)
	}
	vals, err := arr.Values()
	if err != nil {
		return nil, err
	}
	out := make([]any, 0, len(vals))
	for _, v := range vals {
		doc, ok := v.DocumentOK()
		if !ok {
			return nil, fmt.Errorf("mongodb: %s takes documents", cmd.kind)
		}
		var d bson.D
		if err := bson.Unmarshal(doc, &d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

// cursorResult reads what a cursor produced into a result. A console's
// answer is read in full: it is bounded, and a person is reading it.
func cursorResult(ctx context.Context, start time.Time, cur *mongodriver.Cursor) (*source.Result, error) {
	defer cur.Close(ctx)
	var docs []bson.Raw
	for cur.Next(ctx) {
		docs = append(docs, append(bson.Raw(nil), cur.Current...))
	}
	if err := cur.Err(); err != nil {
		return nil, err
	}
	return docsResult(start, docs), nil
}

func docsResult(start time.Time, docs []bson.Raw) *source.Result {
	return &source.Result{Rows: &produced{cols: pipelineColumns(docs), docs: docs},
		Affected: int64(len(docs)), Duration: time.Since(start)}
}

// counted is a number's answer, as a column of one.
func counted(start time.Time, n int64) *source.Result {
	return &source.Result{
		Rows: &listing{cols: []model.ColumnDef{{Name: "count", Type: model.DataType{Class: model.TypeInteger}}},
			rows: []model.Row{{n}}},
		Affected: n, Duration: time.Since(start),
	}
}

// splitCommands divides a console's script into its commands: one a line,
// and a line may end with a semicolon. A bracket left open carries the
// command onto the next line, which is how a pipeline is typed.
func splitCommands(script string) []source.ScriptStatement {
	var out []source.ScriptStatement
	depth, quote, start := 0, byte(0), 0
	flush := func(end int) {
		text := strings.TrimSpace(script[start:end])
		if text != "" && !strings.HasPrefix(text, "//") {
			out = append(out, source.ScriptStatement{Text: text, Offset: start + leading(script[start:end])})
		}
		start = end + 1
	}
	for i := 0; i < len(script); i++ {
		c := script[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '(' || c == '[' || c == '{':
			depth++
		case c == ')' || c == ']' || c == '}':
			if depth > 0 {
				depth--
			}
		case depth == 0 && (c == '\n' || c == ';'):
			flush(i)
		}
	}
	flush(len(script))
	return out
}

// leading is how much space a command begins with, so that its offset is
// where its first character is (FR-5.10).
func leading(text string) int {
	for i := 0; i < len(text); i++ {
		if !isSpace(text[i]) {
			return i
		}
	}
	return 0
}

// said is an answer of words rather than documents: a console says what it
// did where there is nothing to show.
func said(start time.Time, text string) *source.Result {
	return &source.Result{Rows: &produced{cols: []model.ColumnDef{{Name: "message",
		Type: model.DataType{Class: model.TypeString}}}}, Duration: time.Since(start),
		Messages: []source.Message{{Level: source.MessageInfo, Text: text}}}
}

// affected is a write's answer: how many, and a line saying it.
func affected(start time.Time, n int64, text string) *source.Result {
	res := said(start, text)
	res.Affected = n
	return res
}

// listed is a column of names, which is what show and getCollectionNames
// answer with.
func listed(start time.Time, what string, names []string) *source.Result {
	rows := make([]model.Row, 0, len(names))
	for _, n := range names {
		rows = append(rows, model.Row{n})
	}
	return &source.Result{
		Rows:     &listing{cols: []model.ColumnDef{{Name: what, Type: model.DataType{Class: model.TypeString}}}, rows: rows},
		Affected: int64(len(names)), Duration: time.Since(start),
	}
}

// values is a column of a field's values, which distinct answers with.
func values(start time.Time, field string, vals []bson.RawValue) *source.Result {
	rows := make([]model.Row, 0, len(vals))
	for _, v := range vals {
		rows = append(rows, model.Row{goValue(v)})
	}
	return &source.Result{
		Rows:     &listing{cols: []model.ColumnDef{{Name: field}}, rows: rows},
		Affected: int64(len(vals)), Duration: time.Since(start),
	}
}

// listing is a stream over rows already in hand.
type listing struct {
	cols []model.ColumnDef
	rows []model.Row
	at   int
}

func (l *listing) Columns() []model.ColumnDef { return l.cols }
func (l *listing) Close() error               { return nil }
func (l *listing) Next(ctx context.Context) (model.Row, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if l.at >= len(l.rows) {
		return nil, io.EOF
	}
	l.at++
	return l.rows[l.at-1], nil
}
