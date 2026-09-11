package localdb

import "testing"

func TestParameterValuesAreRememberedByConnectionAndName(t *testing.T) {
	d, _ := open(t)
	if _, ok, err := d.Param(ctx, "c1", "id"); ok || err != nil {
		t.Fatalf("a value never given: ok %v, err %v", ok, err)
	}
	for _, p := range []struct {
		conn, name string
		v          ParamValue
	}{
		{"c1", "id", ParamValue{Text: "7"}},
		{"c2", "id", ParamValue{Text: "9"}},
		{"c1", "name", ParamValue{Null: true}},
		{"c1", "id", ParamValue{Text: "8"}}, // given again
	} {
		if err := d.PutParam(ctx, p.conn, p.name, p.v); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		conn, name string
		want       ParamValue
	}{
		{"c1", "id", ParamValue{Text: "8"}},
		{"c2", "id", ParamValue{Text: "9"}},
		{"c1", "name", ParamValue{Null: true}},
	} {
		if got, ok, err := d.Param(ctx, c.conn, c.name); !ok || err != nil || got != c.want {
			t.Errorf("%s %s: %+v, %v, %v; want %+v", c.conn, c.name, got, ok, err, c.want)
		}
	}
}
