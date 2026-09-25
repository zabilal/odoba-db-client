package model

import (
	"encoding/json"
	"errors"
	"fmt"
)

// A geometry written as GeoJSON, read as coordinates (FR-11.5).
//
// The third way a geometry arrives, after PostGIS's binary and a pair of
// numbers: a document store holds one as GeoJSON, and so does a JSON column in
// a relational one.
//
// RFC 7946's coordinates are longitude first, then latitude, which is the order
// this reads them in and the order Position holds. A file that had them the
// other way round would draw the world on its side, and there is no way to tell
// from the numbers alone — so this reads the standard and does not guess.

// GeoJSON reads a GeoJSON geometry, feature, or feature collection as positions.
//
// Anything it cannot read is an error rather than an empty answer: a value that
// is not GeoJSON is a column that is not a geometry, and a map drawn from
// nothing would say the rows have no places in them.
func GeoJSON(b []byte) (Coords, error) {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return Coords{}, fmt.Errorf("that is not JSON: %w", err)
	}
	var out Coords
	if err := geoJSON(v, &out, 0); err != nil {
		return Coords{}, err
	}
	return out, nil
}

// geoJSONDepth bounds how far one value may nest. A feature collection of
// collections is legal and a thousand of them is a value nobody wrote by hand.
const geoJSONDepth = 20

func geoJSON(v any, out *Coords, depth int) error {
	if depth > geoJSONDepth {
		return errors.New("that GeoJSON is nested too deeply to read")
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return errors.New("GeoJSON is an object, and that is not one")
	}
	kind, _ := obj["type"].(string)
	switch kind {
	case "":
		return errors.New("that object does not say what kind of GeoJSON it is")
	case "FeatureCollection":
		features, ok := obj["features"].([]any)
		if !ok {
			return errors.New("a feature collection holds a list of features")
		}
		for _, f := range features {
			if err := geoJSON(f, out, depth+1); err != nil {
				return err
			}
		}
		return nil
	case "Feature":
		g, ok := obj["geometry"]
		if !ok {
			return errors.New("a feature holds a geometry")
		}
		if g == nil {
			// A feature may have no geometry at all, which is a row with no
			// place in it rather than a fault.
			return nil
		}
		return geoJSON(g, out, depth+1)
	case "GeometryCollection":
		geometries, ok := obj["geometries"].([]any)
		if !ok {
			return errors.New("a geometry collection holds a list of geometries")
		}
		for _, g := range geometries {
			if err := geoJSON(g, out, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	coords, ok := obj["coordinates"]
	if !ok {
		return fmt.Errorf("a %s holds coordinates", kind)
	}
	switch kind {
	case "Point":
		p, err := geoPosition(coords)
		if err != nil {
			return err
		}
		out.Points = append(out.Points, p)
		return nil
	case "MultiPoint":
		points, err := geoLine(coords)
		if err != nil {
			return err
		}
		out.Points = append(out.Points, points...)
		return nil
	case "LineString":
		line, err := geoLine(coords)
		if err != nil {
			return err
		}
		if len(line) > 0 {
			out.Lines = append(out.Lines, line)
		}
		return nil
	case "MultiLineString":
		return geoLines(coords, out)
	case "Polygon":
		// A polygon's rings, holes and all: what draws them draws outlines.
		return geoLines(coords, out)
	case "MultiPolygon":
		list, ok := coords.([]any)
		if !ok {
			return errors.New("a multipolygon holds a list of polygons")
		}
		for _, poly := range list {
			if err := geoLines(poly, out); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("%q is not a kind of GeoJSON geometry", kind)
}

// geoPosition reads one position: two numbers, or more, of which the first two
// are the place and the rest is height and whatever else a file carries.
func geoPosition(v any) (Position, error) {
	list, ok := v.([]any)
	if !ok || len(list) < 2 {
		return Position{}, errors.New("a position is a list of at least two numbers")
	}
	x, xok := list[0].(float64)
	y, yok := list[1].(float64)
	if !xok || !yok {
		return Position{}, errors.New("a position's numbers are numbers")
	}
	return Position{X: x, Y: y}, nil
}

// geoLine reads a list of positions.
func geoLine(v any) ([]Position, error) {
	list, ok := v.([]any)
	if !ok {
		return nil, errors.New("that is not a list of positions")
	}
	out := make([]Position, 0, len(list))
	for _, p := range list {
		pos, err := geoPosition(p)
		if err != nil {
			return nil, err
		}
		out = append(out, pos)
	}
	return out, nil
}

// geoLines reads a list of lists of positions: a polygon's rings, or a
// multilinestring's lines.
func geoLines(v any, out *Coords) error {
	list, ok := v.([]any)
	if !ok {
		return errors.New("that is not a list of lines")
	}
	for _, l := range list {
		line, err := geoLine(l)
		if err != nil {
			return err
		}
		if len(line) > 0 {
			out.Lines = append(out.Lines, line)
		}
	}
	return nil
}
