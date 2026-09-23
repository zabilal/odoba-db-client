package cassandra

import (
	"context"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/guardcheck"
)

// Read-only is enforced in the data layer, not in the window (NFR-S4).
// Every mutating operation this driver has is held to its guard here, and
// the table is held to the interfaces themselves, so a new write path
// cannot arrive without one.
func TestEveryWritePathIsGuarded(t *testing.T) {
	ctx := context.Background()
	ro := &cassandraSource{cfg: source.ConnectionConfig{Guard: source.Guard{ReadOnly: true}}}
	prod := &cassandraSource{cfg: source.ConnectionConfig{Guard: source.Guard{Environment: source.EnvProduction}}}

	guardcheck.Check(t, ro, prod, map[string]func(*cassandraSource, bool) error{
		"Apply": func(s *cassandraSource, c bool) error {
			_, err := s.Apply(ctx, &source.WritePlan{
				Target:     model.ObjectRef{Kind: model.KindTable, Path: []string{"ks", "t"}},
				Statements: []source.Statement{{SQL: "DELETE FROM ks.t WHERE id = ? IF EXISTS", Confirmed: c}},
			})
			return err
		},
	})
}
