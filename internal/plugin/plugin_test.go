package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/store"
	"github.com/ikigai-db/ikigai-db/plugin"
)

// Running somebody else's program and believing none of it (FR-16.2).
//
// Two kinds of test here. Most drive the host over a pair of pipes with a plugin
// written in this process, which is how a plugin that answers nonsense — a row
// longer than its columns, a node with no path, a line that is not JSON at all,
// an answer to a question nobody asked — can be exercised without writing a
// program for each. One builds the worked example and runs it for real, because
// a protocol that works only against a test double is a protocol nobody has
// proved.

// ctx is the context every request here is made with. It has a deadline, so
// that a host which stops noticing something — an error it should have read, an
// ending it should have waited for — fails the test in a moment rather than
// hanging the suite until the test binary gives up.
func ctx(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return c
}

// hosted is a host talking to a plugin served in this process.
type hosted struct {
	*Process
	hello plugin.Hello
}

// serve puts a plugin on one end of two pipes and a host on the other.
func serve(t *testing.T, src plugin.Source) *hosted {
	t.Helper()
	toPlugin, hostWrites := io.Pipe()
	pluginWrites, fromPlugin := io.Pipe()
	served := make(chan error, 1)
	go func() { served <- plugin.ServeOn(toPlugin, fromPlugin, src) }()

	p := newProcess("test", hostWrites, pluginWrites, nil, nil)
	t.Cleanup(func() {
		p.Close()
		fromPlugin.Close()
		<-served
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	hello, err := p.hello(ctx)
	if err != nil {
		t.Fatalf("the handshake: %v", err)
	}
	return &hosted{Process: p, hello: hello}
}

// open is the plugin as a source, which is what the application holds.
func (h *hosted) open(t *testing.T, cfg source.ConnectionConfig) *Source {
	t.Helper()
	src, err := Driver{Process: h.Process, Hello: h.hello}.Open(ctx(t), cfg)
	if err != nil {
		t.Fatalf("opening: %v", err)
	}
	return src.(*Source)
}

// fake is a plugin that answers whatever a test needs it to.
type fake struct {
	hello plugin.Hello

	openErr   error
	nodes     []plugin.Node
	object    plugin.Object
	cols      []plugin.Column
	rows      [][]any
	browseErr error
	// slow makes Browse wait for the context to end, for the tests about
	// giving up on a request.
	slow bool
	// held is closed when a slow browse has been let go of, and started when
	// one has begun: a test that slept instead would be a test that passed on
	// a fast machine.
	held    chan struct{}
	started chan struct{}
	// helloEntered and helloReleased hold the handshake where a test needs to
	// say when it finishes: with them, "the input ended while an answer was
	// being written" is arranged rather than hoped for.
	helloEntered  chan struct{}
	helloReleased chan struct{}

	mu   sync.Mutex
	last plugin.BrowseOptions
	shut bool
}

func newFake() *fake {
	return &fake{
		hello: plugin.Hello{ID: "testplug", Name: "Test Plugin", Version: "1",
			Fields:       []plugin.Field{{Key: "database", Label: "Folder"}},
			Capabilities: plugin.Capabilities{Objects: []string{"table"}}},
		nodes: []plugin.Node{{Ref: plugin.Ref{Kind: "table", Path: []string{"db", "items"}},
			Label: "items", Browsable: true}},
		object:  plugin.Object{Columns: []plugin.Column{{Name: "id", Class: "integer"}}},
		cols:    []plugin.Column{{Name: "id", Class: "integer"}, {Name: "name", Class: "string"}},
		rows:    [][]any{{1, "one"}, {2, "two"}},
		held:    make(chan struct{}),
		started: make(chan struct{}),
	}
}

func (f *fake) Hello() plugin.Hello {
	if f.helloEntered != nil {
		close(f.helloEntered)
		<-f.helloReleased
	}
	return f.hello
}

func (f *fake) Open(ctx context.Context, cfg plugin.Config) (string, error) {
	if f.openErr != nil {
		return "", f.openErr
	}
	return "h1", nil
}

func (f *fake) Close(handle string) error {
	f.mu.Lock()
	f.shut = true
	f.mu.Unlock()
	return nil
}

func (f *fake) closed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.shut
}

func (f *fake) Ping(ctx context.Context, handle string) error { return nil }

func (f *fake) Info(ctx context.Context, handle string) (plugin.Info, error) {
	return plugin.Info{Product: "Test", Version: "1", Database: "db"}, nil
}

func (f *fake) Root(ctx context.Context, handle string) ([]plugin.Node, error) {
	return f.nodes, nil
}

func (f *fake) Children(ctx context.Context, handle string, ref plugin.Ref) ([]plugin.Node, error) {
	return f.nodes, nil
}

func (f *fake) Describe(ctx context.Context, handle string, ref plugin.Ref) (plugin.Object, error) {
	o := f.object
	o.Ref = ref
	return o, nil
}

func (f *fake) Browse(ctx context.Context, handle string, ref plugin.Ref,
	opt plugin.BrowseOptions, w plugin.RowWriter) error {
	f.mu.Lock()
	f.last = opt
	f.mu.Unlock()
	if f.browseErr != nil {
		return f.browseErr
	}
	if err := w.Columns(f.cols); err != nil {
		return err
	}
	for _, r := range f.rows {
		if err := w.Row(r...); err != nil {
			return err
		}
	}
	if f.slow {
		close(f.started)
		<-ctx.Done()
		close(f.held)
		return ctx.Err()
	}
	return nil
}

func (f *fake) asked() plugin.BrowseOptions {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.last
}

func TestAPluginBecomesADriver(t *testing.T) {
	h := serve(t, newFake())
	d := Driver{Process: h.Process, Hello: h.hello}
	desc := d.Describe()
	if desc.ID != "testplug" || desc.Name != "Test Plugin" {
		t.Errorf("it describes itself as %+v", desc)
	}
	if len(desc.Fields) != 1 || desc.Fields[0].Key != "database" {
		t.Errorf("its form is %+v", desc.Fields)
	}
	if desc.Paradigm != model.ParadigmRelational {
		t.Errorf("its paradigm is %v", desc.Paradigm)
	}
	src := h.open(t, source.ConnectionConfig{})
	caps := src.Capabilities()
	if caps.Query.Supported {
		t.Error("it claims to take statements, and the protocol has no way to send one")
	}
	if !caps.Objects[model.KindTable] || !caps.Objects[model.KindFolder] {
		t.Errorf("its objects are %v", caps.Objects)
	}
	info, err := src.Info(ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	if info.Product != "Test" || info.Attrs["database"] != "db" {
		t.Errorf("it says %+v", info)
	}
	if err := src.Ping(ctx(t)); err != nil {
		t.Errorf("ping: %v", err)
	}
	// A badge is the expensive question, asked once per visible node, and a
	// plugin is not asked it at all.
	if _, ok, err := src.Badge(ctx(t), model.NewRef(model.KindTable, "db", "items")); ok || err != nil {
		t.Errorf("it offered a badge: %v %v", ok, err)
	}
}

// The handshake is what makes a plugin a plugin. A program that does not answer
// it, or answers it with something this cannot use, is not talked to.
//
// The answers are written by hand rather than served: Serve fills in the
// protocol version and the kind for a Go plugin, which is a kindness to plugin
// authors and exactly what has to be got round to test a version this does not
// know.
func TestAProgramThatCannotSayWhatItIsIsNotUsed(t *testing.T) {
	for name, c := range map[string]struct {
		answer string
		says   string
	}{
		"another version": {`{"id":1,"hello":{"protocol":99,"kind":"source","id":"x"},"done":true}`, "version"},
		"another kind":    {`{"id":1,"hello":{"protocol":1,"kind":"formatter","id":"x"},"done":true}`, "formatter"},
		"no driver name":  {`{"id":1,"hello":{"protocol":1,"kind":"source"},"done":true}`, "no driver name"},
		"nothing at all":  {`{"id":1,"done":true}`, "nothing"},
	} {
		t.Run(name, func(t *testing.T) {
			asked, hostWrites := io.Pipe()
			answers, said := io.Pipe()
			go func() {
				// Read the request, then answer it as the case says.
				buf := make([]byte, 4096)
				asked.Read(buf)
				fmt.Fprintln(said, c.answer)
			}()
			p := newProcess("test", hostWrites, answers, nil, nil)
			defer p.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err := p.hello(ctx)
			if err == nil {
				t.Fatal("it was accepted")
			}
			if !strings.Contains(err.Error(), c.says) {
				t.Errorf("it says %q, which does not mention %q", err, c.says)
			}
		})
	}
}

// A program that says nothing at all is given up on rather than waited for.
func TestAProgramThatSaysNothingIsGivenUpOn(t *testing.T) {
	// It takes what it is sent and answers nothing, which is the shape of a
	// program that has hung: a reader that never ends and a writer that
	// swallows. Waiting for it is what the deadline is for.
	silence, never := io.Pipe()
	defer silence.Close()
	defer never.Close()
	p := newProcess("silent", nopCloser{io.Discard}, silence, nil, nil)
	defer p.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := p.hello(ctx); err == nil {
		t.Fatal("a plugin that answered nothing was accepted")
	}
}

// nopCloser is a writer that can be closed, for a plugin whose input goes
// nowhere.
type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }

// What a plugin says went wrong is what the application says, in its own terms:
// a wrong password reads as a wrong password and not as a plugin error.
func TestWhyAConnectionFailedIsCarriedThrough(t *testing.T) {
	for kind, want := range map[string]source.ConnectKind{
		plugin.FailConfig:     source.ConnectConfig,
		plugin.FailAuth:       source.ConnectAuth,
		plugin.FailNetwork:    source.ConnectUnreachable,
		plugin.FailTLS:        source.ConnectTLS,
		plugin.FailNoDatabase: source.ConnectNoDatabase,
		plugin.FailRefused:    source.ConnectRefused,
		"something else":      source.ConnectUnknown,
	} {
		t.Run(kind, func(t *testing.T) {
			f := newFake()
			f.openErr = plugin.Failed(kind, errors.New("it would not open"))
			h := serve(t, f)
			_, err := Driver{Process: h.Process, Hello: h.hello}.Open(
				ctx(t), source.ConnectionConfig{})
			if err == nil {
				t.Fatal("it opened")
			}
			var ce *source.ConnectError
			if !errors.As(err, &ce) {
				t.Fatalf("it said %v, which is not a connection failure", err)
			}
			if ce.Kind != want {
				t.Errorf("it is kind %v, want %v", ce.Kind, want)
			}
			if !strings.Contains(ce.Error(), "would not open") {
				t.Errorf("it says %q", ce.Error())
			}
		})
	}
}

// The secrets a connection holds reach the plugin when it opens, and only the
// ones its own form asked for.
func TestOnlyTheSecretsItAskedForAreSent(t *testing.T) {
	f := newFake()
	f.hello.Fields = []plugin.Field{
		{Key: "password", Label: "Password", Secret: true},
		{Key: "database", Label: "Folder"},
	}
	got := make(chan plugin.Config, 1)
	h := serve(t, &recording{fake: f, got: got})
	asked := map[string]bool{}
	_ = h.open(t, source.ConnectionConfig{
		Host: "h", Port: 5, Database: "db", User: "u",
		Guard:  source.Guard{ReadOnly: true, Environment: source.EnvProduction},
		Params: map[string]string{"mode": "fast"},
		Secret: func(key string) (string, error) {
			asked[key] = true
			return "s:" + key, nil
		},
	})
	cfg := <-got
	if cfg.Host != "h" || cfg.Port != 5 || cfg.Database != "db" || cfg.User != "u" {
		t.Errorf("it was told %+v", cfg)
	}
	if cfg.Params["mode"] != "fast" {
		t.Errorf("its parameters are %v", cfg.Params)
	}
	// Read-only and which environment this is: a plugin refuses what the
	// application cannot see, and a plugin that was never told cannot (NFR-S4).
	if !cfg.ReadOnly {
		t.Error("a read-only connection was not said to be one")
	}
	if cfg.Environment != "production" {
		t.Errorf("its environment is %q", cfg.Environment)
	}
	if cfg.Secrets["password"] != "s:password" {
		t.Errorf("its secrets are %v", cfg.Secrets)
	}
	if _, sent := cfg.Secrets["database"]; sent {
		t.Error("a field that is not secret was sent as one")
	}
	if asked["database"] {
		t.Error("the keychain was asked for a field that is not secret")
	}
}

// recording is a plugin that keeps the configuration it was opened with.
type recording struct {
	*fake
	got chan plugin.Config
}

func (r *recording) Open(ctx context.Context, cfg plugin.Config) (string, error) {
	r.got <- cfg
	return r.fake.Open(ctx, cfg)
}

func TestTheTreeAndTheRowsComeThrough(t *testing.T) {
	f := newFake()
	h := serve(t, f)
	src := h.open(t, source.ConnectionConfig{})
	roots, err := src.Root(ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 1 || roots[0].Label != "items" || !roots[0].Browsable {
		t.Fatalf("its root is %+v", roots)
	}
	if got := roots[0].Ref.String(); got != "table:db.items" {
		t.Errorf("its reference is %q", got)
	}
	obj, err := src.Describe(ctx(t), roots[0].Ref)
	if err != nil {
		t.Fatal(err)
	}
	table, ok := obj.(*model.Table)
	if !ok {
		t.Fatalf("it described a %T", obj)
	}
	if table.Name != "items" || len(table.Columns) != 1 || table.Columns[0].Name != "id" {
		t.Errorf("it described %+v", table)
	}
	if table.RowsEstimate != -1 {
		t.Errorf("it claims %d rows", table.RowsEstimate)
	}

	rs, err := src.Browse(ctx(t), roots[0].Ref, source.BrowseOptions{
		Offset: 10, Limit: 5, Where: "id > 1",
		Sorts:   []source.Sort{{Column: "id", Descending: true}},
		Filters: []source.Filter{{Column: "name", Op: source.OpEqual, Values: []any{"one"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	if cols := rs.Columns(); len(cols) != 2 || cols[0].Name != "id" ||
		cols[0].Type.Class != model.TypeInteger {
		t.Errorf("its columns are %+v", cols)
	}
	var rows []model.Row
	for {
		row, err := rs.Next(ctx(t))
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
	}
	if len(rows) != 2 {
		t.Fatalf("it read %d rows: %v", len(rows), rows)
	}
	// A whole number in an integer column stays a whole number: JSON has one
	// number type, and a count read as a float prints as 1e+06.
	if n, ok := rows[0][0].(int64); !ok || n != 1 {
		t.Errorf("the first value is %#v", rows[0][0])
	}
	if s, ok := rows[0][1].(string); !ok || s != "one" {
		t.Errorf("the second value is %#v", rows[0][1])
	}
	// And everything the browse was asked for reached the plugin.
	asked := f.asked()
	if asked.Offset != 10 || asked.Limit != 5 || asked.Where != "id > 1" {
		t.Errorf("it was asked %+v", asked)
	}
	if len(asked.Sorts) != 1 || !asked.Sorts[0].Descending {
		t.Errorf("its sorts are %+v", asked.Sorts)
	}
	if len(asked.Filters) != 1 || asked.Filters[0].Value != "one" {
		t.Errorf("its filters are %+v", asked.Filters)
	}
}

// An object with a definition and no columns is a view, which is how one type on
// the wire becomes the several the application has.
func TestADefinitionWithNoColumnsIsAView(t *testing.T) {
	f := newFake()
	f.object = plugin.Object{Definition: "SELECT 1", Comment: "a view"}
	h := serve(t, f)
	src := h.open(t, source.ConnectionConfig{})
	obj, err := src.Describe(ctx(t), model.NewRef(model.KindView, "db", "recent"))
	if err != nil {
		t.Fatal(err)
	}
	v, ok := obj.(*model.View)
	if !ok {
		t.Fatalf("it described a %T", obj)
	}
	if v.Name != "recent" || v.Definition != "SELECT 1" || v.Comment != "a view" {
		t.Errorf("it described %+v", v)
	}
}

// Nothing a plugin says is trusted to be sensible. None of this is hypothetical
// politeness: a plugin is somebody else's program, and the first one written
// against a new protocol gets something wrong.
func TestNonsenseFromAPluginIsSurvived(t *testing.T) {
	f := newFake()
	f.nodes = []plugin.Node{
		{Ref: plugin.Ref{Kind: "table", Path: []string{"db", "good"}}, Label: "good"},
		{Ref: plugin.Ref{Kind: "table"}, Label: "no path"},
		{Ref: plugin.Ref{Path: []string{"db", "no kind"}}, Label: "no kind"},
		{Ref: plugin.Ref{Kind: "table", Path: []string{"db", "unnamed"}}},
	}
	f.rows = [][]any{
		{1, "one", "a column nobody declared"},
		{2},
	}
	h := serve(t, f)
	src := h.open(t, source.ConnectionConfig{})
	nodes, err := src.Root(ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 {
		t.Fatalf("it kept %d of four nodes: %+v", len(nodes), nodes)
	}
	if nodes[1].Label != "unnamed" {
		t.Errorf("a node with no label of its own is called %q", nodes[1].Label)
	}
	rs, err := src.Browse(ctx(t), nodes[0].Ref, source.BrowseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	for i := 0; i < 2; i++ {
		row, err := rs.Next(ctx(t))
		if err != nil {
			t.Fatalf("row %d: %v", i, err)
		}
		if len(row) != 2 {
			t.Errorf("row %d has %d values for 2 columns: %v", i, len(row), row)
		}
	}
}

// A row with nothing in it cannot be sent, because the line it would make is
// indistinguishable from a line with no row in it. The plugin is told so, rather
// than the host guessing about somebody's data.
func TestARowOfNothingIsRefused(t *testing.T) {
	f := newFake()
	f.rows = [][]any{{1, "one"}, {}}
	h := serve(t, f)
	src := h.open(t, source.ConnectionConfig{})
	rs, err := src.Browse(ctx(t), model.NewRef(model.KindTable, "db", "items"),
		source.BrowseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	if _, err := rs.Next(ctx(t)); err != nil {
		t.Fatalf("the first row: %v", err)
	}
	_, err = rs.Next(ctx(t))
	if err == nil || errors.Is(err, io.EOF) {
		t.Fatalf("it said %v, where the rows stopped making sense", err)
	}
	if !strings.Contains(err.Error(), "no values") {
		t.Errorf("it said %q", err)
	}
}

// A failure partway through the rows is the stream's failure, not a short
// stream: a pipeline that read half a table and called it the table would be
// worse than one that stopped.
func TestAFailureWhileReadingIsAFailure(t *testing.T) {
	f := newFake()
	h := serve(t, f)
	src := h.open(t, source.ConnectionConfig{})
	f.browseErr = errors.New("the disk went away")
	_, err := src.Browse(ctx(t), model.NewRef(model.KindTable, "db", "items"),
		source.BrowseOptions{})
	if err == nil {
		t.Fatal("it opened the rows")
	}
	if !strings.Contains(err.Error(), "disk went away") {
		t.Errorf("it said %v", err)
	}
}

// Giving up on a request tells the plugin, so that it can stop doing the work.
// A plugin that ignores it is killed with its process, which is the backstop.
func TestGivingUpOnARequestTellsThePlugin(t *testing.T) {
	f := newFake()
	f.slow = true
	h := serve(t, f)
	src := h.open(t, source.ConnectionConfig{})
	ctx, cancel := context.WithCancel(context.Background())
	rs, err := src.Browse(ctx, model.NewRef(model.KindTable, "db", "items"), source.BrowseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// The rows it did write arrive first.
	if _, err := rs.Next(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err := rs.Next(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("it said %v", err)
	}
	rs.Close()
	select {
	case <-f.held:
	case <-time.After(5 * time.Second):
		t.Error("the plugin was never told to stop")
	}
}

// Closing a connection is not closing the plugin: one process serves every
// connection somebody makes to that driver.
func TestClosingAConnectionLeavesThePluginRunning(t *testing.T) {
	f := newFake()
	h := serve(t, f)
	src := h.open(t, source.ConnectionConfig{})
	if err := src.Close(); err != nil {
		t.Fatal(err)
	}
	if !f.closed() {
		t.Error("the plugin was not told")
	}
	// Closing twice is not an error, and the process still answers.
	if err := src.Close(); err != nil {
		t.Errorf("closing again: %v", err)
	}
	if _, err := h.Process.one(ctx(t), plugin.Request{Op: plugin.OpPing}); err != nil {
		t.Errorf("the plugin stopped answering: %v", err)
	}
}

// Once the plugin has gone, every request fails with why rather than with a
// broken pipe.
func TestOnceThePluginHasGoneItSaysSo(t *testing.T) {
	h := serve(t, newFake())
	src := h.open(t, source.ConnectionConfig{})
	h.Process.Close()
	_, err := src.Root(ctx(t))
	if err == nil {
		t.Fatal("it answered")
	}
	if !strings.Contains(err.Error(), "closed") {
		t.Errorf("it said %v", err)
	}
}

// A line that is not a request, and an answer to a request nobody is waiting
// for: both are dropped and the conversation goes on. A plugin that wrote a
// stray line to its output would otherwise take the connection down with it.
func TestStrayLinesAreDropped(t *testing.T) {
	toPlugin, hostWrites := io.Pipe()
	pluginWrites, fromPlugin := io.Pipe()
	f := newFake()
	go plugin.ServeOn(toPlugin, fromPlugin, f)
	p := newProcess("test", hostWrites, pluginWrites, nil, nil)
	defer p.Close()

	// Nonsense from the plugin's side, before anything else.
	go func() {
		fmt.Fprintln(fromPlugin, "this is not JSON")
		fmt.Fprintln(fromPlugin, `{"id":999,"done":true}`)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	hello, err := p.hello(ctx)
	if err != nil {
		t.Fatalf("after nonsense, the handshake said %v", err)
	}
	if hello.ID != "testplug" {
		t.Errorf("it said %+v", hello)
	}
}

// The example in examples/ is built and run for real, because a protocol proved
// only against a plugin written in the same process is a protocol whose
// process-shaped parts — the pipes, the handshake, the shutdown — nobody has
// tried. It is also the only test of the example itself, which is documentation
// somebody will copy.
func TestTheWorkedExampleWorks(t *testing.T) {
	dir := t.TempDir()
	plugins := filepath.Join(dir, "plugins", "csvdir")
	if err := os.MkdirAll(plugins, 0o755); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-o", filepath.Join(plugins, program()),
		"../../examples/csvdir")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("building the example: %v", err)
	}
	data := filepath.Join(dir, "data")
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "items.csv"),
		[]byte("id,name\n1,one\n2,two\n3,three\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	set, err := Load(ctx(t), filepath.Join(dir, "plugins"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()
	if len(set.Plugins) != 1 || len(set.Failed) != 0 {
		t.Fatalf("it loaded %+v and failed %v", set.Plugins, set.Failed)
	}
	if !strings.Contains(set.Describe(), "CSV folder") {
		t.Errorf("it says %q", set.Describe())
	}
	// Registered like any other driver, which is the whole point: nothing in
	// the application knows this one is a plugin (REQ-DB-1).
	drv, err := source.Lookup("csvdir")
	if err != nil {
		t.Fatalf("it is not in the registry: %v", err)
	}
	src, err := drv.Open(ctx(t), source.ConnectionConfig{Database: data})
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	roots, err := src.Root(ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 1 || roots[0].Label != "Tables" {
		t.Fatalf("its root is %+v", roots)
	}
	tables, err := src.Children(ctx(t), roots[0].Ref)
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) != 1 || tables[0].Label != "items" {
		t.Fatalf("its tables are %+v", tables)
	}
	rs, err := src.Browse(ctx(t), tables[0].Ref, source.BrowseOptions{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	var read int
	for {
		row, err := rs.Next(ctx(t))
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		read++
		if len(row) != 2 {
			t.Errorf("a row of %d values: %v", len(row), row)
		}
	}
	if read != 2 {
		t.Errorf("it read %d rows for a limit of 2", read)
	}
	// A folder that is not there is a connection that says so, in the
	// application's own terms.
	_, err = drv.Open(ctx(t), source.ConnectionConfig{Database: filepath.Join(dir, "nope")})
	var ce *source.ConnectError
	if !errors.As(err, &ce) || ce.Kind != source.ConnectNoDatabase {
		t.Errorf("it said %v", err)
	}
}

// buildTestPlug builds the badly-behaved plugin into a plugin directory of its
// own, and answers the directory that holds it.
func buildTestPlug(t *testing.T, names ...string) string {
	t.Helper()
	root := t.TempDir()
	built := ""
	for _, name := range names {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if name == "nothing" {
			continue // a directory with no program in it
		}
		if built == "" {
			built = filepath.Join(root, program())
			build := exec.Command("go", "build", "-o", built, "./testdata/testplug")
			build.Stderr = os.Stderr
			if err := build.Run(); err != nil {
				t.Fatalf("building the test plugin: %v", err)
			}
		}
		// Copied rather than built again: the same program, in as many plugin
		// directories as the test wants.
		b, err := os.ReadFile(built)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, program()), b, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// What does not load is said, and the rest go on: they are separate programs and
// there is no reason for them to share a fate.
func TestWhatDoesNotLoadIsSaidAndTheRestGoOn(t *testing.T) {
	t.Setenv("IKIGAI_TEST_PLUGIN", "id:loadable")
	dir := buildTestPlug(t, "one", "two", "nothing")
	set, err := Load(ctx(t), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()
	if len(set.Plugins) != 1 {
		t.Fatalf("it loaded %d plugins: %+v", len(set.Plugins), set.Plugins)
	}
	if set.Plugins[0].ID != "loadable" {
		t.Errorf("it loaded %q", set.Plugins[0].ID)
	}
	if len(set.Failed) != 2 {
		t.Fatalf("it failed %v", set.Failed)
	}
	// The second copy claims a driver name the first one took, and a directory
	// with no program in it is not a plugin.
	if why := fmt.Sprint(set.Failed["two"]); !strings.Contains(why, "already taken") {
		t.Errorf("the second copy failed with %q", why)
	}
	if why := fmt.Sprint(set.Failed["nothing"]); !strings.Contains(why, "no "+program()) {
		t.Errorf("a directory with no program failed with %q", why)
	}
	if said := set.Describe(); !strings.Contains(said, "Test loadable") ||
		!strings.Contains(said, "2 did not load") {
		t.Errorf("it says %q", said)
	}
}

// A program that ends before it says anything, and one that says something that
// is not the protocol, are both refused rather than waited for.
func TestAProgramThatIsNotAPluginIsRefused(t *testing.T) {
	for mode, says := range map[string]string{
		"exit":    "did not say what it is",
		"garbage": "did not say what it is",
	} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("IKIGAI_TEST_PLUGIN", mode)
			dir := buildTestPlug(t, "bad")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			set, err := Load(ctx, dir, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer set.Close()
			if len(set.Plugins) != 0 {
				t.Fatalf("it loaded %+v", set.Plugins)
			}
			if why := fmt.Sprint(set.Failed["bad"]); !strings.Contains(why, says) {
				t.Errorf("it failed with %q, which does not mention %q", why, says)
			}
		})
	}
}

// A program that stops listening is killed rather than waited for: an
// application that cannot shut down because of a plugin is an application a
// plugin can hang.
func TestAPluginThatWillNotStopIsKilled(t *testing.T) {
	t.Setenv("IKIGAI_TEST_PLUGIN", "deaf")
	dir := buildTestPlug(t, "deaf")
	set, err := Load(ctx(t), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Plugins) != 1 {
		t.Fatalf("it loaded %+v, failed %v", set.Plugins, set.Failed)
	}
	// It reads nothing, so a request to it fails rather than hanging — after
	// the queue fills, which for one request it does not, so this waits for
	// the deadline it was given rather than for the send.
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err := set.Plugins[0].Process.one(ctx, plugin.Request{Op: plugin.OpPing}); err == nil {
		t.Error("a plugin that reads nothing answered")
	}
	done := make(chan struct{})
	go func() {
		set.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(shutdown + 5*time.Second):
		t.Fatal("closing a plugin that will not stop did not end")
	}
}

// With no directory named, plugins come from the one in the application's own
// data: installing one is putting a folder there and nothing more.
func TestTheDefaultDirectoryIsTheOneInTheData(t *testing.T) {
	t.Setenv("IKIGAI_TEST_PLUGIN", "id:defaultdir")
	data := t.TempDir()
	built := buildTestPlug(t, "one")
	// Moved to where a plugin goes when nobody says otherwise.
	plugins := filepath.Join(data, "plugins")
	if err := os.MkdirAll(plugins, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(built, "one"), filepath.Join(plugins, "one")); err != nil {
		t.Fatal(err)
	}
	set, err := FromSettings(context.Background(), &store.Plugins{Enabled: true},
		store.Paths{Data: data}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()
	if len(set.Plugins) != 1 || set.Plugins[0].ID != "defaultdir" {
		t.Fatalf("it loaded %+v, failed %v", set.Plugins, set.Failed)
	}
}

// The settings decide whether any of this happens at all. Off is the absence of
// a section, and off is also a section that says so.
func TestNothingRunsUnlessItWasTurnedOn(t *testing.T) {
	t.Setenv("IKIGAI_TEST_PLUGIN", "id:offbydefault")
	dir := buildTestPlug(t, "one")
	paths := store.Paths{Data: dir}
	for name, c := range map[string]struct {
		set    *store.Plugins
		loaded int
	}{
		"no section":        {nil, 0},
		"turned off":        {&store.Plugins{Directory: dir}, 0},
		"turned on":         {&store.Plugins{Enabled: true, Directory: dir}, 1},
		"on, and no folder": {&store.Plugins{Enabled: true, Directory: filepath.Join(dir, "nope")}, 0},
	} {
		t.Run(name, func(t *testing.T) {
			set, err := FromSettings(ctx(t), c.set, paths, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer set.Close()
			if len(set.Plugins) != c.loaded {
				t.Errorf("it loaded %d plugins, want %d", len(set.Plugins), c.loaded)
			}
			if c.loaded > 0 {
				// Loaded once per run of this subtest, so the registry would
				// refuse a second: unregistering is not a thing the registry
				// does, and this is the only test that needs it to be.
				return
			}
			if said := set.Describe(); c.loaded == 0 && said != "No plugins." {
				t.Errorf("it says %q", said)
			}
		})
	}
}

// "Off by default" is the absence of a section in the file, not a flag set to
// false: a settings file that has never seen this feature says nothing about it
// (FR-16.2).
func TestAFileWithNoPluginsHasNoSection(t *testing.T) {
	dir := t.TempDir()
	settings, _, err := store.OpenSettings(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(settings.Get())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "plugin") {
		t.Errorf("a fresh settings file says:\n%s", raw)
	}
	// And turned on, it says so and where from, because "which code is
	// running" is a question somebody must be able to answer.
	if err := settings.Update(func(s *store.Settings) error {
		s.Plugins = &store.Plugins{Enabled: true, Directory: "/somewhere"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(settings.Get())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"enabled":true`, `"directory":"/somewhere"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("it holds %s, which does not carry %s", raw, want)
		}
	}
}

// A plugin that gives no name of its own is called by its driver name. A plugin
// with no name at all would be a blank line in the new-connection picker.
func TestAPluginWithNoNameIsCalledByItsDriverName(t *testing.T) {
	asked, hostWrites := io.Pipe()
	answers, said := io.Pipe()
	go func() {
		buf := make([]byte, 4096)
		asked.Read(buf)
		fmt.Fprintln(said, `{"id":1,"hello":{"protocol":1,"kind":"source","id":"nameless"},"done":true}`)
	}()
	p := newProcess("test", hostWrites, answers, nil, nil)
	defer p.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	hello, err := p.hello(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if hello.Name != "nameless" {
		t.Errorf("it is called %q", hello.Name)
	}
	if got := (Driver{Process: p, Hello: hello}).Describe().Name; got != "nameless" {
		t.Errorf("the picker would show %q", got)
	}
}

// One plugin that will not start does not stop the others: they are separate
// programs and there is no reason for them to share a fate.
func TestAPluginThatWillNotStartDoesNotStopTheOthers(t *testing.T) {
	t.Setenv("IKIGAI_TEST_PLUGIN", "bydir")
	// The same program in two places, behaving differently by where it is: the
	// one installed under "bad" writes nonsense instead of a handshake.
	dir := buildTestPlug(t, "bad-one", "good-one")
	// No deadline of this test's own: a caller's deadline covers every plugin
	// together, and the one that hangs here is meant to use up the handshake's
	// own ten seconds. What is being tested is that the other one still loads.
	set, err := Load(context.Background(), dir, nil)
	if err != nil {
		t.Fatalf("it gave up: %v", err)
	}
	defer set.Close()
	if len(set.Plugins) != 1 || set.Plugins[0].ID != "bydir" {
		t.Fatalf("it loaded %+v, failed %v", set.Plugins, set.Failed)
	}
	if len(set.Failed) != 1 {
		t.Errorf("it failed %v", set.Failed)
	}
}

// A plugin that goes away on its own says so to everything asked of it
// afterwards, rather than failing with a broken pipe.
func TestAPluginThatEndsOnItsOwnSaysSo(t *testing.T) {
	t.Setenv("IKIGAI_TEST_PLUGIN", "bye")
	dir := buildTestPlug(t, "bye")
	set, err := Load(ctx(t), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()
	if len(set.Plugins) != 1 {
		t.Fatalf("it loaded %+v, failed %v", set.Plugins, set.Failed)
	}
	p := set.Plugins[0].Process
	// It answered the handshake and then ended. Whatever is asked next is
	// answered with that, and with a deadline of its own so that a host which
	// noticed nothing would fail here rather than wait.
	var err2 error
	for i := 0; i < 50; i++ {
		_, err2 = p.one(ctx(t), plugin.Request{Op: plugin.OpPing})
		if err2 != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err2 == nil {
		t.Fatal("a plugin that has gone answered a request")
	}
	if !strings.Contains(err2.Error(), "stopped answering") {
		t.Errorf("it said %v", err2)
	}
}

// A row written before the columns is a plugin author's mistake, and is reported
// as one rather than drawn as a row of nothing.
func TestARowBeforeItsColumnsIsRefused(t *testing.T) {
	t.Setenv("IKIGAI_TEST_PLUGIN", "rowfirst")
	dir := buildTestPlug(t, "rowfirst")
	set, err := Load(ctx(t), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()
	if len(set.Plugins) != 1 {
		t.Fatalf("it loaded %+v, failed %v", set.Plugins, set.Failed)
	}
	drv, err := source.Lookup("rowfirst")
	if err != nil {
		t.Fatal(err)
	}
	src, err := drv.Open(ctx(t), source.ConnectionConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	_, err = src.Browse(ctx(t), model.NewRef(model.KindTable, "db", "items"),
		source.BrowseOptions{})
	if err == nil {
		t.Fatal("the rows opened")
	}
	if !strings.Contains(err.Error(), "before the columns") {
		t.Errorf("it said %v", err)
	}
}

// An operation a plugin does not have is refused by the plugin, rather than
// answered as though it had happened.
func TestAnOperationAPluginDoesNotHaveIsRefused(t *testing.T) {
	h := serve(t, newFake())
	_, err := h.Process.one(ctx(t), plugin.Request{Op: "sing"})
	if err == nil {
		t.Fatal("it sang")
	}
	if !strings.Contains(err.Error(), "sing") {
		t.Errorf("it said %v, which does not say what it was asked", err)
	}
}

// A plugin whose input ends while it is answering finishes the answer first. A
// host closes a plugin's input to ask it to stop, and an answer lost that way
// reads as a plugin that died in the middle of a request.
//
// Arranged rather than hoped for: the plugin is held inside the handshake, the
// input is ended while it is held, and only then is it let go. What must be true
// when serving returns is that the answer is already written.
func TestAnAnswerInFlightIsFinishedBeforeThePluginEnds(t *testing.T) {
	f := newFake()
	f.helloEntered, f.helloReleased = make(chan struct{}), make(chan struct{})
	var out said
	in, host := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- plugin.ServeOn(in, &out, f) }()

	if _, err := fmt.Fprintln(host, `{"id":1,"op":"hello"}`); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.helloEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("the handshake never started")
	}
	host.Close()           // the host has finished with it, mid-answer
	close(f.helloReleased) // and now the answer can be written
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("serving ended with %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serving never ended")
	}
	if got := out.String(); !strings.Contains(got, "testplug") {
		t.Errorf("serving ended having written %q", got)
	}
}

// said is somewhere a plugin's answers can go that never blocks, so that what
// was written is a question about order and not about who read first.
type said struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *said) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *said) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// When a plugin's input ends, whatever it was working on is given up on: a
// handler that never finished would otherwise keep the plugin alive for ever,
// which is a plugin that cannot be closed.
func TestWorkInFlightIsGivenUpOnWhenTheInputEnds(t *testing.T) {
	f := newFake()
	f.slow = true // its Browse waits for the context and nothing else
	in, host := io.Pipe()
	answers, said := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- plugin.ServeOn(in, said, f) }()
	go io.Copy(io.Discard, answers) // the answers are read and not looked at

	if _, err := fmt.Fprintln(host, `{"id":1,"op":"browse","handle":"h","ref":{"kind":"table","path":["db","items"]}}`); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the browse never started")
	}
	host.Close() // the host has finished with it
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("serving ended with %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a plugin with work in flight never ended")
	}
	select {
	case <-f.held:
	case <-time.After(time.Second):
		t.Error("the work was never told to stop")
	}
}
