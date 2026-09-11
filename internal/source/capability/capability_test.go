package capability

import (
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

func TestSupportsOnlyTheKindsListed(t *testing.T) {
	c := Capabilities{Objects: map[model.ObjectKind]bool{model.KindTable: true}}
	if !c.Supports(model.KindTable) || c.Supports(model.KindView) {
		t.Error("a source supports the kinds it lists, and no others")
	}
	if (Capabilities{}).Supports(model.KindTable) {
		t.Error("a source that lists nothing supports nothing")
	}
}
