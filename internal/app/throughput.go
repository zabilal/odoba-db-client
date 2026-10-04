package app

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Measuring how busy a log is (FR-13.17).
//
// The cluster keeps no rate, so a rate is two readings and the time between
// them. That makes a measurement a thing with a history: a window that has just
// opened knows nothing yet, and one that has been open a minute knows the last
// minute. This holds that history, in a window of fixed size for the reason
// app.Tail's is (NFR-P10) — a topic nobody is watching cannot cost anything, and
// one somebody has left open all day cannot grow all day.

// meterKeeps is how many rates a meter holds when nobody says otherwise: a
// minute of them at one a second, which is as far back as a sparkline drawn
// beside a grid can be read anyway.
const meterKeeps = 60

// meterEvery is how often the totals are read when the caller says nothing. A
// rate over less than a second is mostly the round trip, and a rate over more
// than a few is not a rate somebody watching a log would recognise.
const meterEvery = time.Second

// Meter is how much a topic is carrying, measured while somebody watches.
type Meter struct {
	src   source.TopicMeter
	topic string

	mu    sync.Mutex
	last  model.TopicTotals
	rates []model.Rate
	first int // where the oldest rate is
	n     int
	err   error
	// arrived counts every rate measured, so that a window can tell whether
	// there is anything new to draw without comparing what it holds.
	arrived int64
	closed  bool

	cancel context.CancelFunc
	done   chan struct{}
}

// NewMeter starts measuring a topic. keeps is how many rates it holds and every
// is how often it reads; nothing for either is this layer's own answer.
//
// Nothing is measured before the second reading, which is what a rate is: the
// first reading is taken at once so that the wait for a rate is one interval
// rather than two.
func NewMeter(ctx context.Context, src source.Source, topic string, keeps int, every time.Duration) (_ *Meter, err error) {
	defer panics.Recover(&err, "measuring a topic")

	m, ok := src.(source.TopicMeter)
	if !ok {
		return nil, errNoMeter
	}
	if keeps <= 0 {
		keeps = meterKeeps
	}
	if every <= 0 {
		every = meterEvery
	}
	ctx, cancel := context.WithCancel(ctx)
	out := &Meter{src: m, topic: topic, rates: make([]model.Rate, keeps),
		cancel: cancel, done: make(chan struct{})}
	first, err := m.TopicTotals(ctx, topic)
	if err != nil {
		cancel()
		return nil, err
	}
	out.last = first
	go out.measure(ctx, every)
	return out, nil
}

// measure reads the totals on a timer and keeps what each pair says.
func (m *Meter) measure(ctx context.Context, every time.Duration) {
	defer close(m.done)
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			totals, err := m.read(ctx, m.src, m.topic)
			if err != nil {
				// Not the end of the measuring: a broker that was busy is not
				// a broker that has gone, and the next reading says which. A
				// read given up on ends at the Done above instead.
				m.failed(err)
				continue
			}
			m.keep(totals)
		}
	}
}

// read is the driver call, with a driver's panic turned into an error
// (ADR-0017).
func (m *Meter) read(ctx context.Context, src source.TopicMeter, topic string) (_ model.TopicTotals, err error) {
	defer panics.Recover(&err, "reading how much a topic carries")
	return src.TopicTotals(ctx, topic)
}

// keep works out the rate since the last reading and holds it.
func (m *Meter) keep(totals model.TopicTotals) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rate, ok := model.RateBetween(m.last, totals)
	m.last = totals
	if !ok {
		// Two readings at the same moment, or a clock that went backwards:
		// there is no interval to divide by, so there is no rate.
		return
	}
	m.err = nil
	m.arrived++
	if m.n == len(m.rates) {
		m.rates[m.first] = rate
		m.first = (m.first + 1) % len(m.rates)
		return
	}
	m.rates[(m.first+m.n)%len(m.rates)] = rate
	m.n++
}

// failed remembers what went wrong, unless it is this meter being closed.
func (m *Meter) failed(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.closed && !errors.Is(err, context.Canceled) {
		m.err = err
	}
}

// Rates are the rates measured so far, oldest first. It is a copy: what is held
// changes under the reader, which is what measuring means.
func (m *Meter) Rates() []model.Rate {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]model.Rate, 0, m.n)
	for i := 0; i < m.n; i++ {
		out = append(out, m.rates[(m.first+i)%len(m.rates)])
	}
	return out
}

// Latest is the rate last measured, and false before there is one.
func (m *Meter) Latest() (model.Rate, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.n == 0 {
		return model.Rate{}, false
	}
	return m.rates[(m.first+m.n-1)%len(m.rates)], true
}

// Changes is how many rates have been measured, whether or not they are still
// held: what tells a window whether there is anything new to draw.
func (m *Meter) Changes() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.arrived
}

// Err is what went wrong with the measuring, or nothing. A reading that failed
// once does not end the measuring: a broker that was busy is not a broker that
// has gone, and the next reading says which.
func (m *Meter) Err() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.err
}

// Close stops measuring. It is safe to call more than once: giving up on a
// context twice is nothing, and the goroutine's end is a closed channel.
//
// closed is what a reading that failed on the way out is read against, so that
// being closed is not reported as a fault in the cluster.
func (m *Meter) Close() error {
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()

	m.cancel()
	<-m.done
	return nil
}

var errNoMeter = errors.New("this source cannot say how much a topic carries")
