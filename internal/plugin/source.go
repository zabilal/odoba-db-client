package plugin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/capability"
	"github.com/ikigai-db/ikigai-db/plugin"
)

// A plugin, as a source (FR-16.2, REQ-DB-1).
//
// Everything the application does with a source it does with this one: the tree,
// the grid, the connection form, the status line. What a plugin cannot do it
// says in its capabilities, and the application then does not offer it — the
// same bargain every built-in driver makes.

// Driver is a plugin's entry in the registry. One per plugin, made when the
// plugin says hello.
type Driver struct {
	// Process is the running plugin. One process serves every connection to
	// this driver: a plugin is asked to open as many as somebody makes, and
	// answers a handle for each.
	Process *Process
	Hello   plugin.Hello
}

func (d Driver) Describe() source.Descriptor {
	desc := source.Descriptor{
		ID:       d.Hello.ID,
		Name:     d.Hello.Name,
		Paradigm: paradigm(d.Hello.Paradigm),
	}
	for _, f := range d.Hello.Fields {
		desc.Fields = append(desc.Fields, source.Field{
			Key: f.Key, Label: f.Label, Help: f.Help, Kind: fieldKind(f.Kind),
			Required: f.Required, Secret: f.Secret, Default: f.Default,
			Options: append([]string(nil), f.Choices...),
		})
	}
	return desc
}

// Open asks the plugin for a connection. Every secret the connection holds is
// read here and sent with it: a plugin is code somebody chose to trust with
// their credentials, and that is said where a plugin is turned on.
func (d Driver) Open(ctx context.Context, cfg source.ConnectionConfig) (source.Source, error) {
	c := plugin.Config{
		Host: cfg.Host, Port: cfg.Port, Database: cfg.Database, User: cfg.User,
		ReadOnly: cfg.Guard.ReadOnly, Environment: string(cfg.Guard.Environment),
	}
	if len(cfg.Params) > 0 {
		c.Params = map[string]string{}
		for k, v := range cfg.Params {
			c.Params[k] = v
		}
	}
	if cfg.Secret != nil {
		c.Secrets = map[string]string{}
		for _, f := range d.Hello.Fields {
			if !f.Secret {
				continue
			}
			v, err := cfg.Secret(f.Key)
			if err != nil {
				return nil, &source.ConnectError{Kind: source.ConnectConfig,
					Hint: "A secret this connection needs could not be read.", Err: err}
			}
			c.Secrets[f.Key] = v
		}
	}
	res, err := d.Process.one(ctx, plugin.Request{Op: plugin.OpOpen, Config: &c})
	if err != nil {
		return nil, connectError(err)
	}
	return &Source{p: d.Process, handle: res.Handle, hello: d.Hello}, nil
}

// Source is one open connection to a plugin.
type Source struct {
	p      *Process
	handle string
	hello  plugin.Hello

	once sync.Once
}

func (s *Source) Capabilities() capability.Capabilities {
	h := s.hello.Capabilities
	caps := capability.Capabilities{
		Paradigm: paradigm(s.hello.Paradigm),
		Structure: capability.Structure{
			MultipleDatabases: h.MultipleDatabases,
			Schemas:           h.Schemas,
		},
		// No Query: a plugin takes no statements, and a capability claimed
		// without the interface behind it is worse than one never claimed.
		Data:    capability.Data{ServerFilter: h.ServerFilter, ServerSort: h.ServerSort},
		Objects: map[model.ObjectKind]bool{},
	}
	for _, o := range h.Objects {
		caps.Objects[model.ObjectKind(o)] = true
	}
	// A tree needs folders to hang objects from, whatever else a plugin says.
	caps.Objects[model.KindFolder] = true
	return caps
}

func (s *Source) Info(ctx context.Context) (source.ServerInfo, error) {
	res, err := s.p.one(ctx, plugin.Request{Op: plugin.OpInfo, Handle: s.handle})
	if err != nil {
		return source.ServerInfo{}, err
	}
	if res.Info == nil {
		return source.ServerInfo{Product: s.hello.Name}, nil
	}
	info := source.ServerInfo{Product: res.Info.Product, Version: res.Info.Version}
	if res.Info.Database != "" {
		info.Attrs = map[string]string{"database": res.Info.Database}
	}
	return info, nil
}

func (s *Source) Ping(ctx context.Context) error {
	_, err := s.p.one(ctx, plugin.Request{Op: plugin.OpPing, Handle: s.handle})
	return err
}

// Close closes the connection, not the plugin: another connection to the same
// driver may still be open, and the process serves them all.
func (s *Source) Close() error {
	var err error
	s.once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), shutdown)
		defer cancel()
		_, err = s.p.one(ctx, plugin.Request{Op: plugin.OpClose, Handle: s.handle})
	})
	return err
}

func (s *Source) Root(ctx context.Context) ([]model.Node, error) {
	res, err := s.p.one(ctx, plugin.Request{Op: plugin.OpRoot, Handle: s.handle})
	if err != nil {
		return nil, err
	}
	return nodes(res.Nodes), nil
}

func (s *Source) Children(ctx context.Context, ref model.ObjectRef) ([]model.Node, error) {
	r := wireRef(ref)
	res, err := s.p.one(ctx, plugin.Request{Op: plugin.OpChildren, Handle: s.handle, Ref: &r})
	if err != nil {
		return nil, err
	}
	return nodes(res.Nodes), nil
}

func (s *Source) Describe(ctx context.Context, ref model.ObjectRef) (any, error) {
	r := wireRef(ref)
	res, err := s.p.one(ctx, plugin.Request{Op: plugin.OpDescribe, Handle: s.handle, Ref: &r})
	if err != nil {
		return nil, err
	}
	if res.Object == nil {
		return nil, fmt.Errorf("plugin: nothing was said about %s", ref)
	}
	return object(ref, *res.Object), nil
}

// Badge is the count or size beside a node, and a plugin has none: the protocol
// says nothing about it, because it is the expensive question and a plugin that
// wanted to answer it would be answering it once per visible node over a pipe
// (FR-2.5).
func (s *Source) Badge(ctx context.Context, ref model.ObjectRef) (model.Badge, bool, error) {
	return model.Badge{}, false, nil
}

func (s *Source) Browse(ctx context.Context, ref model.ObjectRef, opt source.BrowseOptions) (model.RowStream, error) {
	r := wireRef(ref)
	b := plugin.BrowseOptions{Offset: opt.Offset, Limit: opt.Limit, Where: opt.Where}
	for _, so := range opt.Sorts {
		b.Sorts = append(b.Sorts, plugin.Sort{Column: so.Column, Descending: so.Descending})
	}
	for _, f := range opt.Filters {
		// One value, which is what every operator but between and in takes;
		// the rest are refused by not being claimed (ServerFilter), and the
		// application filters those itself over what it read.
		pf := plugin.Filter{Column: f.Column, Op: string(f.Op)}
		if len(f.Values) > 0 {
			pf.Value = f.Values[0]
		}
		if f.Negate {
			pf.Op = "not " + pf.Op
		}
		b.Filters = append(b.Filters, pf)
	}
	return s.rows(ctx, plugin.Request{Op: plugin.OpBrowse, Handle: s.handle, Ref: &r, Browse: &b})
}

// rows starts a streaming request and waits for the columns, so that a stream
// which fails at once fails here rather than at the first row.
func (s *Source) rows(ctx context.Context, req plugin.Request) (model.RowStream, error) {
	lines, stop, err := s.p.stream(ctx, req)
	if err != nil {
		return nil, err
	}
	st := &stream{lines: lines, stop: stop}
	for {
		select {
		case res, ok := <-lines:
			if !ok {
				stop()
				return nil, s.p.reason()
			}
			switch {
			case res.Error != "":
				stop()
				return nil, &Error{Kind: res.Kind, Text: res.Error, Plugin: s.p.name}
			case res.Columns != nil:
				st.cols = columns(res.Columns)
				return st, nil
			case res.Done:
				// A stream with no rows in it at all, which is a stream of
				// nothing rather than a failure.
				st.done = true
				return st, nil
			}
		case <-ctx.Done():
			stop()
			return nil, ctx.Err()
		}
	}
}

// stream is a plugin's rows, read as they arrive.
type stream struct {
	lines  <-chan plugin.Response
	stop   func()
	cols   []model.ColumnDef
	done   bool
	err    error
	closed bool
}

func (s *stream) Columns() []model.ColumnDef { return s.cols }

func (s *stream) Next(ctx context.Context) (model.Row, error) {
	switch {
	case s.err != nil:
		return nil, s.err
	case s.done:
		return nil, io.EOF
	}
	for {
		select {
		case res, ok := <-s.lines:
			if !ok {
				s.err = errors.New("plugin: the rows stopped arriving")
				return nil, s.err
			}
			switch {
			case res.Error != "":
				s.err = &Error{Kind: res.Kind, Text: res.Error}
				return nil, s.err
			case res.Done:
				s.done = true
				return nil, io.EOF
			case res.Row != nil:
				return row(res.Row, s.cols), nil
			}
			// Columns said twice, or a line with nothing in it: ignored, the
			// stream being the rows and the ending.
		case <-ctx.Done():
			s.err = ctx.Err()
			return nil, s.err
		}
	}
}

func (s *stream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	s.stop()
	return nil
}
