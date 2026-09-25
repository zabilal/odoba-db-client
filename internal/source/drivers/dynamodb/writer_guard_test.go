package dynamodb

import (
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
// There is one, and the guard is the only thing holding it: DynamoDB has no
// read-only session or transaction to ask for — an identity's permissions are
// the service's own answer, and they are not this connection's to set.
func TestEveryWritePathIsGuarded(t *testing.T) {
	ro := &dynamoSource{cfg: source.ConnectionConfig{Guard: source.Guard{ReadOnly: true}},
		keys: map[string]model.RowIdentity{}}
	prod := &dynamoSource{cfg: source.ConnectionConfig{Guard: source.Guard{Environment: source.EnvProduction}},
		keys: map[string]model.RowIdentity{}}
	table := model.NewRef(model.KindCollection, "eu-west-2", "T")

	guardcheck.Check(t, ro, prod, map[string]func(*dynamoSource, bool) error{
		"Apply": func(s *dynamoSource, c bool) error {
			_, err := s.Apply(t.Context(), &source.WritePlan{
				Target: table,
				Statements: []source.Statement{{SQL: "DeleteItem on T",
					Op: &itemWrite{kind: source.ChangeDelete, table: "T"}, Confirmed: c}},
			})
			return err
		},
	})
}
