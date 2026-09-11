package model

import (
	"reflect"
	"testing"
)

func TestEveryClassHoldsAnObjectAndIsNamedOnce(t *testing.T) {
	kinds, labels := map[ObjectKind]bool{}, map[string]bool{}
	for _, c := range Classes {
		switch c.Kind {
		case KindFolder, KindServer, KindDatabase, KindSchema, KindColumn, KindField, KindPartition, "":
			t.Errorf("%q is not a kind of object a class holds", c.Kind)
		}
		if c.Label == "" || labels[c.Label] || kinds[c.Kind] {
			t.Errorf("class %+v: every class needs its own kind and its own name", c)
		}
		kinds[c.Kind], labels[c.Label] = true, true
	}
}

func TestAClassIsAddressedByTheKindItHolds(t *testing.T) {
	path := make([]string, 2, 8) // room to grow: two classes must not share it
	path[0], path[1] = "db", "sales"
	schema := ObjectRef{Kind: KindSchema, Path: path}
	tables, views := ClassRef(schema, KindTable), ClassRef(schema, KindView)
	if !tables.Equal(NewRef(KindFolder, "db", "sales", "table")) || !views.Equal(NewRef(KindFolder, "db", "sales", "view")) {
		t.Errorf("ClassRef = %v and %v", tables, views)
	}
	if k, ok := ClassOf(tables); k != KindTable || !ok {
		t.Errorf("ClassOf(%v) = %q, %v", tables, k, ok)
	}
	for _, ref := range []ObjectRef{
		NewRef(KindTable, "db", "sales", "table"),   // not a folder
		NewRef(KindFolder, "db", "sales", "tables"), // no class of that name
		NewRef(KindFolder),
	} {
		if k, ok := ClassOf(ref); ok {
			t.Errorf("ClassOf(%v) = %q; it is no class", ref, k)
		}
	}
}

func TestOnlyClassesHoldingSomethingAreListedInOrder(t *testing.T) {
	db := NewRef(KindDatabase, "main")
	got := ClassNodes(db, map[ObjectKind]int64{KindView: 2, KindTrigger: 0, KindTable: 3, KindColumn: 9})
	want := []Node{
		{Ref: NewRef(KindFolder, "main", "table"), Label: "Tables", HasChildren: true, Badge: &Badge{Text: "3", Exact: true}},
		{Ref: NewRef(KindFolder, "main", "view"), Label: "Views", HasChildren: true, Badge: &Badge{Text: "2", Exact: true}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ClassNodes = %+v, want %+v", got, want)
	}
	if n := ClassNode(db, KindTable, 0); n.HasChildren || n.Badge == nil || n.Badge.Text != "0" || n.Label != "Tables" {
		t.Errorf("an empty class, shown: %+v; it has no expander and says 0", n)
	}
	if got := OnTable("orders_total", "orders"); got != "orders_total on orders" {
		t.Errorf("OnTable = %q", got)
	}
}
