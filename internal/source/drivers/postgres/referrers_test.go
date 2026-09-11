//go:build conformance

package postgres

import (
	"context"
	"slices"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

func TestReferrersListTheKeysThatReferToATable(t *testing.T) {
	ctx := context.Background()
	s := openSource(t, false)
	refs, err := s.Referrers(ctx, ordersRef)
	if err != nil {
		t.Fatal(err)
	}
	notes := model.NewRef(model.KindTable, ordersRef.Path[0], "ikigai_it", "order_notes")
	if len(refs) != 1 || !refs[0].From.Equal(notes) || !slices.Equal(refs[0].Key.Columns, []string{"order_id"}) ||
		!slices.Equal(refs[0].Key.RefColumns, []string{"id"}) || refs[0].Key.RefTable != "orders" || refs[0].Key.RefSchema != "ikigai_it" {
		t.Errorf("order_notes refers to orders: %+v", refs)
	}
	if refs, err := s.Referrers(ctx, notes); err != nil || len(refs) != 0 {
		t.Errorf("nothing refers to order_notes: %+v %v", refs, err)
	}
	if _, err := s.Referrers(ctx, model.NewRef(model.KindView, ordersRef.Path[0], "ikigai_it", "paid")); err == nil {
		t.Error("a view is refused: no key refers to one")
	}
}
