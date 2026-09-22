package importer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func myCnfAt(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "my.cnf")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAnOptionFileDescribesOneClient(t *testing.T) {
	path := myCnfAt(t, `# a comment
; another
[client]
host = db.example.com
port = 3307
user = jane
password = "s3:cret"
database = orders

[mysqldump]
user = someone-else
`)
	found, err := ReadMyCnf(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 {
		t.Fatalf("an option file describes one client and it found %d", len(found))
	}
	c := found[0].Connection
	if c.Driver != "mysql" || c.Host != "db.example.com" || c.Port != 3307 ||
		c.User != "jane" || c.Database != "orders" {
		t.Errorf("it read %+v", c)
	}
	// Quotes are the file's, not the password's.
	if found[0].Secrets["password"] != "s3:cret" {
		t.Errorf("the password is %q", found[0].Secrets["password"])
	}
}

func TestTheClientSectionIsReadUnderTheMysqlOne(t *testing.T) {
	// [client] applies to every MySQL program and [mysql] to the command
	// line client, which is the order the tools read them in.
	path := myCnfAt(t, "[client]\nhost = everyone.example.com\nuser = shared\n"+
		"[mysql]\nhost = just-mysql.example.com\n")
	found, err := ReadMyCnf(path)
	if err != nil {
		t.Fatal(err)
	}
	c := found[0].Connection
	if c.Host != "just-mysql.example.com" {
		t.Errorf("the host is %q, and [mysql] should have won", c.Host)
	}
	if c.User != "shared" {
		t.Errorf("the user is %q, and [client] should still have been read", c.User)
	}
}

func TestAnOptionNameIsReadHoweverItIsSpelt(t *testing.T) {
	// MySQL treats an underscore and a dash in an option name as the same.
	path := myCnfAt(t, "[client]\nhost=db.example.com\nno-auto-rehash\n!include /etc/other.cnf\n")
	found, err := ReadMyCnf(path)
	if err != nil {
		t.Fatal(err)
	}
	if !found[0].OK() || found[0].Connection.Host != "db.example.com" {
		t.Errorf("it read %+v (%s)", found[0].Connection, found[0].Note)
	}
}

func TestASocketConnectionIsSaidRatherThanQuietlyChanged(t *testing.T) {
	path := myCnfAt(t, "[client]\nsocket = /tmp/mysql.sock\nuser = jane\npassword = p\n")
	found, err := ReadMyCnf(path)
	if err != nil {
		t.Fatal(err)
	}
	c := found[0].Connection
	if c.Host != "localhost" {
		t.Errorf("the host is %q", c.Host)
	}
	if !strings.Contains(found[0].Note, "/tmp/mysql.sock") || !strings.Contains(found[0].Note, "TCP") {
		t.Errorf("it said %q, which does not say the socket was not brought", found[0].Note)
	}
}

func TestAnOptionFileWithNothingForAClientSaysSo(t *testing.T) {
	path := myCnfAt(t, "[mysqld]\nport = 3306\n")
	found, err := ReadMyCnf(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].OK() {
		t.Fatalf("it made a connection out of a server's own settings: %+v", found)
	}
	if !strings.Contains(found[0].Note, "[client]") {
		t.Errorf("it said %q", found[0].Note)
	}
}
