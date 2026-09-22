package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/importer"
	"github.com/ikigai-db/ikigai-db/internal/store"
	"github.com/ikigai-db/ikigai-db/internal/store/secrets"
)

func exported(t *testing.T, raw []byte) importer.Set {
	t.Helper()
	var set importer.Set
	if err := json.Unmarshal(raw, &set); err != nil {
		t.Fatalf("the export is not JSON: %v", err)
	}
	return set
}

func saveOne(t *testing.T, f fixture, name string, secrets map[string]string) store.SavedConnection {
	t.Helper()
	conn := store.SavedConnection{
		Name: name, Driver: "fake", Host: "db.example.com", Port: 5432,
		Database: "orders", User: "jane", Environment: "production", ReadOnly: true,
	}
	saved, err := f.c.Create(conn, secrets)
	if err != nil {
		t.Fatal(err)
	}
	return saved
}

func TestAnExportedSetSaysWhatItIs(t *testing.T) {
	f := setup(t)
	saveOne(t, f, "Orders", nil)

	raw, err := f.c.ExportSet(nil, false)
	if err != nil {
		t.Fatalf("no export: %v", err)
	}
	set := exported(t, raw)
	if set.Kind != importer.SetKind || set.Version != importer.SetVersion {
		t.Errorf("it says it is %q version %d", set.Kind, set.Version)
	}
	if set.HasSecrets {
		t.Error("a set exported without secrets says it has them")
	}
	if set.Exported == "" {
		t.Error("it does not say when it was exported")
	}
	if len(set.Connections) != 1 {
		t.Fatalf("it exported %d connections", len(set.Connections))
	}
	c := set.Connections[0]
	// The things that decide what this program will let somebody do must
	// survive the trip.
	if c.Environment != "production" || !c.ReadOnly {
		t.Errorf("the guardrails were lost: %+v", c)
	}
}

// The whole point of the default. An exported set is a file that gets
// attached to messages.
func TestASetExportedWithoutSecretsContainsNoneAnywhere(t *testing.T) {
	f := setup(t)
	const secret = "s3cretpassword"
	saveOne(t, f, "Orders", map[string]string{"password": secret})

	raw, err := f.c.ExportSet(nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), secret) {
		t.Fatalf("the password is in the exported file:\n%s", raw)
	}
	// But the file still says one is needed, so an import can say so rather
	// than leaving somebody to find out at connect time.
	set := exported(t, raw)
	if got := set.Connections[0].Needs; len(got) != 1 || got[0] != "password" {
		t.Errorf("it does not say what is still needed: %v", got)
	}
	if len(set.Connections[0].Secrets) != 0 {
		t.Errorf("it carried secrets anyway: %v", set.Connections[0].Secrets)
	}
}

func TestASetExportedWithSecretsCarriesThemAndSaysSo(t *testing.T) {
	f := setup(t)
	const secret = "s3cretpassword"
	saveOne(t, f, "Orders", map[string]string{"password": secret})

	raw, err := f.c.ExportSet(nil, true)
	if err != nil {
		t.Fatal(err)
	}
	set := exported(t, raw)
	if !set.HasSecrets {
		t.Error("a set carrying secrets does not say so, which is how somebody sends it on")
	}
	if set.Connections[0].Secrets["password"] != secret {
		t.Errorf("it carried %v", set.Connections[0].Secrets)
	}
}

func TestOnlyTheChosenConnectionsAreExported(t *testing.T) {
	f := setup(t)
	one := saveOne(t, f, "Orders", nil)
	saveOne(t, f, "Shop", nil)

	raw, err := f.c.ExportSet([]string{one.ID}, false)
	if err != nil {
		t.Fatal(err)
	}
	set := exported(t, raw)
	if len(set.Connections) != 1 || set.Connections[0].Name != "Orders" {
		t.Errorf("it exported %+v", set.Connections)
	}
}

func TestASetGoesOutAndComesBackTheSame(t *testing.T) {
	from := setup(t)
	folder, err := from.c.CreateFolder("Work", "#ff0000")
	if err != nil {
		t.Fatal(err)
	}
	conn := saveOne(t, from, "Orders", map[string]string{"password": "s3cret"})
	if err := from.c.SetFolder(conn.ID, folder.ID); err != nil {
		t.Fatal(err)
	}

	raw, err := from.c.ExportSet(nil, true)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "connections.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	// Into a different installation, which shares no identifiers with the
	// first one.
	to := setup(t)
	found, err := importer.ReadSet(path)
	if err != nil {
		t.Fatalf("it could not be read back: %v", err)
	}
	if found[0].Note != "" {
		t.Errorf("a set that carries credentials said %q about itself", found[0].Note)
	}
	got, err := to.c.Import(found, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Saved) != 1 {
		t.Fatalf("it imported %d of 1: %+v", len(got.Saved), got)
	}

	back := got.Saved[0]
	if back.Name != "Orders" || back.Host != "db.example.com" || back.Port != 5432 ||
		back.User != "jane" || back.Environment != "production" || !back.ReadOnly {
		t.Errorf("it came back as %+v", back)
	}
	// The folder was made on the way in, and the connection is in it.
	folders := to.c.Folders()
	if len(folders) != 1 || folders[0].Name != "Work" {
		t.Fatalf("the folders are %+v", folders)
	}
	if back.Folder != folders[0].ID {
		t.Errorf("it is filed under %q and the folder is %q", back.Folder, folders[0].ID)
	}
	if pw, err := to.c.vault.Get(back.ID, "password"); err != nil || pw != "s3cret" {
		t.Errorf("the password came back as %q, %v", pw, err)
	}
}

func TestASetWithoutSecretsSaysWhatIsStillNeeded(t *testing.T) {
	from := setup(t)
	saveOne(t, from, "Orders", map[string]string{"password": "s3cret"})
	raw, err := from.c.ExportSet(nil, false)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "connections.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	found, err := importer.ReadSet(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 {
		t.Fatalf("it read %d entries", len(found))
	}
	if !strings.Contains(found[0].Note, "still needs password") {
		t.Errorf("it said %q", found[0].Note)
	}
	if len(found[0].Secrets) != 0 {
		t.Errorf("it invented credentials: %v", found[0].Secrets)
	}
}

// A set asked to carry credentials that cannot read one must not come back
// looking complete. Whoever it is sent to would find out instead.
func TestAnExportWhoseSecretsCannotBeReadFailsRatherThanLying(t *testing.T) {
	f := setup(t)
	saveOne(t, f, "Orders", map[string]string{"password": "s3cret"})

	// The same settings, reached through a keychain that will not open.
	sf, _, err := store.OpenSettings(f.path)
	if err != nil {
		t.Fatal(err)
	}
	locked := NewConnections(sf, NewVault(nil, secrets.ErrUnavailable), nil)

	raw, err := locked.ExportSet(nil, true)
	if err == nil {
		t.Fatalf("it exported a set with the credentials missing:\n%s", raw)
	}
	if !strings.Contains(err.Error(), "Orders") || !strings.Contains(err.Error(), "password") {
		t.Errorf("it said %v, which does not say which connection or which secret", err)
	}
	// Without them it is fine, because then it never claimed to have any.
	if _, err := locked.ExportSet(nil, false); err != nil {
		t.Errorf("a set without secrets needed the keychain: %v", err)
	}
}
