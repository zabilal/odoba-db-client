package app

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/store"
	"github.com/ikigai-db/ikigai-db/internal/store/localdb"
)

// Backup writes everything this application keeps into one archive, and
// puts it back (FR-17.5).
//
// What is in it: the settings — connections, folders, favourites, the
// shortcuts somebody changed — and the local database, which is query
// history, saved queries, unsaved query text, saved views, workspaces and
// the session.
//
// What is not in it, and cannot be: the passwords. They are in the
// operating system's keychain and never in this application's files
// (NFR-S1), and an archive that carried them would be the plaintext
// password file a keychain exists to avoid. A restored connection asks for
// its password the first time it is used — on the machine it was backed up
// from, the keychain still has it and nothing is asked.

// The names inside the archive. They are what somebody opening it sees, so
// they are the names of the files themselves.
const (
	manifestName = "ikigai-backup.json"
	settingsName = "settings.json"
	databaseName = "ikigai.db"
)

// backupVersion is the shape of the archive, so that a later one can be
// told from this and refused rather than half read.
const backupVersion = 1

// aboutSecrets is in the manifest because somebody reading the archive
// should not have to work out whether their passwords are in it.
const aboutSecrets = "No passwords are in this archive. They are in the operating system's keychain."

// A Manifest says what an archive is.
type Manifest struct {
	Application string    `json:"application"`
	Version     int       `json:"version"`
	Written     time.Time `json:"written"`
	Secrets     string    `json:"secrets"`
}

// Backup is the application's data, where it is kept.
type Backup struct {
	Paths store.Paths
	// DB is the open local database. A copy of it goes into the archive,
	// and it is closed before a restore replaces its file.
	DB *localdb.DB
}

// Write puts the settings and a copy of the local database into one zip.
//
// The database is copied through SQLite rather than read from disk: it is
// open and in use, and what is on disk at any moment may be mid-write.
func (b Backup) Write(ctx context.Context, w io.Writer) (err error) {
	tmp, err := os.MkdirTemp("", "ikigai-backup")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	z := zip.NewWriter(w)
	man := Manifest{Application: "Ikigai DB", Version: backupVersion, Written: time.Now().UTC(), Secrets: aboutSecrets}
	if err := writeJSON(z, manifestName, man); err != nil {
		return err
	}
	if err := copyInto(z, settingsName, b.Paths.SettingsFile()); err != nil {
		return err
	}
	if b.DB != nil {
		copied := filepath.Join(tmp, databaseName)
		if err := b.DB.BackupTo(ctx, copied); err != nil {
			return err
		}
		if err := copyInto(z, databaseName, copied); err != nil {
			return err
		}
	}
	return z.Close()
}

// About reads an archive's manifest, so that whatever asks can say what it
// is about to put back.
func About(r io.ReaderAt, size int64) (Manifest, error) {
	z, err := zip.NewReader(r, size)
	if err != nil {
		return Manifest{}, fmt.Errorf("app: this is not an archive this application wrote: %w", err)
	}
	return manifestOf(z)
}

func manifestOf(z *zip.Reader) (Manifest, error) {
	f, err := z.Open(manifestName)
	if err != nil {
		return Manifest{}, errors.New("app: this archive was not written by this application")
	}
	defer f.Close()
	var man Manifest
	if err := json.NewDecoder(f).Decode(&man); err != nil {
		return Manifest{}, fmt.Errorf("app: what this archive says about itself could not be read: %w", err)
	}
	if man.Version > backupVersion {
		return man, fmt.Errorf("app: this archive was written by a later version of the application (%d)", man.Version)
	}
	return man, nil
}

// Restore puts an archive back, replacing this machine's data with it.
//
// Both files are written beside their targets first and moved into place
// afterwards, so that a failure part way leaves what was there. What was
// there is kept beside the new one, under .before-restore, rather than
// removed: a restore somebody did not mean is then one they can undo.
//
// The local database is closed on the way, because its file is about to be
// replaced. Whatever asked for this has to end the session: everything the
// application is holding is about to be about a database that has gone.
func (b Backup) Restore(r io.ReaderAt, size int64) (Manifest, error) {
	z, err := zip.NewReader(r, size)
	if err != nil {
		return Manifest{}, fmt.Errorf("app: this is not an archive this application wrote: %w", err)
	}
	man, err := manifestOf(z)
	if err != nil {
		return man, err
	}
	// In this order, and not over a map: what is staged, and what is left
	// behind when staging fails, should not depend on where the runtime
	// happened to start.
	want := []struct{ name, target string }{
		{settingsName, b.Paths.SettingsFile()},
		{databaseName, b.Paths.DatabaseFile()},
	}
	var staged [][2]string
	for _, w := range want {
		at, err := stage(z, w.name, w.target)
		if err != nil {
			for _, s := range staged {
				os.Remove(s[1])
			}
			return man, err
		}
		if at != "" {
			staged = append(staged, [2]string{w.target, at})
		}
	}
	if b.DB != nil {
		if err := b.DB.Close(); err != nil {
			return man, fmt.Errorf("app: the local database could not be closed to replace it: %w", err)
		}
	}
	var failed []error
	for _, s := range staged {
		target, at := s[0], s[1]
		if err := keepAside(target); err != nil {
			failed = append(failed, err)
			continue
		}
		if err := os.Rename(at, target); err != nil {
			failed = append(failed, fmt.Errorf("app: putting %s back: %w", filepath.Base(target), err))
		}
	}
	return man, errors.Join(failed...)
}

// stage writes one of the archive's files beside where it belongs. A file
// the archive does not hold is not an error: an archive written before a
// database existed has none, and what is there stays.
func stage(z *zip.Reader, name, target string) (string, error) {
	f, err := z.Open(name)
	if err != nil {
		return "", nil
	}
	defer f.Close()
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return "", err
	}
	out, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+".restoring-*")
	if err != nil {
		return "", err
	}
	defer out.Close()
	if err := out.Chmod(0o600); err != nil {
		os.Remove(out.Name())
		return "", err
	}
	if _, err := io.Copy(out, f); err != nil {
		os.Remove(out.Name())
		return "", fmt.Errorf("app: reading %s out of the archive: %w", name, err)
	}
	if err := out.Sync(); err != nil {
		os.Remove(out.Name())
		return "", err
	}
	return out.Name(), nil
}

// keepAside moves what is there out of the way, if there is anything.
func keepAside(target string) error {
	if _, err := os.Stat(target); err != nil {
		return nil // nothing there to keep
	}
	if err := os.Rename(target, target+".before-restore"); err != nil {
		return fmt.Errorf("app: keeping the old %s: %w", filepath.Base(target), err)
	}
	return nil
}

func writeJSON(z *zip.Writer, name string, v any) error {
	w, err := z.Create(name)
	if err != nil {
		return err
	}
	e := json.NewEncoder(w)
	e.SetIndent("", "  ")
	return e.Encode(v)
}

// copyInto puts a file into the archive. A file that is not there is left
// out rather than failing: a settings file is written at the first change,
// and a backup before then is a backup of nothing, which is a true one.
func copyInto(z *zip.Writer, name, path string) error {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	w, err := z.Create(name)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, f)
	return err
}
