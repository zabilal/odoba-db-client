package app

import (
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/importer"
	"github.com/ikigai-db/ikigai-db/internal/store"
)

func aFound(name, host string, port int, user, password string) importer.Found {
	return importer.Found{
		Connection: store.SavedConnection{
			Driver: "fake", Name: name, Host: host, Port: port,
			Database: "orders", User: user,
		},
		Secrets: map[string]string{"password": password},
		Where:   "~/.pgpass:1",
	}
}

func TestAnImportSavesWhatItWasGiven(t *testing.T) {
	f := setup(t)
	got, err := f.c.Import([]importer.Found{
		aFound("Orders", "db.example.com", 5432, "jane", "s3cret"),
		aFound("Shop", "shop.example.com", 5432, "bob", ""),
	}, true)
	if err != nil {
		t.Fatalf("the import failed: %v", err)
	}
	if len(got.Saved) != 2 {
		t.Fatalf("it saved %d of 2: %+v", len(got.Saved), got)
	}
	if len(f.c.List()) != 2 {
		t.Errorf("the settings hold %d connections", len(f.c.List()))
	}
	// The password went to the keychain, which is the only place it may go.
	pw, err := f.c.vault.Get(got.Saved[0].ID, "password")
	if err != nil || pw != "s3cret" {
		t.Errorf("the password came back as %q, %v", pw, err)
	}
}

func TestAnImportWithoutPasswordsBringsNone(t *testing.T) {
	f := setup(t)
	got, err := f.c.Import([]importer.Found{
		aFound("Orders", "db.example.com", 5432, "jane", "s3cret"),
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Saved) != 1 {
		t.Fatalf("it saved %d", len(got.Saved))
	}
	if names := got.Saved[0].Secrets; len(names) != 0 {
		t.Errorf("it recorded secrets %v for an import that was told to bring none", names)
	}
	if pw, _ := f.c.vault.Get(got.Saved[0].ID, "password"); pw != "" {
		t.Errorf("a password was kept anyway: %q", pw)
	}
}

func TestImportingTheSameFileTwiceDoesNotDoubleEverything(t *testing.T) {
	f := setup(t)
	entries := []importer.Found{aFound("Orders", "db.example.com", 5432, "jane", "s3cret")}

	if _, err := f.c.Import(entries, true); err != nil {
		t.Fatal(err)
	}
	// The same file again, which is what somebody does when they are not
	// sure the first one worked.
	got, err := f.c.Import(entries, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Saved) != 0 {
		t.Errorf("it saved %d connections the second time", len(got.Saved))
	}
	if len(got.Already) != 1 || got.Already[0] != "Orders" {
		t.Errorf("it did not say which were already there: %v", got.Already)
	}
	if n := len(f.c.List()); n != 1 {
		t.Errorf("there are now %d connections", n)
	}
}

func TestTheSameDatabaseUnderTwoNamesIsStillOneDatabase(t *testing.T) {
	f := setup(t)
	if _, err := f.c.Import([]importer.Found{
		aFound("Orders", "db.example.com", 5432, "jane", ""),
	}, false); err != nil {
		t.Fatal(err)
	}
	got, err := f.c.Import([]importer.Found{
		aFound("Production orders", "DB.example.com", 5432, "jane", ""),
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Saved) != 0 {
		t.Errorf("the same database came in again under another name: %+v", got.Saved)
	}
}

// A different account on the same server is a different connection, which is
// the whole point of a password file having more than one line for a host.
func TestADifferentAccountIsADifferentConnection(t *testing.T) {
	f := setup(t)
	if _, err := f.c.Import([]importer.Found{
		aFound("Orders", "db.example.com", 5432, "jane", ""),
	}, false); err != nil {
		t.Fatal(err)
	}
	got, err := f.c.Import([]importer.Found{
		aFound("Orders as bob", "db.example.com", 5432, "bob", ""),
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Saved) != 1 {
		t.Errorf("a second account was taken for the same connection: %+v", got)
	}
}

func TestWhatCouldNotBeBroughtIsReportedWithItsReason(t *testing.T) {
	f := setup(t)
	got, err := f.c.Import([]importer.Found{
		{Where: "~/.pgpass:4", Note: "this line matches any host, so it names a password but no server to connect to"},
		aFound("Orders", "db.example.com", 5432, "jane", ""),
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Saved) != 1 {
		t.Errorf("one bad entry cost %d good ones", 1-len(got.Saved))
	}
	if len(got.Left) != 1 {
		t.Fatalf("it left %d entries unexplained", len(got.Left))
	}
	if !strings.Contains(got.Left[0], "~/.pgpass:4") || !strings.Contains(got.Left[0], "no server") {
		t.Errorf("it said %q, which does not say which entry or why", got.Left[0])
	}
}

func TestAConnectionThatWillNotSaveDoesNotTakeTheOthersWithIt(t *testing.T) {
	f := setup(t)
	bad := aFound("Nonsense", "db.example.com", 5432, "jane", "")
	bad.Connection.Driver = "no-such-driver"

	got, err := f.c.Import([]importer.Found{
		bad,
		aFound("Orders", "other.example.com", 5432, "jane", ""),
	}, false)
	if err != nil {
		t.Fatalf("one unsaveable connection failed the whole import: %v", err)
	}
	if len(got.Saved) != 1 {
		t.Errorf("it saved %d of the 1 that could be saved", len(got.Saved))
	}
	if len(got.Left) != 1 || !strings.Contains(got.Left[0], "Nonsense") {
		t.Errorf("it did not say which one would not save: %v", got.Left)
	}
}

// The same entry twice in one file, which a password file with a duplicated
// line really does produce.
func TestTheSameConnectionTwiceInOneImportIsSavedOnce(t *testing.T) {
	f := setup(t)
	entry := aFound("Orders", "db.example.com", 5432, "jane", "s3cret")

	got, err := f.c.Import([]importer.Found{entry, entry}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Saved) != 1 {
		t.Errorf("it saved %d copies of one connection", len(got.Saved))
	}
	if len(got.Already) != 1 {
		t.Errorf("it did not say the second was the same: %v", got.Already)
	}
	if n := len(f.c.List()); n != 1 {
		t.Errorf("there are %d connections", n)
	}
}

func TestAFolderComesAcrossByNameAndIsMadeOnce(t *testing.T) {
	f := setup(t)
	one := aFound("Orders", "db.example.com", 5432, "jane", "")
	one.Folder = "Work"
	two := aFound("Shop", "shop.example.com", 5432, "bob", "")
	two.Folder = "work" // the same folder, spelt as somebody typed it

	got, err := f.c.Import([]importer.Found{one, two}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Saved) != 2 {
		t.Fatalf("it saved %d of 2: %+v", len(got.Saved), got)
	}
	folders := f.c.Folders()
	if len(folders) != 1 {
		t.Fatalf("it made %d folders for one name: %+v", len(folders), folders)
	}
	// And what is stored is the folder's ID, not its name.
	for _, saved := range got.Saved {
		if saved.Folder != folders[0].ID {
			t.Errorf("%s is filed under %q and the folder is %q", saved.Name, saved.Folder, folders[0].ID)
		}
	}
}

func TestAFolderThatAlreadyExistsIsUsedRatherThanDoubled(t *testing.T) {
	f := setup(t)
	existing, err := f.c.CreateFolder("Work", "#ff0000")
	if err != nil {
		t.Fatal(err)
	}
	entry := aFound("Orders", "db.example.com", 5432, "jane", "")
	entry.Folder = "Work"

	got, err := f.c.Import([]importer.Found{entry}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.c.Folders()) != 1 {
		t.Errorf("there are now %d folders called Work", len(f.c.Folders()))
	}
	if got.Saved[0].Folder != existing.ID {
		t.Errorf("it was filed under %q, not the folder that was there", got.Saved[0].Folder)
	}
}
