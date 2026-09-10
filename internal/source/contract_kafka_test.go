package source_test

// T0.29 — validate the driver contract against Kafka BEFORE any driver is
// written.
//
// REQ-DRV-2 states that a log source must be first-class without implementing
// a query language. This file proves it by compilation: logSource below is a
// Kafka-shaped source implementing ONLY the required interfaces, and the
// assertions at the bottom fail to build if the contract ever grows a
// requirement a log cannot satisfy.
//
// The exercise changed the design. The contract originally assumed the grid
// was fed by Queryer, which is true for relational sources and false for
// Kafka, Redis and document stores. Rather than special-casing those, Browse
// became the required data path and Queryer became optional — see the note on
// source.Browser. That is the difference between a log being a first-class
// source and a bolted-on one, and it was cheap to find here and expensive to
// find in Phase 2.

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/capability"
)

// logSource implements the minimum a source must provide. It has no query
// language, no dialect, no schemas, no primary keys and no updatable rows.
type logSource struct {
	closed bool
	topics map[string][]model.Record
}

func newLogSource() *logSource {
	return &logSource{topics: map[string][]model.Record{
		"orders": {
			{Topic: "orders", Partition: 0, Offset: 100, Timestamp: time.Unix(1700000000, 0), Key: []byte("k1"), Value: []byte(`{"id":1}`)},
			{Topic: "orders", Partition: 0, Offset: 101, Timestamp: time.Unix(1700000060, 0), Key: []byte("k2"), Value: []byte(`{"id":2}`)},
			{Topic: "orders", Partition: 1, Offset: 55, Timestamp: time.Unix(1700000120, 0), Key: []byte("k3"), Value: []byte(`{"id":3}`)},
		},
	}}
}

func (s *logSource) Capabilities() capability.Capabilities {
	return capability.Capabilities{
		Paradigm: model.ParadigmStream,
		// Query is left zeroed: a log has no query language, and the UI
		// therefore offers no query editor.
		Data: capability.Data{
			// No Insert/Update/Delete: records are immutable.
			ApproximateCount: true,
		},
		Stream: capability.Stream{
			Consume: true, SeekTimestamp: true, Follow: true,
			ConsumerGroups: true, TopicAdmin: true,
		},
		Objects: map[model.ObjectKind]bool{
			model.KindCluster: true, model.KindTopic: true,
			model.KindPartition: true, model.KindConsumerGroup: true,
		},
	}
}

func (s *logSource) Info(context.Context) (source.ServerInfo, error) {
	return source.ServerInfo{Product: "Apache Kafka", Version: "3.7.0"}, nil
}

func (s *logSource) Ping(context.Context) error { return nil }

func (s *logSource) Close() error { s.closed = true; return nil }

func (s *logSource) Root(context.Context) ([]model.Node, error) {
	return []model.Node{{
		Ref:         model.NewRef(model.KindCluster, "local"),
		Label:       "local",
		HasChildren: true,
	}}, nil
}

func (s *logSource) Children(_ context.Context, ref model.ObjectRef) ([]model.Node, error) {
	if ref.Kind != model.KindCluster {
		return nil, nil
	}
	out := make([]model.Node, 0, len(s.topics))
	for name := range s.topics {
		out = append(out, model.Node{
			Ref:       model.NewRef(model.KindTopic, name),
			Label:     name,
			Browsable: true, // opens in the grid, with no SQL involved
		})
	}
	return out, nil
}

func (s *logSource) Describe(_ context.Context, ref model.ObjectRef) (any, error) {
	if ref.Kind != model.KindTopic {
		return nil, errors.New("unsupported kind")
	}
	return &model.Topic{
		Name: ref.Name(),
		Partitions: []model.Partition{
			{ID: 0, LowWatermark: 100, HighWatermark: 102},
			{ID: 1, LowWatermark: 55, HighWatermark: 56},
		},
	}, nil
}

func (s *logSource) Badge(_ context.Context, ref model.ObjectRef) (model.Badge, bool, error) {
	if ref.Kind != model.KindTopic {
		return model.Badge{}, false, nil
	}
	return model.Badge{Text: "3", Exact: false}, true, nil
}

// Browse is the whole point: records reach the grid through the same interface
// relational rows do.
func (s *logSource) Browse(_ context.Context, ref model.ObjectRef, opt source.BrowseOptions) (model.RowStream, error) {
	if ref.Kind != model.KindTopic {
		return nil, errors.New("only topics are browsable")
	}
	if len(opt.Sorts) > 0 {
		// A log has one order: offset. Refusing rather than ignoring is what
		// checkUnsupportedOptionsRejected requires.
		return nil, errors.New("a log cannot be sorted; records are ordered by offset")
	}

	recs := s.topics[ref.Name()]
	if opt.Seek != nil && opt.Seek.Mode == source.SeekTimestamp {
		filtered := recs[:0:0]
		for _, r := range recs {
			if !r.Timestamp.Before(opt.Seek.Time) {
				filtered = append(filtered, r)
			}
		}
		recs = filtered
	}
	if opt.Limit > 0 && int64(len(recs)) > opt.Limit {
		recs = recs[:opt.Limit]
	}
	return &recordStream{recs: recs}, nil
}

// recordStream adapts log records to the grid's row contract.
type recordStream struct {
	recs []model.Record
	i    int
}

func (r *recordStream) Columns() []model.ColumnDef {
	return []model.ColumnDef{
		{Name: "partition", Type: model.DataType{Class: model.TypeInteger, Native: "int32"}, ReadOnly: true},
		{Name: "offset", Type: model.DataType{Class: model.TypeInteger, Native: "int64"}, ReadOnly: true},
		{Name: "timestamp", Type: model.DataType{Class: model.TypeTimestamp, Native: "timestamp"}, ReadOnly: true},
		{Name: "key", Type: model.DataType{Class: model.TypeBytes, Native: "bytes", Nullable: true}, ReadOnly: true},
		{Name: "value", Type: model.DataType{Class: model.TypeJSON, Native: "json", Nullable: true}, ReadOnly: true},
	}
}

func (r *recordStream) Next(ctx context.Context) (model.Row, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r.i >= len(r.recs) {
		return nil, io.EOF
	}
	rec := r.recs[r.i]
	r.i++
	return model.Row{
		int64(rec.Partition), rec.Offset, rec.Timestamp,
		rec.Key, model.JSON(rec.Value),
	}, nil
}

func (r *recordStream) Close() error { return nil }

// Identity reports that records are addressable but not mutable, so the grid
// shows them and refuses to edit them without any Kafka-specific UI logic.
func (r *recordStream) Identity() model.RowIdentity {
	return model.RowIdentity{
		Kind:    model.IdentityLogOffset,
		Columns: []string{"partition", "offset"},
		Target:  model.NewRef(model.KindTopic, "orders"),
	}
}

// --- The proof -------------------------------------------------------------
//
// These assertions are the deliverable of T0.29. If the required contract ever
// grows something a log cannot provide, this file stops compiling.

var (
	_ source.Source       = (*logSource)(nil)
	_ source.Introspector = (*logSource)(nil)
	_ source.Browser      = (*logSource)(nil)
	_ model.RowStream     = (*recordStream)(nil)
	_ model.Identified    = (*recordStream)(nil)
)

func TestLogSourceSatisfiesContractWithoutQueryLanguage(t *testing.T) {
	var s source.Source = newLogSource()

	// The negative assertions matter as much as the positive ones: a log must
	// NOT be required to fake a query language or a dialect.
	if _, ok := s.(source.Queryer); ok {
		t.Error("the minimal log source should not need to implement Queryer")
	}
	if _, ok := s.(source.Writer); ok {
		t.Error("the minimal log source should not need to implement Writer")
	}
	if caps := s.Capabilities(); caps.Query.Supported {
		t.Error("a log must not claim query support")
	}
}

func TestLogRecordsReachTheGridThroughBrowse(t *testing.T) {
	ctx := context.Background()
	s := newLogSource()

	stream, err := s.Browse(ctx, model.NewRef(model.KindTopic, "orders"), source.BrowseOptions{Limit: 10})
	if err != nil {
		t.Fatalf("Browse: %v", err)
	}
	defer stream.Close()

	if got := len(stream.Columns()); got != 5 {
		t.Fatalf("got %d columns, want 5", got)
	}

	n := 0
	for {
		row, err := stream.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if len(row) != len(stream.Columns()) {
			t.Fatalf("row width %d != column count %d", len(row), len(stream.Columns()))
		}
		n++
	}
	if n != 3 {
		t.Errorf("read %d records, want 3", n)
	}
}

func TestLogSeekByTimestamp(t *testing.T) {
	ctx := context.Background()
	s := newLogSource()

	stream, err := s.Browse(ctx, model.NewRef(model.KindTopic, "orders"), source.BrowseOptions{
		Seek: &source.Seek{Mode: source.SeekTimestamp, Time: time.Unix(1700000060, 0)},
	})
	if err != nil {
		t.Fatalf("Browse: %v", err)
	}
	defer stream.Close()

	n := 0
	for {
		if _, err := stream.Next(ctx); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatalf("Next: %v", err)
		}
		n++
	}
	if n != 2 {
		t.Errorf("seek by timestamp returned %d records, want 2", n)
	}
}

func TestLogRecordsAreNotEditable(t *testing.T) {
	ctx := context.Background()
	s := newLogSource()

	stream, err := s.Browse(ctx, model.NewRef(model.KindTopic, "orders"), source.BrowseOptions{Limit: 1})
	if err != nil {
		t.Fatalf("Browse: %v", err)
	}
	defer stream.Close()

	id, ok := stream.(model.Identified)
	if !ok {
		t.Fatal("record stream should expose identity so rows can be addressed for export")
	}
	if id.Identity().Editable() {
		t.Error("log records must never be editable: the grid would offer an edit that cannot work")
	}
}

func TestLogRefusesSortRatherThanIgnoringIt(t *testing.T) {
	s := newLogSource()

	_, err := s.Browse(context.Background(), model.NewRef(model.KindTopic, "orders"),
		source.BrowseOptions{Sorts: []source.Sort{{Column: "key"}}})
	if err == nil {
		t.Error("Browse must refuse an unsupported sort, not silently return unsorted rows")
	}
}
