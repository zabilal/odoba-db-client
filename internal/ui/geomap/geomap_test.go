package geomap

import (
	"encoding/binary"
	"image/color"
	"math"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/ui/scene"
)

// The map: where a result's places are, and what is drawn for them (FR-11.5).
//
// Everything here is the reading and the drawing, with no widget: a picture said
// as a scene can be asserted, and a picture on a screen cannot.

// num and text make the columns a result has.
func num(name string) model.ColumnDef {
	return model.ColumnDef{Name: name, Type: model.DataType{Class: model.TypeFloat, Length: -1}}
}

func text(name string) model.ColumnDef {
	return model.ColumnDef{Name: name, Type: model.DataType{Class: model.TypeString, Length: -1}}
}

func geom(name string) model.ColumnDef {
	return model.ColumnDef{Name: name, Type: model.DataType{Class: model.TypeGeometry, Length: -1}}
}

func jsonCol(name string) model.ColumnDef {
	return model.ColumnDef{Name: name, Type: model.DataType{Class: model.TypeJSON, Length: -1}}
}

// wkbPoint is a point as well-known binary, little-endian.
func wkbPoint(x, y float64) []byte {
	b := []byte{1}
	b = binary.LittleEndian.AppendUint32(b, 1)
	b = binary.LittleEndian.AppendUint64(b, math.Float64bits(x))
	b = binary.LittleEndian.AppendUint64(b, math.Float64bits(y))
	return b
}

// A column whose type is a geometry needs no guessing at all.
func TestAGeometryColumnIsFoundByItsType(t *testing.T) {
	cols := []model.ColumnDef{text("name"), geom("shape"), num("lat"), num("lon")}
	rows := []model.Row{{"here", model.Geometry{WKB: wkbPoint(1, 2)}, 51.5, -0.1}}
	got := Guess(cols, rows)
	if got.Geometry != 1 {
		t.Fatalf("it found %+v", got)
	}
	if !got.Found() {
		t.Error("it found nothing")
	}
	// The geometry wins over the pair, because a type is not a guess.
	if got.Lat != None || got.Lon != None {
		t.Errorf("it also took the pair: %+v", got)
	}
	// And it wins on its type alone: a geometry column whose values are null is
	// still where the places are, so the pair beside it is not taken instead.
	empty := []model.Row{{"here", nil, 51.5, -0.1}}
	if got := Guess(cols, empty); got.Geometry != 1 || got.Lat != None || got.Lon != None {
		t.Errorf("with no values to read, it found %+v", got)
	}
	if said := got.Describe(cols); !strings.Contains(said, "shape") {
		t.Errorf("it says %q", said)
	}
}

// A pair of numbers is found by name, and only by name: two numeric columns that
// happen to be side by side are not coordinates, and drawing them as though they
// were would put somebody's prices in the Atlantic.
func TestAPairIsFoundByNameAndNotByPosition(t *testing.T) {
	for name, c := range map[string]struct {
		cols     []model.ColumnDef
		lat, lon int
	}{
		"lat and lon":            {[]model.ColumnDef{num("lat"), num("lon")}, 0, 1},
		"latitude and longitude": {[]model.ColumnDef{num("latitude"), num("longitude")}, 0, 1},
		"Lat and Lng, spelt as anybody spells them": {
			[]model.ColumnDef{num("Lat"), num("Lng")}, 0, 1},
		"underscores and cases":  {[]model.ColumnDef{num("LAT_DEG"), num("lon_deg")}, 0, 1},
		"the other way round":    {[]model.ColumnDef{num("lon"), num("lat")}, 1, 0},
		"among other columns":    {[]model.ColumnDef{text("name"), num("price"), num("lon"), num("lat")}, 3, 2},
		"two numbers, no names":  {[]model.ColumnDef{num("price"), num("cost")}, None, None},
		"a latitude of words":    {[]model.ColumnDef{text("lat"), text("lon")}, None, None},
		"a latitude and nothing": {[]model.ColumnDef{num("lat"), num("price")}, 0, None},
	} {
		t.Run(name, func(t *testing.T) {
			got := Guess(c.cols, nil)
			if got.Lat != c.lat || got.Lon != c.lon {
				t.Errorf("it found lat=%d lon=%d, want %d and %d", got.Lat, got.Lon, c.lat, c.lon)
			}
			if got.Found() != (c.lat != None && c.lon != None) {
				t.Errorf("it reads as found=%v", got.Found())
			}
		})
	}
}

// GeoJSON in a JSON column is found by what its values are, because the type
// says only that it is JSON.
func TestGeoJSONIsFoundByWhatItHolds(t *testing.T) {
	cols := []model.ColumnDef{text("name"), jsonCol("doc")}
	places := []model.Row{{"here", model.JSON(`{"type":"Point","coordinates":[13.4,52.5]}`)}}
	if got := Guess(cols, places); got.Geometry != 1 {
		t.Errorf("it found %+v", got)
	}
	// JSON that is not a geometry is not a geometry.
	other := []model.Row{{"here", model.JSON(`{"total":42}`)}}
	if got := Guess(cols, other); got.Found() {
		t.Errorf("it found %+v in a document with no place in it", got)
	}
	// A null first value is skipped rather than deciding: a column is one kind
	// of thing, and the first value that is anything says which.
	nulls := []model.Row{{"a", nil}, {"b", model.JSON(`{"type":"Point","coordinates":[1,2]}`)}}
	if got := Guess(cols, nulls); got.Geometry != 1 {
		t.Errorf("it found %+v", got)
	}
}

// Well-known binary as text is how SQLite and MySQL hand a geometry to a driver
// that was not told what the column is.
func TestBinaryGeometryInATextColumnIsFound(t *testing.T) {
	cols := []model.ColumnDef{text("shape")}
	var hex string
	for _, b := range wkbPoint(1, 2) {
		hex += string("0123456789abcdef"[b>>4]) + string("0123456789abcdef"[b&0xf])
	}
	if got := Guess(cols, []model.Row{{hex}}); got.Geometry != 0 {
		t.Errorf("hex: it found %+v", got)
	}
	if got := Guess(cols, []model.Row{{wkbPoint(3, 4)}}); got.Geometry != 0 {
		t.Errorf("bytes: it found %+v", got)
	}
	if got := Guess(cols, []model.Row{{"not a geometry at all"}}); got.Found() {
		t.Errorf("it found %+v in ordinary text", got)
	}
	// Hexadecimal that is not a geometry is not a geometry: read as bytes and
	// then refused, rather than taken for one because it was readable.
	if got := Guess(cols, []model.Row{{"0000"}}); got.Found() {
		t.Errorf("it found %+v in hexadecimal that is not WKB", got)
	}
	// And a geometry with a character more than it should have is not a
	// geometry: reading the bytes it does have would draw a place from a value
	// that has been damaged.
	if got := Guess(cols, []model.Row{{hex + "a"}}); got.Found() {
		t.Errorf("it found %+v in hexadecimal of an odd length", got)
	}
}

// Building the places: each row's own, and the ones that have none counted.
func TestTheRowsThatHaveNoPlaceAreCountedAndSaid(t *testing.T) {
	cols := []model.ColumnDef{num("lat"), num("lon")}
	rows := []model.Row{
		{51.5, -0.12},
		{nil, 1.0},     // no latitude
		{"north", 1.0}, // a latitude that is not a number
		{48.85, 2.35},
		{}, // a row with nothing in it
	}
	got := Build(cols, rows, Guess(cols, rows))
	if len(got.Places) != 2 {
		t.Fatalf("it read %d places: %+v", len(got.Places), got.Places)
	}
	if got.Skipped != 3 {
		t.Errorf("it left out %d rows, want 3", got.Skipped)
	}
	// Each place remembers its row, which is what a click filters by.
	if got.Places[0].Row != 0 || got.Places[1].Row != 3 {
		t.Errorf("the places are rows %d and %d", got.Places[0].Row, got.Places[1].Row)
	}
	// X is longitude and Y is latitude, whatever order the columns are in.
	if p := got.Places[0].Points[0]; p.X != -0.12 || p.Y != 51.5 {
		t.Errorf("the first place is %+v", p)
	}
	if !got.Ok || got.MinX != -0.12 || got.MaxX != 2.35 || got.MinY != 48.85 || got.MaxY != 51.5 {
		t.Errorf("it covers %v %v %v %v (%v)", got.MinX, got.MinY, got.MaxX, got.MaxY, got.Ok)
	}
}

// A result with nothing in it has no map, and that is an answer rather than a
// failure.
func TestAResultWithNoPlacesSaysSo(t *testing.T) {
	cols := []model.ColumnDef{text("name")}
	got := Build(cols, []model.Row{{"nowhere"}}, Guess(cols, nil))
	if len(got.Places) != 0 || got.Ok {
		t.Errorf("it read %+v", got)
	}
	if said := (Source{Geometry: None, Lat: None, Lon: None}).Describe(cols); !strings.Contains(said, "holds a place") {
		t.Errorf("it says %q", said)
	}
}

// The two axes are to the same scale, because a degree across and a degree up
// have to be the same length: a square lake drawn as a rectangle is a drawing
// that misleads about the data.
func TestBothAxesAreToTheSameScale(t *testing.T) {
	b := Built{MinX: 0, MinY: 0, MaxX: 10, MaxY: 1, Ok: true}
	f := Layout(b, 600, 400)
	across := (f.X.Max - f.X.Min) / f.X.Pixels
	up := (f.Y.Max - f.Y.Min) / f.Y.Pixels
	if math.Abs(across-up) > 1e-9 {
		t.Errorf("a pixel is %v across and %v up", across, up)
	}
	// And everything fits: the wider axis decides, so the data is inside what
	// is shown.
	if f.X.Min > b.MinX || f.X.Max < b.MaxX || f.Y.Min > b.MinY || f.Y.Max < b.MaxY {
		t.Errorf("it shows %v..%v by %v..%v, which does not hold the data",
			f.X.Min, f.X.Max, f.Y.Min, f.Y.Max)
	}
}

// One place is a place, not a nothing: a scale of no span would project it to the
// middle and say nothing about where it is.
func TestOnePlaceIsShownWithRoomAroundIt(t *testing.T) {
	b := Built{MinX: 13.4, MinY: 52.5, MaxX: 13.4, MaxY: 52.5, Ok: true}
	f := Layout(b, 400, 400)
	if f.X.Max-f.X.Min < 0.1 || f.Y.Max-f.Y.Min < 0.1 {
		t.Errorf("it shows %v..%v by %v..%v", f.X.Min, f.X.Max, f.Y.Min, f.Y.Max)
	}
	x, y := f.At(13.4, 52.5)
	if x < float64(f.Plot.Min.X) || x > float64(f.Plot.Max.X) {
		t.Errorf("the place is at x=%v, outside %v..%v", x, f.Plot.Min.X, f.Plot.Max.X)
	}
	if y < float64(f.Plot.Min.Y) || y > float64(f.Plot.Max.Y) {
		t.Errorf("the place is at y=%v, outside %v..%v", y, f.Plot.Min.Y, f.Plot.Max.Y)
	}
}

// North is up. A map whose latitude grew downward would be a map of the world
// upside down, and every shape in it would be mirrored.
func TestNorthIsUp(t *testing.T) {
	b := Built{MinX: 0, MinY: 50, MaxX: 10, MaxY: 60, Ok: true}
	f := Layout(b, 400, 400)
	_, north := f.At(5, 60)
	_, south := f.At(5, 50)
	if north >= south {
		t.Errorf("60° is at y=%v and 50° at y=%v", north, south)
	}
}

// Nothing to draw is drawn as the whole world, so that a map of no rows is a map
// and not an empty box.
func TestNothingToDrawIsTheWholeWorld(t *testing.T) {
	f := Layout(Built{}, 800, 400)
	if f.X.Min > -180 || f.X.Max < 180 {
		t.Errorf("it shows %v..%v across", f.X.Min, f.X.Max)
	}
}

// A size nothing fits in is a frame with no plot, rather than a panic or a
// picture drawn outside itself.
func TestASizeNothingFitsIn(t *testing.T) {
	f := Layout(Built{MinX: 0, MinY: 0, MaxX: 1, MaxY: 1, Ok: true}, 10, 10)
	if f.Plot.Dx() != 0 || f.Plot.Dy() != 0 {
		t.Errorf("its plot is %v", f.Plot)
	}
	s := Draw(Map{Built: Built{}, Frame: f})
	if len(s.Shapes) != 0 {
		t.Errorf("it drew %d shapes into no room", len(s.Shapes))
	}
}

// What is drawn: the graticule, then a mark for every place.
func TestWhatIsDrawn(t *testing.T) {
	cols := []model.ColumnDef{num("lat"), num("lon")}
	rows := []model.Row{{51.5, -0.12}, {48.85, 2.35}}
	b := Build(cols, rows, Guess(cols, rows))
	m := Map{Built: b, Frame: Layout(b, 600, 400),
		Colours: []color.NRGBA{{R: 1, A: 0xff}, {G: 1, A: 0xff}},
		Axis:    color.NRGBA{A: 0xff}, Grid: color.NRGBA{A: 0x20},
	}
	m.Highlight = -1
	s := Draw(m)
	var dots []scene.Dot
	var lines, labels int
	for _, sh := range s.Shapes {
		switch v := sh.(type) {
		case scene.Dot:
			dots = append(dots, v)
		case scene.Line:
			lines++
		case scene.Text:
			labels++
		}
	}
	if len(dots) != 2 {
		t.Errorf("it drew %d dots for two places", len(dots))
	}
	if lines == 0 || labels == 0 {
		t.Errorf("it drew %d graticule lines and %d labels", lines, labels)
	}
	// Both ways: a graticule of one direction is half a graticule.
	var across, up int
	for _, sh := range s.Shapes {
		if line, ok := sh.(scene.Line); ok {
			switch {
			case line.X1 == line.X2:
				up++
			case line.Y1 == line.Y2:
				across++
			}
		}
	}
	if across == 0 || up == 0 {
		t.Errorf("it drew %d lines across and %d up", across, up)
	}
	// Each row's own colour, so that two rows are told apart.
	if len(dots) == 2 && dots[0].Fill == dots[1].Fill {
		t.Error("both places are the same colour")
	}
	// A degree label says it is degrees, because a number alone could be
	// anything.
	var degree bool
	for _, sh := range s.Shapes {
		if txt, ok := sh.(scene.Text); ok && strings.Contains(txt.S, "°") {
			degree = true
		}
	}
	if !degree {
		t.Error("no label says degrees")
	}
}

// A polygon is drawn as its outline: lines between its positions, and no fill. A
// fill needs a rule about holes, and a rule applied wrongly draws a lake where
// there is land.
func TestAPolygonIsDrawnAsItsOutline(t *testing.T) {
	ring := []model.Position{{X: 0, Y: 0}, {X: 1, Y: 0}, {X: 1, Y: 1}, {X: 0, Y: 0}}
	b := Built{Places: []Place{{Row: 0, Coords: model.Coords{Lines: [][]model.Position{ring}}}},
		MinX: 0, MinY: 0, MaxX: 1, MaxY: 1, Ok: true}
	m := Map{Built: b, Frame: Layout(b, 400, 400), Highlight: -1,
		Colours: []color.NRGBA{{R: 1, A: 0xff}}}
	var mine int
	for _, sh := range Draw(m).Shapes {
		if line, ok := sh.(scene.Line); ok && line.Stroke == (color.NRGBA{R: 1, A: 0xff}) {
			mine++
		}
	}
	if mine != len(ring)-1 {
		t.Errorf("it drew %d segments for a ring of %d positions", mine, len(ring))
	}
	for _, sh := range Draw(m).Shapes {
		if box, ok := sh.(scene.Box); ok && box.Fill.A != 0 {
			t.Error("something was filled")
		}
	}
}

// The row somebody is looking at is drawn to stand out, which is how the map
// answers the grid's selection.
func TestTheChosenRowStandsOut(t *testing.T) {
	cols := []model.ColumnDef{num("lat"), num("lon")}
	rows := []model.Row{{51.5, -0.12}, {48.85, 2.35}}
	b := Build(cols, rows, Guess(cols, rows))
	plain := radiusOf(t, Map{Built: b, Frame: Layout(b, 600, 400), Highlight: -1}, 0)
	chosen := radiusOf(t, Map{Built: b, Frame: Layout(b, 600, 400), Highlight: 0}, 0)
	if chosen <= plain {
		t.Errorf("the chosen place is %v and the others %v", chosen, plain)
	}
}

// radiusOf is the radius of the nth dot drawn.
func radiusOf(t *testing.T, m Map, n int) float64 {
	t.Helper()
	at := 0
	for _, sh := range Draw(m).Shapes {
		if dot, ok := sh.(scene.Dot); ok {
			if at == n {
				return dot.R
			}
			at++
		}
	}
	t.Fatalf("there is no dot %d", n)
	return 0
}

// Reading the map: what is under the pointer, and where it is.
func TestWhatIsUnderThePointer(t *testing.T) {
	cols := []model.ColumnDef{num("lat"), num("lon")}
	rows := []model.Row{{51.5, -0.12}, {48.85, 2.35}}
	b := Build(cols, rows, Guess(cols, rows))
	m := Map{Built: b, Frame: Layout(b, 600, 400), Highlight: -1}
	// Over the second place.
	x, y := m.Frame.At(2.35, 48.85)
	got := Read(m, x, y)
	if got.Row != 1 {
		t.Errorf("it read row %d", got.Row)
	}
	if math.Abs(got.X-2.35) > 0.01 || math.Abs(got.Y-48.85) > 0.01 {
		t.Errorf("it reads the place as %v, %v", got.X, got.Y)
	}
	// Two places at the same position: the first row wins, so that reading the
	// same pixel twice says the same thing.
	same := Built{Places: []Place{
		{Row: 3, Coords: model.Coords{Points: []model.Position{{X: 1, Y: 1}}}},
		{Row: 4, Coords: model.Coords{Points: []model.Position{{X: 1, Y: 1}}}},
	}, MinX: 0, MinY: 0, MaxX: 2, MaxY: 2, Ok: true}
	tied := Map{Built: same, Frame: Layout(same, 400, 400), Highlight: -1}
	tx, ty := tied.Frame.At(1, 1)
	if got := Read(tied, tx, ty).Row; got != 3 {
		t.Errorf("a tie went to row %d", got)
	}

	// Far from anything: no row, and still a coordinate, because where the
	// pointer is is worth saying whether or not there is a place there.
	away := Read(m, x+200, y)
	if away.Row != -1 {
		t.Errorf("it read row %d away from everything", away.Row)
	}
	if away.X == got.X {
		t.Error("the coordinate did not follow the pointer")
	}
}
