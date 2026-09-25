package geomap

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// Turning rows into places.
//
// A place carries the row it came from, because the point of a map over a result
// is to get back to the row: hovering says which one it is, and clicking filters
// the grid by it, exactly as a chart's marks do (FR-11.4).

// Place is one row's geometry.
type Place struct {
	// Row is the row's place in the result, from 0.
	Row int
	model.Coords
}

// Built is what a result gave.
type Built struct {
	Places []Place

	// Skipped is the rows left out because their place could not be read: a
	// null, a value that is not a geometry, a coordinate that is not a number.
	// A map drawn over gaps nobody was told about is a map that lies by
	// omission.
	Skipped int

	// MinX and the rest are the rectangle every place together covers, in the
	// geometry's own coordinates. Ok is false when there is nothing to draw.
	MinX, MinY, MaxX, MaxY float64
	Ok                     bool
}

// ErrNoPlaces is a result with nothing to draw, which is not a failure: a query
// that returned no rows has no map, and saying so is the answer.
var ErrNoPlaces = errors.New("geomap: there is nothing to draw")

// Build reads a result's places.
func Build(cols []model.ColumnDef, rows []model.Row, src Source) Built {
	var out Built
	for i, row := range rows {
		c, err := place(row, src)
		if err != nil || c.Empty() {
			out.Skipped++
			continue
		}
		out.Places = append(out.Places, Place{Row: i, Coords: c})
	}
	all := model.Coords{}
	for _, p := range out.Places {
		all.Points = append(all.Points, p.Points...)
		all.Lines = append(all.Lines, p.Lines...)
	}
	out.MinX, out.MinY, out.MaxX, out.MaxY, out.Ok = all.Bounds()
	return out
}

// place reads one row's geometry.
func place(row model.Row, src Source) (model.Coords, error) {
	if src.Geometry != None {
		if src.Geometry >= len(row) || row[src.Geometry] == nil {
			return model.Coords{}, errNoPlace
		}
		return coordsOf(row[src.Geometry])
	}
	if src.Lat == None || src.Lon == None || src.Lat >= len(row) || src.Lon >= len(row) {
		return model.Coords{}, errNoPlace
	}
	lat, latOK := number(row[src.Lat])
	lon, lonOK := number(row[src.Lon])
	if !latOK || !lonOK {
		return model.Coords{}, errNoPlace
	}
	// X is longitude and Y is latitude, which is the order everything that
	// draws uses and the opposite of the order a pair of columns is usually
	// named in.
	return model.Coords{Points: []model.Position{{X: lon, Y: lat}}}, nil
}

var errNoPlace = errors.New("geomap: this row holds no place")

// coordsOf reads a value as a geometry: PostGIS's own type, bytes of well-known
// binary, or GeoJSON.
func coordsOf(v any) (model.Coords, error) {
	switch t := v.(type) {
	case model.Geometry:
		return t.Coordinates()
	case model.JSON:
		return model.GeoJSON([]byte(t))
	}
	if b, ok := wkbBytes(v); ok {
		if c, err := model.Coordinates(b); err == nil {
			return c, nil
		}
	}
	if b, ok := jsonBytes(v); ok {
		return model.GeoJSON(b)
	}
	return model.Coords{}, fmt.Errorf("geomap: a %T is not a place", v)
}

// wkbBytes is a value's well-known binary, where it has any: bytes as they are,
// or the hexadecimal a driver hands back for a geometry it was not told about.
func wkbBytes(v any) ([]byte, bool) {
	switch t := v.(type) {
	case model.Geometry:
		return t.WKB, true
	case []byte:
		if b, ok := unhex(string(t)); ok {
			return b, true
		}
		return t, true
	case string:
		return unhex(t)
	}
	return nil, false
}

// jsonBytes is a value's JSON text, where it looks like any.
func jsonBytes(v any) ([]byte, bool) {
	switch t := v.(type) {
	case model.JSON:
		return []byte(t), true
	case []byte:
		return t, true
	case string:
		return []byte(t), true
	}
	return nil, false
}

// unhex reads a string of hexadecimal as bytes. PostGIS hands its binary to a
// driver as hex text, and so do SQLite and MySQL through some drivers.
func unhex(s string) ([]byte, bool) {
	if len(s) < 2 || len(s)%2 != 0 {
		return nil, false
	}
	out := make([]byte, len(s)/2)
	for i := 0; i < len(out); i++ {
		hi, ok1 := hexDigit(s[2*i])
		lo, ok2 := hexDigit(s[2*i+1])
		if !ok1 || !ok2 {
			return nil, false
		}
		out[i] = hi<<4 | lo
	}
	return out, true
}

func hexDigit(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// number reads a value as a coordinate. Text is read as a number too, because a
// decimal arrives as text where every digit matters — and a coordinate's digits
// matter.
func number(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int64:
		return float64(t), true
	case int32:
		return float64(t), true
	case int:
		return float64(t), true
	case string:
		return parseFloat(t)
	case []byte:
		return parseFloat(string(t))
	}
	return 0, false
}

// parseFloat reads a number written as text, and nothing else: a coordinate that
// is not a number is a row with no place in it.
func parseFloat(s string) (float64, bool) {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0, false
	}
	return f, true
}
