package shell

import (
	"fmt"
	"strconv"
	"time"

	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/ui/spark"
	uitheme "github.com/ikigai-db/ikigai-db/internal/ui/theme"
)

// How busy a log is, beside the log (FR-13.17, T5.14).
//
// The cluster keeps no rate, so the rate is measured here while the topic is
// open: two readings of the totals and the time between them (app.Meter). That
// makes the picture a history of the window's own watching, which is why it
// starts empty and fills — and why the label says "measuring" rather than
// showing a nought that would read as a quiet topic.
//
// It goes in the bar above the records, beside the controls for reading them,
// because that is where somebody is when they wonder whether a topic is busy.
// One picture, of the records: two sparklines on a line that already holds three
// buttons would be a line nobody can read, and the bytes are a number beside it.

// rateKeeps is how many rates the picture is of: a minute of them, which is as
// far back as a line 96 pixels wide can be read anyway.
//
// A variable so that a test can fill the window without waiting a minute.
var rateKeeps = 60

// rateRedraw is how often the picture is drawn again, and rateEvery how often
// the totals behind it are read. The rates arrive about once a second, so the
// picture is drawn about that often: oftener would draw the same picture.
//
// Variables so that a test can fill the picture without waiting a minute for it.
var (
	rateRedraw = time.Second
	rateEvery  = time.Second
)

// measuring reports whether the tab in front is showing something whose
// throughput can be measured.
func (s *Shell) measuring(t *tab) bool {
	if t == nil || t.browse == nil {
		return false
	}
	// A topic, because that is what a meter measures: the contract takes a
	// topic's name, and a partition of one is part of the same log.
	if t.ref.Kind != model.KindTopic {
		return false
	}
	live, ok := s.d.WS.Get(t.connID)
	return ok && live.Source.Capabilities().Stream.Throughput
}

// measure starts measuring the tab's topic and puts the picture in the bar.
//
// A cluster that cannot answer for this topic is left alone: the picture is not
// there, the controls are, and an error about a sparkline would be an
// interruption about something nobody asked for.
func (b *streamBar) measure() {
	if b.meter != nil || !b.s.measuring(b.t) {
		return
	}
	live, ok := b.s.d.WS.Get(b.t.connID)
	if !ok {
		return
	}
	m, err := app.NewMeter(b.t.ctx, live.Source, b.t.ref.Name(), rateKeeps, rateEvery)
	if err != nil {
		return
	}
	b.meter, b.measured = m, -1
	pal := b.s.colours()
	b.spark = spark.New(spark.Spark{Line: pal.ControlAccent, Fill: pal.ControlAccentSubtle})
	b.rate = widget.NewLabel("measuring…")
	b.rate.Importance = widget.LowImportance
	b.box.Add(b.spark)
	b.box.Add(b.rate)
	b.box.Refresh()
	b.watchRates()
}

// watchRates draws the picture again as rates arrive, and not otherwise. It runs
// for as long as the tab does: the tab's own context is what ends it, because
// measuring is not something a person turns off.
func (b *streamBar) watchRates() {
	ctx := b.t.ctx
	go func() {
		tick := time.NewTicker(rateRedraw)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				b.s.d.Run(func() {
					if ctx.Err() != nil {
						return
					}
					b.drawRates()
				})
			}
		}
	}()
}

// drawRates puts what has been measured since the last time into the picture.
func (b *streamBar) drawRates() {
	if b.meter == nil || b.spark == nil {
		return
	}
	if n := b.meter.Changes(); n == b.measured {
		return // nothing new; a quiet topic costs nothing
	} else {
		b.measured = n
	}
	rates := b.meter.Rates()
	records := make([]float64, 0, len(rates))
	for _, r := range rates {
		records = append(records, r.Records)
	}
	s := b.spark.Spark()
	s.Values = records
	b.spark.SetSpark(s)
	b.rate.SetText(rateSaid(b.meter))
	b.rate.Refresh()
}

// rateSaid is the numbers beside the picture: what is being carried now, and
// what is not known.
func rateSaid(m *app.Meter) string {
	if err := m.Err(); err != nil {
		// The last reading failed. The picture is still what was measured, and
		// the number would be a claim about now.
		return "not measuring"
	}
	latest, ok := m.Latest()
	if !ok {
		return "measuring…"
	}
	said := perSecond(latest.Records) + "/s"
	if latest.Known() {
		said += " · " + bytesPerSecond(latest.Bytes)
	}
	return said
}

// perSecond is a count somebody can read at a glance: whole numbers while they
// are small, and thousands once they are not.
func perSecond(n float64) string {
	switch {
	case n < 0:
		return "0"
	case n < 1000:
		return strconv.FormatFloat(n, 'f', -1, 64)
	case n < 10_000:
		return strconv.FormatFloat(n/1000, 'f', 1, 64) + "k"
	}
	return strconv.FormatFloat(n/1000, 'f', 0, 64) + "k"
}

// bytesPerSecond is a rate of bytes, in the units the rest of the window uses
// for sizes.
func bytesPerSecond(n float64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%.0f B/s", n)
	}
	div, exp := float64(unit), 0
	for m := n / unit; m >= unit && exp < 5; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB/s", n/div, "KMGTPE"[exp])
}

// recolour repaints the picture for a new theme: the widget holds its colours
// rather than reading a theme when it draws, which is what lets the same picture
// be drawn and measured (ADR-0004).
func (b *streamBar) recolour(pal uitheme.Palette) {
	if b.spark == nil {
		return
	}
	s := b.spark.Spark()
	s.Line, s.Fill = pal.ControlAccent, pal.ControlAccentSubtle
	b.spark.SetSpark(s)
}

// close lets go of what the bar is holding, for a tab that is going.
//
// The tab's context ends the reading; it does not close what was being read or
// stop what was measuring it. Both may wait on the network, so neither is closed
// on the goroutine that draws the window.
func (b *streamBar) close() {
	if b.stop != nil {
		b.stop()
		b.stop = nil
	}
	if tail := b.tail; tail != nil {
		b.tail = nil
		go tail.Close()
	}
	if m := b.meter; m != nil {
		b.meter = nil
		go m.Close()
	}
}
