package scene

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image/color"
	"image/png"
	"io"
	"strings"
)

// Writing a scene as text.
//
// Text is written as text rather than as outlines, so a name in an exported
// picture can be searched for and copied, and the file stays small enough to
// put in a document. A raster is the exception and is embedded, because it
// is pixels by the time it gets here (ADR-0004).

// WriteSVG writes a scene as an SVG document.
func WriteSVG(out io.Writer, s Scene) error {
	var b strings.Builder
	fmt.Fprint(&b, `<?xml version="1.0" encoding="UTF-8"?>`+"\n")
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" `+
		`width="%.0f" height="%.0f" viewBox="0 0 %.0f %.0f">`+"\n", s.W, s.H, s.W, s.H)
	fmt.Fprintf(&b, `  <rect width="100%%" height="100%%" fill="%s"/>`+"\n", Hex(s.Background))

	for _, sh := range s.Shapes {
		switch v := sh.(type) {
		case Box:
			fmt.Fprintf(&b, `  <rect x="%.2f" y="%.2f" width="%.2f" height="%.2f" rx="%.2f" `+
				`%s %s stroke-width="%.2f"/>`+"\n",
				v.X, v.Y, v.W, v.H, v.Radius, paint("fill", v.Fill), paint("stroke", v.Stroke), v.StrokeWidth)
		case Line:
			fmt.Fprintf(&b, `  <line x1="%.2f" y1="%.2f" x2="%.2f" y2="%.2f" `+
				`%s stroke-width="%.2f"/>`+"\n",
				v.X1, v.Y1, v.X2, v.Y2, paint("stroke", v.Stroke), v.Width)
		case Dot:
			fmt.Fprintf(&b, `  <circle cx="%.2f" cy="%.2f" r="%.2f" %s/>`+"\n",
				v.X, v.Y, v.R, paint("fill", v.Fill))
		case Raster:
			data, err := dataURI(v)
			if err != nil {
				return err
			}
			fmt.Fprintf(&b, `  <image x="%.2f" y="%.2f" width="%.2f" height="%.2f" `+
				`xlink:href="%s"/>`+"\n", v.X, v.Y, v.W, v.H, data)
		case Text:
			anchor := ""
			switch v.Align {
			case Center:
				anchor = ` text-anchor="middle"`
			case Trailing:
				// The Fyne renderer measures and moves; SVG is told to end
				// here instead, which needs no font metrics at all.
				anchor = ` text-anchor="end"`
			}
			weight := ""
			if v.Bold {
				weight = ` font-weight="bold"`
			}
			fmt.Fprintf(&b, `  <text x="%.2f" y="%.2f" font-size="%.2f" %s%s%s`+
				` font-family="sans-serif">%s</text>`+"\n",
				v.X, baseline(v), v.Size, paint("fill", v.Fill), anchor, weight, Escape(v.S))
		}
	}
	b.WriteString("</svg>\n")
	_, err := io.WriteString(out, b.String())
	return err
}

// baseline turns a scene's Y into SVG's.
//
// A scene's Y is the top of a line of text, or its middle; SVG's is the
// baseline. The arithmetic rather than dominant-baseline, because that
// attribute is not honoured everywhere an SVG is opened, and a tick label
// half a line out of place would be worse than one placed by hand.
func baseline(v Text) float64 {
	if v.Middle {
		// Roughly half a capital above the baseline, which puts the visible
		// body of the text on the line rather than the line's own box.
		return v.Y + v.Size*0.36
	}
	return v.Y + v.Size
}

// dataURI is a raster written into the document, so that an exported file is
// one file.
func dataURI(v Raster) (string, error) {
	if v.Img == nil {
		return "", fmt.Errorf("scene: a raster with no image")
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, v.Img); err != nil {
		return "", fmt.Errorf("scene: could not write the marks: %w", err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// paint writes a colour as SVG's fill or stroke.
//
// Transparency goes in its own attribute rather than into an eight-digit
// hex, which is SVG 2 and not read by everything an exported file might be
// opened in. A gridline that came out solid black in one viewer would be a
// different picture, which is the one thing an export must not be.
func paint(attr string, c color.NRGBA) string {
	if c.A == 0xff {
		return fmt.Sprintf(`%s="%s"`, attr, Hex(c))
	}
	return fmt.Sprintf(`%s="%s" %s-opacity="%.3f"`, attr, Hex(c), attr, float64(c.A)/255)
}

// Hex writes a colour as SVG's six digits, without its alpha.
func Hex(c color.NRGBA) string {
	return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
}

// Escape writes a label so that a table called <b> is a table called <b>.
func Escape(s string) string {
	return strings.NewReplacer(
		"&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;",
	).Replace(s)
}
