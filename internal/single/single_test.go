package single

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// shortDir is a temporary directory short enough for a socket's path.
func shortDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "ik")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

func TestASecondCopyGivesWayAndTheFirstComesForward(t *testing.T) {
	dir := shortDir(t)
	shown := make(chan struct{}, 1)
	first, err := Acquire(dir, func() { shown <- struct{}{} })
	if err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(socketPath(dir)); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("the socket should be the user's alone: %v, %v", fi, err)
	}
	if _, err := Acquire(dir, nil); !errors.Is(err, ErrRunning) {
		t.Fatalf("a second copy got %v, want ErrRunning", err)
	}
	select {
	case <-shown:
	case <-time.After(2 * time.Second):
		t.Error("the first copy was never asked to come forward")
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	again, err := Acquire(dir, nil)
	if err != nil {
		t.Fatalf("once released, the directory should be free: %v", err)
	}
	again.Release()
}

func TestASocketLeftByACrashIsTakenOver(t *testing.T) {
	dir := shortDir(t)
	path := socketPath(dir)
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	ln.Close() // the socket file stays, and nobody answers: a crashed copy
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("setup: the stale socket should remain: %v", err)
	}
	in, err := Acquire(dir, nil)
	if err != nil {
		t.Fatalf("a crashed copy's socket should be taken over: %v", err)
	}
	in.Release()
}

func TestSeparateDataRunsSideBySide(t *testing.T) {
	a, err := Acquire(shortDir(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Release()
	b, err := Acquire(shortDir(t), nil)
	if err != nil {
		t.Fatalf("a copy with its own data should run beside the first: %v", err)
	}
	b.Release()
}

func TestADeepDataDirectoryHasAShortSocket(t *testing.T) {
	deep := filepath.Join("/", strings.Repeat("portable-copy-folder/", 8))
	p := socketPath(deep)
	if len(p) > maxSocketPath || filepath.Dir(p) != filepath.Clean(os.TempDir()) {
		t.Errorf("a deep directory's socket %q should be short, in the temporary directory", p)
	}
	if socketPath(deep) != p || socketPath(deep+"x") == p {
		t.Error("the socket should be the same for one directory, and differ between two")
	}
	if got := socketPath("/short"); got != filepath.Join("/short", "ikigai.sock") {
		t.Errorf("a short directory keeps its socket inside: %q", got)
	}
}
