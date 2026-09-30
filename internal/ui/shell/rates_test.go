package shell

import (
	"context"
	"errors"
	"image/color"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/ui/spark"
)

// How busy a log is, beside the log (FR-13.17, T5.14).
//
// The readings the fake gives are stamped a second apart, so the rates here are
// exact: what is tested is what the window makes of them, not how fast this
// machine happens to run.

// sparkIn is the picture in a bar, or nil where there is none.
func sparkIn(b *streamBar) *spark.Widget {
	for _, o := range b.box.Objects {
		if w, ok := o.(*spark.Widget); ok {
			return w
		}
	}
	return nil
}

// inBar reports whether something is on the bar.
func inBar(b *streamBar, o fyne.CanvasObject) bool {
	for _, in := range b.box.Objects {
		if in == o {
			return true
		}
	}
	return false
}

// countSparks is how many pictures the bar holds.
func countSparks(b *streamBar) int {
	n := 0
	for _, o := range b.box.Objects {
		if _, ok := o.(*spark.Widget); ok {
			n++
		}
	}
	return n
}

// measuredTopic opens the fake's topic and waits for the picture to hold that
// many rates.
func measuredTopic(t *testing.T, host string, rates int) (*fixture, *tab, *streamBar, *recordSource) {
	t.Helper()
	was := rateKeeps
	rateKeeps = 4
	t.Cleanup(func() { rateKeeps = was })
	fx, tb, bar, src := openLog(t, host)
	if rates > 0 {
		pump(t, fx.q, func() bool { return bar.meter != nil && len(bar.meter.Rates()) >= rates })
	}
	return fx, tb, bar, src
}

// A log that says how much it is carrying gets a picture of it, drawn from the
// rates the window has measured while it has been open.
func TestABusyLogGetsAPictureOfHowBusyItIs(t *testing.T) {
	was := meterFast(t)
	defer was()
	fx, _, bar, src := measuredTopic(t, "kafka1", 3)

	if src.read() < 2 {
		t.Errorf("it took %d readings of the totals", src.read())
	}
	pic := sparkIn(bar)
	if pic == nil {
		t.Fatal("a log that can be measured has no picture of it")
	}
	// The numbers are beside it, in the bar rather than only in a field.
	if !inBar(bar, bar.rate) {
		t.Error("the numbers are not in the bar")
	}
	// Asked for again — which happens whenever the rows are read again — there
	// is still one picture and one meter.
	measuring := bar.meter
	fx.s.showStreamBar(bar.t)
	fx.s.showStreamBar(bar.t)
	if bar.meter != measuring {
		t.Error("it started measuring again")
	}
	if n := countSparks(bar); n != 1 {
		t.Errorf("the bar holds %d pictures", n)
	}
	// Drawn when it is asked to be, which is what the timer does.
	bar.drawRates()
	values := pic.Spark().Values
	if len(values) < 3 {
		t.Fatalf("the picture is of %v", values)
	}
	// A hundred records a second, whatever this machine's pace: the readings
	// are stamped a second apart.
	for i, v := range values {
		if v != 100 {
			t.Errorf("rate %d is %v records a second", i, v)
		}
	}
	// And the numbers beside it say both of what there is to say.
	if got := bar.rate.Text; got != "100/s · 2.0 KB/s" {
		t.Errorf("it says %q", got)
	}
	// The picture holds only the last few: a topic somebody left open all day
	// cannot grow all day (NFR-P10).
	pump(t, fx.q, func() bool { return bar.meter.Changes() > int64(rateKeeps)+2 })
	bar.drawRates()
	if got := len(bar.spark.Spark().Values); got != rateKeeps {
		t.Errorf("the picture is of %d rates, and it keeps %d", got, rateKeeps)
	}
}

// meterFast makes the measuring quick enough for a test, and puts it back.
func meterFast(t *testing.T) func() {
	t.Helper()
	wasRedraw, wasEvery := rateRedraw, rateEvery
	rateRedraw, rateEvery = time.Millisecond, time.Millisecond
	return func() { rateRedraw, rateEvery = wasRedraw, wasEvery }
}

// A quiet topic is not drawn again: a window that redrew a sparkline nobody's
// records had changed would be a window warming a room.
func TestAQuietTopicsPictureIsNotDrawnAgain(t *testing.T) {
	was := meterFast(t)
	defer was()
	_, _, bar, _ := measuredTopic(t, "kafka1", 2)
	// The measuring is stopped before anything is asserted, because a rate
	// arriving between two draws would make a redraw the right answer. Then one
	// draw to settle what is on the screen, and what is on it is marked so that
	// drawing over it would show.
	bar.meter.Close()
	bar.drawRates()
	held := bar.measured
	before := bar.spark.Spark().Values

	bar.rate.SetText("left alone")
	bar.drawRates()
	if bar.rate.Text != "left alone" {
		t.Errorf("it drew again and said %q", bar.rate.Text)
	}
	if bar.measured != held {
		t.Errorf("it drew again: %d rates, was %d", bar.measured, held)
	}
	if got := bar.spark.Spark().Values; len(got) != len(before) {
		t.Errorf("the picture is of %v, was %v", got, before)
	}
}

// A cluster that cannot say how much a topic carries gets no picture, and no
// error about a picture nobody asked for.
func TestATopicThatCannotBeMeasuredGetsNoPicture(t *testing.T) {
	fx, tb, bar, _ := openLog(t, "norate")
	if fx.s.measuring(tb) {
		t.Error("a cluster that keeps no totals is measured")
	}
	if sparkIn(bar) != nil {
		t.Error("there is a picture of what cannot be measured")
	}
	if bar.meter != nil {
		t.Error("it is measuring anyway")
	}
	// The controls are still there: not being able to measure a log is not a
	// reason to stop offering to read it.
	within := func(o fyne.CanvasObject) bool {
		for _, in := range bar.box.Objects {
			if in == o {
				return true
			}
		}
		return false
	}
	if !within(bar.follow) {
		t.Error("a log that cannot be measured cannot be followed either")
	}
}

// Only a topic is measured. A partition of one is part of the same log, and a
// meter is asked about topics by name.
func TestOnlyATopicIsMeasured(t *testing.T) {
	fx, tb, _, _ := openLog(t, "kafka1")
	if !fx.s.measuring(tb) {
		t.Fatal("a topic is not measured")
	}
	// Nothing at all, and a tab whose rows have not arrived: neither has a
	// topic to measure.
	if fx.s.measuring(nil) {
		t.Error("nothing is measured")
	}
	if fx.s.measuring(&tab{ref: topicRef, connID: tb.connID}) {
		t.Error("a tab with no rows yet is measured")
	}
	was := tb.ref
	t.Cleanup(func() { tb.ref = was })
	for _, ref := range []model.ObjectRef{partitionRef, groupRef, model.NewRef(model.KindCluster, "cluster")} {
		tb.ref = ref
		if fx.s.measuring(tb) {
			t.Errorf("%s is measured", ref)
		}
	}
}

// Closing the tab lets go of the tail and the meter. The tab's context ends the
// reading; it does not close the stream that was being read, and a window that
// left one open for every closed tab would hold a consumer per tab.
func TestClosingATabLetsGoOfWhatTheBarHeld(t *testing.T) {
	was := meterFast(t)
	defer was()
	fx, tb, bar, src := measuredTopic(t, "kafka1", 2)
	bar.toggleFollow()
	if !bar.following() {
		t.Fatal("it is not following")
	}
	fx.s.closeTab(tb.item)

	pump(t, fx.q, func() bool { return src.closedFollows() == 1 })
	if bar.tail != nil {
		t.Error("the bar is still holding a tail")
	}
	// And the measuring stops: the readings stop arriving.
	pump(t, fx.q, func() bool { return bar.meter == nil })
	took := src.read()
	time.Sleep(20 * time.Millisecond)
	if after := src.read(); after > took+1 {
		t.Errorf("a closed tab took %d more readings", after-took)
	}
}

// What the numbers say, at the sizes they say it in.
func TestWhatTheNumbersBesideThePictureSay(t *testing.T) {
	for _, c := range []struct {
		n    float64
		want string
	}{
		{0, "0"}, {1, "1"}, {12.5, "12.5"}, {999, "999"},
		{1000, "1.0k"}, {1500, "1.5k"}, {9999, "10.0k"},
		{10_000, "10k"}, {1_234_567, "1235k"},
		{-1, "0"},
	} {
		if got := perSecond(c.n); got != c.want {
			t.Errorf("%v a second reads as %q, want %q", c.n, got, c.want)
		}
	}
	for _, c := range []struct {
		n    float64
		want string
	}{
		{0, "0 B/s"}, {512, "512 B/s"}, {1024, "1.0 KB/s"},
		{1536, "1.5 KB/s"}, {1 << 20, "1.0 MB/s"}, {1 << 30, "1.0 GB/s"},
		{1 << 50, "1.0 PB/s"}, {1 << 60, "1.0 EB/s"},
		// Past the largest unit there is a name for, the number goes on rather
		// than the units: a rate nothing can reach should not reach past the
		// end of the letters either.
		{1e30, "867361737988.4 EB/s"},
	} {
		if got := bytesPerSecond(c.n); got != c.want {
			t.Errorf("%v bytes a second reads as %q, want %q", c.n, got, c.want)
		}
	}
}

// Before there is a rate the label says so, and a reading that failed is said
// rather than drawn as a rate of nothing, which would read as a quiet topic.
func TestWhatIsSaidBeforeAndAfterARate(t *testing.T) {
	fx, _, _, src := openLog(t, "kafka1")
	live, ok := fx.s.d.WS.Get(fx.s.activeTab().connID)
	if !ok {
		t.Fatal("the connection is not open")
	}
	// An interval no test will wait out: there will be no second reading, so
	// there will be no rate.
	quiet, err := app.NewMeter(context.Background(), live.Source, "events", 4, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { quiet.Close() })
	if got := rateSaid(quiet); got != "measuring…" {
		t.Errorf("before a rate it says %q", got)
	}

	// One that measured and then could not: the picture stays as it was, and the
	// number would be a claim about now.
	m, err := app.NewMeter(context.Background(), live.Source, "events", 4, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	pump(t, fx.q, func() bool { return len(m.Rates()) > 0 })
	if got := rateSaid(m); !strings.Contains(got, "/s") {
		t.Errorf("with a rate it says %q", got)
	}
	src.breakRates(errors.New("the broker went away"))
	pump(t, fx.q, func() bool { return m.Err() != nil })
	if got := rateSaid(m); got != "not measuring" {
		t.Errorf("after a reading failed it says %q", got)
	}
}

// The picture is repainted when the theme changes, because the widget holds the
// colours it was drawn with rather than reading a theme as it draws.
func TestThePictureFollowsTheTheme(t *testing.T) {
	was := meterFast(t)
	defer was()
	fx, _, bar, _ := measuredTopic(t, "kafka1", 2)
	pic := sparkIn(bar)
	if pic == nil {
		t.Fatal("there is no picture")
	}
	// Drawn in some other colour, as a picture made under another theme is, and
	// then repainted.
	wrong := pic.Spark()
	wrong.Line, wrong.Fill = color.NRGBA{R: 1, A: 255}, color.NRGBA{G: 1, A: 255}
	pic.SetSpark(wrong)
	bar.recolour(fx.s.colours())
	if pic.Spark().Line != fx.s.colours().ControlAccent {
		t.Errorf("the line is %v", pic.Spark().Line)
	}
	if pic.Spark().Fill != fx.s.colours().ControlAccentSubtle {
		t.Errorf("the fill is %v", pic.Spark().Fill)
	}
	// A bar with no picture is asked to repaint and does nothing about it.
	_, _, bare, _ := openLog(t, "norate")
	bare.recolour(fx.s.colours())
	if sparkIn(bare) != nil {
		t.Error("repainting made a picture")
	}
}

// And the label is a quiet one: a number beside a picture is not the thing on
// the line that should catch an eye.
func TestTheNumbersAreQuiet(t *testing.T) {
	was := meterFast(t)
	defer was()
	_, _, bar, _ := measuredTopic(t, "kafka1", 1)
	if bar.rate.Importance != widget.LowImportance {
		t.Errorf("the numbers are drawn with importance %v", bar.rate.Importance)
	}
}

// A cluster that says how many records and not how many bytes gets a number for
// the one it says: a nought would be a claim, and a negative one a puzzle.
func TestTheBytesAreNotSaidWhereTheyAreNotKnown(t *testing.T) {
	was := meterFast(t)
	defer was()
	fx, _, bar, _ := openLog(t, "nosize")
	pump(t, fx.q, func() bool { return bar.meter != nil && len(bar.meter.Rates()) > 0 })
	bar.drawRates()
	if got := bar.rate.Text; got != "100/s" {
		t.Errorf("it says %q", got)
	}
	// The picture is still of the records, which are known.
	if got := bar.spark.Spark().Values; len(got) == 0 || got[0] != 100 {
		t.Errorf("the picture is of %v", got)
	}
}
