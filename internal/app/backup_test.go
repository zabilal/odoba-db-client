package app

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/store"
	"github.com/ikigai-db/ikigai-db/internal/store/localdb"
	"github.com/ikigai-db/ikigai-db/internal/store/secrets"
)

// Backup and restore of everything the application keeps (FR-17.5).

// kept is an application's data: a settings file with a connection in it,
// and a local database with a saved query.
func kept(t *testing.T) (Backup, *Connections, *secrets.Memory) {
	t.Helper()
	dir := t.TempDir()
	paths := store.Paths{Config: dir, Data: dir}
	sf, _, err := store.OpenSettings(paths.SettingsFile())
	if err != nil {
		t.Fatal(err)
	}
	db, err := localdb.Open(context.Background(), paths.DatabaseFile())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	keys := secrets.NewMemory()
	conns := NewConnections(sf, NewVault(keys, nil), nil)
	if _, err := conns.Create(draft("Orders"), map[string]string{"password": "hunter2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveQuery(context.Background(), localdb.SavedQuery{Name: "totals", Body: "select 1"}); err != nil {
		t.Fatal(err)
	}
	return Backup{Paths: paths, DB: db}, conns, keys
}

// archived writes the archive and opens it for reading.
func archived(t *testing.T, b Backup) (*bytes.Reader, int64) {
	t.Helper()
	var buf bytes.Buffer
	if err := b.Write(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(buf.Bytes()), int64(buf.Len())
}

// The archive holds the settings and the database, and says what it is.
func TestABackupHoldsTheSettingsAndTheDatabase(t *testing.T) {
	b, _, _ := kept(t)
	r, size := archived(t, b)
	z, err := zip.NewReader(r, size)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range z.File {
		names = append(names, f.Name)
	}
	for _, want := range []string{manifestName, settingsName, databaseName} {
		if !has(names, want) {
			t.Errorf("the archive holds %v, without %q", names, want)
		}
	}
	man, err := About(r, size)
	if err != nil {
		t.Fatal(err)
	}
	if man.Application != "Ikigai DB" || man.Version != backupVersion || man.Written.IsZero() {
		t.Errorf("it says %+v", man)
	}
	if !strings.Contains(man.Secrets, "keychain") {
		t.Errorf("it says this about passwords: %q", man.Secrets)
	}
}

// No password is in it, wherever one is looked for: they are the one thing
// a keychain exists to keep out of files.
func TestABackupHoldsNoPassword(t *testing.T) {
	b, _, _ := kept(t)
	var buf bytes.Buffer
	if err := b.Write(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(buf.Bytes(), []byte("hunter2")) {
		t.Error("a password is in the archive")
	}
	// The connection is, though: a backup that lost the connections would
	// be a backup of nothing anybody wanted.
	z, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	f, err := z.Open(settingsName)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var st store.Settings
	if err := json.NewDecoder(f).Decode(&st); err != nil {
		t.Fatal(err)
	}
	if len(st.Connections) != 1 || st.Connections[0].Name != "Orders" {
		t.Errorf("the archive's settings hold %+v", st.Connections)
	}
}

// A restore puts both files back, and keeps what was there beside them.
func TestARestorePutsItBack(t *testing.T) {
	b, conns, _ := kept(t)
	r, size := archived(t, b)

	// The machine moves on: the connection is renamed and the query gone.
	c := conns.List()[0]
	c.Name = "Renamed"
	if err := conns.Update(c, SecretEdit{}); err != nil {
		t.Fatal(err)
	}
	qs, err := b.DB.SavedQueries(context.Background())
	if err != nil || len(qs) != 1 {
		t.Fatalf("the query is %+v (%v)", qs, err)
	}
	if err := b.DB.DeleteQuery(context.Background(), qs[0].ID); err != nil {
		t.Fatal(err)
	}

	man, err := b.Restore(r, size)
	if err != nil {
		t.Fatal(err)
	}
	if man.Application != "Ikigai DB" {
		t.Errorf("it restored %+v", man)
	}
	// The settings are the archive's again.
	sf, _, err := store.OpenSettings(b.Paths.SettingsFile())
	if err != nil {
		t.Fatal(err)
	}
	if got := sf.Get().Connections; len(got) != 1 || got[0].Name != "Orders" {
		t.Errorf("the settings hold %+v", got)
	}
	// And so is the database.
	db, err := localdb.Open(context.Background(), b.Paths.DatabaseFile())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if got, err := db.SavedQueries(context.Background()); err != nil || len(got) != 1 || got[0].Name != "totals" {
		t.Errorf("the database holds %+v (%v)", got, err)
	}
	// What was there is beside it, so a restore nobody meant can be undone.
	for _, p := range []string{b.Paths.SettingsFile(), b.Paths.DatabaseFile()} {
		if _, err := os.Stat(p + ".before-restore"); err != nil {
			t.Errorf("what was at %s was not kept: %v", filepath.Base(p), err)
		}
	}
}

// Something that is not one of these archives is refused, and nothing is
// touched.
func TestWhatIsNotAnArchiveIsRefused(t *testing.T) {
	b, _, _ := kept(t)
	junk := bytes.NewReader([]byte("this is not a zip"))
	if _, err := b.Restore(junk, int64(junk.Size())); err == nil {
		t.Error("it restored from something that is not an archive")
	}
	// A zip that is not ours: a zip all the same, without the manifest.
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	w, err := z.Create("something-else.txt")
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte("hello"))
	z.Close()
	r := bytes.NewReader(buf.Bytes())
	if _, err := b.Restore(r, r.Size()); err == nil || !strings.Contains(err.Error(), "not written by this application") {
		t.Errorf("it said %v", err)
	}
	if _, err := os.Stat(b.Paths.SettingsFile() + ".before-restore"); err == nil {
		t.Error("it moved what was there aside for an archive it refused")
	}
}

// An archive from a later version is refused rather than half read.
func TestAnArchiveFromLaterIsRefused(t *testing.T) {
	b, _, _ := kept(t)
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	if err := writeJSON(z, manifestName, Manifest{Application: "Ikigai DB", Version: backupVersion + 1}); err != nil {
		t.Fatal(err)
	}
	z.Close()
	r := bytes.NewReader(buf.Bytes())
	if _, err := b.Restore(r, r.Size()); err == nil || !strings.Contains(err.Error(), "later version") {
		t.Errorf("it said %v", err)
	}
}

// A backup taken before anything was saved is a backup of nothing, which
// is a true one: it holds the manifest and whatever was there.
func TestABackupOfNothing(t *testing.T) {
	dir := t.TempDir()
	b := Backup{Paths: store.Paths{Config: dir, Data: dir}}
	var buf bytes.Buffer
	if err := b.Write(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if len(z.File) != 1 || z.File[0].Name != manifestName {
		t.Errorf("it holds %d files", len(z.File))
	}
	// And restoring it leaves what is there, having nothing to put back.
	r := bytes.NewReader(buf.Bytes())
	if _, err := b.Restore(r, r.Size()); err != nil {
		t.Errorf("it said %v", err)
	}
}

// Restoring onto a machine with nothing on it puts both files there: a
// backup is how somebody moves to a new machine.
func TestRestoringOntoNothing(t *testing.T) {
	b, _, _ := kept(t)
	r, size := archived(t, b)

	fresh := t.TempDir()
	onto := Backup{Paths: store.Paths{Config: fresh, Data: fresh}}
	if _, err := onto.Restore(r, size); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{onto.Paths.SettingsFile(), onto.Paths.DatabaseFile()} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s was not put there: %v", filepath.Base(p), err)
		}
		if _, err := os.Stat(p + ".before-restore"); err == nil {
			t.Errorf("%s was kept aside, and there was nothing there", filepath.Base(p))
		}
	}
	sf, _, err := store.OpenSettings(onto.Paths.SettingsFile())
	if err != nil {
		t.Fatal(err)
	}
	if got := sf.Get().Connections; len(got) != 1 {
		t.Errorf("the settings hold %+v", got)
	}
}

// A restore that cannot finish leaves nothing half written: the files it
// had already staged are taken away again.
func TestAFailedRestoreStagesNothing(t *testing.T) {
	b, _, _ := kept(t)
	r, size := archived(t, b)

	// A config directory that works and a data directory that cannot: the
	// settings stage, and the database cannot.
	config := t.TempDir()
	blocked := filepath.Join(t.TempDir(), "in-the-way")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	onto := Backup{Paths: store.Paths{Config: config, Data: blocked}}
	if _, err := onto.Restore(r, size); err == nil {
		t.Fatal("it restored into a directory that is a file")
	}
	left, err := os.ReadDir(config)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range left {
		if strings.Contains(e.Name(), "restoring") {
			t.Errorf("it left %s behind", e.Name())
		}
	}
	if _, err := os.Stat(onto.Paths.SettingsFile()); err == nil {
		t.Error("it put the settings back although the restore failed")
	}
}
