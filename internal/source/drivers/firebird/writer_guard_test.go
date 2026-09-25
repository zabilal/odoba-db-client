package firebird

import (
	"context"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/guardcheck"
)

// Read-only is enforced in the data layer, not in the window (NFR-S4). Every
// mutating operation this driver has is held to its guard here, and the table
// is held to the interfaces themselves, so a new write path cannot arrive
// without one.
//
// Firebird has a read-only transaction as well, asked for in Begin, so the
// guard is the first of two defences on a query tab; on these paths, which
// hold no transaction of their own, it is the only one.
func TestEveryWritePathIsGuarded(t *testing.T) {
	ctx := context.Background()
	ro := &firebirdSource{cfg: source.ConnectionConfig{Guard: source.Guard{ReadOnly: true}}}
	prod := &firebirdSource{cfg: source.ConnectionConfig{Guard: source.Guard{Environment: source.EnvProduction}}}
	table := model.NewRef(model.KindTable, "ikigai.fdb", "T")

	guardcheck.Check(t, ro, prod, map[string]func(*firebirdSource, bool) error{
		"Apply": func(s *firebirdSource, c bool) error {
			_, err := s.Apply(ctx, &source.WritePlan{
				Target:     table,
				Statements: []source.Statement{{SQL: `DELETE FROM "T"`, Confirmed: c}},
			})
			return err
		},
		"LoadRows": func(s *firebirdSource, c bool) error {
			_, err := s.LoadRows(ctx, table, []string{"A"}, nil, source.LoadOptions{Confirmed: c})
			return err
		},
	})
}
