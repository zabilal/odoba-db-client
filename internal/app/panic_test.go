package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

func isPanic(err error) bool {
	var pe *panics.Error
	return errors.As(err, &pe)
}

// A driver that panics fails the call; the application goes on (NFR-R1).
func TestADriversPanicFailsTheCallNotTheApplication(t *testing.T) {
	src := &browseFake{n: 10}
	b, err := NewBrowseSource(context.Background(), src, orders, source.BrowseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	src.boom.Store(true)
	if _, err := b.Fetch(context.Background(), 0, 5); !isPanic(err) {
		t.Errorf("Fetch: %v; want the panic as an error", err)
	}
	if _, err := b.With(context.Background(), source.BrowseOptions{}); !isPanic(err) {
		t.Errorf("With: %v; want the panic as an error", err)
	}
}

// panicStream panics on its first row.
type panicStream struct{}

func (panicStream) Columns() []model.ColumnDef { return []model.ColumnDef{{Name: "x"}} }
func (panicStream) Next(context.Context) (model.Row, error) {
	panic("fake driver: the row reader fell over")
}
func (panicStream) Close() error { return nil }

func TestAResultWhoseRowsPanicEndsInAnError(t *testing.T) {
	rs := newResultSet(context.Background(), panicStream{}, 100)
	<-rs.Done()
	if !isPanic(rs.Err()) {
		t.Errorf("result ended with %v; want the panic as its error", rs.Err())
	}
}

// A ping that panics is a failed ping: the connection reads as down, and the
// application goes on.
func TestAPanickingPingIsAFailedPing(t *testing.T) {
	src := &fakeSource{}
	src.boomPing.Store(true)
	l := startLive("c1", src, MonitorConfig{Interval: time.Hour, MinBackoff: time.Hour, MaxBackoff: time.Hour, PingTimeout: time.Second})
	defer l.Close()
	seen := make(chan Status, 8)
	defer l.Subscribe(func(st Status) { seen <- st })()
	l.Check()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case st := <-seen:
			if st.State != StateDisconnected {
				continue
			}
			if !isPanic(st.Err) {
				t.Errorf("down with %v; want the panic as the reason", st.Err)
			}
			return
		case <-deadline:
			t.Fatal("a panicking ping never read as a failure")
		}
	}
}
