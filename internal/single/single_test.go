package single

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
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
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	return d
}

func TestASecondCopyGivesWayAndTheFirstComesForward(t *testing.T) {
	dir := shortDir(t)
	shown := make(chan struct{}, 1)
	first, err := Acquire(dir, func() { shown <- struct{}{} })
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(socketPath(dir))
	switch {
	case err != nil:
		t.Errorf("there is no socket: %v", err)
	case runtime.GOOS == "windows":
		// Windows has no mode to set: what protects a socket there is the ACL of
		// the directory it is in, which is the user's own profile. Asserting a
		// mode would be asserting something the operating system does not mean.
	case fi.Mode().Perm() != 0o600:
		t.Errorf("the socket should be the user's alone, and is %04o", fi.Mode().Perm())
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
	_ = again.Release()
}

func TestASocketLeftByACrashIsTakenOver(t *testing.T) {
	dir := shortDir(t)
	path := socketPath(dir)
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = ln.Close() // the socket file stays, and nobody answers: a crashed copy
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("setup: the stale socket should remain: %v", err)
	}
	in, err := Acquire(dir, nil)
	if err != nil {
		t.Fatalf("a crashed copy's socket should be taken over: %v", err)
	}
	_ = in.Release()
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
	_ = b.Release()
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
