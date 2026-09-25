package plugin

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/store"
)

// Finding the plugins somebody installed, and running them (FR-16.2).
//
// Nothing here runs anything that was not chosen. A plugin is a program, and a
// program that runs because it was in a folder is a program nobody chose: the
// directory is somebody's own and the whole thing is off until they turn it on.
// What is loaded is said in the log, by name and by path, because "which code
// is running" is a question somebody must be able to answer.

// Loaded is one plugin that started and said what it is.
type Loaded struct {
	// Path is the program. Name is what it calls itself, and ID is the driver
	// it registered.
	Path    string
	Name    string
	ID      string
	Version string

	Process *Process
}

// Set is every plugin loaded in this process, so that they can all be closed
// when the application does.
type Set struct {
	Plugins []Loaded
	// Failed is what did not load, and why, so that a plugin that is there and
	// not working is visible rather than silently absent.
	Failed map[string]error
}

// Close ends every plugin.
func (s *Set) Close() {
	if s == nil {
		return
	}
	for _, p := range s.Plugins {
		p.Process.Close()
	}
}

// Load starts every plugin in dir and registers each as a driver.
//
// A directory holds one plugin per subdirectory, each with a program in it
// named "plugin" — a name rather than "whatever is executable", so that a
// README, a licence and a data file can sit beside it without being run.
//
// A plugin that fails to start, fails the handshake, or names a driver the
// application already has is left out and recorded in Failed. One bad plugin
// does not stop the others: they are separate programs and there is no reason
// for them to share a fate.
//
// Each handshake is bounded by its own timeout, so a plugin that hangs costs
// that long and not the rest of the loading. A deadline on ctx, though, covers
// them all together — which is right for a caller that has a moment to spare
// and wrong for one that means "each": the application passes no deadline here.
func Load(ctx context.Context, dir string, log *slog.Logger) (*Set, error) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	set := &Set{Failed: map[string]error{}}
	entries, err := os.ReadDir(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		// No directory is no plugins, which is the ordinary case and not a
		// failure: the folder is made by whoever puts something in it.
		return set, nil
	case err != nil:
		return set, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names) // loaded in the same order every time
	for _, name := range names {
		path := filepath.Join(dir, name, program())
		if _, err := os.Stat(path); err != nil {
			set.Failed[name] = fmt.Errorf("no %s in %s", program(), filepath.Join(dir, name))
			continue
		}
		p, hello, err := Start(ctx, path, log)
		if err != nil {
			set.Failed[name] = err
			log.Warn("a plugin did not load", "plugin", name, "err", err)
			continue
		}
		if _, taken := source.Lookup(hello.ID); taken == nil {
			p.Close()
			set.Failed[name] = fmt.Errorf("the driver name %q is already taken", hello.ID)
			log.Warn("a plugin did not load", "plugin", name, "err", set.Failed[name])
			continue
		}
		source.Register(Driver{Process: p, Hello: hello})
		set.Plugins = append(set.Plugins, Loaded{Path: path, Name: hello.Name,
			ID: hello.ID, Version: hello.Version, Process: p})
		log.Info("plugin loaded", "plugin", hello.Name, "driver", hello.ID,
			"version", hello.Version, "path", path)
	}
	return set, nil
}

// program is the file a plugin's directory holds.
func program() string {
	if runtime.GOOS == "windows" {
		return "plugin.exe"
	}
	return "plugin"
}

// Describe says what loaded and what did not, for a line somebody reads.
func (s *Set) Describe() string {
	if s == nil || (len(s.Plugins) == 0 && len(s.Failed) == 0) {
		return "No plugins."
	}
	var parts []string
	for _, p := range s.Plugins {
		name := p.Name
		if p.Version != "" {
			name += " " + p.Version
		}
		parts = append(parts, name)
	}
	out := ""
	if len(parts) > 0 {
		out = "Plugins: " + strings.Join(parts, ", ") + "."
	}
	if n := len(s.Failed); n > 0 {
		if out != "" {
			out += " "
		}
		out += fmt.Sprintf("%d did not load; the log says why.", n)
	}
	return out
}

// FromSettings loads the plugins the settings say to, and nothing at all where
// they say nothing.
//
// The three-way answer matters: no section means nobody has ever turned
// plugins on, a section with Enabled false means somebody turned them off, and
// neither runs a program. The caller keeps the Set and closes it when the
// application ends.
func FromSettings(ctx context.Context, set *store.Plugins, paths store.Paths, log *slog.Logger) (*Set, error) {
	if set == nil || !set.Enabled {
		return &Set{Failed: map[string]error{}}, nil
	}
	dir := strings.TrimSpace(set.Directory)
	if dir == "" {
		dir = paths.PluginsDir()
	}
	return Load(ctx, dir, log)
}
