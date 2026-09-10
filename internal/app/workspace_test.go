package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/store"
)

func workspace(t *testing.T, host string) (*Workspace, string) {
	t.Helper()
	f := setup(t)
	d := draft("W")
	d.Host = host
	conn, err := f.c.Create(d, nil)
	if err != nil {
		t.Fatal(err)
	}
	w := NewWorkspace(f.c, fast)
	t.Cleanup(func() { w.CloseAll() })
	return w, conn.ID
}

func TestConcurrentConnectsShareOneDial(t *testing.T) {
	w, id := workspace(t, "slowish")
	before := fakeOpens.Load()

	var wg sync.WaitGroup
	lives := make([]*Live, 20)
	for i := range lives {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			l, err := w.Connect(context.Background(), id)
			if err != nil {
				t.Error(err)
			}
			lives[i] = l
		}(i)
	}
	wg.Wait()
	if n := fakeOpens.Load() - before; n != 1 {
		t.Errorf("20 concurrent Connects dialled %d times, want 1", n)
	}
	for _, l := range lives[1:] {
		if l != lives[0] {
			t.Fatal("callers got different connections")
		}
	}
}

func TestConnectReusesAnOpenConnection(t *testing.T) {
	w, id := workspace(t, "db.local")
	a, _ := w.Connect(context.Background(), id)
	before := fakeOpens.Load()
	b, _ := w.Connect(context.Background(), id)
	if a != b || fakeOpens.Load() != before {
		t.Error("a second Connect dialled again instead of sharing")
	}
}

func TestFailedConnectIsRetriedNotRemembered(t *testing.T) {
	w, id := workspace(t, "unreachable")
	if _, err := w.Connect(context.Background(), id); err == nil {
		t.Fatal("expected a failure")
	}
	before := fakeOpens.Load()
	w.Connect(context.Background(), id)
	if fakeOpens.Load() == before {
		t.Error("the failure was cached; a server that recovers would stay unreachable")
	}
	if len(w.OpenIDs()) != 0 {
		t.Error("a failed connection is listed as open")
	}
}

func TestGivingUpDoesNotCancelOthersDial(t *testing.T) {
	w, id := workspace(t, "slowish")
	impatient, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	patient := make(chan error, 1)
	go func() {
		_, err := w.Connect(context.Background(), id)
		patient <- err
	}()
	if _, err := w.Connect(impatient, id); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("impatient caller: %v", err)
	}
	if err := <-patient; err != nil {
		t.Errorf("the patient caller's dial was cancelled by the impatient one: %v", err)
	}
}

func TestDisconnectThenReconnectOpensFresh(t *testing.T) {
	w, id := workspace(t, "db.local")
	var mu sync.Mutex
	var seen []State
	w.OnStatus(func(gotID string, st Status) {
		if gotID == id {
			mu.Lock()
			seen = append(seen, st.State)
			mu.Unlock()
		}
	})
	a, _ := w.Connect(context.Background(), id)
	if err := w.Disconnect(id); err != nil {
		t.Fatal(err)
	}
	if !a.Source.(*fakeSource).closed.Load() {
		t.Error("Disconnect did not close the source")
	}
	mu.Lock()
	if len(seen) == 0 || seen[len(seen)-1] != StateClosed {
		t.Errorf("status listeners not told of the close: %v", seen)
	}
	mu.Unlock()

	b, _ := w.Connect(context.Background(), id)
	if a == b {
		t.Error("reconnect returned the closed connection")
	}
}

func TestMissingSecretsPassThrough(t *testing.T) {
	f := setup(t)
	conn, _ := f.c.Create(draft("S"), map[string]string{"password": "p"})
	// A fresh vault, as after a restart on a machine without a keychain.
	sf, _, _ := store.OpenSettings(f.path)
	c2 := NewConnections(sf, NewVault(nil, errors.New("no keychain")), nil)
	w := NewWorkspace(c2, fast)
	var mse *MissingSecretsError
	if _, err := w.Connect(context.Background(), conn.ID); !errors.As(err, &mse) {
		t.Errorf("want MissingSecretsError so the UI can ask, got %v", err)
	}
}
