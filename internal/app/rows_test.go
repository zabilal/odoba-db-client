package app

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/capability"
)

// tableSource serves a table of n rows through Browse, honouring Offset and
// Limit, and remembers what it was asked for.
type tableSource struct {
	n    int64
	mu   sync.Mutex
	asks [][2]int64
}

func (s *tableSource) Root(context.Context) ([]model.Node, error) { return nil, nil }
func (s *tableSource) Children(context.Context, model.ObjectRef) ([]model.Node, error) {
	return nil, nil
}
func (s *tableSource) Describe(context.Context, model.ObjectRef) (any, error) { return nil, nil }
func (s *tableSource) Badge(context.Context, model.ObjectRef) (model.Badge, bool, error) {
	return model.Badge{}, false, nil
}
func (s *tableSource) Capabilities() capability.Capabilities { return capability.Capabilities{} }
func (s *tableSource) Info(context.Context) (source.ServerInfo, error) {
	return source.ServerInfo{}, nil
}
func (s *tableSource) Ping(context.Context) error { return nil }
func (s *tableSource) Close() error               { return nil }
func (s *tableSource) Browse(_ context.Context, _ model.ObjectRef, opt source.BrowseOptions) (model.RowStream, error) {
	s.mu.Lock()
	s.asks = append(s.asks, [2]int64{opt.Offset, opt.Limit})
	s.mu.Unlock()
	end := min(s.n, opt.Offset+opt.Limit)
	return &countStream{n: int(end), i: int(opt.Offset)}, nil
}

func drain(t *testing.T, rs model.RowStream) []model.Row {
	t.Helper()
	var out []model.Row
	for {
		r, err := rs.Next(context.Background())
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
}

func TestBrowseRowsPageThroughTheWholeTable(t *testing.T) {
	src := &tableSource{n: 12345}
	bs, err := NewBrowseSource(context.Background(), src, model.NewRef(model.KindTable, "db", "t"), source.BrowseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got := drain(t, bs.Rows())
	if len(got) != 12345 || got[0][0] != int64(1) || got[12344][0] != int64(12345) {
		t.Fatalf("%d rows, first %v, last %v", len(got), got[0], got[len(got)-1])
	}
	src.mu.Lock()
	defer src.mu.Unlock()
	pages := src.asks[1:] // the first ask is NewBrowseSource's one-row probe
	want := [][2]int64{{0, exportPage}, {exportPage, exportPage}, {2 * exportPage, exportPage}}
	if len(pages) != len(want) {
		t.Fatalf("asked for %v", pages)
	}
	for i := range want {
		if pages[i] != want[i] {
			t.Errorf("page %d asked %v, want %v", i, pages[i], want[i])
		}
	}
}

func TestResultRowsStreamWhatArrives(t *testing.T) {
	rs := newResultSet(context.Background(), &countStream{n: 700, slow: true}, MaxResultRows)
	if got := drain(t, rs.Rows()); len(got) != 700 {
		t.Errorf("%d rows", len(got))
	}
}
