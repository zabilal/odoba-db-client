// Package scene says what a picture is made of, once.
//
// A drawing in this program is shown three ways: as Fyne objects on a screen,
// as pixels in a PNG, and as text in an SVG. Saying it three times would mean
// three drawings that could drift, and an exported picture that differs from
// the one somebody exported it from is worse than no export at all
// (ADR-0130).
//
// So a drawing is said here, in screen coordinates, and each of the three
// reads the same description. Nothing in this file imports Fyne.
package scene

import (
	"image"
	"image/color"
)

// Shape is one thing to draw.
type Shape interface{ shape() }

// Box is a rectangle: a node, a legend swatch, the plot area of a chart.
type Box struct {
	X, Y, W, H  float64
	Fill        color.NRGBA
	Stroke      color.NRGBA
	StrokeWidth float64
	Radius      float64
}

// Line is a segment: an edge, a hairline, an axis, a gridline.
type Line struct {
	X1, Y1, X2, Y2 float64
	Stroke         color.NRGBA
	Width          float64
}

// Align is where a label sits against the X it is given.
type Align uint8

const (
	// Leading begins the text at X, which is what text normally does.
	Leading Align = iota
	// Center centres the text on X, which is what a tick label under an
	// axis does.
	Center
	// Trailing ends the text at X, which is how a column's type sits
	// against the right edge of its box, and how a Y tick label sits
	// against its axis, without this having to measure it.
	Trailing
)

// Text is a label.
//
// Y is the top of the line of text, as it is in Fyne, unless Middle is set,
// in which case it is the middle. Each of the three renderers puts that in
// its own terms.
type Text struct {
	X, Y   float64
	S      string
	Size   float64
	Fill   color.NRGBA
	Bold   bool
	Align  Align
	Middle bool
}

// Dot is a small filled circle: a key mark, a point of interest.
type Dot struct {
	X, Y, R float64
	Fill    color.NRGBA
}

// Raster is an image drawn into a rectangle.
//
// A chart's marks are pixels on purpose (ADR-0004): a hundred thousand
// scatter points are drawn far faster as one image, and as an SVG of a
// hundred thousand circles they are a file nothing will open. So the marks
// arrive here already drawn, and the frame around them stays text.
type Raster struct {
	X, Y, W, H float64
	Img        *image.NRGBA
}

func (Box) shape()    {}
func (Line) shape()   {}
func (Text) shape()   {}
func (Dot) shape()    {}
func (Raster) shape() {}

// Scene is everything worth drawing, in the order it is drawn.
type Scene struct {
	Shapes []Shape
	// W and H are the size of what was drawn, which an exported file needs
	// and a screen already knows.
	W, H float64
	// Background is what the drawing sits on.
	Background color.NRGBA
}

// Add appends shapes, so that building a scene reads as a list of what is in
// it rather than as a list of appends.
func (s *Scene) Add(shapes ...Shape) { s.Shapes = append(s.Shapes, shapes...) }
