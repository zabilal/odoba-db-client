package model

import (
	"errors"
	"math"
)

// A geometry read as coordinates, for drawing it (FR-11.5).
//
// Geometry.String writes a geometry as text, which is what a cell shows. A map
// needs the numbers, so this is the same well-known binary read a second way:
// the positions, and which of them are joined.
//
// What a map needs of a geometry is only its outline. A polygon's holes are its
// rings like any other, drawn as the lines they are — a filled polygon with a
// hole in it needs a fill rule and a fill, and there is no filling here (see
// ADR-0163). Reading the rings faithfully now means a fill later is a drawing
// change and not a reading one.

// Position is one point of a geometry, in the geometry's own coordinates: for
// SRID 4326 that is longitude as X and latitude as Y, in degrees.
type Position struct{ X, Y float64 }

// Coords is a geometry as positions.
type Coords struct {
	// Points are the stand-alone positions: a point, and the members of a
	// multipoint.
	Points []Position
	// Lines are the runs of positions that are joined: a line string, and each
	// ring of each polygon. A ring arrives closed, as WKB writes it.
	Lines [][]Position
}

// Empty reports whether there is nothing to draw.
func (c Coords) Empty() bool { return len(c.Points) == 0 && len(c.Lines) == 0 }

// Bounds is the rectangle a set of coordinates covers. ok is false for nothing,
// and for coordinates that are all NaN — an empty point is written as NaN, and a
// map of NaN is a map of nowhere.
func (c Coords) Bounds() (minX, minY, maxX, maxY float64, ok bool) {
	minX, minY = math.Inf(1), math.Inf(1)
	maxX, maxY = math.Inf(-1), math.Inf(-1)
	see := func(p Position) {
		if math.IsNaN(p.X) || math.IsNaN(p.Y) {
			return
		}
		ok = true
		minX, minY = math.Min(minX, p.X), math.Min(minY, p.Y)
		maxX, maxY = math.Max(maxX, p.X), math.Max(maxY, p.Y)
	}
	for _, p := range c.Points {
		see(p)
	}
	for _, line := range c.Lines {
		for _, p := range line {
			see(p)
		}
	}
	if !ok {
		return 0, 0, 0, 0, false
	}
	return minX, minY, maxX, maxY, true
}

// Coordinates reads the geometry's positions.
func (g Geometry) Coordinates() (Coords, error) { return Coordinates(g.WKB) }

// Coordinates reads well-known binary as positions. It reads what WKT reads —
// either byte order, Z and M marked either way, PostGIS's embedded SRID — and
// answers the same errors for the same bytes, because it is the same reader.
func Coordinates(b []byte) (Coords, error) {
	r := &wkbReader{b: b}
	var out Coords
	if err := r.positions(&out); err != nil {
		return Coords{}, err
	}
	return out, nil
}

// positions reads one geometry, and everything inside it, into out.
func (r *wkbReader) positions(out *Coords) error {
	h, err := r.header()
	if err != nil {
		return err
	}
	return r.shapePositions(h, out)
}

// shapePositions reads the coordinates of one geometry after its header.
//
// An empty geometry adds nothing, which is the whole of what "empty" means to
// something that draws: WKT has a word for it and a map has no mark for it.
func (r *wkbReader) shapePositions(h wkbHeader, out *Coords) error {
	switch h.kind {
	case 1: // point
		p, empty, err := r.position(h)
		if err != nil || empty {
			return err
		}
		out.Points = append(out.Points, p)
		return nil
	case 2: // line string
		line, err := r.positionRun(h)
		if err != nil || len(line) == 0 {
			return err
		}
		out.Lines = append(out.Lines, line)
		return nil
	case 3: // polygon: its rings, each a closed run
		n, err := r.count(h.order, 4)
		if err != nil || n == 0 {
			return err
		}
		for i := 0; i < n; i++ {
			ring, err := r.positionRun(h)
			if err != nil {
				return err
			}
			if len(ring) > 0 {
				out.Lines = append(out.Lines, ring)
			}
		}
		return nil
	case 4, 5, 6, 7: // multipoint, multilinestring, multipolygon, collection
		n, err := r.count(h.order, 5)
		if err != nil || n == 0 {
			return err
		}
		for i := 0; i < n; i++ {
			if h.kind == 7 {
				if err := r.positions(out); err != nil {
					return err
				}
				continue
			}
			mh, err := r.header()
			if err != nil {
				return err
			}
			if mh.kind != h.kind-3 {
				return errors.New("a multi-geometry holds a member of another kind")
			}
			if err := r.shapePositions(mh, out); err != nil {
				return err
			}
		}
		return nil
	}
	return errors.New("that is not a geometry type WKB has")
}

// position reads one position, keeping the first two dimensions and reading past
// the rest: a map draws in two, and a Z that was thrown away silently would be a
// height nobody could find again — which is why the cell still shows the whole
// text (Geometry.String).
func (r *wkbReader) position(h wkbHeader) (Position, bool, error) {
	var p Position
	empty := true
	for i := 0; i < h.dims; i++ {
		v, err := r.f64(h.order)
		if err != nil {
			return Position{}, false, err
		}
		empty = empty && math.IsNaN(v)
		switch i {
		case 0:
			p.X = v
		case 1:
			p.Y = v
		}
	}
	return p, empty, nil
}

// positionRun reads a counted run of positions: a line, or a polygon's ring.
func (r *wkbReader) positionRun(h wkbHeader) ([]Position, error) {
	n, err := r.count(h.order, 8*h.dims)
	if err != nil || n == 0 {
		return nil, err
	}
	out := make([]Position, n)
	for i := range out {
		p, _, err := r.position(h)
		if err != nil {
			return nil, err
		}
		out[i] = p
	}
	return out, nil
}
