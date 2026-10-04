package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/store"
	"github.com/ikigai-db/ikigai-db/internal/store/localdb"
)

// savedIn puts a saved query in this run's local database, in a home of its own,
// and returns nothing: what the test needs is the environment, not a handle.
func savedIn(t *testing.T, name, body string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the home directory is found another way here")
	}
	t.Setenv("HOME", t.TempDir())
	paths, err := store.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.DatabaseFile()), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := localdb.Open(context.Background(), paths.DatabaseFile())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.SaveQuery(context.Background(),
		localdb.SavedQuery{Name: name, Body: body}); err != nil {
		t.Fatal(err)
	}
}

// A saved query is read by name, and the name is matched as somebody would
// type it rather than as it is stored.
func TestASavedQueryIsFoundByName(t *testing.T) {
	savedIn(t, "Monthly totals", "SELECT count(*) FROM orders")

	got, err := savedQuery(context.Background(), "  monthly TOTALS ")
	if err != nil {
		t.Fatalf("it was not found: %v", err)
	}
	if got != "SELECT count(*) FROM orders" {
		t.Errorf("the body reads as %q", got)
	}
	if _, err := savedQuery(context.Background(), "nothing of the sort"); err == nil ||
		!strings.Contains(err.Error(), `no saved query called "nothing of the sort"`) {
		t.Errorf("a name nobody saved says %v", err)
	}
}

// A run somebody interrupted stops here too.
//
// The local database is a file on a disk that can be slow, busy or locked by
// another copy of the application, so `ikigai query --saved x` followed by
// Ctrl-C has to give up rather than sit waiting for it. That means this reads
// with the command's context and not one of its own.
func TestAnInterruptedRunStopsBeforeReadingTheSavedQuery(t *testing.T) {
	savedIn(t, "Monthly totals", "SELECT count(*) FROM orders")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := savedQuery(ctx, "Monthly totals"); !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled run read the query anyway: %v", err)
	}
}
