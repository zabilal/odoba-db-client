package importer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func pgpassAt(t *testing.T, body string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pgpass")
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAPasswordFileBringsItsPassword(t *testing.T) {
	path := pgpassAt(t, "db.example.com:5432:orders:jane:s3cret\n", 0o600)
	found, err := ReadPgpass(path)
	if err != nil {
		t.Fatalf("could not read it: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("it found %d entries", len(found))
	}
	f := found[0]
	if !f.OK() {
		t.Fatalf("it could not be brought: %s", f.Note)
	}
	if f.Connection.Driver != "postgres" || f.Connection.Host != "db.example.com" ||
		f.Connection.Port != 5432 || f.Connection.Database != "orders" || f.Connection.User != "jane" {
		t.Errorf("it read %+v", f.Connection)
	}
	if f.Password != "s3cret" {
		t.Errorf("the password is %q", f.Password)
	}
	if want := "jane@db.example.com/orders"; f.Connection.Name != want {
		t.Errorf("it is called %q, want %q", f.Connection.Name, want)
	}
	if !strings.HasSuffix(f.Where, ":1") {
		t.Errorf("it came from %q, which does not say which line", f.Where)
	}
}

func TestWhatAStarMeansInEachField(t *testing.T) {
	path := pgpassAt(t, strings.Join([]string{
		"db.example.com:*:orders:jane:p1",
		"db.example.com:5432:*:jane:p2",
		"db.example.com:5432:orders:*:p3",
		"*:5432:orders:jane:p4",
	}, "\n")+"\n", 0o600)
	found, err := ReadPgpass(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 4 {
		t.Fatalf("it found %d entries", len(found))
	}
	// Any port means the one PostgreSQL uses.
	if found[0].Connection.Port != 5432 {
		t.Errorf("a star port became %d", found[0].Connection.Port)
	}
	// Any database means none in particular.
	if found[1].Connection.Database != "" {
		t.Errorf("a star database became %q", found[1].Connection.Database)
	}
	if found[2].Connection.User != "" {
		t.Errorf("a star user became %q", found[2].Connection.User)
	}
	// Any host names a password but no server, so there is nothing to dial.
	if found[3].OK() {
		t.Errorf("a star host became a connection to %q", found[3].Connection.Host)
	}
	if !strings.Contains(found[3].Note, "no server") {
		t.Errorf("it said %q", found[3].Note)
	}
}

func TestAColonInsideAFieldIsNotASeparator(t *testing.T) {
	// PostgreSQL documents the backslash for exactly this.
	path := pgpassAt(t, `db.example.com:5432:orders:jane:pa\:ss\\word`+"\n", 0o600)
	found, err := ReadPgpass(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || !found[0].OK() {
		t.Fatalf("it found %d entries: %+v", len(found), found)
	}
	if want := `pa:ss\word`; found[0].Password != want {
		t.Errorf("the password is %q, want %q", found[0].Password, want)
	}
}

func TestCommentsAndBlankLinesAreNotConnections(t *testing.T) {
	path := pgpassAt(t, "# hostname:port:database:username:password\n\n   \n"+
		"db.example.com:5432:orders:jane:p\n", 0o600)
	found, err := ReadPgpass(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 {
		t.Fatalf("it found %d entries, and three of those lines are not connections", len(found))
	}
	if !strings.HasSuffix(found[0].Where, ":4") {
		t.Errorf("the line number is wrong in %q: blank lines still count", found[0].Where)
	}
}

func TestALineOfTheWrongShapeIsReportedNotDropped(t *testing.T) {
	// Too few, too many, and a port that is not one. The middle case is the
	// one somebody actually writes: a password with a colon in it that they
	// forgot to escape, which turns one line into six fields.
	path := pgpassAt(t, "db.example.com:5432:orders\n"+
		"db.example.com:5432:orders:jane:pa:ss\n"+
		"db.example.com:nope:orders:jane:p\n", 0o600)
	found, err := ReadPgpass(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 3 {
		t.Fatalf("it found %d entries, and all three lines should be accounted for", len(found))
	}
	if found[1].OK() || !strings.Contains(found[1].Note, "6 fields") {
		t.Errorf("an unescaped colon read as %+v (%s)", found[1].Connection, found[1].Note)
	}
	if found[0].OK() || !strings.Contains(found[0].Note, "3 fields") {
		t.Errorf("the short line said %q", found[0].Note)
	}
	if found[2].OK() || !strings.Contains(found[2].Note, "not a port") {
		t.Errorf("the bad port said %q", found[2].Note)
	}
}

func TestAPasswordFileOthersCanReadIsRefused(t *testing.T) {
	// libpq ignores it, so importing from it would be taking a password out
	// of a file PostgreSQL has already judged unsafe, and doing it quietly.
	path := pgpassAt(t, "db.example.com:5432:orders:jane:p\n", 0o644)
	_, err := ReadPgpass(path)
	if err == nil {
		t.Fatal("it read a world-readable password file")
	}
	if !strings.Contains(err.Error(), "chmod 0600") {
		t.Errorf("it said %v, which does not say what to do about it", err)
	}
}

func TestAPasswordFileIsLookedForWhereLibpqLooks(t *testing.T) {
	path := pgpassAt(t, "db.example.com:5432:orders:jane:p\n", 0o600)
	t.Setenv("PGPASSFILE", path)

	var got []string
	for _, c := range Scan() {
		got = append(got, c.Path)
	}
	if len(got) == 0 || got[0] != path {
		t.Errorf("PGPASSFILE was not followed: %v", got)
	}
}
