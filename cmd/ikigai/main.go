// Command ikigai is the Ikigai DB desktop client.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"runtime"

	fyneapp "fyne.io/fyne/v2/app"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/logging"
	"github.com/ikigai-db/ikigai-db/internal/store"
	"github.com/ikigai-db/ikigai-db/internal/store/localdb"
	"github.com/ikigai-db/ikigai-db/internal/store/secrets"
	"github.com/ikigai-db/ikigai-db/internal/ui/shell"
	uitheme "github.com/ikigai-db/ikigai-db/internal/ui/theme"

	// Drivers register themselves on import (REQ-DB-1). Adding a source to the
	// application is a blank import here, and nothing else.
	_ "github.com/ikigai-db/ikigai-db/internal/source/drivers/mysql"
	_ "github.com/ikigai-db/ikigai-db/internal/source/drivers/postgres"
	_ "github.com/ikigai-db/ikigai-db/internal/source/drivers/sqlite"
)

// version is set at build time via -ldflags.
var version = "dev"

// appID keys Fyne's per-application storage. Like the module path it is a
// placeholder until the project's home is settled (OQ-1).
const appID = "io.github.ikigai-db"

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "-version" || os.Args[1] == "--version") {
		fmt.Println("ikigai", version)
		return
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "ikigai:", err)
		os.Exit(1)
	}
}

func run() error {
	paths, err := store.Resolve()
	if err != nil {
		return err
	}
	if err := paths.Ensure(); err != nil {
		return err
	}
	log, logFile, err := logging.New(paths.Logs, logging.Options{})
	if err != nil {
		return err
	}
	defer logFile.Close()
	log.Info("starting", "version", version, "os", runtime.GOOS, "portable", paths.Portable)
	slog.SetDefault(log) // where panics.Recover writes a driver's stack (NFR-R1)

	settings, notice, err := store.OpenSettings(paths.SettingsFile())
	if err != nil {
		return err
	}
	// History is an aid, not a requirement: if its database cannot open, the
	// app runs without it rather than not at all.
	var (
		history app.HistoryStore
		saved   app.SavedQueryStore
	)
	if db, err := localdb.Open(context.Background(), paths.DatabaseFile()); err != nil {
		log.Warn("history and saved queries unavailable", "err", err)
	} else {
		defer db.Close()
		history, saved = db, db
	}

	vault := app.NewVault(secrets.OS(), keychainAvailability())
	conns := app.NewConnections(settings, vault, log)
	ws := app.NewWorkspace(conns, app.MonitorConfig{})

	s := shell.New(fyneapp.NewWithID(appID), shell.Deps{
		Conns: conns, WS: ws, Settings: settings, History: history, Saved: saved, Theme: uitheme.New(), Log: log,
	})
	s.ShowNotice(notice)
	s.Window().ShowAndRun()
	log.Info("stopped")
	return nil
}

// keychainAvailability probes the OS keychain only where it can be missing.
// macOS and Windows always have one, and the probe is a round trip to the
// keychain service inside the cold-start budget (NFR-P1). On Linux the Secret
// Service may be absent, and the vault must know, so that it never claims to
// have saved a password it could not.
func keychainAvailability() error {
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		return nil
	}
	return secrets.Available()
}
