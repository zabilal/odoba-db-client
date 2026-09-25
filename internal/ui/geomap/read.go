// Package geomap draws the places in a result (FR-11.5).
//
// There is no basemap. A tile map needs tiles, and tiles come from somebody's
// server over the network — which this application does not do on its own
// (NFR-D6), and which would put a stranger's host in the path of a person
// looking at their own data. So what is drawn is the geometry itself, on a
// graticule of degrees: enough to see that the polygons came out right, where
// the points are in relation to each other, and which row is which. What it is
// not is a street map, and it does not pretend to be one (ADR-0163).
//
// The reading is here and the drawing is beside it, in the same shape the chart
// package uses: a reading that decides nothing about colour or size, and a
// drawing that decides nothing about what the data means.
package geomap

import (
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// Where the places in a result are, and how they got there.

// Source says which columns hold the places.
type Source struct {
	// Geometry is the column holding a geometry — PostGIS's binary, or
	// GeoJSON — or None.
	Geometry int
	// Lat and Lon are a pair of numeric columns, where that is how the places
	// are given, or None each.
	Lat, Lon int
}

// None is a column nobody chose.
const None = -1

// Found reports whether there is anything to draw from.
func (s Source) Found() bool { return s.Geometry != None || (s.Lat != None && s.Lon != None) }

// Describe says where the places came from, for the line beside the map: a map
// drawn from the wrong two columns is a map of nowhere, and somebody has to be
// able to see which columns it used.
func (s Source) Describe(cols []model.ColumnDef) string {
	switch {
	case s.Geometry != None && s.Geometry < len(cols):
		return "Drawn from " + cols[s.Geometry].Name + "."
	case s.Lat != None && s.Lon != None && s.Lat < len(cols) && s.Lon < len(cols):
		return "Drawn from " + cols[s.Lat].Name + " and " + cols[s.Lon].Name + "."
	}
	return "Nothing here holds a place."
}

// Guess finds the places in a result.
//
// A geometry column first, because a column whose type is a geometry is not a
// guess at all. Then a pair of numbers named as latitude and longitude, which is
// how everything that is not a spatial database holds a place — and only by
// name: two numeric columns that happen to be side by side are not coordinates,
// and drawing them as though they were would put somebody's prices in the
// Atlantic.
func Guess(cols []model.ColumnDef, rows []model.Row) Source {
	out := Source{Geometry: None, Lat: None, Lon: None}
	for i, c := range cols {
		if c.Type.Class == model.TypeGeometry {
			out.Geometry = i
			return out
		}
	}
	// A JSON column holding GeoJSON: read as a geometry where its values say
	// they are one, which is a question about the values and not the type.
	for i, c := range cols {
		if c.Type.Class == model.TypeJSON && holdsGeoJSON(rows, i) {
			out.Geometry = i
			return out
		}
	}
	// Text that reads as well-known binary, which is how SQLite and MySQL hand
	// a geometry to a driver that was not told what the column is.
	for i := range cols {
		if holdsWKB(rows, i) {
			out.Geometry = i
			return out
		}
	}
	for i, c := range cols {
		if !numeric(c) {
			continue
		}
		switch {
		case out.Lat == None && named(c.Name, latNames):
			out.Lat = i
		case out.Lon == None && named(c.Name, lonNames):
			out.Lon = i
		}
	}
	return out
}

// latNames and lonNames are the names a coordinate goes by. Short ones are
// matched whole, because a column called "long" is a length as often as it is a
// longitude, and one called "y" is a number.
var (
	latNames = []string{"lat", "latitude", "y_lat", "lat_deg"}
	lonNames = []string{"lon", "lng", "long", "longitude", "x_lon", "lon_deg"}
)

// named compares a column's name to the ones a coordinate goes by, telling no
// difference between cases, spaces, underscores and hyphens — the same reading
// an import's column pairing makes.
func named(name string, want []string) bool {
	key := nameKey(name)
	for _, w := range want {
		if key == nameKey(w) {
			return true
		}
	}
	return false
}

func nameKey(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func numeric(c model.ColumnDef) bool {
	switch c.Type.Class {
	case model.TypeInteger, model.TypeFloat, model.TypeDecimal:
		return true
	}
	return false
}

// holdsGeoJSON reports whether a column's values read as GeoJSON. The first
// value that is not null decides: a column is one kind of thing.
func holdsGeoJSON(rows []model.Row, at int) bool {
	for _, row := range rows {
		if at >= len(row) || row[at] == nil {
			continue
		}
		_, err := coordsOf(row[at])
		return err == nil
	}
	return false
}

// holdsWKB reports whether a column's values read as well-known binary.
func holdsWKB(rows []model.Row, at int) bool {
	for _, row := range rows {
		if at >= len(row) || row[at] == nil {
			continue
		}
		b, ok := wkbBytes(row[at])
		if !ok {
			return false
		}
		_, err := model.Coordinates(b)
		return err == nil
	}
	return false
}
