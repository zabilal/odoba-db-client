// Command ikigai is the Ikigai DB desktop client.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"

	"fyne.io/fyne/v2"
	fyneapp "fyne.io/fyne/v2/app"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/logging"
	"github.com/ikigai-db/ikigai-db/internal/single"
	"github.com/ikigai-db/ikigai-db/internal/store"
	"github.com/ikigai-db/ikigai-db/internal/store/localdb"
	"github.com/ikigai-db/ikigai-db/internal/store/secrets"
	"github.com/ikigai-db/ikigai-db/internal/ui/shell"
	uitheme "github.com/ikigai-db/ikigai-db/internal/ui/theme"

	// Drivers register themselves on import (REQ-DB-1). Adding a source to the
	// application is a blank import here, and nothing else.
	_ "github.com/ikigai-db/ikigai-db/internal/source/drivers/cassandra"
	_ "github.com/ikigai-db/ikigai-db/internal/source/drivers/clickhouse"
	_ "github.com/ikigai-db/ikigai-db/internal/source/drivers/cockroach"
	_ "github.com/ikigai-db/ikigai-db/internal/source/drivers/mongo"
	_ "github.com/ikigai-db/ikigai-db/internal/source/drivers/mysql"
	_ "github.com/ikigai-db/ikigai-db/internal/source/drivers/oracle"
	_ "github.com/ikigai-db/ikigai-db/internal/source/drivers/postgres"
	_ "github.com/ikigai-db/ikigai-db/internal/source/drivers/redis"
	_ "github.com/ikigai-db/ikigai-db/internal/source/drivers/sqlite"
	_ "github.com/ikigai-db/ikigai-db/internal/source/drivers/sqlserver"
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

	// One copy to a data directory (T1.1, ADR-0022). A second copy asks the
	// first to come forward and gives way, before touching the settings or
	// the database. If the check itself cannot be made, the app still
	// starts: refusing to open would be worse than the risk it guards.
	var shown atomic.Pointer[fyne.Window]
	inst, err := single.Acquire(paths.Data, func() {
		if w := shown.Load(); w != nil {
			fyne.Do(func() { (*w).Show(); (*w).RequestFocus() })
		}
	})
	switch {
	case errors.Is(err, single.ErrRunning):
		log.Info("another copy is running with this data; asked it to come forward")
		return nil
	case err != nil:
		log.Warn("the single-copy check is unavailable", "err", err)
	default:
		defer inst.Release()
	}

	settings, notice, err := store.OpenSettings(paths.SettingsFile())
	if err != nil {
		return err
	}
	// History is an aid, not a requirement: if its database cannot open, the
	// app runs without it rather than not at all.
	var (
		history  app.HistoryStore
		saved    app.SavedQueryStore
		scratch  app.ScratchStore
		session  app.SessionStore
		params   app.ParamStore
		decoders app.DecoderStore
		layouts  app.LayoutStore
		views    app.ViewStore
		spaces   app.WorkspaceStore
		backup   *app.Backup
	)
	if db, err := localdb.Open(context.Background(), paths.DatabaseFile()); err != nil {
		log.Warn("history, saved queries, autosave and the session unavailable", "err", err)
	} else {
		defer db.Close()
		history, saved, scratch, session, params, decoders, layouts, views, spaces = db, db, db, db, db, db, db, db, db
		backup = &app.Backup{Paths: paths, DB: db}
	}

	// A portable copy keeps its secrets beside itself rather than in this
	// machine's keychain, and only sealed (FR-17.6, ADR-0154). Left in the
	// keychain they would stay on every machine the copy was carried to,
	// and travel with it nowhere.
	vault := app.NewVault(secrets.OS(), keychainAvailability())
	if paths.Portable {
		vault = app.NewPortableVault(secrets.NewFile(filepath.Join(paths.Data, "secrets.json")))
	}
	// The app-level lock over the vault, where one was put on (NFR-S7).
	// What is saved recognises a passphrase; the window asks for it before
	// anything reads a secret.
	vault.SetLock(app.NewLock(settings.Get().Vault))
	conns := app.NewConnections(settings, vault, log)
	ws := app.NewWorkspace(conns, app.MonitorConfig{})

	s := shell.New(fyneapp.NewWithID(appID), shell.Deps{
		Conns: conns, WS: ws, Settings: settings, History: history, Saved: saved, Scratch: scratch, Session: session, Views: views, Workspaces: spaces, Backup: backup, Params: params, Decoders: decoders, Layouts: layouts, Theme: uitheme.New(), Log: log,
	})
	s.ShowNotice(notice)
	w := s.Window()
	shown.Store(&w)
	w.ShowAndRun()
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
