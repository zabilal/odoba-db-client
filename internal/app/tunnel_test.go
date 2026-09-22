package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Carrying a connection through an SSH tunnel (T2.85, FR-1.9).
//
// What a tunnel does once it is open is proved where it is written, against
// an SSH server in process. What is proved here is the part this layer owns:
// that a connection asking for no tunnel gets none, and that a tunnel does
// not outlive the connection it was opened for.

func TestAConnectionWithNoTunnelAsksForNone(t *testing.T) {
	cfg := source.ConnectionConfig{Host: "db.example", Port: 5432}
	got, closeTunnel, err := throughTunnel(context.Background(), cfg)
	if err != nil {
		t.Fatalf("a connection with no tunnel: %v", err)
	}
	if closeTunnel != nil {
		t.Error("a connection with no tunnel was given one to close")
	}
	// And it dials exactly what it was going to dial.
	if got.Host != "db.example" || got.Port != 5432 {
		t.Errorf("the address became %s:%d", got.Host, got.Port)
	}
}

func TestClosingAConnectionClosesItsTunnel(t *testing.T) {
	// A tunnel outliving its connection is a listener on a local port that
	// nobody remembers opening (ADR-0109).
	closed := 0
	l := startLive("c1", &fakeSource{}, MonitorConfig{Interval: time.Hour},
		func() error { closed++; return nil })
	if err := l.Close(); err != nil {
		t.Fatalf("closing the connection: %v", err)
	}
	if closed != 1 {
		t.Errorf("closing a connection closed its tunnel %d times", closed)
	}
	// Closing again is somebody else's second thought, not a second tunnel.
	if err := l.Close(); err != nil {
		t.Errorf("closing it again: %v", err)
	}
	if closed != 1 {
		t.Errorf("the tunnel was closed %d times over two closes", closed)
	}
}

func TestATunnelThatWillNotCloseIsStillReported(t *testing.T) {
	// The source closed cleanly and the tunnel did not. Saying nothing would
	// leave somebody believing the connection went away entirely.
	boom := errors.New("the tunnel would not close")
	l := startLive("c1", &fakeSource{}, MonitorConfig{Interval: time.Hour},
		func() error { return boom })
	if err := l.Close(); !errors.Is(err, boom) {
		t.Errorf("closing reported %v", err)
	}
}
