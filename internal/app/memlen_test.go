package app

import (
	"reflect"

	"github.com/ikigai-db/ikigai-db/internal/store/secrets"
)

// memLen reports how many secrets a Memory holds. Test-only: Memory has no
// listing API, and it should not grow one just for this.
func memLen(m *secrets.Memory) int {
	return reflect.ValueOf(m).Elem().FieldByName("m").Len()
}
