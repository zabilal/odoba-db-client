package model

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Geometry is a spatial value (FR-3.8): its spatial reference and its shape
// as OGC well-known binary. Drivers turn their engine's own forms into this
// one, such as MySQL's SRID-prefixed WKB or PostGIS's extended WKB, so a
// geometry can be shown, exported and written back without loss.
type Geometry struct {
	SRID uint32
	WKB  []byte
}

// String writes the geometry as extended well-known text: "SRID=4326;POINT(1
// 2)", with no SRID part when it is 0. Bytes that do not read as WKB are
// described by their size, never guessed at.
func (g Geometry) String() string {
	wkt, err := WKT(g.WKB)
	if err != nil {
		return fmt.Sprintf("geometry (%d bytes that do not read as WKB)", len(g.WKB))
	}
	if g.SRID != 0 {
		return "SRID=" + strconv.FormatUint(uint64(g.SRID), 10) + ";" + wkt
	}
	return wkt
}

// WKT reads OGC well-known binary into ISO well-known text: "POINT(1 2)",
// "POLYGON((0 0,4 0,4 4,0 0))", "POINT Z (1 2 3)". It reads either byte
// order, and Z and M coordinates marked either way: the ISO way, a type plus
// 1000, 2000 or 3000, or the PostGIS way, in flag bits, where an embedded
// SRID is skipped. Coordinates are written exactly, in the fewest digits
// that read back as the same number.
func WKT(b []byte) (string, error) {
	r := &wkbReader{b: b}
	s, err := r.geometry()
	if err != nil {
		return "", err
	}
	if r.off != len(b) {
		return "", fmt.Errorf("%d bytes past the end of the geometry", len(b)-r.off)
	}
	return s, nil
}

var errShort = errors.New("the geometry ends early")

var wkbNames = [...]string{1: "POINT", 2: "LINESTRING", 3: "POLYGON", 4: "MULTIPOINT",
	5: "MULTILINESTRING", 6: "MULTIPOLYGON", 7: "GEOMETRYCOLLECTION"}

type wkbReader struct {
	b   []byte
	off int
}

type wkbHeader struct {
	order binary.ByteOrder
	kind  uint32
	dims  int
	tag   string // " Z", " M", " ZM" or ""
}

func (r *wkbReader) header() (wkbHeader, error) {
	var h wkbHeader
	if r.off >= len(r.b) {
		return h, errShort
	}
	switch r.b[r.off] {
	case 0:
		h.order = binary.BigEndian
	case 1:
		h.order = binary.LittleEndian
	default:
		return h, fmt.Errorf("byte order %d is neither 0 nor 1", r.b[r.off])
	}
	r.off++
	t, err := r.u32(h.order)
	if err != nil {
		return h, err
	}
	z, m := t&0x80000000 != 0, t&0x40000000 != 0
	if t&0x20000000 != 0 { // extended WKB's SRID, which the Geometry carries already
		if _, err := r.u32(h.order); err != nil {
			return h, err
		}
	}
	t &= 0x0fffffff
	switch t / 1000 {
	case 1:
		z = true
	case 2:
		m = true
	case 3:
		z, m = true, true
	}
	h.kind = t % 1000
	if h.kind < 1 || h.kind >= uint32(len(wkbNames)) {
		return h, fmt.Errorf("geometry type %d is not one WKB has", t)
	}
	h.dims = 2
	switch {
	case z && m:
		h.dims, h.tag = 4, " ZM"
	case z:
		h.dims, h.tag = 3, " Z"
	case m:
		h.dims, h.tag = 3, " M"
	}
	return h, nil
}

func (r *wkbReader) u32(o binary.ByteOrder) (uint32, error) {
	if r.off+4 > len(r.b) {
		return 0, errShort
	}
	v := o.Uint32(r.b[r.off:])
	r.off += 4
	return v, nil
}

func (r *wkbReader) f64(o binary.ByteOrder) (float64, error) {
	if r.off+8 > len(r.b) {
		return 0, errShort
	}
	v := math.Float64frombits(o.Uint64(r.b[r.off:]))
	r.off += 8
	return v, nil
}

// count reads how many parts follow, refusing more than the bytes left
// could hold: a corrupt count must not make the reader allocate without
// bound.
func (r *wkbReader) count(o binary.ByteOrder, least int) (int, error) {
	n, err := r.u32(o)
	if err != nil {
		return 0, err
	}
	if int64(n)*int64(least) > int64(len(r.b)-r.off) {
		return 0, errShort
	}
	return int(n), nil
}

// geometry reads one whole geometry, header and all.
func (r *wkbReader) geometry() (string, error) {
	h, err := r.header()
	if err != nil {
		return "", err
	}
	shape, empty, err := r.shape(h)
	if err != nil {
		return "", err
	}
	name := wkbNames[h.kind] + h.tag
	switch {
	case empty:
		return name + " EMPTY", nil
	case h.tag != "":
		return name + " " + shape, nil
	}
	return name + shape, nil
}

// shape reads the coordinates of one geometry after its header, in the
// parentheses WKT writes them in. An empty geometry is reported apart, since
// WKT writes it as a word.
func (r *wkbReader) shape(h wkbHeader) (string, bool, error) {
	switch h.kind {
	case 1:
		p, empty, err := r.point(h)
		if err != nil || empty {
			return "", empty, err
		}
		return "(" + p + ")", false, nil
	case 2:
		return r.points(h)
	case 3:
		n, err := r.count(h.order, 4)
		if err != nil || n == 0 {
			return "", n == 0, err
		}
		rings := make([]string, n)
		for i := range rings {
			ring, _, err := r.points(h)
			if err != nil {
				return "", false, err
			}
			rings[i] = ring
		}
		return "(" + strings.Join(rings, ",") + ")", false, nil
	case 4, 5, 6, 7:
		n, err := r.count(h.order, 5)
		if err != nil || n == 0 {
			return "", n == 0, err
		}
		parts := make([]string, n)
		for i := range parts {
			if h.kind == 7 {
				if parts[i], err = r.geometry(); err != nil {
					return "", false, err
				}
				continue
			}
			mh, err := r.header()
			if err != nil {
				return "", false, err
			}
			if mh.kind != h.kind-3 {
				return "", false, fmt.Errorf("a %s holds a %s", wkbNames[h.kind], wkbNames[mh.kind])
			}
			part, empty, err := r.shape(mh)
			if err != nil {
				return "", false, err
			}
			if empty {
				part = "EMPTY"
			}
			parts[i] = part
		}
		return "(" + strings.Join(parts, ",") + ")", false, nil
	}
	return "", false, fmt.Errorf("geometry type %d is not one WKB has", h.kind)
}

// point reads one position. WKB writes an empty point as all NaN.
func (r *wkbReader) point(h wkbHeader) (string, bool, error) {
	parts := make([]string, h.dims)
	empty := true
	for i := range parts {
		v, err := r.f64(h.order)
		if err != nil {
			return "", false, err
		}
		empty = empty && math.IsNaN(v)
		parts[i] = strconv.FormatFloat(v, 'f', -1, 64)
	}
	return strings.Join(parts, " "), empty, nil
}

// points reads a counted run of positions: a line, or a polygon's ring.
func (r *wkbReader) points(h wkbHeader) (string, bool, error) {
	n, err := r.count(h.order, 8*h.dims)
	if err != nil || n == 0 {
		return "", n == 0, err
	}
	out := make([]string, n)
	for i := range out {
		p, _, err := r.point(h)
		if err != nil {
			return "", false, err
		}
		out[i] = p
	}
	return "(" + strings.Join(out, ",") + ")", false, nil
}
