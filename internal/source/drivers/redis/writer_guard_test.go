package redis

import (
	"context"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/guardcheck"
)

// Read-only is enforced in the data layer, not in the window (NFR-S4).
func TestEveryWritePathIsGuarded(t *testing.T) {
	ctx := context.Background()
	ro := &redisSource{cfg: source.ConnectionConfig{Guard: source.Guard{ReadOnly: true}}}
	prod := &redisSource{cfg: source.ConnectionConfig{Guard: source.Guard{Environment: source.EnvProduction}}}

	guardcheck.Check(t, ro, prod, map[string]func(*redisSource, bool) error{
		"Apply": func(s *redisSource, c bool) error {
			_, err := s.Apply(ctx, &source.WritePlan{
				Target:     model.ObjectRef{Kind: model.KindKey, Path: []string{"0", "k"}},
				Statements: []source.Statement{{SQL: "SET k v", Confirmed: c}},
			})
			return err
		},
	})
}
