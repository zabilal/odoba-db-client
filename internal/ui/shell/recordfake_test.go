package shell

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/capability"
	"github.com/ikigai-db/ikigai-db/internal/store"
)

// A stream source, enough of one to open a topic's records in a tab. What it
// holds is chosen to be awkward on purpose: a value that is JSON, one that is
// text and not JSON, one that is not text at all, a record with no key, and
// headers with a name that repeats.

var topicRef = model.NewRef(model.KindTopic, "cluster", "events")

// recordFake is a stream source: a cluster of one topic, whose rows are
// records rather than rows of a table.
type recordFake struct{}

func init() { source.Register(recordFake{}) }

func (recordFake) Describe() source.Descriptor {
	return source.Descriptor{ID: "recordfake", Name: "Record Fake", Paradigm: model.ParadigmStream,
		Fields: []source.Field{{Key: "host", Label: "Host", Kind: source.FieldText, Required: true}}}
}

func (recordFake) Open(context.Context, source.ConnectionConfig) (source.Source, error) {
	return &recordSource{}, nil
}

type recordSource struct{}

func (*recordSource) Capabilities() capability.Capabilities {
	return capability.Capabilities{
		Paradigm: model.ParadigmStream,
		Stream:   capability.Stream{Consume: true, SeekTimestamp: true, Follow: true},
		Objects: map[model.ObjectKind]bool{
			model.KindCluster: true, model.KindTopic: true,
		},
	}
}

func (*recordSource) Info(context.Context) (source.ServerInfo, error) {
	return source.ServerInfo{Product: "RecordFake", Version: "1.0"}, nil
}
func (*recordSource) Ping(context.Context) error { return nil }
func (*recordSource) Close() error               { return nil }

func (*recordSource) Root(context.Context) ([]model.Node, error) {
	// Describable and not browsable, as a real cluster is: it has an account
	// of itself and no rows at all (ADR-0106).
	return []model.Node{{Ref: model.NewRef(model.KindCluster, "cluster"), Label: "cluster",
		HasChildren: true, Describable: true}}, nil
}

func (*recordSource) Children(_ context.Context, ref model.ObjectRef) ([]model.Node, error) {
	if ref.Kind == model.KindCluster {
		return []model.Node{{Ref: topicRef, Label: "events", Browsable: true}}, nil
	}
	return nil, nil
}

// Badge is what the tree writes beside an object. A topic's is its partition
// count, which this fake has no business inventing.
func (*recordSource) Badge(context.Context, model.ObjectRef) (model.Badge, bool, error) {
	return model.Badge{}, false, nil
}

func (*recordSource) Describe(_ context.Context, ref model.ObjectRef) (any, error) {
	switch ref.Kind {
	case model.KindTopic:
		return &model.Topic{Name: "events"}, nil
	case model.KindCluster:
		return &model.Cluster{ID: "cluster", Controller: 1,
			Brokers: []model.Broker{{ID: 1, Host: "kafka1", Port: 9092}}}, nil
	}
	return nil, errors.New("recordfake: nothing to describe")
}

var recordCols = []model.ColumnDef{
	{Name: "partition", Type: model.DataType{Class: model.TypeInteger, Native: "int32"}, ReadOnly: true},
	{Name: "offset", Type: model.DataType{Class: model.TypeInteger, Native: "int64"}, ReadOnly: true},
	{Name: "timestamp", Type: model.DataType{Class: model.TypeTimestamp, Native: "timestamp"}, ReadOnly: true},
	{Name: "key", Type: model.DataType{Class: model.TypeBytes, Native: "bytes", Nullable: true}, ReadOnly: true},
	{Name: "value", Type: model.DataType{Class: model.TypeBytes, Native: "bytes", Nullable: true}, ReadOnly: true},
	{Name: "headers", Type: model.DataType{Class: model.TypeArray, Native: "headers", Nullable: true}, ReadOnly: true},
}

var recordWhen = time.Date(2026, 9, 19, 10, 30, 0, 0, time.UTC)

// fakeRecords are the records the topic holds, in order.
var fakeRecords = []model.Row{
	{int64(0), int64(10), recordWhen, []byte("order-1"), []byte(`{"total":12.50}`), []any{
		map[string]any{"key": "trace-id", "value": []byte("abc123")},
		map[string]any{"key": "trace-id", "value": []byte("second")},
	}},
	{int64(0), int64(11), recordWhen, []byte("order-2"), []byte("plain words"), nil},
	// No key at all, and bytes that are not text: a producer may send either.
	{int64(3), int64(12), recordWhen, nil, []byte{0xff, 0xfe, 0x00}, nil},
}

func (*recordSource) Browse(_ context.Context, ref model.ObjectRef, _ source.BrowseOptions) (model.RowStream, error) {
	if ref.Kind != model.KindTopic {
		return nil, errors.New("recordfake: only a topic holds records")
	}
	return &recordStream{}, nil
}

type recordStream struct{ at int }

func (*recordStream) Columns() []model.ColumnDef { return recordCols }

func (r *recordStream) Next(context.Context) (model.Row, error) {
	if r.at >= len(fakeRecords) {
		return nil, io.EOF
	}
	row := fakeRecords[r.at]
	r.at++
	return row, nil
}

func (*recordStream) Close() error { return nil }

// openRecords opens the fake topic's records in a tab.
func openRecords(t *testing.T) (*fixture, *tab) {
	t.Helper()
	fx := newFixture(t)
	c, err := fx.conns.Create(store.SavedConnection{Name: "kafka1", Driver: "recordfake", Host: "kafka1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	fx.s.OpenObject(c.ID, model.Node{Ref: topicRef, Label: "events", Browsable: true})
	tb := fx.onlyTab(t)
	pump(t, fx.q, func() bool { return tb.browse != nil })
	return fx, tb
}
