package view

import (
	"testing"

	fynetheme "fyne.io/fyne/v2/theme"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

func TestEveryObjectAClassHoldsHasItsOwnIcon(t *testing.T) {
	for _, c := range model.Classes {
		if c.Kind == model.KindSubject {
			continue // the schema registry's subjects come with Kafka (T2.74)
		}
		if iconFor(c.Kind) == fynetheme.IconNameFile {
			t.Errorf("%s have no icon of their own", c.Label)
		}
	}
}
