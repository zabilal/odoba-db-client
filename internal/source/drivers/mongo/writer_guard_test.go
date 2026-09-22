package mongo

import (
	"context"
	"errors"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/guardcheck"
)

func collection() model.ObjectRef {
	return model.ObjectRef{Kind: model.KindCollection, Path: []string{"shop", "orders"}}
}

// Read-only is enforced in the data layer, not in the window (NFR-S4).
func TestEveryWritePathIsGuarded(t *testing.T) {
	ctx := context.Background()
	ro := &mongoSource{cfg: source.ConnectionConfig{Guard: source.Guard{ReadOnly: true}}}
	prod := &mongoSource{cfg: source.ConnectionConfig{Guard: source.Guard{Environment: source.EnvProduction}}}

	guardcheck.Check(t, ro, prod, map[string]func(*mongoSource, bool) error{
		"Apply": func(s *mongoSource, c bool) error {
			_, err := s.Apply(ctx, &source.WritePlan{Target: collection(),
				Statements: []source.Statement{{SQL: "deleteOne", Confirmed: c}}})
			return err
		},
		"ApplyIndex": func(s *mongoSource, c bool) error {
			_, err := s.ApplyIndex(ctx, &source.WritePlan{Target: collection(),
				Statements: []source.Statement{{SQL: "createIndex", Confirmed: c}}})
			return err
		},
		"Aggregate": func(s *mongoSource, c bool) error {
			// A pipeline that ends in $out writes a collection. One that does
			// not is a read, and is allowed on a read-only connection.
			_, err := s.Aggregate(ctx, collection(),
				`[{"$match": {}}, {"$out": "copies"}]`, source.BrowseOptions{}, c)
			return err
		},
	})
}

// A pipeline that only reads is not a write path, and refusing it would make
// a read-only connection useless rather than safe.
func TestAPipelineThatOnlyReadsIsNotRefused(t *testing.T) {
	ro := &mongoSource{cfg: source.ConnectionConfig{Guard: source.Guard{ReadOnly: true}}}
	_, err := ro.Aggregate(context.Background(), collection(),
		`[{"$match": {}}]`, source.BrowseOptions{}, false)
	if err == nil {
		return // it got as far as needing a server, which is past the guard
	}
	if errors.Is(err, source.ErrReadOnly) {
		t.Errorf("a reading pipeline was refused on a read-only connection: %v", err)
	}
}
