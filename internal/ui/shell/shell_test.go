package shell

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/capability"
	"github.com/ikigai-db/ikigai-db/internal/store"
	"github.com/ikigai-db/ikigai-db/internal/store/localdb"
	"github.com/ikigai-db/ikigai-db/internal/store/secrets"
	"github.com/ikigai-db/ikigai-db/internal/ui/commands"
	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
	uitheme "github.com/ikigai-db/ikigai-db/internal/ui/theme"
	"github.com/ikigai-db/ikigai-db/internal/ui/uithread"
)

// pgFake stands in for PostgreSQL: the same ID and URL schemes, so a pasted
// postgres:// URL resolves to it, but served from memory. The real driver
// cannot be linked here, because depguard keeps drivers out of internal/ui.
type pgFake struct{}

// otherFake has different fields, for switching engines in the form.
type otherFake struct{}

const fakeRows = 250

var itemsNode = model.Node{Ref: model.NewRef(model.KindTable, "main", "items"), Label: "items", Browsable: true}

func init() {
	source.Register(pgFake{})
	source.Register(otherFake{})
	source.Register(fileFake{})
}

// fileFake is a database that is a file, as SQLite is.
type fileFake struct{}

func (fileFake) Describe() source.Descriptor {
	return source.Descriptor{ID: "filefake", Name: "File Fake", Paradigm: model.ParadigmRelational,
		Fields: []source.Field{{Key: "database", Label: "File", Kind: source.FieldFile, Required: true}}}
}

func (fileFake) Open(context.Context, source.ConnectionConfig) (source.Source, error) {
	return fakeSource{}, nil
}

func (pgFake) Describe() source.Descriptor {
	return source.Descriptor{
		ID: "postgres", Name: "PG Fake", Paradigm: model.ParadigmRelational, DefaultPort: 7000,
		URLSchemes: []string{"postgres", "postgresql"},
		Fields: []source.Field{
			{Key: "host", Label: "Host", Kind: source.FieldText, Required: true, Default: "localhost"},
			{Key: "port", Label: "Port", Kind: source.FieldNumber, Default: "7000"},
			{Key: "database", Label: "Database", Kind: source.FieldText, Default: "main"},
			{Key: "user", Label: "User", Kind: source.FieldText},
			{Key: "password", Label: "Password", Kind: source.FieldPassword, Secret: true},
			{Key: "compress", Label: "Compress", Kind: source.FieldBool, Default: "true"},
		},
	}
}

func (pgFake) Open(_ context.Context, cfg source.ConnectionConfig) (source.Source, error) {
	if cfg.Host == "down" {
		return nil, &source.ConnectError{Kind: source.ConnectUnreachable,
			Hint: "The server could not be reached.", Err: errors.New("dial tcp: connection refused")}
	}
	return fakeSource{uncounted: cfg.Host == "nocount", unkeyed: cfg.Host == "nokey", fkeys: cfg.Host == "fkeys",
		labels: cfg.Host == "labels", noAnalyse: cfg.Host == "noanalyse",
		noTx: cfg.Host == "notx", txPoisons: cfg.Host == "txpoisons",
		noStats: cfg.Host == "nostats", guard: cfg.Guard}, nil
}

func (otherFake) Describe() source.Descriptor {
	return source.Descriptor{
		ID: "otherfake", Name: "Other Fake", Paradigm: model.ParadigmRelational,
		Fields: []source.Field{
			{Key: "host", Label: "Host", Kind: source.FieldText, Required: true},
			{Key: "path", Label: "File", Kind: source.FieldFile, Required: true},
		},
	}
}

func (otherFake) Open(context.Context, source.ConnectionConfig) (source.Source, error) {
	return fakeSource{}, nil
}

// fakeSource serves one database holding one table of fakeRows rows.
type fakeSource struct {
	uncounted bool
	unkeyed   bool // its rows cannot be told apart
	fkeys     bool // its items refer to parts by name
	labels    bool // its items refer to owners by id, and to parts by name
	noAnalyse bool // it will plan a statement but not run one to measure it
	noTx      bool // it holds no explicit transactions
	noStats   bool // it will not measure a column
	txPoisons bool // a statement that fails in a transaction ends what it could do
	guard     source.Guard
}

// Explain answers a small tree, measured where it was asked to run the
// statement (FR-5.13). The shape is what the window draws: a step that does
// most of the work under one that does little.
func (f fakeSource) Explain(_ context.Context, st source.Statement, analyze bool) (*source.Plan, error) {
	if analyze && f.noAnalyse {
		return nil, fmt.Errorf("fakesql: this server will not measure a statement")
	}
	access := source.AccessRead
	if analyze {
		access = fakeSource{}.Classify(st.SQL)
	}
	if err := f.guard.Allow(access, st.Confirmed); err != nil {
		return nil, err
	}
	if strings.Contains(st.SQL, "unplannable") {
		return nil, fmt.Errorf("fakesql: no plan for that")
	}
	leaf := &source.PlanNode{Operation: "Seq Scan on items", Detail: "Filter: (id > 1)",
		EstimatedCost: 90, EstimatedRows: 10, ActualRows: -1}
	root := &source.PlanNode{Operation: "Aggregate", EstimatedCost: 100, EstimatedRows: 1,
		ActualRows: -1, Children: []*source.PlanNode{leaf}}
	if analyze {
		leaf.ActualRows, leaf.ActualTime = 9000, 90*time.Millisecond
		root.ActualRows, root.ActualTime = 1, 100*time.Millisecond
	}
	return &source.Plan{Root: root, Text: `{"Plan": "as the server said"}`}, nil
}

func (fakeSource) Root(context.Context) ([]model.Node, error) {
	return []model.Node{{Ref: model.NewRef(model.KindDatabase, "main"), Label: "main", HasChildren: true}}, nil
}

func (fakeSource) Children(_ context.Context, ref model.ObjectRef) ([]model.Node, error) {
	switch ref.Kind {
	case model.KindDatabase:
		return []model.Node{itemsNode}, nil
	case model.KindTable:
		// A table's columns, as every driver lists them, with the type the
		// explorer and completion both read from Attrs (T2.29).
		var out []model.Node
		for _, c := range []struct{ name, typ string }{{"id", "integer"}, {"name", "text"}} {
			out = append(out, model.Node{
				Ref:   model.NewRef(model.KindColumn, append(append([]string{}, ref.Path...), c.name)...),
				Label: c.name, Attrs: map[string]string{"type": c.typ},
			})
		}
		return out, nil
	}
	return nil, nil
}

func (f fakeSource) Capabilities() capability.Capabilities {
	return capability.Capabilities{Paradigm: model.ParadigmRelational,
		Query: capability.Query{Supported: true, Language: "postgresql", MultiStatement: true,
			Explain: true, ExplainAnalyze: !f.noAnalyse, Transactions: !f.noTx},
		Data: capability.Data{ExactCount: !f.uncounted, ServerSort: true, ServerFilter: true,
			DistinctValues: true, ColumnStats: !f.noStats},
		Schema: capability.Schema{DDL: true}}
}
func (fakeSource) Info(context.Context) (source.ServerInfo, error) {
	return source.ServerInfo{Product: "FakeSQL", Version: "1.0"}, nil
}
func (fakeSource) Ping(context.Context) error { return pingErr() }
func (fakeSource) Close() error               { logClose("source"); return nil }
func (f fakeSource) Describe(_ context.Context, ref model.ObjectRef) (any, error) {
	if ref.Name() == "boom" {
		panic("fakesql: describing fell over")
	}
	// The objects whose structure is their source (FR-6.5).
	switch ref.Kind {
	case model.KindView, model.KindMaterializedView:
		return &model.View{Name: ref.Name(), Materialized: ref.Kind == model.KindMaterializedView,
			Definition: "SELECT id, name FROM items"}, nil
	case model.KindRoutine:
		return &model.Routine{Name: ref.Name(), Kind: model.RoutineFunction, Language: "sql",
			Definition: "CREATE OR REPLACE FUNCTION " + ref.Name() + " RETURNS integer AS $$ SELECT 1 $$"}, nil
	case model.KindTrigger:
		return &model.Trigger{Name: ref.Name(),
			Definition: "CREATE TRIGGER " + ref.Name() + " AFTER INSERT ON items EXECUTE FUNCTION log()"}, nil
	case model.KindSequence:
		return &model.Sequence{Name: ref.Name(), Start: 1, Increment: 1}, nil
	}
	tbl := &model.Table{Name: ref.Name(), RowsEstimate: 41,
		Columns: []model.Column{
			{Name: "id", Type: model.DataType{Class: model.TypeInteger, Native: "integer"}, Identity: true},
			{Name: "name", Type: model.DataType{Class: model.TypeString, Native: "text", Nullable: true}, Default: "'x'", HasDefault: true},
		},
		PrimaryKey: &model.PrimaryKey{Name: "items_pkey", Columns: []string{"id"}},
		Indexes:    []model.Index{{Name: "items_name", Columns: []model.IndexColumn{{Name: "name"}}}},
	}
	if f.labels {
		tbl.ForeignKeys = []model.ForeignKey{
			{Name: "items_owner", Columns: []string{"id"}, RefSchema: "main", RefTable: "owners", RefColumns: []string{"id"}},
			{Name: "items_part", Columns: []string{"name"}, RefSchema: "main", RefTable: "parts", RefColumns: []string{"name"}}}
	}
	if f.fkeys {
		tbl.ForeignKeys = []model.ForeignKey{{Name: "items_part", Columns: []string{"name"},
			RefSchema: "main", RefTable: "parts", RefColumns: []string{"name"}}}
	}
	return tbl, nil
}

// Referrers, on a source with foreign keys, says notes (by name) and tags
// (by id) refer to items, as widgets do by a column items do not have, and
// items (by name) refer to parts.
func (f fakeSource) Referrers(_ context.Context, ref model.ObjectRef) ([]model.Referrer, error) {
	if !f.fkeys {
		return nil, nil
	}
	key := func(from, col string) model.Referrer {
		return model.Referrer{From: model.NewRef(model.KindTable, "main", from), Key: model.ForeignKey{
			Columns: []string{col}, RefSchema: "main", RefTable: ref.Name(), RefColumns: []string{col}}}
	}
	switch ref.Name() {
	case "items":
		ghost := key("widgets", "ghost")
		ghost.Key.RefColumns = []string{"missing"}
		return []model.Referrer{key("notes", "name"), key("tags", "id"), ghost}, nil
	case "parts":
		return []model.Referrer{key("items", "name")}, nil
	}
	return nil, nil
}
func (fakeSource) Badge(context.Context, model.ObjectRef) (model.Badge, bool, error) {
	return model.Badge{}, false, nil
}
func (fakeSource) Count(context.Context, model.ObjectRef, source.BrowseOptions) (int64, error) {
	return fakeRows, nil
}

// distinctGate, when set, holds every Distinct until it is closed or the
// caller gives up; distinctStopped records a caller giving up.
var (
	distinctGate    chan struct{}
	distinctStopped atomic.Bool
)

// Distinct lists four values of any column, the most frequent first.
func (fakeSource) Distinct(ctx context.Context, _ model.ObjectRef, _ string, _ source.BrowseOptions, limit int) ([]source.DistinctValue, error) {
	if g := distinctGate; g != nil {
		select {
		case <-ctx.Done():
			distinctStopped.Store(true)
			return nil, ctx.Err()
		case <-g:
		}
	}
	vals := []source.DistinctValue{{Value: "item 1", Count: 5}, {Value: "item 2", Count: 3},
		{Value: nil, Count: 2}, {Value: "a,b", Count: 1}}
	return vals[:min(limit, len(vals))], nil
}

// ColumnStats measures a column: what the fake's one table holds, said
// plainly, and whatever was asked about the column's own kind (FR-3.14).
func (f fakeSource) ColumnStats(ctx context.Context, _ model.ObjectRef, def model.ColumnDef,
	opt source.BrowseOptions) (*source.ColumnStats, error) {
	if statsFails.Load() {
		return nil, fmt.Errorf("fakesql: the column could not be measured")
	}
	statsAsked.Add(1)
	// Only the unnarrowed reading waits, so that a test can have a later
	// one answer first and watch which of the two lands.
	if g := statsGate; g != nil && len(opt.Filters) == 0 && opt.Where == "" {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-g:
		}
	}
	rows := int64(fakeRows)
	if len(opt.Filters) > 0 || opt.Where != "" {
		rows = 10 // narrowed, so the figures are about what is left
	}
	out := &source.ColumnStats{Rows: rows, Nulls: rows / 5, Distinct: -1,
		Duration: time.Millisecond}
	if source.ManyValued(def) {
		out.Distinct = rows / 2
	}
	if source.Ordered(def) {
		out.Min, out.Max = int64(0), rows-1
	}
	if source.Numeric(def) {
		out.Mean, out.HasMean = float64(rows)/2, true
		out.Sum, out.HasSum = float64(rows)*float64(rows-1)/2, true
	}
	return out, nil
}

// statsFails makes measuring fail, and statsGate holds it until a test lets
// it go.
var (
	statsFails atomic.Bool
	statsGate  chan struct{}
	statsAsked atomic.Int64
)

// InsertRows writes each row as a statement naming what it was given.
func (fakeSource) InsertRows(_ model.ObjectRef, cols []model.ColumnDef, rows []model.Row) (string, error) {
	var b strings.Builder
	for _, r := range rows {
		fmt.Fprintf(&b, "INSERT %d %v;\n", len(cols), r)
	}
	return b.String(), nil
}

// fakeWrites records the plans the fake source applied. failWrite, when not
// 0, makes the statement at failWrite-1 fail.
var (
	fakeWrites struct {
		sync.Mutex
		plans []*source.WritePlan
		loads []fakeLoad
	}
	failWrite atomic.Int32
)

func writtenPlans() []*source.WritePlan {
	fakeWrites.Lock()
	defer fakeWrites.Unlock()
	return append([]*source.WritePlan(nil), fakeWrites.plans...)
}

// fakeLoad is a load the fake source took.
type fakeLoad struct {
	columns []string
	rows    []model.Row
	opt     source.LoadOptions
}

func loadsSoFar() []fakeLoad {
	fakeWrites.Lock()
	defer fakeWrites.Unlock()
	return append([]fakeLoad(nil), fakeWrites.loads...)
}

// LoadRows keeps the rows a load gives it, as the guard allows, committing
// 500 rows at a time as the drivers do; failWrite, when not 0, is the row
// refused, by its place among those it is given, and left out when told.
func (f fakeSource) LoadRows(ctx context.Context, _ model.ObjectRef, columns []string, rows model.RowStream, opt source.LoadOptions) (int64, error) {
	if err := f.guard.Allow(source.AccessWrite, opt.Confirmed); err != nil {
		return 0, err
	}
	var got []model.Row
	given := 0
	committed := func() int64 {
		if opt.Truncate {
			return 0
		}
		return int64(len(got) / 500 * 500)
	}
	for {
		r, err := rows.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return committed(), err
		}
		if given++; given == int(failWrite.Load()) {
			e := &source.LoadError{Row: int64(given), Err: errors.New("fakesql: duplicate key")}
			if opt.OnError != "skip" {
				return committed(), e
			}
			opt.Skipped(e)
			continue
		}
		got = append(got, r)
	}
	fakeWrites.Lock()
	fakeWrites.loads = append(fakeWrites.loads, fakeLoad{columns: columns, rows: got, opt: opt})
	fakeWrites.Unlock()
	return int64(len(got)), nil
}

// Plan writes each change as a line of text, and binds one value to it.
func (f fakeSource) Plan(_ context.Context, cs source.Changeset) (*source.WritePlan, error) {
	plan := &source.WritePlan{Target: cs.Target, Atomic: true, Guarded: f.guard.RequiresConfirmation(source.AccessWrite)}
	for i, c := range cs.Changes {
		plan.Statements = append(plan.Statements, source.Statement{
			SQL: fmt.Sprintf("%d %v %v", c.Kind, c.Key, c.Values), Args: []any{"x"}, Confirmed: cs.Confirmed})
		plan.Descriptions = append(plan.Descriptions, fmt.Sprintf("change %d", i+1))
	}
	return plan, nil
}

// Apply records a plan the guard allows, failing where failWrite says.
func (f fakeSource) Apply(_ context.Context, plan *source.WritePlan) (*source.WriteOutcome, error) {
	for _, st := range plan.Statements {
		if err := f.guard.Allow(source.AccessWrite, st.Confirmed); err != nil {
			return nil, err
		}
	}
	fakeWrites.Lock()
	fakeWrites.plans = append(fakeWrites.plans, plan)
	fakeWrites.Unlock()
	if at := int(failWrite.Load()) - 1; at >= 0 && at < len(plan.Statements) {
		return &source.WriteOutcome{Applied: at, FailedAt: at, RolledBack: true, Err: errors.New("fakesql: duplicate key")}, nil
	}
	return &source.WriteOutcome{Applied: len(plan.Statements), Affected: int64(len(plan.Statements)), FailedAt: -1}, nil
}

// browses records every Browse, for tests that check what was asked for.
var browses struct {
	sync.Mutex
	opts []source.BrowseOptions
}

func (f fakeSource) Browse(_ context.Context, _ model.ObjectRef, opt source.BrowseOptions) (model.RowStream, error) {
	browses.Lock()
	browses.opts = append(browses.opts, opt)
	browses.Unlock()
	if strings.Contains(opt.Where, "panic") {
		panic("fakesql: the driver fell over")
	}
	if strings.Contains(opt.Where, "boom") {
		return nil, errors.New(`fakesql: syntax error at or near "boom"`)
	}
	for _, f := range opt.Filters {
		if f.Op == source.OpRegex {
			return nil, errors.New("fakesql: no regular expressions")
		}
	}
	end := int64(fakeRows)
	if opt.Limit > 0 && opt.Offset+opt.Limit < end {
		end = opt.Offset + opt.Limit
	}
	rows := &sliceStream{next: opt.Offset, end: end, keyed: !f.unkeyed}
	if len(opt.Columns) == 0 {
		return rows, nil
	}
	p := &projected{RowStream: rows}
	for _, name := range opt.Columns {
		i := slices.IndexFunc(rows.Columns(), func(c model.ColumnDef) bool { return c.Name == name })
		if i < 0 {
			return nil, fmt.Errorf("fakesql: no column %q", name)
		}
		p.idx = append(p.idx, i)
	}
	return p, nil
}

// projected serves a stream's rows in the columns a browse named, in that
// order, as a source does.
type projected struct {
	model.RowStream
	idx []int
}

func (p *projected) Columns() []model.ColumnDef {
	all := p.RowStream.Columns()
	out := make([]model.ColumnDef, len(p.idx))
	for i, j := range p.idx {
		out[i] = all[j]
	}
	return out
}

func (p *projected) Next(ctx context.Context) (model.Row, error) {
	r, err := p.RowStream.Next(ctx)
	if err != nil {
		return nil, err
	}
	out := make(model.Row, len(p.idx))
	for i, j := range p.idx {
		out[i] = r[j]
	}
	return out, nil
}

type sliceStream struct {
	next, end int64
	keyed     bool
}

// Identity is the items' key, id, on a source whose rows have one.
func (s *sliceStream) Identity() model.RowIdentity {
	if !s.keyed {
		return model.RowIdentity{}
	}
	return model.RowIdentity{Kind: model.IdentityPrimaryKey, Columns: []string{"id"}, Target: itemsNode.Ref}
}

func (*sliceStream) Columns() []model.ColumnDef {
	return []model.ColumnDef{{Name: "id", Type: model.DataType{Class: model.TypeInteger}}, {Name: "name", Type: model.DataType{Nullable: true}}}
}
func (s *sliceStream) Next(context.Context) (model.Row, error) {
	if s.next >= s.end {
		return nil, io.EOF
	}
	i := s.next
	s.next++
	return model.Row{i, fmt.Sprintf("item %d", i)}, nil
}
func (*sliceStream) Close() error { return nil }

type fixture struct {
	s            *Shell
	q            *uithread.Queue
	conns        *app.Connections
	ws           *app.Workspace
	settings     *store.SettingsFile
	settingsPath string
	hist         *localdb.DB
	deps         Deps
	files        *fakeFiles
	app          *notedApp
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	a := &notedApp{App: test.NewTempApp(t)}
	// Opened first so it closes last: history writes finish in the background.
	hist, err := localdb.Open(context.Background(), filepath.Join(t.TempDir(), "ikigai.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { hist.Close() })
	path := filepath.Join(t.TempDir(), "settings.json")
	sf, _, err := store.OpenSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	conns := app.NewConnections(sf, app.NewVault(secrets.NewMemory(), nil), nil)
	ws := app.NewWorkspace(conns, app.MonitorConfig{Interval: time.Hour})
	q := &uithread.Queue{}
	files := &fakeFiles{}
	d := Deps{Conns: conns, WS: ws, Settings: sf, History: hist, Saved: hist, Scratch: hist, Session: hist, Params: hist,
		Decoders: hist,
		Layouts:  hist,
		Autosave: time.Millisecond, Run: q.Run, GOOS: "darwin", Files: files}
	s := New(a, d)
	t.Cleanup(s.shutdown)
	return &fixture{s: s, q: q, conns: conns, ws: ws, settings: sf, settingsPath: path, hist: hist, deps: d,
		files: files, app: a}
}

func (fx *fixture) create(t *testing.T, host string, sec map[string]string) store.SavedConnection {
	t.Helper()
	c, err := fx.conns.Create(store.SavedConnection{Name: host, Driver: "postgres", Host: host}, sec)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func (fx *fixture) onlyTab(t *testing.T) *tab {
	t.Helper()
	if len(fx.s.open) != 1 {
		t.Fatalf("%d tabs open, want 1", len(fx.s.open))
	}
	return fx.s.open[0]
}

// pump runs queued UI work, as the UI goroutine would, until cond holds.
func pump(t *testing.T, q *uithread.Queue, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition never held")
		}
		if q.Flush() == 0 {
			time.Sleep(2 * time.Millisecond)
		}
	}
}

func findButton(o fyne.CanvasObject, text string) *widget.Button {
	switch v := o.(type) {
	case *widget.Button:
		if v.Text == text {
			return v
		}
	case *widget.PopUp:
		return findButton(v.Content, text)
	case *fyne.Container:
		for _, c := range v.Objects {
			if b := findButton(c, text); b != nil {
				return b
			}
		}
	case fyne.Widget:
		// A dialog sits in an OverlayContainer, a widget: look inside
		// through its renderer.
		for _, c := range test.WidgetRenderer(v).Objects() {
			if b := findButton(c, text); b != nil {
				return b
			}
		}
	}
	return nil
}

func labelText(o fyne.CanvasObject) string {
	var objs []fyne.CanvasObject
	switch v := o.(type) {
	case *widget.Label:
		return v.Text
	case *fyne.Container:
		objs = v.Objects
	case fyne.Widget:
		objs = test.WidgetRenderer(v).Objects()
	}
	var parts []string
	for _, c := range objs {
		if s := labelText(c); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "\n")
}

func TestEveryShortcutIsOnTheMenuBar(t *testing.T) {
	fx := newFixture(t)
	seen := map[string]string{}
	for _, c := range fx.s.reg.All() {
		if c.Shortcut.IsZero() {
			continue
		}
		it, ok := fx.s.menuItems[c.ID]
		if !ok {
			t.Errorf("%s has a shortcut but no menu item, so it would not work while a text field has focus", c.ID)
			continue
		}
		name := it.Shortcut.ShortcutName()
		if other, dup := seen[name]; dup {
			t.Errorf("%s and %s share %s", c.ID, other, c.Shortcut.Label("darwin"))
		}
		seen[name] = c.ID
	}
	if len(seen) == 0 {
		t.Fatal("no shortcuts registered")
	}
}

func TestDisabledMenuItemsDoNothingEvenWhenTriggered(t *testing.T) {
	fx := newFixture(t)
	// Fyne's shortcut matching calls a menu item's Action whatever Disabled
	// says, so the action itself must refuse. The real commands' Run bodies are
	// defensive enough to hide the difference; a probe that always has an
	// effect does not.
	ran := false
	fx.s.reg.MustRegister(commands.Command{ID: "test.probe", Title: "Probe",
		Enabled: func() bool { return false }, Run: func() { ran = true }})
	fx.s.menuItemsFor([]menuEntry{item("test.probe")})[0].Action()
	if ran {
		t.Fatal("a disabled command ran from its menu item")
	}

	it := fx.s.menuItems[cmdTabClose]
	if !it.Disabled {
		t.Error("Close Tab should be disabled with no tabs open")
	}

	c := fx.create(t, "db1", nil)
	fx.s.OpenObject(c.ID, itemsNode)
	if it.Disabled {
		t.Error("Close Tab should be enabled once a tab is open")
	}
	it.Action()
	if len(fx.s.open) != 0 {
		t.Error("Close Tab did not close the tab")
	}
}

func TestOpenObjectShowsRowsAndCount(t *testing.T) {
	fx := newFixture(t)
	if !fx.s.empty.Visible() || fx.s.tabs.Visible() {
		t.Fatal("a new window should show the empty state")
	}
	c := fx.create(t, "db1", nil)
	fx.s.OpenObject(c.ID, itemsNode)
	if fx.s.empty.Visible() || !fx.s.tabs.Visible() {
		t.Error("opening a tab should replace the empty state")
	}
	tb := fx.onlyTab(t)
	if tb.item.Text != "items" {
		t.Errorf("tab title %q", tb.item.Text)
	}
	pump(t, fx.q, func() bool { return tb.footer.Text == "250 rows" })

	tb.grid.Prefetch(0, 100)
	pump(t, fx.q, func() bool { return tb.model.Resident(99) })
	row, loaded := tb.model.Row(context.Background(), 3)
	if !loaded || len(row) != 2 || row[1] != "item 3" {
		t.Errorf("row 3 = %v (loaded %v)", row, loaded)
	}
	fx.s.win.Canvas().Capture() // renders the grid inside the shell
	fx.q.Flush()

	if got := fx.s.status.Text; !strings.HasPrefix(got, "db1 — ") || strings.Contains(got, "not connected") {
		t.Errorf("status %q should describe the connection in focus", got)
	}
}

func TestOpeningAnOpenObjectSelectsItsTab(t *testing.T) {
	fx := newFixture(t)
	c := fx.create(t, "db1", nil)
	other := itemsNode
	other.Ref, other.Label = model.NewRef(model.KindTable, "main", "other"), "other"

	fx.s.OpenObject(c.ID, itemsNode)
	fx.s.OpenObject(c.ID, other)
	fx.s.OpenObject(c.ID, itemsNode)
	if len(fx.s.open) != 2 {
		t.Fatalf("%d tabs, want 2", len(fx.s.open))
	}
	if fx.s.tabs.Selected() != fx.s.open[0].item {
		t.Error("reopening should bring the existing tab forward")
	}
	fx.s.run(cmdTabNext)
	if fx.s.tabs.Selected() != fx.s.open[1].item {
		t.Error("Show Next Tab did not move on")
	}
}

func TestClosingATabCancelsItsLoading(t *testing.T) {
	fx := newFixture(t)
	c := fx.create(t, "db1", nil)
	fx.s.OpenObject(c.ID, itemsNode)
	tb := fx.onlyTab(t)
	fx.s.closeTab(tb.item)
	if tb.ctx.Err() == nil {
		t.Error("closing must cancel the tab's work")
	}
	if !fx.s.empty.Visible() {
		t.Error("closing the last tab should bring back the empty state")
	}
	time.Sleep(20 * time.Millisecond)
	fx.q.Flush()
	if tb.grid != nil {
		t.Error("a result arriving after the tab closed built a grid anyway")
	}
}

func TestUnreachableServerIsExplainedInTheTab(t *testing.T) {
	fx := newFixture(t)
	c := fx.create(t, "down", nil)
	fx.s.OpenObject(c.ID, itemsNode)
	tb := fx.onlyTab(t)
	pump(t, fx.q, func() bool { return strings.Contains(labelText(tb.body), "could not be reached") })
}

func TestMissingPasswordOffersToEditTheConnection(t *testing.T) {
	fx := newFixture(t)
	c := fx.create(t, "db1", map[string]string{"password": "pw"})
	if err := fx.conns.Vault().Delete(c.ID, "password"); err != nil {
		t.Fatal(err)
	}
	fx.s.OpenObject(c.ID, itemsNode)
	tb := fx.onlyTab(t)
	var btn *widget.Button
	pump(t, fx.q, func() bool { btn = findButton(tb.body, "Edit Connection…"); return btn != nil })
	test.Tap(btn)
	if fx.s.win.Canvas().Overlays().Top() == nil {
		t.Error("the button should open the connection form")
	}
}

func TestDeletingAConnectionClosesItsTabsAndSession(t *testing.T) {
	fx := newFixture(t)
	c := fx.create(t, "db1", nil)
	fx.s.OpenObject(c.ID, itemsNode)
	pump(t, fx.q, func() bool { _, open := fx.ws.Get(c.ID); return open })
	fx.s.deleteConnection(c.ID)
	if len(fx.s.open) != 0 || len(fx.conns.List()) != 0 {
		t.Errorf("tabs %d, connections %d; want none", len(fx.s.open), len(fx.conns.List()))
	}
	if _, open := fx.ws.Get(c.ID); open {
		t.Error("the session outlived its connection")
	}
}

func TestNewConnectionKeepsThePasswordOutOfTheSettingsFile(t *testing.T) {
	fx := newFixture(t)
	f := fx.s.showConnectionForm("")
	f.driver.SetSelected("PG Fake")
	test.Type(f.inputs["host"].entry, "db1")
	test.Type(f.inputs["user"].entry, "ann")
	test.Type(f.inputs["password"].entry, "s3cret")
	test.Tap(f.saveBtn)

	list := fx.conns.List()
	if len(list) != 1 {
		t.Fatalf("%d connections saved, want 1 (form says %q)", len(list), f.result.Text)
	}
	c := list[0]
	if c.Host != "db1" || c.Port != 7000 || c.Database != "main" || c.User != "ann" ||
		c.Name != "db1/main" || c.Params["compress"] != "true" {
		t.Errorf("saved %+v; blank fields should take the driver's defaults", c)
	}
	if pw, err := fx.conns.Vault().Get(c.ID, "password"); err != nil || pw != "s3cret" {
		t.Errorf("keychain has %q, %v", pw, err)
	}
	raw, err := os.ReadFile(fx.settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("s3cret")) {
		t.Fatal("the password reached the settings file")
	}
	if fx.s.win.Canvas().Overlays().Top() != nil {
		t.Error("the form should close after saving")
	}
}

func TestFormRefusesABadPortAndKeepsTheInput(t *testing.T) {
	fx := newFixture(t)
	f := fx.s.showConnectionForm("")
	f.driver.SetSelected("PG Fake")
	test.Type(f.inputs["port"].entry, "70000")
	test.Tap(f.saveBtn)
	if len(fx.conns.List()) != 0 {
		t.Fatal("saved a connection with an impossible port")
	}
	if !strings.Contains(f.result.Text, "1 to 65535") || f.result.Importance != widget.DangerImportance {
		t.Errorf("message %q (importance %v)", f.result.Text, f.result.Importance)
	}
	if f.inputs["port"].entry.Text != "70000" {
		t.Error("the form must keep what was typed so it can be corrected")
	}
}

func TestEditingWithABlankPasswordKeepsTheSavedOne(t *testing.T) {
	fx := newFixture(t)
	c := fx.create(t, "db1", map[string]string{"password": "old"})
	f := fx.s.showConnectionForm(c.ID)
	if f.inputs["password"].entry.Text != "" {
		t.Error("a saved password must never be read back into the form")
	}
	var hint string
	for _, it := range f.form.Items {
		if it.Text == "Password" {
			hint = it.HintText
		}
	}
	if !strings.Contains(hint, "Leave blank") {
		t.Errorf("password hint %q should say a blank keeps the saved one", hint)
	}
	f.inputs["user"].entry.SetText("bob")
	test.Tap(f.saveBtn)
	got, _ := fx.conns.Get(c.ID)
	if got.User != "bob" {
		t.Errorf("user %q, want bob (form says %q)", got.User, f.result.Text)
	}
	if pw, _ := fx.conns.Vault().Get(c.ID, "password"); pw != "old" {
		t.Errorf("saved password became %q", pw)
	}
}

func TestPastedURLFillsTheFormAndClearsItself(t *testing.T) {
	fx := newFixture(t)
	f := fx.s.showConnectionForm("")
	f.url.SetText("postgres://ann:pw@db.example.com:6543/sales?application_name=ikigai")
	f.applyURL(f.url.Text)

	for key, want := range map[string]string{
		"host": "db.example.com", "port": "6543", "database": "sales", "user": "ann", "password": "pw",
	} {
		if got := f.inputs[key].entry.Text; got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if f.driver.Selected != "PG Fake" {
		t.Errorf("driver %q", f.driver.Selected)
	}
	if f.url.Text != "" {
		t.Error("the URL field still shows a password in clear text")
	}
	test.Tap(f.saveBtn)
	list := fx.conns.List()
	if len(list) != 1 || list[0].Params["application_name"] != "ikigai" {
		t.Fatalf("saved %+v; parameters the form has no field for must survive", list)
	}
	if pw, _ := fx.conns.Vault().Get(list[0].ID, "password"); pw != "pw" {
		t.Errorf("password from the URL was not saved: %q", pw)
	}
}

func TestSwitchingEngineKeepsSharedFields(t *testing.T) {
	fx := newFixture(t)
	f := fx.s.showConnectionForm("")
	f.driver.SetSelected("PG Fake")
	test.Type(f.inputs["host"].entry, "db1")
	f.driver.SetSelected("Other Fake")
	if _, ok := f.inputs["port"]; ok {
		t.Error("Other Fake has no port field")
	}
	if f.inputs["path"] == nil || f.inputs["host"].entry.Text != "db1" {
		t.Error("the host should carry over and the file field appear")
	}
	// Name, Type, URL; host, file; encryption, environment, read-only, message.
	if n := len(f.form.Items); n != 9 {
		t.Errorf("%d form rows, want 9", n)
	}
}

func TestTestConnectionReportsWithoutSaving(t *testing.T) {
	fx := newFixture(t)
	f := fx.s.showConnectionForm("")
	f.driver.SetSelected("PG Fake")
	test.Type(f.inputs["host"].entry, "db1")
	test.Tap(f.testBtn)
	pump(t, fx.q, func() bool { return strings.HasPrefix(f.result.Text, "Connected to FakeSQL 1.0") })
	if len(fx.conns.List()) != 0 {
		t.Error("testing a connection saved it")
	}

	f.inputs["host"].entry.SetText("down")
	test.Tap(f.testBtn)
	pump(t, fx.q, func() bool { return f.result.Importance == widget.DangerImportance })
	if !strings.Contains(f.result.Text, "could not be reached") {
		t.Errorf("failure message %q", f.result.Text)
	}
}

func TestAppearanceChoiceIsCheckedAndRemembered(t *testing.T) {
	fx := newFixture(t)
	if !fx.s.menuItems[cmdAppearSystem].Checked {
		t.Error("a new install follows the system")
	}
	fx.s.run(cmdAppearDark)
	if !fx.s.menuItems[cmdAppearDark].Checked || fx.s.menuItems[cmdAppearSystem].Checked {
		t.Error("the menu should tick the chosen appearance, and only it")
	}
	if got := appearanceNamed(fx.settings.Get().Appearance); got != uitheme.AppearanceDark {
		t.Errorf("the next launch would start as %v", got)
	}
}

func TestToggleSidebar(t *testing.T) {
	fx := newFixture(t)
	fx.s.run(cmdSidebar)
	if fx.s.sidebar.Visible() {
		t.Error("sidebar still visible")
	}
	fx.s.run(cmdSidebar)
	if !fx.s.sidebar.Visible() {
		t.Error("sidebar did not come back")
	}
}

func TestRowCountWording(t *testing.T) {
	for _, c := range []struct {
		n     int64
		known bool
		want  string
	}{
		{0, true, "0 rows"}, {1, true, "1 row"}, {999, true, "999 rows"},
		{1000, true, "1,000 rows"}, {1234567, true, "1,234,567 rows"},
		{-1, true, "Row count unknown"}, {5, false, "Row count unknown"},
	} {
		if got := rowCount(c.n, c.known); got != c.want {
			t.Errorf("rowCount(%d, %v) = %q, want %q", c.n, c.known, got, c.want)
		}
	}
}

func TestUncountedTableLoadsAndFindsItsEnd(t *testing.T) {
	// PostgreSQL reports no exact count, because counting is a full scan. The
	// tab must still load rows by itself, with nobody calling Prefetch.
	fx := newFixture(t)
	c := fx.create(t, "nocount", nil)
	fx.s.OpenObject(c.ID, itemsNode)
	tb := fx.onlyTab(t)
	want := "250 rows"
	if grid.PageSize < fakeRows {
		want = group(grid.PageSize) + "+ rows"
	}
	pump(t, fx.q, func() bool {
		fx.s.win.Canvas().Capture() // drawing the placeholders is what fetches
		return tb.footer.Text == want
	})
	if !tb.model.Resident(0) {
		t.Error("footer updated but no rows are resident")
	}
}

func TestAFileDatabaseHasNoEncryptionSetting(t *testing.T) {
	fx := newFixture(t)
	f := fx.s.showConnectionForm("")
	f.driver.SetSelected("File Fake")
	for _, it := range f.form.Items {
		if it.Text == "Encryption" {
			t.Fatal("a file database has nothing to encrypt in transit")
		}
	}
	test.Type(f.inputs["database"].entry, "/data/app.db")
	test.Tap(f.saveBtn)
	list := fx.conns.List()
	if len(list) != 1 || list[0].TLS.Mode != "" || list[0].Database != "/data/app.db" {
		t.Errorf("saved %+v", list)
	}
}

func TestSortingAColumnBrowsesAgainOnTheServer(t *testing.T) {
	fx := newFixture(t)
	c := fx.create(t, "db1", nil)
	fx.s.OpenObject(c.ID, itemsNode)
	tb := fx.onlyTab(t)
	pump(t, fx.q, func() bool { return tb.browse != nil })
	if !tb.grid.Sortable {
		t.Fatal("a table the server can sort should have sortable headers")
	}
	tb.grid.ToggleSort(1, false)
	pump(t, fx.q, func() bool { return len(tb.browse.Options().Sorts) == 1 })
	if got := tb.browse.Options().Sorts[0]; got.Column != "name" || got.Descending {
		t.Errorf("sort %+v, want name ascending", got)
	}
	browses.Lock()
	browses.opts = nil
	browses.Unlock()
	tb.grid.Prefetch(0, 10)
	pump(t, fx.q, func() bool {
		browses.Lock()
		defer browses.Unlock()
		for _, o := range browses.opts {
			if o.Limit > 1 && len(o.Sorts) == 1 && o.Sorts[0].Column == "name" {
				return true
			}
		}
		return false
	})
	tb.grid.ToggleSort(1, false)
	pump(t, fx.q, func() bool { s := tb.browse.Options().Sorts; return len(s) == 1 && s[0].Descending })
}

// This fake renders DDL, so that the designer's preview has something to
// show. What it renders is not SQL anybody would run: it is the shape of the
// change, which is what a test about the preview is asking about.

func (fakeSource) CreateObject(ref model.ObjectRef, obj any) ([]source.Statement, error) {
	switch v := obj.(type) {
	case *model.View:
		return []source.Statement{{SQL: "CREATE OR REPLACE VIEW " + ref.Name() + " AS " + v.Definition}}, nil
	case *model.Routine:
		return []source.Statement{{SQL: v.Definition}}, nil
	case *model.Trigger:
		return []source.Statement{{SQL: "DROP TRIGGER " + v.Name}, {SQL: v.Definition}}, nil
	case *model.Sequence:
		return []source.Statement{{SQL: fmt.Sprintf("ALTER SEQUENCE %s START WITH %d", ref.Name(), v.Start)}}, nil
	}
	// A table names itself, so that a script can be read as the objects it
	// builds rather than as the word CREATE several times over.
	if t, ok := obj.(*model.Table); ok {
		return []source.Statement{{SQL: "CREATE TABLE " + ref.Name() + " -- " + t.Name}}, nil
	}
	return []source.Statement{{SQL: "CREATE"}}, nil
}

func (fakeSource) DropObject(ref model.ObjectRef, cascade bool) ([]source.Statement, error) {
	return []source.Statement{{SQL: "DROP " + ref.Name()}}, nil
}

func (fakeSource) RenameColumn(_ model.ObjectRef, from, to string) ([]source.Statement, error) {
	return []source.Statement{{SQL: "RENAME " + from + " TO " + to}}, nil
}

// Snapshot reads the whole database in one pass, as a driver that can does.
// Without it the walk would find nothing: this fake lists its tables under
// the database directly rather than under a class folder.
func (fakeSource) Snapshot(_ context.Context, database string) (*model.Database, error) {
	if database == "boom" {
		return nil, errors.New("fakesql: this schema will not be read")
	}
	// items is what the tree lists; the other three give a diagram a shape
	// to be narrowed down — orders points at items, lines points at orders,
	// and alone points at nothing.
	items := model.Table{Name: "items", RowsEstimate: -1, Columns: fakeColumns(),
		PrimaryKey: &model.PrimaryKey{Name: "items_pkey", Columns: []string{"id"}}}
	orders := model.Table{Name: "orders", RowsEstimate: -1,
		Columns: []model.Column{{Name: "id", Position: 1,
			Type: model.DataType{Class: model.TypeInteger, Native: "integer", Length: -1}},
			{Name: "item", Position: 2,
				Type: model.DataType{Class: model.TypeInteger, Native: "integer", Length: -1}}},
		PrimaryKey: &model.PrimaryKey{Name: "orders_pkey", Columns: []string{"id"}},
		ForeignKeys: []model.ForeignKey{{Name: "orders_item_fkey", Columns: []string{"item"},
			RefSchema: database, RefTable: "items", RefColumns: []string{"id"}}}}
	lines := model.Table{Name: "lines", RowsEstimate: -1,
		Columns: []model.Column{{Name: "ord", Position: 1,
			Type: model.DataType{Class: model.TypeInteger, Native: "integer", Length: -1}}},
		ForeignKeys: []model.ForeignKey{{Name: "lines_ord_fkey", Columns: []string{"ord"},
			RefSchema: database, RefTable: "orders", RefColumns: []string{"id"}}}}
	alone := model.Table{Name: "alone", RowsEstimate: -1,
		Columns: []model.Column{{Name: "id", Position: 1,
			Type: model.DataType{Class: model.TypeInteger, Native: "integer", Length: -1}}}}

	// A second schema, so that drawing one is not the same as drawing the
	// database: every real server has more than one.
	audit := model.Schema{Name: "audit", Tables: []model.Table{{Name: "log", RowsEstimate: -1,
		Columns: []model.Column{{Name: "id", Position: 1,
			Type: model.DataType{Class: model.TypeInteger, Native: "integer", Length: -1}}}}}}

	return &model.Database{Name: database, Schemas: []model.Schema{
		{Name: database, Tables: []model.Table{items, orders, lines, alone}}, audit}}, nil
}

// fakeColumns are the columns this fake's one table has, the same two its
// tree lists.
func fakeColumns() []model.Column {
	return []model.Column{
		{Name: "id", Position: 1, Type: model.DataType{Class: model.TypeInteger, Native: "integer", Length: -1}},
		{Name: "name", Position: 2, Type: model.DataType{Class: model.TypeString, Native: "text", Length: -1}},
	}
}

// Dependents answers what names an object, the way PostgreSQL does: a view
// and a key that the rename carries, and a routine body that it does not.
func (fakeSource) Dependents(_ context.Context, ref model.ObjectRef) ([]model.Dependent, error) {
	if ref.Name() == "nothing" {
		return nil, nil
	}
	return []model.Dependent{
		{Ref: model.NewRef(model.KindView, "main", "recent"), Label: "recent",
			Note: "A view that selects from it. PostgreSQL holds it by identity, so the rename carries it."},
		{Ref: model.NewRef(model.KindRoutine, "main", "total()"), Label: "total()",
			Note: "Its plpgsql body names it in text, which PostgreSQL never resolved.", Breaks: true},
	}, nil
}

// RenameObject renames only what the real driver can: a fake that renamed
// anything would let a test pass where the window offers a rename that no
// engine could carry out.
func (fakeSource) RenameObject(ref model.ObjectRef, to string) ([]source.Statement, error) {
	switch ref.Kind {
	case model.KindTable, model.KindView, model.KindMaterializedView,
		model.KindSequence, model.KindIndex, model.KindRoutine, model.KindTrigger:
	default:
		return nil, fmt.Errorf("fakesql: a %s cannot be renamed", ref.Kind)
	}
	if to == "" {
		return nil, fmt.Errorf("fakesql: a rename needs a new name")
	}
	if to == ref.Name() {
		return nil, nil
	}
	return []source.Statement{{SQL: "ALTER " + strings.ToUpper(string(ref.Kind)) + " " + ref.Name() + " RENAME TO " + to}}, nil
}

func (fakeSource) AlterObject(_ model.ObjectRef, from, to any) ([]source.Statement, error) {
	was, _ := from.(*model.Table)
	now, _ := to.(*model.Table)
	if was == nil || now == nil {
		return nil, fmt.Errorf("fakesql: %T cannot be altered", from)
	}
	var out []source.Statement
	for _, w := range was.Columns {
		if !slices.ContainsFunc(now.Columns, func(n model.Column) bool { return n.Name == w.Name }) {
			out = append(out, source.Statement{SQL: "DROP COLUMN " + w.Name})
		}
	}
	for _, n := range now.Columns {
		if !slices.ContainsFunc(was.Columns, func(w model.Column) bool { return w.Name == n.Name }) {
			out = append(out, source.Statement{SQL: "ADD COLUMN " + n.Name})
		}
	}
	return out, nil
}
