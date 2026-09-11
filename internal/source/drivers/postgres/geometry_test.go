package postgres

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

func TestPostGISTypesAreGeometry(t *testing.T) {
	for _, c := range []struct {
		name, typtype string
		want          model.TypeClass
	}{
		{"geometry", "b", model.TypeGeometry},
		{"public.geography", "b", model.TypeGeometry},
		{"order_status", "e", model.TypeEnum},
		{"address", "c", model.TypeStruct},
		{"citext", "b", model.TypeUnknown},
	} {
		if got := classOf(c.name, c.typtype); got != c.want {
			t.Errorf("%s (%s): class %v, want %v", c.name, c.typtype, got, c.want)
		}
	}
}

// PostGIS's own bytes, since the test server has no PostGIS: what it sends
// for SELECT 'SRID=4326;POINT Z (1 2 3)'::geometry in text form.
const postgisPointZ = "01010000A0E6100000000000000000F03F00000000000000400000000000000840"

func TestPostGISValuesReadAsGeometryAndGoBackAsTheyCame(t *testing.T) {
	g, err := pgGeometry([]byte(postgisPointZ))
	if err != nil || g.SRID != 4326 || g.String() != "SRID=4326;POINT Z (1 2 3)" {
		t.Fatalf("%#v, %v; shown %q", g, err, g.String())
	}
	if got := strings.ToUpper(hex.EncodeToString(ewkb(g))); got != postgisPointZ {
		t.Errorf("written back as %s; PostGIS should get the bytes it sent", got)
	}
	raw, _ := hex.DecodeString(postgisPointZ)
	if b, err := pgGeometry(raw); err != nil || b.String() != g.String() {
		t.Errorf("the binary form: %q, %v", b.String(), err)
	}
	for _, bad := range []string{"", "zz", "0101"} {
		if _, err := pgGeometry([]byte(bad)); err == nil {
			t.Errorf("%q is no geometry, yet it read as one", bad)
		}
	}
}
