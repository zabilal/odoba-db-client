// Package chart implements data visualisation — spike W4 (T0.55–T0.58).
//
// Charts here exist to understand a result set, not to build dashboards
// (REQUIREMENTS NG-2). That shapes every decision: the priority is reading
// values accurately and quickly from data the user just queried, not
// presentation polish.
//
// Nothing in scale.go, downsample.go or hit.go imports Fyne, so the numeric
// core is testable and benchmarkable on its own.
package chart

import (
	"math"
	"time"
)

// Scale maps a data domain to a pixel range.
type Scale struct {
	// Min and Max bound the data domain.
	Min, Max float64

	// Pixels is the length of the axis in pixels.
	Pixels float64

	// Invert flips the direction, which the Y axis needs because screen
	// coordinates grow downward and values grow upward.
	Invert bool
}

// Project maps a data value to a pixel offset.
func (s Scale) Project(v float64) float64 {
	span := s.Max - s.Min
	if span == 0 {
		return s.Pixels / 2
	}
	t := (v - s.Min) / span
	if s.Invert {
		t = 1 - t
	}
	return t * s.Pixels
}

// Unproject maps a pixel offset back to a data value, which is what turns a
// cursor position into a value the tooltip can show.
func (s Scale) Unproject(px float64) float64 {
	if s.Pixels == 0 {
		return s.Min
	}
	t := px / s.Pixels
	if s.Invert {
		t = 1 - t
	}
	return s.Min + t*(s.Max-s.Min)
}

// NiceScale returns a scale whose bounds fall on round numbers.
//
// Axes that run from 3.7194 to 91.2 are readable only with effort. Rounding
// outward to human numbers is most of what makes a chart legible, and it is
// the part naive charting code skips.
func NiceScale(min, max, pixels float64, invert bool) Scale {
	if math.IsNaN(min) || math.IsNaN(max) || min > max {
		return Scale{Min: 0, Max: 1, Pixels: pixels, Invert: invert}
	}
	if min == max {
		// A constant series still needs a readable axis around its value.
		if min == 0 {
			return Scale{Min: -1, Max: 1, Pixels: pixels, Invert: invert}
		}
		pad := math.Abs(min) * 0.1
		return Scale{Min: min - pad, Max: min + pad, Pixels: pixels, Invert: invert}
	}

	step := niceStep((max - min) / 6)
	lo := math.Floor(min/step) * step
	hi := math.Ceil(max/step) * step
	return Scale{Min: lo, Max: hi, Pixels: pixels, Invert: invert}
}

// niceStep rounds a raw interval to 1, 2, 2.5 or 5 times a power of ten.
func niceStep(raw float64) float64 {
	if raw <= 0 {
		return 1
	}
	mag := math.Pow(10, math.Floor(math.Log10(raw)))
	switch n := raw / mag; {
	case n <= 1:
		return mag
	case n <= 2:
		return 2 * mag
	case n <= 2.5:
		return 2.5 * mag
	case n <= 5:
		return 5 * mag
	default:
		return 10 * mag
	}
}

// Tick is one labelled position on an axis.
type Tick struct {
	Value float64
	Pixel float64
	Label string
}

// Ticks generates axis ticks at nice intervals.
//
// target is how many are wanted; the actual count differs because tick
// positions are rounded to human numbers, and an axis labelled 0, 25, 50, 75,
// 100 is worth more than one with exactly the requested number of ticks.
func (s Scale) Ticks(target int, format func(float64) string) []Tick {
	if target < 2 {
		target = 2
	}
	step := niceStep((s.Max - s.Min) / float64(target))
	if step <= 0 {
		return nil
	}

	var out []Tick
	start := math.Ceil(s.Min/step) * step
	for v := start; v <= s.Max+step*1e-9; v += step {
		// Snap values that are within rounding error of zero, so an axis shows
		// "0" rather than "-2.7755575615628914e-17".
		if math.Abs(v) < step*1e-9 {
			v = 0
		}
		label := ""
		if format != nil {
			label = format(v)
		}
		out = append(out, Tick{Value: v, Pixel: s.Project(v), Label: label})
	}
	return out
}

// TimeScale maps timestamps to pixels, choosing a sensible unit.
type TimeScale struct {
	Min, Max time.Time
	Pixels   float64
}

// Project maps a time to a pixel offset.
func (t TimeScale) Project(v time.Time) float64 {
	span := t.Max.Sub(t.Min)
	if span == 0 {
		return t.Pixels / 2
	}
	return float64(v.Sub(t.Min)) / float64(span) * t.Pixels
}

// timeUnits are the intervals a time axis may snap to, coarsest last.
var timeUnits = []struct {
	d      time.Duration
	layout string
}{
	{time.Second, "15:04:05"},
	{15 * time.Second, "15:04:05"},
	{time.Minute, "15:04"},
	{15 * time.Minute, "15:04"},
	{time.Hour, "15:04"},
	{6 * time.Hour, "Jan 2 15:04"},
	{24 * time.Hour, "Jan 2"},
	{7 * 24 * time.Hour, "Jan 2"},
	{30 * 24 * time.Hour, "Jan 2006"},
	{365 * 24 * time.Hour, "2006"},
}

// Ticks generates time-axis ticks at a human interval.
func (t TimeScale) Ticks(target int) []Tick {
	span := t.Max.Sub(t.Min)
	if span <= 0 || target < 2 {
		return nil
	}
	want := span / time.Duration(target)

	unit := timeUnits[len(timeUnits)-1]
	for _, u := range timeUnits {
		if u.d >= want {
			unit = u
			break
		}
	}

	var out []Tick
	start := t.Min.Truncate(unit.d)
	if start.Before(t.Min) {
		start = start.Add(unit.d)
	}
	for v := start; !v.After(t.Max); v = v.Add(unit.d) {
		out = append(out, Tick{
			Value: float64(v.UnixNano()),
			Pixel: t.Project(v),
			Label: v.Format(unit.layout),
		})
	}
	return out
}
