// Package store persists what outlives a session: the directories everything
// is kept under, the settings file, and saved connections (FR-17.1, FR-17.2).
//
// Secrets never pass through this package's files. Passwords and keys live in
// the OS keychain (internal/store/secrets). A saved connection records only
// the NAMES of the secrets held for it, never their values (NFR-S1).
//
// This package must not import any UI package (ARCH-1).
package store

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

const (
	// appName names directories on macOS and Windows, where users see them.
	appName = "Ikigai DB"
	// appSlug names directories on Linux and other Unix, per XDG convention.
	appSlug = "ikigai-db"
	// portableMarker, beside the executable, selects portable mode (FR-17.6).
	portableMarker = "ikigai-portable"
)

// Paths are the directories the application writes to.
type Paths struct {
	Config string // settings.json
	Data   string // the local database: history, saved queries, session state
	Logs   string
	Cache  string

	// Portable reports that everything lives beside the executable.
	Portable bool
}

// SettingsFile is where settings are kept.
func (p Paths) SettingsFile() string { return filepath.Join(p.Config, "settings.json") }

// DatabaseFile is where the local database is kept.
func (p Paths) DatabaseFile() string { return filepath.Join(p.Data, "ikigai.db") }

// Resolve returns this run's paths: portable if the marker file sits beside
// the executable (FR-17.6), otherwise the platform's convention (FR-17.1).
func Resolve() (Paths, error) {
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		if _, err := os.Stat(filepath.Join(dir, portableMarker)); err == nil {
			return portablePaths(dir), nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, fmt.Errorf("store: no home directory: %w", err)
	}
	return pathsFor(runtime.GOOS, home, os.Getenv), nil
}

func portablePaths(exeDir string) Paths {
	root := filepath.Join(exeDir, appName+" Data")
	return Paths{
		Config:   filepath.Join(root, "config"),
		Data:     filepath.Join(root, "data"),
		Logs:     filepath.Join(root, "logs"),
		Cache:    filepath.Join(root, "cache"),
		Portable: true,
	}
}

// pathsFor applies each platform's convention. Taking the OS, the home
// directory and the environment as arguments lets every platform's rules be
// tested on any machine.
func pathsFor(goos, home string, getenv func(string) string) Paths {
	switch goos {
	case "darwin":
		lib := filepath.Join(home, "Library")
		support := filepath.Join(lib, "Application Support", appName)
		return Paths{
			Config: support,
			Data:   support,
			Logs:   filepath.Join(lib, "Logs", appName),
			Cache:  filepath.Join(lib, "Caches", appName),
		}

	case "windows":
		roaming := getenv("APPDATA")
		if roaming == "" {
			roaming = filepath.Join(home, "AppData", "Roaming")
		}
		local := getenv("LOCALAPPDATA")
		if local == "" {
			local = filepath.Join(home, "AppData", "Local")
		}
		return Paths{
			// Settings roam with a domain profile, which is what a user moving
			// between machines expects. The history database and logs are
			// machine-local and can grow large, so they must not roam.
			Config: filepath.Join(roaming, appName),
			Data:   filepath.Join(local, appName),
			Logs:   filepath.Join(local, appName, "Logs"),
			Cache:  filepath.Join(local, appName, "Cache"),
		}
	}

	xdg := func(key, fallback string) string {
		// The XDG spec says a relative path in these variables is invalid and
		// must be ignored, not resolved against the working directory.
		if v := getenv(key); v != "" && filepath.IsAbs(v) {
			return v
		}
		return filepath.Join(home, fallback)
	}
	return Paths{
		Config: filepath.Join(xdg("XDG_CONFIG_HOME", ".config"), appSlug),
		Data:   filepath.Join(xdg("XDG_DATA_HOME", filepath.Join(".local", "share")), appSlug),
		Logs:   filepath.Join(xdg("XDG_STATE_HOME", filepath.Join(".local", "state")), appSlug, "logs"),
		Cache:  filepath.Join(xdg("XDG_CACHE_HOME", ".cache"), appSlug),
	}
}

// Ensure creates every directory, readable only by the user.
func (p Paths) Ensure() error {
	for _, d := range []string{p.Config, p.Data, p.Logs, p.Cache} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return fmt.Errorf("store: creating %s: %w", d, err)
		}
	}
	return nil
}
