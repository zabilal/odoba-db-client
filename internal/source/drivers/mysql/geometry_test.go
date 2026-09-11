package mysql

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// POINT(1 2) in little-endian WKB.
var pointWKB = []byte{1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0xf0, 0x3f, 0, 0, 0, 0, 0, 0, 0, 0x40}

func TestGeometryArrivesAsSRIDAndWKBAndGoesBackAsItCame(t *testing.T) {
	raw := append([]byte{0x11, 0x0f, 0, 0}, pointWKB...) // SRID 3857
	g, ok := normalize(raw, dataType("GEOMETRY")).(model.Geometry)
	if !ok || g.SRID != 3857 || !bytes.Equal(g.WKB, pointWKB) {
		t.Fatalf("%#v", g)
	}
	if g.String() != "SRID=3857;POINT(1 2)" {
		t.Errorf("shown as %q", g.String())
	}
	lit, err := insertLiteral(g, dataType("GEOMETRY"))
	if want := "X'" + hex.EncodeToString(raw) + "'"; err != nil || lit != want {
		t.Errorf("literal %s, %v; want %s", lit, err, want)
	}
}
