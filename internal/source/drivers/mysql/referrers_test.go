//go:build conformance

package mysql

import (
	"context"
	"slices"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

func TestReferrersListTheKeysThatReferToATable(t *testing.T) {
	each(t, func(t *testing.T, srv server) {
		ctx := context.Background()
		r, ok := any(open(t, srv, source.Guard{})).(source.Referrer)
		if !ok {
			t.Fatal("MySQL lists what refers to a table")
		}
		refs, err := r.Referrers(ctx, model.NewRef(model.KindTable, "ikigai_it", "people"))
		if err != nil {
			t.Fatal(err)
		}
		if len(refs) != 1 || !refs[0].From.Equal(model.NewRef(model.KindTable, "ikigai_it", "orders")) ||
			refs[0].Key.Name != "fk_person" || !slices.Equal(refs[0].Key.Columns, []string{"person_id"}) ||
			!slices.Equal(refs[0].Key.RefColumns, []string{"id"}) || refs[0].Key.OnDelete != "CASCADE" {
			t.Errorf("orders refers to people: %+v", refs)
		}
		if refs, err := r.Referrers(ctx, model.NewRef(model.KindTable, "ikigai_it", "orders")); err != nil || len(refs) != 0 {
			t.Errorf("nothing refers to orders: %+v %v", refs, err)
		}
		if _, err := r.Referrers(ctx, model.NewRef(model.KindView, "ikigai_it", "adults")); err == nil {
			t.Error("a view is refused: no key refers to one")
		}
	})
}
