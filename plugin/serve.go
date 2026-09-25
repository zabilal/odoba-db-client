package plugin

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
)

// Writing a plugin in Go: implement Source, call Serve, and that is the whole
// of it. Everything below is the conversation, which a plugin author should not
// have to think about.

// Source is what a source plugin implements.
//
// Every method takes a context that ends when the host gives up on the request,
// so a plugin that honours it stops when the application stops waiting — and a
// plugin that does not is killed with its process, which is the backstop
// (ARCH-4).
type Source interface {
	// Hello describes the plugin. It is asked once, before anything opens.
	Hello() Hello

	// Open makes a connection and answers a handle for it. A handle is the
	// plugin's own string; the host only carries it back.
	Open(ctx context.Context, cfg Config) (handle string, err error)
	Close(handle string) error
	Ping(ctx context.Context, handle string) error
	Info(ctx context.Context, handle string) (Info, error)

	// Root is the top of the object tree, and Children is what is under a
	// node. A source with one database answers its class folders from Root.
	Root(ctx context.Context, handle string) ([]Node, error)
	Children(ctx context.Context, handle string, ref Ref) ([]Node, error)

	// Describe is one object's structure.
	Describe(ctx context.Context, handle string, ref Ref) (Object, error)

	// Browse writes an object's rows to w. It is the required data path: the
	// grid needs rows, not statements (ADR-0005).
	Browse(ctx context.Context, handle string, ref Ref, opt BrowseOptions, w RowWriter) error
}

// RowWriter is how rows leave a plugin: the columns once, then a row at a time.
// Writing them as they are found rather than gathering them keeps a plugin's
// memory flat over a large object, which is the same bargain every source here
// makes (FR-10.3).
type RowWriter interface {
	// Columns must be called once, before any row.
	Columns(cols []Column) error
	// Row writes one row. Values are anything encoding/json can write.
	Row(values ...any) error
}

// Serve reads requests on stdin and writes answers to stdout, until stdin ends
// or the process is killed. It is what a plugin's main function calls.
//
// Anything a plugin writes to stderr is the plugin's own and is logged by the
// application with the plugin's name on it; stdout is the conversation and
// nothing else may be written there.
func Serve(s Source) error { return ServeOn(os.Stdin, os.Stdout, s) }

// ServeOn is Serve over anything, which is how a plugin is tested without a
// process.
func ServeOn(in io.Reader, out io.Writer, s Source) error {
	c := &conn{out: out, running: map[int64]context.CancelFunc{}}
	lines := bufio.NewScanner(in)
	lines.Buffer(make([]byte, 0, 64*1024), maxLine)
	for lines.Scan() {
		var req Request
		if err := json.Unmarshal(lines.Bytes(), &req); err != nil {
			// A line that is not a request cannot be answered, because an
			// answer needs the ID that was in it. It is reported on stderr,
			// where the application logs it, and the conversation goes on.
			fmt.Fprintf(os.Stderr, "plugin: a request could not be read: %v\n", err)
			continue
		}
		if req.Op == OpCancel {
			c.cancel(req.Of)
			continue
		}
		c.busy.Add(1)
		go func() {
			defer c.busy.Done()
			c.handle(s, req)
		}()
	}
	// The input ending means the host has finished with this plugin, so
	// everything in flight is given up on and then waited for. Given up on
	// first: a handler still working would otherwise keep the plugin alive for
	// as long as it took, and one that never finishes would keep it alive for
	// ever. Waited for after, because a host closes a plugin's input to ask it
	// to stop and an answer dropped on the way out reads, at the other end, as
	// a plugin that died mid-request.
	c.cancelAll()
	c.busy.Wait()
	return lines.Err()
}

// maxLine bounds one request. A request larger than this is a request that has
// gone wrong: nothing the host sends is big, and the rows that are big go the
// other way.
const maxLine = 8 << 20

// conn is one conversation: a writer nothing else writes to, and what is in
// flight.
type conn struct {
	mu  sync.Mutex
	out io.Writer

	running map[int64]context.CancelFunc
	// busy counts the answers being worked on, so that the last of them is
	// written before the plugin ends.
	busy sync.WaitGroup
}

func (c *conn) send(r Response) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err := c.out.Write(append(b, '\n')); err != nil {
		return err
	}
	if f, ok := c.out.(interface{ Flush() error }); ok {
		return f.Flush()
	}
	return nil
}

func (c *conn) cancel(id int64) {
	c.mu.Lock()
	stop := c.running[id]
	delete(c.running, id)
	c.mu.Unlock()
	if stop != nil {
		stop()
	}
}

// cancelAll gives up on everything in flight, which is what the end of the
// input means.
func (c *conn) cancelAll() {
	c.mu.Lock()
	running := c.running
	c.running = map[int64]context.CancelFunc{}
	c.mu.Unlock()
	for _, stop := range running {
		stop()
	}
}

func (c *conn) begin(id int64) context.Context {
	ctx, stop := context.WithCancel(context.Background())
	c.mu.Lock()
	c.running[id] = stop
	c.mu.Unlock()
	return ctx
}

func (c *conn) end(id int64) {
	c.mu.Lock()
	stop := c.running[id]
	delete(c.running, id)
	c.mu.Unlock()
	if stop != nil {
		stop()
	}
}

// handle does one request and answers it. Every path ends in exactly one
// terminating response — an error or a done — because the host waits for one.
func (c *conn) handle(s Source, req Request) {
	ctx := c.begin(req.ID)
	defer c.end(req.ID)

	fail := func(err error) {
		kind := ""
		var f *Failure
		if errors.As(err, &f) {
			kind = f.Kind
		}
		c.send(Response{ID: req.ID, Error: err.Error(), Kind: kind})
	}
	switch req.Op {
	case OpHello:
		h := s.Hello()
		h.Protocol = Protocol
		if h.Kind == "" {
			h.Kind = KindSource
		}
		c.send(Response{ID: req.ID, Hello: &h, Done: true})
	case OpOpen:
		cfg := Config{}
		if req.Config != nil {
			cfg = *req.Config
		}
		handle, err := s.Open(ctx, cfg)
		if err != nil {
			fail(err)
			return
		}
		c.send(Response{ID: req.ID, Handle: handle, Done: true})
	case OpClose:
		if err := s.Close(req.Handle); err != nil {
			fail(err)
			return
		}
		c.send(Response{ID: req.ID, OK: true, Done: true})
	case OpPing:
		if err := s.Ping(ctx, req.Handle); err != nil {
			fail(err)
			return
		}
		c.send(Response{ID: req.ID, OK: true, Done: true})
	case OpInfo:
		info, err := s.Info(ctx, req.Handle)
		if err != nil {
			fail(err)
			return
		}
		c.send(Response{ID: req.ID, Info: &info, Done: true})
	case OpRoot:
		nodes, err := s.Root(ctx, req.Handle)
		if err != nil {
			fail(err)
			return
		}
		c.send(Response{ID: req.ID, Nodes: nodes, Done: true})
	case OpChildren:
		nodes, err := s.Children(ctx, req.Handle, refOf(req))
		if err != nil {
			fail(err)
			return
		}
		c.send(Response{ID: req.ID, Nodes: nodes, Done: true})
	case OpDescribe:
		obj, err := s.Describe(ctx, req.Handle, refOf(req))
		if err != nil {
			fail(err)
			return
		}
		c.send(Response{ID: req.ID, Object: &obj, Done: true})
	case OpBrowse:
		opt := BrowseOptions{}
		if req.Browse != nil {
			opt = *req.Browse
		}
		w := &rows{c: c, id: req.ID}
		if err := s.Browse(ctx, req.Handle, refOf(req), opt, w); err != nil {
			fail(err)
			return
		}
		c.send(Response{ID: req.ID, Done: true})
	default:
		fail(fmt.Errorf("%q is not something this plugin does", req.Op))
	}
}

func refOf(req Request) Ref {
	if req.Ref == nil {
		return Ref{}
	}
	return *req.Ref
}

// rows writes a stream back to the host.
type rows struct {
	c    *conn
	id   int64
	said bool
}

func (r *rows) Columns(cols []Column) error {
	if r.said {
		return errors.New("plugin: the columns were written twice")
	}
	r.said = true
	return r.c.send(Response{ID: r.id, Columns: cols})
}

func (r *rows) Row(values ...any) error {
	switch {
	case !r.said:
		return errors.New("plugin: a row was written before the columns")
	case len(values) == 0:
		// A row of nothing cannot be sent: the line it would make is
		// indistinguishable from a line with no row in it, and a host that
		// guessed would be guessing about somebody's data. Said here, where
		// the plugin author can fix it, rather than dropped quietly.
		return errors.New("plugin: a row with no values in it")
	}
	raw := make([]json.RawMessage, len(values))
	for i, v := range values {
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("plugin: value %d cannot be written: %w", i, err)
		}
		raw[i] = b
	}
	return r.c.send(Response{ID: r.id, Row: raw})
}

// Failure is an error that says what kind of failure it is, so that the
// application can tell a wrong password from an unreachable host.
type Failure struct {
	Kind string
	Err  error
}

func (f *Failure) Error() string { return f.Err.Error() }
func (f *Failure) Unwrap() error { return f.Err }

// Failed makes one: plugin.Failed(plugin.FailAuth, err).
func Failed(kind string, err error) error { return &Failure{Kind: kind, Err: err} }
