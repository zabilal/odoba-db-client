package chart

import (
	"image"
	"image/color"
)

// Rasterised data layer.
//
// Used where the number of marks outgrows what individual canvas objects can
// carry — see ADR-0004. Draws directly into an NRGBA buffer that the renderer
// hands to Fyne as a single canvas.Image.
//
// Marks are alpha-blended rather than painted opaque. At 100 000 points, most
// of them overlap, and blending turns that overlap into visible density: the
// dense core of a cluster reads darker than its fringe. Opaque marks would
// draw every cluster as the same flat blob and hide the distribution, which is
// the thing a scatter plot exists to show.

// NewCanvas allocates a transparent raster of the given size.
func NewCanvas(w, h int) *image.NRGBA {
	return image.NewNRGBA(image.Rect(0, 0, w, h))
}

// blend composites c over the pixel at (x,y), source-over.
func blend(img *image.NRGBA, x, y int, c color.NRGBA) {
	if x < 0 || y < 0 || x >= img.Rect.Max.X || y >= img.Rect.Max.Y {
		return
	}
	i := img.PixOffset(x, y)
	sa := uint32(c.A)
	if sa == 0 {
		return
	}
	da := uint32(img.Pix[i+3])
	ia := 255 - sa

	img.Pix[i+0] = uint8((uint32(c.R)*sa + uint32(img.Pix[i+0])*ia) / 255)
	img.Pix[i+1] = uint8((uint32(c.G)*sa + uint32(img.Pix[i+1])*ia) / 255)
	img.Pix[i+2] = uint8((uint32(c.B)*sa + uint32(img.Pix[i+2])*ia) / 255)
	img.Pix[i+3] = uint8(sa + da*ia/255)
}

// RasterScatter draws each point as a small disc.
func RasterScatter(img *image.NRGBA, pts []Point, xs, ys Scale, c color.NRGBA, radius int) {
	r2 := radius * radius
	for _, p := range pts {
		cx := int(xs.Project(p.X) + 0.5)
		cy := int(ys.Project(p.Y) + 0.5)
		for dy := -radius; dy <= radius; dy++ {
			for dx := -radius; dx <= radius; dx++ {
				if dx*dx+dy*dy <= r2 {
					blend(img, cx+dx, cy+dy, c)
				}
			}
		}
	}
}

// RasterLine draws a polyline with Bresenham's algorithm.
func RasterLine(img *image.NRGBA, pts []Point, xs, ys Scale, c color.NRGBA) {
	for i := 1; i < len(pts); i++ {
		x0 := int(xs.Project(pts[i-1].X) + 0.5)
		y0 := int(ys.Project(pts[i-1].Y) + 0.5)
		x1 := int(xs.Project(pts[i].X) + 0.5)
		y1 := int(ys.Project(pts[i].Y) + 0.5)
		bresenham(img, x0, y0, x1, y1, c)
	}
}

func bresenham(img *image.NRGBA, x0, y0, x1, y1 int, c color.NRGBA) {
	dx := abs(x1 - x0)
	dy := -abs(y1 - y0)
	sx, sy := 1, 1
	if x0 > x1 {
		sx = -1
	}
	if y0 > y1 {
		sy = -1
	}
	err := dx + dy
	for {
		blend(img, x0, y0, c)
		if x0 == x1 && y0 == y1 {
			return
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			x0 += sx
		}
		if e2 <= dx {
			err += dx
			y0 += sy
		}
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
