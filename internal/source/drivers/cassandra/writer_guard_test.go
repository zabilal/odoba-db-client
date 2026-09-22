package cassandra

import (
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/guardcheck"
)

// This driver writes through statements alone: there is no Writer, no bulk
// load and no index manager on it, so everything it changes goes through the
// guard in its session (NFR-S4).
//
// The check is still worth making. If one of those interfaces is added here
// later, this fails until the operation is held to the guard like every
// other.
func TestThisDriverHasNoWritePathOfItsOwn(t *testing.T) {
	src := &cassandraSource{cfg: source.ConnectionConfig{Guard: source.Guard{ReadOnly: true}}}
	guardcheck.Complete(t, src, map[string]func(*cassandraSource, bool) error{})
}
