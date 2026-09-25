// Package plugin runs the plugins somebody installed and makes each one a
// source the application cannot tell from its own (FR-16.2).
//
// The protocol is in the public plugin package, which is what a plugin author
// imports; this is the other end of it. Nothing here trusts a plugin: what it
// says is data — nodes, rows, errors — and there is no path from any of it to a
// statement this runs.
package plugin

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/redact"
	"github.com/ikigai-db/ikigai-db/plugin"
)

// Process is one running plugin, and the conversation with it.
type Process struct {
	name string
	cmd  *exec.Cmd
	in   io.WriteCloser
	log  *slog.Logger

	next atomic.Int64

	// outbox is every line on its way to the plugin, written by one goroutine
	// of its own. Nothing else writes to a plugin's input: a write to a pipe
	// blocks until the far end reads, so a plugin that has stopped reading
	// would otherwise hang whoever was talking to it — and one of those is the
	// goroutine that draws the window.
	outbox chan []byte

	mu      sync.Mutex
	waiting map[int64]*call
	closed  bool
	// why is what ended the conversation, so that every request after it
	// fails with the same reason rather than with a closed pipe.
	why error

	done chan struct{}
}

// call is one request in flight. Lines arrive on rows; the last one closes it.
type call struct {
	rows chan plugin.Response
}

// Start launches a plugin and shakes hands with it.
//
// The handshake is what makes a plugin a plugin: a program that does not answer
// it, answers it wrongly, or speaks a protocol version this does not know is not
// started, and says so. A plugin is code somebody chose to run, and the least
// this can do is not run one it cannot talk to.
func Start(ctx context.Context, path string, log *slog.Logger) (*Process, plugin.Hello, error) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	cmd := exec.Command(path)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, plugin.Hello{}, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, plugin.Hello{}, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, plugin.Hello{}, err
	}
	if err := cmd.Start(); err != nil {
		return nil, plugin.Hello{}, fmt.Errorf("plugin %s would not start: %w", path, err)
	}
	p := newProcess(path, stdin, stdout, stderr, log)
	p.cmd = cmd

	hello, err := p.hello(ctx)
	if err != nil {
		p.Close()
		return nil, plugin.Hello{}, err
	}
	return p, hello, nil
}

// newProcess is the conversation without the process: Start uses it over a
// program's pipes, and a test uses it over pipes of its own, which is how every
// answer a plugin can give — including the ones no real plugin would give — is
// exercised without building a program for each.
func newProcess(name string, in io.WriteCloser, out, errOut io.Reader, log *slog.Logger) *Process {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	p := &Process{name: name, in: in, log: log, outbox: make(chan []byte, outbox),
		waiting: map[int64]*call{}, done: make(chan struct{})}
	go p.write()
	go p.read(out)
	if errOut != nil {
		go p.readErrors(errOut)
	}
	return p
}

func (p *Process) hello(ctx context.Context) (plugin.Hello, error) {
	ctx, cancel := context.WithTimeout(ctx, handshake)
	defer cancel()
	res, err := p.one(ctx, plugin.Request{Op: plugin.OpHello})
	if err != nil {
		return plugin.Hello{}, fmt.Errorf("plugin %s did not say what it is: %w", p.name, err)
	}
	if res.Hello == nil {
		return plugin.Hello{}, fmt.Errorf("plugin %s answered the handshake with nothing", p.name)
	}
	h := *res.Hello
	switch {
	case h.Protocol != plugin.Protocol:
		return plugin.Hello{}, fmt.Errorf("plugin %s speaks version %d and this application speaks %d",
			p.name, h.Protocol, plugin.Protocol)
	case h.Kind != plugin.KindSource:
		return plugin.Hello{}, fmt.Errorf("plugin %s is a %q, which this application has no use for",
			p.name, h.Kind)
	case h.ID == "":
		return plugin.Hello{}, fmt.Errorf("plugin %s has no driver name of its own", p.name)
	}
	if h.Name == "" {
		h.Name = h.ID
	}
	return h, nil
}

// handshake bounds how long a plugin has to say what it is. A program that has
// not answered in this long is not going to.
const handshake = 10 * time.Second

// read is the one reader of the plugin's answers: it takes each line and hands
// it to whoever is waiting for that request.
func (p *Process) read(out io.Reader) {
	defer close(p.done)
	lines := bufio.NewScanner(out)
	lines.Buffer(make([]byte, 0, 64*1024), maxLine)
	for lines.Scan() {
		var res plugin.Response
		if err := json.Unmarshal(lines.Bytes(), &res); err != nil {
			p.log.Warn("a plugin said something that could not be read",
				"plugin", p.name, "err", err)
			continue
		}
		p.mu.Lock()
		c := p.waiting[res.ID]
		p.mu.Unlock()
		if c == nil {
			// An answer to a request nobody is waiting for: one that was
			// given up on, or one this never sent. Dropped rather than
			// puzzled over.
			continue
		}
		select {
		case c.rows <- res:
		case <-p.done:
			return
		}
	}
	p.finish(fmt.Errorf("plugin %s stopped answering", p.name))
}

// maxLine bounds one answer. A row larger than this is a row nothing can draw.
const maxLine = 16 << 20

// readErrors logs whatever the plugin writes to its standard error, as the
// plugin's own words and redacted like everything else (NFR-S2).
func (p *Process) readErrors(errOut io.Reader) {
	lines := bufio.NewScanner(errOut)
	lines.Buffer(make([]byte, 0, 8*1024), 1<<20)
	for lines.Scan() {
		p.log.Info("plugin said", "plugin", p.name, "text", redact.String(lines.Text()))
	}
}

// finish ends every conversation, once, with a reason.
func (p *Process) finish(err error) {
	p.mu.Lock()
	if p.why == nil {
		p.why = err
	}
	waiting := p.waiting
	p.waiting = map[int64]*call{}
	p.mu.Unlock()
	for _, c := range waiting {
		close(c.rows)
	}
}

// outbox is how many lines may be waiting to go to a plugin. More than a
// handful waiting means the plugin is not reading, which is what the timeout
// below is about; the queue is there so that a request never waits on the one
// before it.
const outbox = 64

// write is the only writer of a plugin's input, and the reason send cannot
// block: a plugin that stopped reading fails its requests rather than freezing
// whoever made them.
func (p *Process) write() {
	for b := range p.outbox {
		if _, err := p.in.Write(b); err != nil {
			p.finish(fmt.Errorf("plugin %s stopped listening: %w", p.name, err))
			return
		}
	}
}

// send queues one request.
//
// It waits a little, because a plugin briefly behind is a plugin catching up,
// and then gives up: waiting forever on a program that has hung is the one
// thing a host must not do.
func (p *Process) send(req plugin.Request) error {
	b, err := json.Marshal(req)
	if err != nil {
		return err
	}
	p.mu.Lock()
	why := p.why
	p.mu.Unlock()
	if why != nil {
		return why
	}
	select {
	case p.outbox <- append(b, '\n'):
		return nil
	case <-time.After(sendWait):
		return fmt.Errorf("plugin %s is not reading what it is sent", p.name)
	}
}

// sendNow queues a request and does not wait at all, for the ones where waiting
// is pointless: a cancel to a plugin whose queue is already full will not help,
// and the caller is on its way out.
func (p *Process) sendNow(req plugin.Request) {
	b, err := json.Marshal(req)
	if err != nil {
		return
	}
	select {
	case p.outbox <- append(b, '\n'):
	default:
	}
}

// sendWait is how long a request waits for a plugin that is behind.
const sendWait = 5 * time.Second

// stream sends a request and answers the channel its responses arrive on, and a
// function that stops waiting. The caller must call stop.
func (p *Process) stream(ctx context.Context, req plugin.Request) (<-chan plugin.Response, func(), error) {
	id := p.next.Add(1)
	req.ID = id
	c := &call{rows: make(chan plugin.Response, 16)}

	p.mu.Lock()
	if p.why != nil {
		err := p.why
		p.mu.Unlock()
		return nil, nil, err
	}
	p.waiting[id] = c
	p.mu.Unlock()

	stop := func() {
		p.mu.Lock()
		_, waiting := p.waiting[id]
		delete(p.waiting, id)
		p.mu.Unlock()
		if waiting {
			// The plugin is told, so that it can stop doing the work; if it
			// does not, closing the process is what stops it (ARCH-4). Told
			// without waiting: giving up on a request must not itself wait.
			p.sendNow(plugin.Request{Op: plugin.OpCancel, Of: id})
		}
	}
	if err := p.send(req); err != nil {
		stop()
		return nil, nil, err
	}
	return c.rows, stop, nil
}

// one sends a request and waits for the single answer that ends it.
func (p *Process) one(ctx context.Context, req plugin.Request) (plugin.Response, error) {
	rows, stop, err := p.stream(ctx, req)
	if err != nil {
		return plugin.Response{}, err
	}
	defer stop()
	for {
		select {
		case res, ok := <-rows:
			if !ok {
				return plugin.Response{}, p.reason()
			}
			if res.Error != "" {
				return plugin.Response{}, &Error{Kind: res.Kind, Text: res.Error, Plugin: p.name}
			}
			if res.Done {
				return res, nil
			}
			// Anything else for a request that answers once is a plugin
			// saying more than it was asked; the ending is what is waited
			// for.
		case <-ctx.Done():
			return plugin.Response{}, ctx.Err()
		}
	}
}

func (p *Process) reason() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.why != nil {
		return p.why
	}
	return errors.New("plugin: the conversation ended")
}

// Close ends the conversation and the process.
//
// Politely first — closing its input, which is what tells a plugin to stop —
// and then not: a plugin that has not gone in a moment is killed, because an
// application that cannot shut down because of a plugin is an application a
// plugin can hang.
func (p *Process) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	p.mu.Unlock()

	p.in.Close()
	if p.cmd != nil {
		gone := make(chan error, 1)
		go func() { gone <- p.cmd.Wait() }()
		select {
		case <-gone:
		case <-time.After(shutdown):
			p.cmd.Process.Kill()
			<-gone
		}
	}
	p.finish(fmt.Errorf("plugin %s is closed", p.name))
	return nil
}

// shutdown is how long a plugin has to end itself once its input is closed.
const shutdown = 2 * time.Second

// Error is a failure a plugin reported, with the kind it gave it.
type Error struct {
	Kind   string
	Text   string
	Plugin string
}

func (e *Error) Error() string { return e.Text }
