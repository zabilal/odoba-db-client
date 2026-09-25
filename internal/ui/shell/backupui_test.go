package shell

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/store"
	"github.com/ikigai-db/ikigai-db/internal/store/localdb"
)

// Backing up everything the application keeps, and putting it back
// (FR-17.5).

// backing gives the fixture somewhere to back up from: the settings and
// the local database it is already using.
func backing(t *testing.T, fx *fixture) store.Paths {
	t.Helper()
	paths := store.Paths{Config: filepath.Dir(fx.settingsPath), Data: t.TempDir()}
	// The fixture's own settings file, and its own database.
	fx.deps.Backup = &app.Backup{Paths: paths, DB: fx.hist}
	fx.s.d.Backup = fx.deps.Backup
	return paths
}

// The archive is written where it was asked for, and says it holds no
// password.
func TestBackingUpWritesTheArchive(t *testing.T) {
	fx := newFixture(t)
	fx.create(t, "db1", map[string]string{"password": "hunter2"})
	backing(t, fx)
	fx.s.sync()
	if fx.s.menuItems[cmdBackUp].Disabled {
		t.Fatal("there is nowhere to back up from")
	}
	dir := t.TempDir()
	fx.s.run(cmdBackUp)
	if len(fx.files.saves) != 1 {
		t.Fatalf("it asked for %d places", len(fx.files.saves))
	}
	if got := fx.files.saves[0].Name; !strings.HasSuffix(got, ".zip") {
		t.Errorf("it suggests %q", got)
	}
	path := filepath.Join(dir, "backup.zip")
	fx.files.answer(path, nil)
	pump(t, fx.q, func() bool { _, err := os.Stat(path); return err == nil })
	pump(t, fx.q, func() bool { return strings.Contains(fx.s.status.Text, "Backed up") })

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "hunter2") {
		t.Error("the password is in the archive")
	}
	if !strings.Contains(fx.s.status.Text, "keychain") {
		t.Errorf("the window says %q", fx.s.status.Text)
	}
}

// Restoring says what it is about to do, and does nothing until it is
// answered.
func TestRestoringAsksFirst(t *testing.T) {
	fx := newFixture(t)
	fx.create(t, "db1", nil) // so that there is a settings file to replace
	paths := backing(t, fx)
	path := filepath.Join(t.TempDir(), "backup.zip")
	writeArchive(t, fx, path)

	fx.s.run(cmdRestore)
	if len(fx.files.opens) != 1 {
		t.Fatalf("it asked for %d files", len(fx.files.opens))
	}
	fx.files.answer(path, nil)
	fx.q.Flush()
	top := fx.s.win.Canvas().Overlays().Top()
	if top == nil {
		t.Fatal("it did not ask")
	}
	said := strings.Join(drawnSkipping(top, nil), " ")
	for _, want := range []string{"replaces", "closes", "kept beside it", "keychain"} {
		if !strings.Contains(said, want) {
			t.Errorf("it does not say %q: %s", want, said)
		}
	}
	test.Tap(findButton(top, "Cancel"))
	fx.q.Flush()
	if _, err := os.Stat(paths.SettingsFile() + ".before-restore"); err == nil {
		t.Error("it restored after being told not to")
	}

	// And answering it does restore: what the question is for.
	fx.s.run(cmdRestore)
	fx.files.answer(path, nil)
	fx.q.Flush()
	test.Tap(findButton(fx.s.win.Canvas().Overlays().Top(), "Restore"))
	fx.q.Flush()
	if _, err := os.Stat(paths.SettingsFile() + ".before-restore"); err != nil {
		t.Errorf("it did not restore when told to: %v", err)
	}
}

// An archive that could not be written is taken away: a half-written one
// looks like a whole one.
func TestAHalfWrittenArchiveIsTakenAway(t *testing.T) {
	fx := newFixture(t)
	backing(t, fx)
	// A database that is closed cannot be copied, which is how this fails
	// after the file has been created.
	shut, err := localdb.Open(context.Background(), filepath.Join(t.TempDir(), "shut.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := shut.Close(); err != nil {
		t.Fatal(err)
	}
	fx.s.d.Backup = &app.Backup{Paths: fx.s.d.Backup.Paths, DB: shut}

	path := filepath.Join(t.TempDir(), "backup.zip")
	fx.s.backUpTo(path)
	pump(t, fx.q, func() bool {
		return strings.Contains(strings.Join(drawnSkipping(fx.s.errors.slot, nil), " "), "could not be written")
	})
	if _, err := os.Stat(path); err == nil {
		t.Error("the half-written archive is still there")
	}
}

// Restoring puts the archive back and closes the window, and nothing of
// the session is written into the database that has gone.
func TestRestoringPutsItBackAndCloses(t *testing.T) {
	fx := newFixture(t)
	paths := backing(t, fx)
	path := filepath.Join(t.TempDir(), "backup.zip")
	writeArchive(t, fx, path)

	fx.s.restoreFrom(path)
	if fx.s.writer != nil {
		t.Error("the session is still being written into a database that has gone")
	}
	if _, err := os.Stat(paths.DatabaseFile()); err != nil {
		t.Errorf("the database was not put back: %v", err)
	}
	if got := fx.s.status.Text; !strings.Contains(got, "Restored") {
		t.Errorf("the window says %q", got)
	}
}

// writeArchive writes a backup of what the fixture holds.
func writeArchive(t *testing.T, fx *fixture, path string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := fx.s.d.Backup.Write(context.Background(), f); err != nil {
		t.Fatal(err)
	}
}

// An archive that is not one is refused, and says so.
func TestRestoringFromSomethingElse(t *testing.T) {
	fx := newFixture(t)
	backing(t, fx)
	path := filepath.Join(t.TempDir(), "not-a-backup.zip")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	fx.s.run(cmdRestore)
	fx.files.answer(path, nil)
	fx.q.Flush()
	if _, err := os.Stat(fx.s.d.Backup.Paths.SettingsFile() + ".before-restore"); err == nil {
		t.Error("it restored from something that is not an archive")
	}
	if said := strings.Join(drawnSkipping(fx.s.errors.slot, nil), " "); !strings.Contains(said, "could not be read") {
		t.Errorf("the window says %q", said)
	}
}

// With nowhere to back up from, neither is offered.
func TestNoBackupNoCommands(t *testing.T) {
	fx := newFixture(t)
	fx.s.sync()
	if !fx.s.menuItems[cmdBackUp].Disabled || !fx.s.menuItems[cmdRestore].Disabled {
		t.Error("a window with nowhere to back up from offers to")
	}
}

// The local database can be copied while it is open and in use, which is
// what a backup of a running application needs.
func TestTheDatabaseIsCopiedWhileOpen(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()
	if _, err := fx.hist.SaveQuery(ctx, localdb.SavedQuery{Name: "totals", Body: "select 1"}); err != nil {
		t.Fatal(err)
	}
	to := filepath.Join(t.TempDir(), "copy.db")
	if err := fx.hist.BackupTo(ctx, to); err != nil {
		t.Fatal(err)
	}
	copied, err := localdb.Open(ctx, to)
	if err != nil {
		t.Fatal(err)
	}
	defer copied.Close()
	if got, err := copied.SavedQueries(ctx); err != nil || len(got) != 1 || got[0].Name != "totals" {
		t.Errorf("the copy holds %+v (%v)", got, err)
	}
}
