package app

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/capability"
)

// fakeDriver behaves according to the connection's host, so tests can ask for
// each failure mode by name.
type fakeDriver struct{}

var lastPassword atomic.Value // the password the fake last saw

var fakeOpens atomic.Int64 // how many times the fake driver dialled

func init() { source.Register(fakeDriver{}) }

func (fakeDriver) Describe() source.Descriptor {
	return source.Descriptor{ID: "fake", Name: "Fake", Paradigm: model.ParadigmRelational, URLSchemes: []string{"fake"}}
}

func (fakeDriver) Open(ctx context.Context, cfg source.ConnectionConfig) (source.Source, error) {
	pw := ""
	if cfg.Secret != nil {
		var err error
		if pw, err = cfg.Secret("password"); err != nil {
			return nil, &source.ConnectError{Kind: source.ConnectConfig, Hint: "no password", Err: err}
		}
	}
	lastPassword.Store(pw)
	fakeOpens.Add(1)
	switch cfg.Host {
	case "slowish":
		select {
		case <-time.After(60 * time.Millisecond):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	case "unreachable":
		return nil, &source.ConnectError{Kind: source.ConnectUnreachable, Hint: "the server could not be reached",
			Err: errors.New("dial tcp: connection refused")}
	case "badauth":
		// The underlying error embeds the password the way real DSN errors do.
		return nil, &source.ConnectError{Kind: source.ConnectAuth, Hint: "the server rejected these credentials",
			Err: errors.New("auth failed: password=" + pw)}
	case "slow":
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return &fakeSource{}, nil
}

type fakeSource struct {
	pingErr atomic.Pointer[error]
	pings   atomic.Int64
	closed  atomic.Bool
	// boomPing makes Ping panic, as a broken driver might.
	boomPing atomic.Bool
}

func (f *fakeSource) failPing(err error) {
	if err == nil {
		f.pingErr.Store(nil)
		return
	}
	f.pingErr.Store(&err)
}

func (f *fakeSource) Ping(context.Context) error {
	f.pings.Add(1)
	if f.boomPing.Load() {
		panic("fake driver: the ping fell over")
	}
	if e := f.pingErr.Load(); e != nil {
		return *e
	}
	return nil
}

func (f *fakeSource) Capabilities() capability.Capabilities {
	return capability.Capabilities{Paradigm: model.ParadigmRelational, Objects: map[model.ObjectKind]bool{model.KindDatabase: true}}
}
func (f *fakeSource) Info(context.Context) (source.ServerInfo, error) {
	return source.ServerInfo{Product: "Fake", Version: "1.0"}, nil
}
func (f *fakeSource) Close() error                               { f.closed.Store(true); return nil }
func (f *fakeSource) Root(context.Context) ([]model.Node, error) { return nil, nil }
func (f *fakeSource) Children(context.Context, model.ObjectRef) ([]model.Node, error) {
	return nil, nil
}
func (f *fakeSource) Describe(context.Context, model.ObjectRef) (any, error) { return nil, nil }
func (f *fakeSource) Badge(context.Context, model.ObjectRef) (model.Badge, bool, error) {
	return model.Badge{}, false, nil
}
func (f *fakeSource) Browse(context.Context, model.ObjectRef, source.BrowseOptions) (model.RowStream, error) {
	return nil, errors.New("not implemented")
}
