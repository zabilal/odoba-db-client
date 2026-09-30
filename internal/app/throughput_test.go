package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Measuring how busy a log is (FR-13.17).
//
// The readings are stamped by the fake rather than by the clock, so the rates
// are exact: what is being tested is what a pair of readings makes of itself,
// not how fast this machine happens to run.

// counter is a topic whose totals grow by the same amount every reading, each
// reading stamped a second after the last.
type counter struct {
	source.Source

	mu      sync.Mutex
	at      time.Time
	records int64
	bytes   int64
	step    int64
	sized   bool
	reads   int
	// fails is what every reading says instead of answering, until it is
	// mended. One failed reading would be a moment too narrow for a test to
	// see, and what is being tested is that a meter lives through one.
	fails error
}

func (c *counter) TopicTotals(_ context.Context, topic string) (model.TopicTotals, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reads++
	if c.fails != nil {
		return model.TopicTotals{}, c.fails
	}
	c.at = c.at.Add(time.Second)
	c.records += c.step
	c.bytes += c.step * 10
	return model.TopicTotals{At: c.at, Records: c.records, Bytes: c.bytes, Sized: c.sized}, nil
}

func (c *counter) taken() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reads
}

func (c *counter) breaks(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fails = err
}

func (c *counter) mend() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fails = nil
}

// measured starts a meter over a counter, measuring as fast as the test can.
func measured(t *testing.T, keeps int, step int64) (*Meter, *counter) {
	t.Helper()
	c := &counter{at: time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC), step: step, sized: true}
	m, err := NewMeter(context.Background(), c, "events", keeps, time.Millisecond)
	if err != nil {
		t.Fatalf("measuring: %v", err)
	}
	t.Cleanup(func() { m.Close() })
	return m, c
}

// waitRates gives a meter a moment to measure that many.
func waitRates(t *testing.T, m *Meter, n int) []model.Rate {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		held := m.Rates()
		if len(held) >= n {
			return held
		}
		if time.Now().After(deadline) {
			t.Fatalf("a meter holds %d rates, waiting for %d: %v", len(held), n, m.Err())
		}
		time.Sleep(time.Millisecond)
	}
}

// A rate is what two readings and the time between them say, and the first
// reading is taken when the measuring starts so that the wait for a rate is one
// interval rather than two.
func TestAMeterMeasuresWhatTwoReadingsSay(t *testing.T) {
	m, c := measured(t, 0, 100)
	if c.taken() != 1 {
		t.Errorf("starting took %d readings", c.taken())
	}
	rates := waitRates(t, m, 3)
	for i, r := range rates[:3] {
		// A hundred records and a thousand bytes a second, whatever this
		// machine's own pace: the readings are a second apart.
		if r.Records != 100 {
			t.Errorf("rate %d is %v records a second", i, r.Records)
		}
		if !r.Known() || r.Bytes != 1000 {
			t.Errorf("rate %d is %v bytes a second", i, r.Bytes)
		}
	}
	// The newest of them is what a window shows beside the sparkline.
	latest, ok := m.Latest()
	if !ok {
		t.Fatal("a meter with rates has no latest one")
	}
	if held := m.Rates(); latest != held[len(held)-1] {
		t.Errorf("the latest rate is %+v and the last held is %+v", latest, held[len(held)-1])
	}
	if m.Changes() < 3 {
		t.Errorf("it counted %d rates", m.Changes())
	}
}

// Before the second reading there is no rate, and saying so is not the same as
// saying the topic is quiet.
func TestAMeterHasNothingToSayAtFirst(t *testing.T) {
	c := &counter{at: time.Now(), step: 1, sized: true}
	// An interval no test will wait out: there will be no second reading.
	m, err := NewMeter(context.Background(), c, "events", 0, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	if rates := m.Rates(); len(rates) != 0 {
		t.Errorf("it measured %v before it had two readings", rates)
	}
	if _, ok := m.Latest(); ok {
		t.Error("it has a latest rate before it has any")
	}
	if m.Changes() != 0 {
		t.Errorf("it counted %d rates", m.Changes())
	}
}

// A meter holds the last few and lets the rest go: a topic somebody left open
// all day cannot grow all day (NFR-P10).
func TestAMeterKeepsTheLastFewRates(t *testing.T) {
	m, _ := measured(t, 3, 100)
	waitRates(t, m, 3)
	// Enough more that the window must have turned over.
	deadline := time.Now().Add(5 * time.Second)
	for m.Changes() < 8 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	held := m.Rates()
	if len(held) != 3 {
		t.Fatalf("it holds %d rates of three", len(held))
	}
	// And they are the newest three, in the order they were measured.
	for i := 1; i < len(held); i++ {
		if !held[i].At.After(held[i-1].At) {
			t.Errorf("rate %d is stamped %v, after %v", i, held[i].At, held[i-1].At)
		}
	}
	if m.Changes() < 8 {
		t.Errorf("it only measured %d rates", m.Changes())
	}
}

// A reading that failed is said and not the end of it: a broker that was busy is
// not a broker that has gone, and the next reading says which.
func TestAMeterSaysWhatWentWrongAndKeepsMeasuring(t *testing.T) {
	m, c := measured(t, 0, 100)
	waitRates(t, m, 1)
	boom := errors.New("the broker was busy")
	c.breaks(boom)
	until := func(what string, ok func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for !ok() {
			if time.Now().After(deadline) {
				t.Fatalf("%s: it says %v after %d rates", what, m.Err(), m.Changes())
			}
			time.Sleep(time.Millisecond)
		}
	}
	until("a failed reading is not said", func() bool { return errors.Is(m.Err(), boom) })

	// And it goes on. The failure clears when a reading works again, and the
	// rates begin arriving: a meter that gave up on one busy moment would
	// leave a window with a sparkline that stopped for no stated reason.
	was := m.Changes()
	c.mend()
	until("it did not read again", func() bool { return m.Changes() > was })
	if m.Err() != nil {
		t.Errorf("a meter that read again still says %v", m.Err())
	}
}

// A source that cannot say how much a topic carries is not measured, and the
// first reading failing is a meter that never starts: a window showing an empty
// sparkline for a cluster that will not answer would be showing a quiet topic.
func TestAMeterNeedsASourceThatCanMeasure(t *testing.T) {
	if _, err := NewMeter(context.Background(), &feedSource{}, "events", 0, time.Millisecond); !errors.Is(err, errNoMeter) {
		t.Errorf("a source that cannot measure was measured: %v", err)
	}
	boom := errors.New("no such topic")
	c := &counter{at: time.Now(), step: 1}
	c.breaks(boom)
	if _, err := NewMeter(context.Background(), c, "events", 0, time.Millisecond); !errors.Is(err, boom) {
		t.Errorf("a first reading that failed reads as %v", err)
	}
}

// Closing stops the measuring, and twice is as safe as once.
func TestAMeterIsClosedOnce(t *testing.T) {
	m, c := measured(t, 0, 100)
	waitRates(t, m, 2)
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	// Being closed is not a fault in the cluster, so it is not reported as one.
	if err := m.Err(); err != nil {
		t.Errorf("a closed meter says %v", err)
	}
	took := c.taken()
	if err := m.Close(); err != nil {
		t.Errorf("closing twice: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if after := c.taken(); after != took {
		t.Errorf("a closed meter took %d more readings", after-took)
	}
}

// The interval and the window are the caller's to choose: the window shows what
// the caller has room for, and the interval is what the caller thinks a rate is
// over. Asking for neither takes this layer's own answers.
func TestAMeterTakesTheIntervalItWasGiven(t *testing.T) {
	c := &counter{at: time.Now(), step: 1, sized: true}
	// An interval no test will wait out: if it were ignored, rates would arrive.
	m, err := NewMeter(context.Background(), c, "events", 2, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	time.Sleep(20 * time.Millisecond)
	if rates := m.Rates(); len(rates) != 0 {
		t.Errorf("an hour apart, it measured %v in twenty milliseconds", rates)
	}
	// And an interval a test can wait out gives rates inside it: a meter that
	// read on its own schedule rather than the one it was given would take a
	// second for each of these.
	quickly := &counter{at: time.Now(), step: 1, sized: true}
	fast, err := NewMeter(context.Background(), quickly, "events", 8, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fast.Close() })
	deadline := time.Now().Add(500 * time.Millisecond)
	for len(fast.Rates()) < 3 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := len(fast.Rates()); got < 3 {
		t.Errorf("a millisecond apart, it measured %d rates in half a second", got)
	}

	// And nothing for either is this layer's own answer, which is a window of a
	// minute read once a second.
	quick := &counter{at: time.Now(), step: 1, sized: true}
	d, err := NewMeter(context.Background(), quick, "events", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if len(d.rates) != meterKeeps {
		t.Errorf("it holds room for %d rates, and its own answer is %d", len(d.rates), meterKeeps)
	}
}

// held is a topic whose second reading waits to be let go, so that a test can
// close a meter while one is in flight. The first answers at once, because a
// meter that could not take it would never start.
type held struct {
	source.Source
	first   sync.Once
	said    sync.Once
	blocked chan struct{} // closed once a reading is waiting
	wait    chan struct{} // closed to let it go
}

func (h *held) TopicTotals(ctx context.Context, _ string) (model.TopicTotals, error) {
	answered := false
	h.first.Do(func() { answered = true })
	if answered {
		return model.TopicTotals{At: time.Now(), Records: 1}, nil
	}
	h.said.Do(func() { close(h.blocked) })
	<-h.wait
	if err := ctx.Err(); err != nil {
		return model.TopicTotals{}, err
	}
	return model.TopicTotals{At: time.Now(), Records: 2}, nil
}

// Closing a meter waits for the reading in flight, and being closed is not
// reported as a fault in the cluster: a window that said "not measuring" about
// a topic whose tab had just been closed would be saying it about nothing.
func TestClosingWaitsForTheReadingInFlight(t *testing.T) {
	h := &held{wait: make(chan struct{}), blocked: make(chan struct{})}
	m, err := NewMeter(context.Background(), h, "events", 4, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-h.blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("no reading was ever in flight")
	}
	closed := make(chan struct{})
	go func() { m.Close(); close(closed) }()
	select {
	case <-closed:
		t.Fatal("closing did not wait for the reading in flight")
	case <-time.After(50 * time.Millisecond):
	}
	// Let it go: the reading comes back given up on, and closing finishes.
	close(h.wait)
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("closing never finished")
	}
	if err := m.Err(); err != nil {
		t.Errorf("a meter closed mid-reading says %v", err)
	}
}
