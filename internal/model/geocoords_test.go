package model

import (
	"math"
	"strings"
	"testing"
)

// Reading a geometry as coordinates (FR-11.5).
//
// The same bytes the WKT tests use, read the other way: a geometry that writes
// as "POLYGON((0 0,4 0,4 4,0 0))" has to draw as those four positions joined,
// and the two readings cannot be allowed to disagree about what is in the bytes.

func TestCoordinatesReadsEveryShape(t *testing.T) {
	ring := func(w *wkb, pts ...float64) *wkb { return w.u32(uint32(len(pts) / 2)).f(pts...) }
	for _, c := range []struct {
		name   string
		b      []byte
		points []Position
		lines  [][]Position
	}{
		{name: "point", b: le().head(1).f(1, 2).b, points: []Position{{1, 2}}},
		{name: "big-endian line", b: ring(be().head(2), 0, 0, 1.5, -2).b,
			lines: [][]Position{{{0, 0}, {1.5, -2}}}},
		{
			// A hole is a ring like any other: what draws it draws an outline,
			// and a fill would need a rule this does not decide (ADR-0163).
			name: "polygon with a hole",
			b: ring(ring(le().head(3).u32(2), 0, 0, 4, 0, 4, 4, 0, 0),
				1, 1, 2, 1, 2, 2, 1, 1).b,
			lines: [][]Position{
				{{0, 0}, {4, 0}, {4, 4}, {0, 0}},
				{{1, 1}, {2, 1}, {2, 2}, {1, 1}},
			},
		},
		{name: "multipoint", b: le().head(4).u32(2).head(1).f(1, 2).head(1).f(3, 4).b,
			points: []Position{{1, 2}, {3, 4}}},
		{name: "collection", b: ring(le().head(7).u32(2).head(1).f(1, 2).head(2), 0, 0, 1, 1).b,
			points: []Position{{1, 2}}, lines: [][]Position{{{0, 0}, {1, 1}}}},
		// A third dimension is read past rather than read: a map draws in two,
		// and the cell still shows the whole text.
		{name: "ISO Z", b: le().head(1001).f(1, 2, 3).b, points: []Position{{1, 2}}},
		{name: "PostGIS Z with an SRID", b: le().head(0xA0000001).u32(4326).f(1, 2, 3).b,
			points: []Position{{1, 2}}},
		// Empty adds nothing: WKT has a word for it and a map has no mark.
		{name: "empty point", b: le().head(1).f(math.NaN(), math.NaN()).b},
		{name: "empty line", b: le().head(2).u32(0).b},
		{name: "empty polygon", b: le().head(3).u32(0).b},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := Coordinates(c.b)
			if err != nil {
				t.Fatalf("%v", err)
			}
			if len(got.Points) != len(c.points) {
				t.Fatalf("it read %d points, want %d: %+v", len(got.Points), len(c.points), got.Points)
			}
			for i, p := range c.points {
				if got.Points[i] != p {
					t.Errorf("point %d is %+v, want %+v", i, got.Points[i], p)
				}
			}
			if len(got.Lines) != len(c.lines) {
				t.Fatalf("it read %d lines, want %d: %+v", len(got.Lines), len(c.lines), got.Lines)
			}
			for i, line := range c.lines {
				if len(got.Lines[i]) != len(line) {
					t.Fatalf("line %d has %d positions, want %d", i, len(got.Lines[i]), len(line))
				}
				for j, p := range line {
					if got.Lines[i][j] != p {
						t.Errorf("line %d position %d is %+v, want %+v", i, j, got.Lines[i][j], p)
					}
				}
			}
			if got.Empty() != (len(c.points) == 0 && len(c.lines) == 0) {
				t.Errorf("it reads as empty=%v", got.Empty())
			}
		})
	}
}

// What the two readings say about the same bytes cannot differ, so the bytes
// that WKT refuses are refused here too — and for a reason a person can act on
// rather than a zero value.
func TestCoordinatesRefusesWhatIsNotWKB(t *testing.T) {
	for name, b := range map[string][]byte{
		"nothing":           nil,
		"a byte order of 2": {2, 1, 0, 0, 0},
		"a type nobody has": le().head(99).b,
		"a point cut short": le().head(1).b,
		"a count cut short": le().head(2).b,
		"a line cut short":  le().head(2).u32(2).f(1, 2).b,
		"a count too large": le().head(2).u32(1 << 30).b,
		"a mixed multi":     le().head(5).u32(1).head(1).f(1, 2).b,
		"a ring cut short":  le().head(3).u32(1).b,
		// A collection that says two and holds one, which is short where it
		// matters: the member's header is not there at all.
		"a collection short": le().head(7).u32(2).head(1).f(1, 2).b,
	} {
		t.Run(name, func(t *testing.T) {
			got, err := Coordinates(b)
			if err == nil {
				t.Fatalf("it read %+v", got)
			}
			if !got.Empty() {
				t.Errorf("it answered %+v as well as an error", got)
			}
			if strings.TrimSpace(err.Error()) == "" {
				t.Error("it said nothing about what is wrong")
			}
		})
	}
}

// The rectangle a geometry covers, which is what something drawing it has to fit
// into a window. Nothing, and NaN, cover nothing: a map of NaN is a map of
// nowhere, and a window fitted to it would show everything at once.
func TestTheRectangleCoordinatesCover(t *testing.T) {
	c := Coords{
		Points: []Position{{1, 2}, {-3, 4}},
		Lines:  [][]Position{{{0, 0}, {5, -6}}},
	}
	minX, minY, maxX, maxY, ok := c.Bounds()
	if !ok {
		t.Fatal("it covers nothing")
	}
	if minX != -3 || minY != -6 || maxX != 5 || maxY != 4 {
		t.Errorf("it covers %v %v %v %v", minX, minY, maxX, maxY)
	}
	if _, _, _, _, ok := (Coords{}).Bounds(); ok {
		t.Error("nothing covers something")
	}
	nan := Coords{Points: []Position{{math.NaN(), math.NaN()}}}
	if _, _, _, _, ok := nan.Bounds(); ok {
		t.Error("NaN covers something")
	}
	// One position covers a rectangle of no size, which is a place and not a
	// nothing: what draws it has to decide what to show around it.
	one := Coords{Points: []Position{{7, 8}}}
	minX, minY, maxX, maxY, ok = one.Bounds()
	if !ok || minX != 7 || maxX != 7 || minY != 8 || maxY != 8 {
		t.Errorf("one position covers %v %v %v %v (%v)", minX, minY, maxX, maxY, ok)
	}
}

// A geometry reads its own coordinates, which is the way a cell's value reaches
// a map: the value in the grid is a Geometry, not bytes.
func TestAGeometryReadsItsOwnCoordinates(t *testing.T) {
	g := Geometry{SRID: 4326, WKB: le().head(1).f(13.4, 52.5).b}
	got, err := g.Coordinates()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Points) != 1 || got.Points[0] != (Position{13.4, 52.5}) {
		t.Errorf("it read %+v", got)
	}
	if _, err := (Geometry{WKB: []byte{9}}).Coordinates(); err == nil {
		t.Error("bytes that are not WKB read as coordinates")
	}
}
