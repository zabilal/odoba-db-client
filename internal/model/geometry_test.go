package model

import (
	"encoding/binary"
	"math"
	"strings"
	"testing"
)

// wkb writes well-known binary for tests, in either byte order.
type wkb struct {
	o binary.AppendByteOrder
	b []byte
}

func le() *wkb { return &wkb{o: binary.LittleEndian} }
func be() *wkb { return &wkb{o: binary.BigEndian} }

func (w *wkb) head(t uint32) *wkb {
	if w.o == binary.BigEndian {
		w.b = append(w.b, 0)
	} else {
		w.b = append(w.b, 1)
	}
	return w.u32(t)
}
func (w *wkb) u32(v uint32) *wkb { w.b = w.o.AppendUint32(w.b, v); return w }
func (w *wkb) f(vs ...float64) *wkb {
	for _, v := range vs {
		w.b = w.o.AppendUint64(w.b, math.Float64bits(v))
	}
	return w
}

func TestWKTReadsEveryShape(t *testing.T) {
	ring := func(w *wkb, pts ...float64) *wkb { return w.u32(uint32(len(pts) / 2)).f(pts...) }
	for _, c := range []struct {
		name string
		b    []byte
		want string
	}{
		{"point", le().head(1).f(1, 2).b, "POINT(1 2)"},
		{"exact digits", le().head(1).f(0.1, -2.25).b, "POINT(0.1 -2.25)"},
		{"big-endian line", ring(be().head(2), 0, 0, 1.5, -2).b, "LINESTRING(0 0,1.5 -2)"},
		{"polygon with a hole", ring(ring(le().head(3).u32(2), 0, 0, 4, 0, 4, 4, 0, 0), 1, 1, 2, 1, 2, 2, 1, 1).b,
			"POLYGON((0 0,4 0,4 4,0 0),(1 1,2 1,2 2,1 1))"},
		{"multipoint", le().head(4).u32(2).head(1).f(1, 2).head(1).f(3, 4).b, "MULTIPOINT((1 2),(3 4))"},
		{"collection", ring(le().head(7).u32(2).head(1).f(1, 2).head(2), 0, 0, 1, 1).b,
			"GEOMETRYCOLLECTION(POINT(1 2),LINESTRING(0 0,1 1))"},
		{"ISO Z", le().head(1001).f(1, 2, 3).b, "POINT Z (1 2 3)"},
		{"PostGIS Z with an SRID", le().head(0xA0000001).u32(4326).f(1, 2, 3).b, "POINT Z (1 2 3)"},
		{"empty point", le().head(1).f(math.NaN(), math.NaN()).b, "POINT EMPTY"},
		{"empty line", le().head(2).u32(0).b, "LINESTRING EMPTY"},
	} {
		got, err := WKT(c.b)
		if err != nil || got != c.want {
			t.Errorf("%s: %q, %v; want %q", c.name, got, err, c.want)
		}
	}
}

func TestWKTRefusesWhatIsNotWKB(t *testing.T) {
	for _, c := range []struct {
		name string
		b    []byte
		want string
	}{
		{"cut short", le().head(1).f(1).b, "ends early"},
		{"byte order", []byte{7, 1, 0, 0, 0}, "byte order"},
		{"unknown type", le().head(99).b, "not one WKB has"},
		{"trailing bytes", append(le().head(1).f(1, 2).b, 0), "past the end"},
		{"a count no bytes could hold", le().head(2).u32(1 << 30).b, "ends early"},
		{"a multipoint of lines", le().head(4).u32(1).head(2).u32(0).b, "holds a LINESTRING"},
	} {
		if _, err := WKT(c.b); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want an error saying %q", c.name, err, c.want)
		}
	}
}

func TestGeometryStringNamesItsSRID(t *testing.T) {
	pt := le().head(1).f(1, 2).b
	if got := (Geometry{SRID: 4326, WKB: pt}).String(); got != "SRID=4326;POINT(1 2)" {
		t.Errorf("%q", got)
	}
	if got := (Geometry{WKB: pt}).String(); got != "POINT(1 2)" {
		t.Errorf("SRID 0 is not written: %q", got)
	}
	if got := (Geometry{WKB: []byte{1, 2, 3}}).String(); got != "geometry (3 bytes that do not read as WKB)" {
		t.Errorf("%q", got)
	}
}
