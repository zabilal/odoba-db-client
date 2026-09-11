// Package single keeps one copy of the application to a data directory
// (T1.1, ADR-0022). The first copy listens on a local socket there. A
// second connects, asks the first to come forward, and gives way, so two
// copies never share one settings file and one local database, each
// writing its own session and unsaved query text over the other's.
package single

import (
	"bufio"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ErrRunning reports that another copy holds the data directory, and has
// been asked to come forward.
var ErrRunning = errors.New("another copy is running with this data")

// dialTimeout bounds asking a copy that may have crashed.
const dialTimeout = time.Second

// Instance is this copy's claim on a data directory.
type Instance struct {
	ln     net.Listener
	onShow func()
}

// Acquire claims a data directory for this copy. When another copy answers
// on it, that copy is asked to come forward and ErrRunning is returned. A
// socket nobody answers on was left by a copy that crashed, and is taken
// over. onShow runs, off the UI goroutine, whenever a later copy asks this
// one to come forward.
func Acquire(dataDir string, onShow func()) (*Instance, error) {
	path := socketPath(dataDir)
	for attempt := 0; ; attempt++ {
		if ask(path) {
			return nil, ErrRunning
		}
		_ = os.Remove(path) // a crashed copy's socket, if any
		ln, err := net.Listen("unix", path)
		if err == nil {
			_ = os.Chmod(path, 0o600)
			in := &Instance{ln: ln, onShow: onShow}
			go in.serve()
			return in, nil
		}
		if attempt > 0 {
			return nil, err
		}
		// Two copies starting together: the other took the socket between
		// the ask and the listen. Ask again; it answers now.
	}
}

// ask asks the copy listening at path to come forward, and reports whether
// one answered.
func ask(path string) bool {
	c, err := net.DialTimeout("unix", path, dialTimeout)
	if err != nil {
		return false
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(dialTimeout))
	_, err = c.Write([]byte("show\n"))
	return err == nil
}

func (in *Instance) serve() {
	for {
		c, err := in.ln.Accept()
		if err != nil {
			return // released
		}
		go func() {
			defer c.Close()
			_ = c.SetDeadline(time.Now().Add(dialTimeout))
			line, _ := bufio.NewReader(c).ReadString('\n')
			if strings.TrimSpace(line) == "show" && in.onShow != nil {
				in.onShow()
			}
		}()
	}
}

// Release gives the data directory up. Closing a listener made by Listen
// removes its socket file too.
func (in *Instance) Release() error { return in.ln.Close() }

// maxSocketPath keeps under the shortest limit on a socket's path, macOS's
// 104 bytes.
const maxSocketPath = 100

// socketPath is where a data directory's socket lives: in the directory,
// or, when that path is too long for a socket, as a portable copy's deep
// folder can be, in the temporary directory under a name taken from it.
func socketPath(dataDir string) string {
	if p := filepath.Join(dataDir, "ikigai.sock"); len(p) <= maxSocketPath {
		return p
	}
	sum := sha256.Sum256([]byte(dataDir))
	return filepath.Join(os.TempDir(), fmt.Sprintf("ikigai-%x.sock", sum[:8]))
}
