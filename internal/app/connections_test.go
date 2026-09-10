package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/store"
	"github.com/ikigai-db/ikigai-db/internal/store/secrets"
)

type fixture struct {
	c    *Connections
	kc   *secrets.Memory
	path string
}

func setup(t *testing.T) fixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.json")
	sf, _, err := store.OpenSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	kc := secrets.NewMemory()
	return fixture{c: NewConnections(sf, NewVault(kc, nil), nil), kc: kc, path: path}
}

func draft(name string) store.SavedConnection {
	return store.SavedConnection{Name: name, Driver: "fake", Host: "db.local", User: "ada", Environment: "dev"}
}

func TestCreateKeepsSecretsOutOfTheSettingsFile(t *testing.T) {
	f := setup(t)
	conn, err := f.c.Create(draft("Orders"), map[string]string{"password": "hunter2"})
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := f.kc.Get(conn.ID, "password"); v != "hunter2" {
		t.Error("secret not in the keychain")
	}
	if len(conn.Secrets) != 1 || conn.Secrets[0] != "password" {
		t.Errorf("secret names = %v", conn.Secrets)
	}
	data, _ := os.ReadFile(f.path)
	if strings.Contains(string(data), "hunter2") {
		t.Fatal("the password reached the settings file")
	}
}

func TestCreateRefusesUnknownDriverAndWritesNothing(t *testing.T) {
	f := setup(t)
	d := draft("X")
	d.Driver = "nonesuch"
	if _, err := f.c.Create(d, map[string]string{"password": "p"}); err == nil {
		t.Fatal("unknown driver accepted")
	}
	if len(f.c.List()) != 0 {
		t.Error("a connection was saved")
	}
}

func TestFailedCreateLeavesNoOrphanedSecret(t *testing.T) {
	f := setup(t)
	d := draft("X")
	d.Environment = "not-an-environment" // settings validation will refuse it
	if _, err := f.c.Create(d, map[string]string{"password": "hunter2"}); err == nil {
		t.Fatal("invalid connection accepted")
	}
	// The ID is gone with the failed create, so check the keychain is empty.
	if n := countSecrets(f.kc); n != 0 {
		t.Errorf("%d secrets orphaned in the keychain", n)
	}
}

func countSecrets(kc *secrets.Memory) int {
	// Memory has no listing; probe through a reflection-free round trip.
	return memLen(kc)
}

func TestUpdateSecretSemantics(t *testing.T) {
	f := setup(t)
	conn, _ := f.c.Create(draft("A"), map[string]string{"password": "old", "ssh_passphrase": "pp"})

	// Absent keys are kept; set keys change; cleared keys go.
	if err := f.c.Update(conn, SecretEdit{Set: map[string]string{"password": "new"}}); err != nil {
		t.Fatal(err)
	}
	if v, _ := f.kc.Get(conn.ID, "password"); v != "new" {
		t.Errorf("password = %q", v)
	}
	if v, _ := f.kc.Get(conn.ID, "ssh_passphrase"); v != "pp" {
		t.Error("an untouched secret changed")
	}
	cur, _ := f.c.Get(conn.ID)
	if err := f.c.Update(cur, SecretEdit{Clear: []string{"ssh_passphrase"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.kc.Get(conn.ID, "ssh_passphrase"); !errors.Is(err, secrets.ErrNotFound) {
		t.Error("a cleared secret survived")
	}
	if cur, _ := f.c.Get(conn.ID); len(cur.Secrets) != 1 || cur.Secrets[0] != "password" {
		t.Errorf("names after clear = %v", cur.Secrets)
	}
}

func TestFailedUpdateRestoresThePreviousSecret(t *testing.T) {
	f := setup(t)
	conn, _ := f.c.Create(draft("A"), map[string]string{"password": "original"})
	conn.Environment = "bogus"
	if err := f.c.Update(conn, SecretEdit{Set: map[string]string{"password": "replacement"}}); err == nil {
		t.Fatal("invalid update accepted")
	}
	if v, _ := f.kc.Get(conn.ID, "password"); v != "original" {
		t.Errorf("a failed update changed the password to %q", v)
	}
}

func TestDuplicateCopiesSecretsAndPlacesAfterOriginal(t *testing.T) {
	f := setup(t)
	a, _ := f.c.Create(draft("A"), map[string]string{"password": "pa"})
	f.c.Create(draft("B"), nil)
	dup, err := f.c.Duplicate(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	list := f.c.List()
	if len(list) != 3 || list[1].ID != dup.ID || dup.Name != "A copy" {
		t.Errorf("order/name wrong: %v", names(list))
	}
	if v, _ := f.kc.Get(dup.ID, "password"); v != "pa" {
		t.Error("secret not copied")
	}
	// Changing the copy's secret must not change the original's.
	f.c.Update(dup, SecretEdit{Set: map[string]string{"password": "pdup"}})
	if v, _ := f.kc.Get(a.ID, "password"); v != "pa" {
		t.Error("the copy shares the original's secret")
	}
}

func names(cs []store.SavedConnection) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Name)
	}
	return out
}

func TestDeleteRemovesSecrets(t *testing.T) {
	f := setup(t)
	a, _ := f.c.Create(draft("A"), map[string]string{"password": "pa"})
	if err := f.c.Delete(a.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.c.Get(a.ID); ok {
		t.Error("connection still listed")
	}
	if _, err := f.kc.Get(a.ID, "password"); !errors.Is(err, secrets.ErrNotFound) {
		t.Error("secret left behind")
	}
}

func TestReorderRequiresAPermutation(t *testing.T) {
	f := setup(t)
	a, _ := f.c.Create(draft("A"), nil)
	b, _ := f.c.Create(draft("B"), nil)
	c, _ := f.c.Create(draft("C"), nil)
	if err := f.c.Reorder([]string{c.ID, a.ID, b.ID}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(names(f.c.List()), ","); got != "C,A,B" {
		t.Errorf("order = %s", got)
	}
	for _, bad := range [][]string{{a.ID, b.ID}, {a.ID, a.ID, b.ID}, {a.ID, b.ID, "nope"}} {
		if err := f.c.Reorder(bad); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}
	if len(f.c.List()) != 3 {
		t.Error("a refused reorder lost connections")
	}
}

func TestDeletingAFolderKeepsItsConnections(t *testing.T) {
	f := setup(t)
	fo, err := f.c.CreateFolder("Production", "red")
	if err != nil {
		t.Fatal(err)
	}
	a, _ := f.c.Create(draft("A"), nil)
	if err := f.c.SetFolder(a.ID, fo.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.c.DeleteFolder(fo.ID); err != nil {
		t.Fatal(err)
	}
	cur, ok := f.c.Get(a.ID)
	if !ok || cur.Folder != "" {
		t.Errorf("connection lost or still in the deleted folder: %+v", cur)
	}
	if err := f.c.SetFolder(a.ID, "no-such-folder"); err == nil {
		t.Error("moved into a folder that does not exist")
	}
}

func TestTestConnectionReportsWhatToFix(t *testing.T) {
	f := setup(t)
	ok := f.c.Test(context.Background(), draft("A"), nil)
	if !ok.OK || ok.Server.Product != "Fake" {
		t.Errorf("success: %+v", ok)
	}

	for host, want := range map[string]source.ConnectKind{
		"unreachable": source.ConnectUnreachable,
		"badauth":     source.ConnectAuth,
	} {
		d := draft("A")
		d.Host = host
		r := f.c.Test(context.Background(), d, map[string]string{"password": "hunter2"})
		if r.OK || r.Kind != want || r.Hint == "" {
			t.Errorf("%s: %+v", host, r)
		}
		if strings.Contains(r.Detail, "hunter2") || strings.Contains(r.Hint, "hunter2") {
			t.Errorf("%s: the password leaked into the result: %+v", host, r)
		}
	}
}

func TestTestUsesTypedSecretThenVault(t *testing.T) {
	f := setup(t)
	conn, _ := f.c.Create(draft("A"), map[string]string{"password": "saved"})

	f.c.Test(context.Background(), conn, nil)
	if lastPassword.Load() != "saved" {
		t.Errorf("did not fall back to the vault: %v", lastPassword.Load())
	}
	f.c.Test(context.Background(), conn, map[string]string{"password": "typed"})
	if lastPassword.Load() != "typed" {
		t.Errorf("typed secret did not win: %v", lastPassword.Load())
	}
}

func TestSessionOnlyVaultAsksAgainAfterRestart(t *testing.T) {
	// No keychain: the password lives for the session, and a restart has to
	// ask for it rather than fail mysteriously.
	path := filepath.Join(t.TempDir(), "settings.json")
	sf, _, _ := store.OpenSettings(path)
	c1 := NewConnections(sf, NewVault(nil, secrets.ErrUnavailable), nil)
	if c1.Vault().Persistent() {
		t.Fatal("vault without a keychain claims to persist")
	}
	conn, _ := c1.Create(draft("A"), map[string]string{"password": "p"})
	if len(c1.MissingSecrets(conn.ID)) != 0 {
		t.Error("the secret should be available this session")
	}

	sf2, _, _ := store.OpenSettings(path) // "restart"
	c2 := NewConnections(sf2, NewVault(nil, secrets.ErrUnavailable), nil)
	missing := c2.MissingSecrets(conn.ID)
	if len(missing) != 1 || missing[0] != "password" {
		t.Errorf("missing = %v", missing)
	}
	var mse *MissingSecretsError
	if _, err := c2.Open(context.Background(), conn.ID, MonitorConfig{}); !errors.As(err, &mse) {
		t.Errorf("Open should ask for the password first, got %v", err)
	}
	c2.Vault().SetSession(conn.ID, "password", "p")
	live, err := c2.Open(context.Background(), conn.ID, MonitorConfig{})
	if err != nil {
		t.Fatalf("Open after supplying the password: %v", err)
	}
	live.Close()
}

func TestSavedConnectionWithoutPasswordOpens(t *testing.T) {
	// Trust or peer authentication: no password exists. The driver still asks
	// for one, and "none saved" must mean empty, not a keychain failure. Every
	// earlier test saved a password, which is how this was missed.
	f := setup(t)
	conn, err := f.c.Create(draft("Local"), nil)
	if err != nil {
		t.Fatal(err)
	}
	live, err := f.c.Open(context.Background(), conn.ID, fast)
	if err != nil {
		t.Fatalf("a connection with no password failed to open: %v", err)
	}
	live.Close()
	if lastPassword.Load() != "" {
		t.Errorf("driver received %q, want an empty password", lastPassword.Load())
	}
}
