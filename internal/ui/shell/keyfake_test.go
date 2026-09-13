package shell

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/capability"
	"github.com/ikigai-db/ikigai-db/internal/store"
)

// keyFake is a key-value store: a database that browses as its keys, each of
// which is an object of its own holding rows. It is what the parts of the UI
// that open what a row names are tested against (FR-12.2).
type keyFake struct{}

func init() { source.Register(keyFake{}) }

func (keyFake) Describe() source.Descriptor {
	return source.Descriptor{ID: "keyfake", Name: "Key Fake", Paradigm: model.ParadigmKeyValue,
		Fields: []source.Field{{Key: "host", Label: "Host", Kind: source.FieldText, Required: true}}}
}

func (keyFake) Open(_ context.Context, cfg source.ConnectionConfig) (source.Source, error) {
	// A host of "flat" is a key-value store whose rows name nothing: the
	// same shape, without the rows being objects.
	return &keySource{flat: cfg.Host == "flat"}, nil
}

type keySource struct{ flat bool }

var (
	keyDB    = model.NewRef(model.KindDatabase, "db0")
	firstKey = model.NewRef(model.KindKey, "db0", "user:1")
)

func (k *keySource) Capabilities() capability.Capabilities {
	return capability.Capabilities{
		Paradigm:  model.ParadigmKeyValue,
		Structure: capability.Structure{MultipleDatabases: true},
		Data:      capability.Data{RowObjects: !k.flat, Update: true},
		Objects:   map[model.ObjectKind]bool{model.KindDatabase: true, model.KindKey: true},
	}
}

func (*keySource) Info(context.Context) (source.ServerInfo, error) {
	return source.ServerInfo{Product: "KeyFake", Version: "1.0"}, nil
}
func (*keySource) Ping(context.Context) error { return nil }
func (*keySource) Close() error               { return nil }

func (*keySource) Root(context.Context) ([]model.Node, error) {
	return []model.Node{{Ref: keyDB, Label: "db0", Browsable: true}}, nil
}
func (*keySource) Children(context.Context, model.ObjectRef) ([]model.Node, error) { return nil, nil }

// Describe is a keyspace or a key, as a key-value store describes itself.
func (*keySource) Describe(_ context.Context, ref model.ObjectRef) (any, error) {
	switch ref.Kind {
	case model.KindDatabase:
		return &model.Keyspace{Name: "db0", Keys: 3, Expiring: 1, Figures: []model.FigureGroup{
			{Title: "Memory", Values: []model.Figure{{Name: "used_memory_human", Value: "1.05M"}}},
		}}, nil
	case model.KindKey:
		return &model.StoredKey{Name: ref.Path[1], Kind: "hash", TTL: 90 * time.Second,
			Bytes: 104, Length: 2, Encoding: "listpack"}, nil
	}
	return nil, errors.New("keyfake: nothing to describe")
}
func (*keySource) Badge(context.Context, model.ObjectRef) (model.Badge, bool, error) {
	return model.Badge{}, false, nil
}

// ObjectOf is the key a row of the keyspace names.
func (k *keySource) ObjectOf(ref model.ObjectRef, cols []model.ColumnDef, row model.Row) (model.ObjectRef, bool) {
	if ref.Kind != model.KindDatabase || len(cols) == 0 || len(row) == 0 {
		return model.ObjectRef{}, false
	}
	name, ok := row[0].(string)
	if !ok {
		return model.ObjectRef{}, false
	}
	return model.NewRef(model.KindKey, ref.Path[0], name), true
}

func (k *keySource) Browse(_ context.Context, ref model.ObjectRef, opt source.BrowseOptions) (model.RowStream, error) {
	switch ref.Kind {
	case model.KindDatabase:
		rows := []model.Row{{"user:1", "hash"}, {"user:2", "string"}, {"queue", "list"}}
		return &keyRows{ref: ref, id: "key", cols: []model.ColumnDef{
			{Name: "key", Type: model.DataType{Class: model.TypeString}},
			{Name: "type", Type: model.DataType{Class: model.TypeString}, ReadOnly: true},
		}, rows: page(rows, opt)}, nil
	case model.KindKey:
		rows := []model.Row{{"city", "London"}, {"name", "Ada"}}
		return &keyRows{ref: ref, id: "field", cols: []model.ColumnDef{
			{Name: "field", Type: model.DataType{Class: model.TypeString}},
			{Name: "value", Type: model.DataType{Class: model.TypeString}},
		}, rows: page(rows, opt)}, nil
	}
	return nil, fmt.Errorf("keyfake: %s is neither a database nor a key", ref)
}

type keyRows struct {
	ref  model.ObjectRef
	id   string
	cols []model.ColumnDef
	rows []model.Row
	at   int
}

func (r *keyRows) Columns() []model.ColumnDef { return r.cols }
func (r *keyRows) Identity() model.RowIdentity {
	return model.RowIdentity{Kind: model.IdentityKeyName, Columns: []string{r.id}, Target: r.ref}
}
func (r *keyRows) Next(context.Context) (model.Row, error) {
	if r.at >= len(r.rows) {
		return nil, io.EOF
	}
	r.at++
	return r.rows[r.at-1], nil
}
func (r *keyRows) Close() error { return nil }

// openKeyspace opens a data tab over the key fake's database.
func openKeyspace(t *testing.T, fx *fixture, host string) *tab {
	t.Helper()
	c, err := fx.conns.Create(store.SavedConnection{Name: host, Driver: "keyfake", Host: host}, nil)
	if err != nil {
		t.Fatal(err)
	}
	fx.s.OpenObject(c.ID, model.Node{Ref: keyDB, Label: "db0", Browsable: true})
	tb := fx.onlyTab(t)
	pump(t, fx.q, func() bool {
		if tb.model == nil {
			return false
		}
		_, ok := tb.model.Row(tb.ctx, 1)
		return ok
	})
	return tb
}
