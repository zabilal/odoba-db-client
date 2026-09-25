package model

import (
	"strings"
	"testing"
)

// GeoJSON, read as coordinates (FR-11.5).
//
// RFC 7946 puts longitude first. Everything below depends on that and nothing
// guesses: a file with them the other way round draws the world on its side, and
// no amount of looking at the numbers can tell.

func TestGeoJSONReadsEveryShape(t *testing.T) {
	for _, c := range []struct {
		name   string
		json   string
		points []Position
		lines  [][]Position
	}{
		{name: "a point", json: `{"type":"Point","coordinates":[13.4,52.5]}`,
			points: []Position{{13.4, 52.5}}},
		{name: "a point with a height", json: `{"type":"Point","coordinates":[1,2,300]}`,
			points: []Position{{1, 2}}},
		{name: "many points", json: `{"type":"MultiPoint","coordinates":[[1,2],[3,4]]}`,
			points: []Position{{1, 2}, {3, 4}}},
		{name: "a line", json: `{"type":"LineString","coordinates":[[0,0],[1,1]]}`,
			lines: [][]Position{{{0, 0}, {1, 1}}}},
		{name: "many lines", json: `{"type":"MultiLineString","coordinates":[[[0,0],[1,1]],[[2,2],[3,3]]]}`,
			lines: [][]Position{{{0, 0}, {1, 1}}, {{2, 2}, {3, 3}}}},
		{name: "a polygon with a hole",
			json: `{"type":"Polygon","coordinates":[[[0,0],[4,0],[4,4],[0,0]],[[1,1],[2,1],[2,2],[1,1]]]}`,
			lines: [][]Position{
				{{0, 0}, {4, 0}, {4, 4}, {0, 0}},
				{{1, 1}, {2, 1}, {2, 2}, {1, 1}},
			}},
		{name: "many polygons",
			json:  `{"type":"MultiPolygon","coordinates":[[[[0,0],[1,0],[1,1],[0,0]]],[[[5,5],[6,5],[6,6],[5,5]]]]}`,
			lines: [][]Position{{{0, 0}, {1, 0}, {1, 1}, {0, 0}}, {{5, 5}, {6, 5}, {6, 6}, {5, 5}}}},
		{name: "a collection",
			json:   `{"type":"GeometryCollection","geometries":[{"type":"Point","coordinates":[1,2]},{"type":"LineString","coordinates":[[0,0],[1,1]]}]}`,
			points: []Position{{1, 2}}, lines: [][]Position{{{0, 0}, {1, 1}}}},
		{name: "a feature",
			json:   `{"type":"Feature","properties":{"name":"here"},"geometry":{"type":"Point","coordinates":[9,8]}}`,
			points: []Position{{9, 8}}},
		{name: "a feature collection",
			json: `{"type":"FeatureCollection","features":[
				{"type":"Feature","geometry":{"type":"Point","coordinates":[1,2]}},
				{"type":"Feature","geometry":{"type":"Point","coordinates":[3,4]}}]}`,
			points: []Position{{1, 2}, {3, 4}}},
		// A feature with no geometry is a row with no place in it, not a fault.
		{name: "a feature with no geometry",
			json: `{"type":"Feature","properties":{},"geometry":null}`},
		{name: "an empty line", json: `{"type":"LineString","coordinates":[]}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := GeoJSON([]byte(c.json))
			if err != nil {
				t.Fatalf("%v", err)
			}
			if len(got.Points) != len(c.points) || len(got.Lines) != len(c.lines) {
				t.Fatalf("it read %+v", got)
			}
			for i, p := range c.points {
				if got.Points[i] != p {
					t.Errorf("point %d is %+v, want %+v", i, got.Points[i], p)
				}
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
		})
	}
}

// What is not GeoJSON says so. A column that is not a geometry must not read as
// a geometry with nothing in it: a map drawn from that would say the rows have no
// places in them, which is a different thing from "this is not a place".
func TestGeoJSONRefusesWhatItCannotRead(t *testing.T) {
	for name, c := range map[string]struct {
		json string
		says string
	}{
		"not JSON at all":     {`POINT(1 2)`, "not JSON"},
		"not an object":       {`[1,2]`, "is an object"},
		"no type":             {`{"coordinates":[1,2]}`, "what kind"},
		"a type nobody has":   {`{"type":"Doughnut","coordinates":[1,2]}`, "not a kind"},
		"no coordinates":      {`{"type":"Point"}`, "holds coordinates"},
		"one number":          {`{"type":"Point","coordinates":[1]}`, "at least two"},
		"words for numbers":   {`{"type":"Point","coordinates":["1","2"]}`, "are numbers"},
		"a feature of naught": {`{"type":"Feature"}`, "holds a geometry"},
		"a collection of one": {`{"type":"GeometryCollection","geometries":{}}`, "list of geometries"},
		"features that are not a list": {`{"type":"FeatureCollection","features":{}}`,
			"list of features"},
		"a line of points that are not lists": {`{"type":"MultiLineString","coordinates":[1,2]}`,
			"not a list of positions"},
		"a polygon of nothing": {`{"type":"Polygon","coordinates":7}`, "not a list of lines"},
		"polygons of nothing":  {`{"type":"MultiPolygon","coordinates":7}`, "list of polygons"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := GeoJSON([]byte(c.json))
			if err == nil {
				t.Fatalf("it read %+v", got)
			}
			if !strings.Contains(err.Error(), c.says) {
				t.Errorf("it says %q, which does not mention %q", err, c.says)
			}
			if !got.Empty() {
				t.Errorf("it answered %+v as well as an error", got)
			}
		})
	}
}

// A value that nests for ever is refused rather than followed: a collection of
// collections is legal, and a thousand of them is nothing anybody wrote.
func TestGeoJSONThatNestsForEverIsRefused(t *testing.T) {
	deep := `{"type":"Point","coordinates":[1,2]}`
	for i := 0; i < 40; i++ {
		deep = `{"type":"GeometryCollection","geometries":[` + deep + `]}`
	}
	if _, err := GeoJSON([]byte(deep)); err == nil {
		t.Fatal("it read it")
	} else if !strings.Contains(err.Error(), "deeply") {
		t.Errorf("it says %q", err)
	}
}
