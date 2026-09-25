package firebird

import (
	"errors"
	"net"
	"net/url"
	"strings"
	"syscall"
	"testing"

	fb "github.com/nakagami/firebirdsql"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

func kindOf(t *testing.T, err error) source.ConnectKind {
	t.Helper()
	var ce *source.ConnectError
	if !errors.As(err, &ce) {
		t.Fatalf("that was not a connect error: %v", err)
	}
	return ce.Kind
}

func hintOf(t *testing.T, err error) string {
	t.Helper()
	var ce *source.ConnectError
	if !errors.As(err, &ce) {
		t.Fatalf("that was not a connect error: %v", err)
	}
	return ce.Hint
}

func TestTheDescriptorIsWhatTheFormNeeds(t *testing.T) {
	desc := Driver{}.Describe()
	if desc.ID != driverID || desc.DefaultPort != 3050 {
		t.Errorf("it registers as %q on port %d", desc.ID, desc.DefaultPort)
	}
	if desc.Paradigm != model.ParadigmRelational {
		t.Errorf("it calls itself %v", desc.Paradigm)
	}
	fields := map[string]source.Field{}
	for _, f := range desc.Fields {
		fields[f.Key] = f
	}
	for _, key := range []string{"host", "port", "database", "user", "password", "role"} {
		if _, ok := fields[key]; !ok {
			t.Errorf("the form has no %s", key)
		}
	}
	if !fields["password"].Secret {
		t.Error("the password is not a secret, so it would be written to settings.json")
	}
	for _, key := range []string{"host", "port", "user", "role"} {
		if fields[key].Secret {
			t.Errorf("%s is a secret, so nobody could see what they typed", key)
		}
	}
	if !fields["database"].Required {
		t.Error("the database is optional, and there is nothing to connect to without it")
	}
	if !fields["host"].Required {
		t.Error("the host is optional, and there is nowhere to dial without it")
	}
}

// The language the capability names is one the editor knows, so that
// highlighting and the rules history is redacted by are this dialect's.
func TestTheQueryLanguageIsOneTheEditorKnows(t *testing.T) {
	lang := (&firebirdSource{}).Capabilities().Query.Language
	if !sqllex.Known(lang) {
		t.Errorf("it calls its language %q, which resolves to no dialect", lang)
	}
}

func TestWhatTheConnectionWouldDial(t *testing.T) {
	base := source.ConnectionConfig{Host: "db.example", Port: 3050, Database: "/var/db/x.fdb",
		User: "ikigai", Secret: func(string) (string, error) { return "pw", nil }}
	got, err := dsn(base)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"firebird://", "ikigai:pw@db.example:3050", "/var/db/x.fdb",
		"charset=UTF8", "wire_crypt=required"} {
		if !strings.Contains(got, want) {
			t.Errorf("it would dial %q, which lacks %q", got, want)
		}
	}
	if strings.Contains(got, "role=") {
		t.Errorf("it named a role nobody asked for: %s", got)
	}
}

func TestTheConnectionsDefaults(t *testing.T) {
	got, err := dsn(source.ConnectionConfig{Database: "x.fdb"})
	if err != nil {
		t.Fatal(err)
	}
	// Firebird's own defaults, so that a form filled in with only a database
	// connects to a server on this machine as the administrator it ships with.
	if !strings.Contains(got, "localhost:3050") {
		t.Errorf("it would dial %s", got)
	}
	if !strings.Contains(got, "SYSDBA") {
		t.Errorf("it would connect as nobody: %s", got)
	}
}

func TestARoleIsCarriedWhenOneIsAskedFor(t *testing.T) {
	got, err := dsn(source.ConnectionConfig{Database: "x.fdb", Params: map[string]string{"role": "READER"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "role=READER") {
		t.Errorf("it would dial %s", got)
	}
}

// A password with punctuation in it is escaped rather than ending the address
// early and sending half of itself somewhere else (NFR-S6).
func TestAPasswordWithPunctuationDoesNotEndTheAddress(t *testing.T) {
	const pw = "p@ss/word:with?bits#and&more"
	got, err := dsn(source.ConnectionConfig{Host: "db.example", Database: "x.fdb", User: "u",
		Secret: func(string) (string, error) { return pw, nil }})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, pw) {
		t.Errorf("the password went in as it was: %s", got)
	}
	// And it has to come back out as itself, or the server is handed a
	// password nobody typed. The library reads the address with net/url, so
	// that is what reads it back here.
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("what it built does not parse: %v", err)
	}
	back, ok := u.User.Password()
	if !ok || back != pw {
		t.Errorf("the password came back as %q", back)
	}
	if u.Host != "db.example:3050" {
		t.Errorf("the address is %q, so part of the password became one", u.Host)
	}
	if u.Path != "/x.fdb" {
		t.Errorf("the database is %q", u.Path)
	}
}

func TestADatabaseIsNeededBeforeAnythingIsDialled(t *testing.T) {
	for name, path := range map[string]string{"nothing at all": "", "only spaces": "   "} {
		t.Run(name, func(t *testing.T) {
			_, err := dsn(source.ConnectionConfig{Host: "db.example", Database: path})
			if err == nil {
				t.Fatal("it found something to open")
			}
			if kindOf(t, err) != source.ConnectConfig {
				t.Errorf("it called that %v", kindOf(t, err))
			}
		})
	}
}

// Firebird does not speak TLS. Encryption of its own is on by default and
// never silently given up; a mode asking for a certificate to be checked is
// refused, because there is no certificate to check (NFR-S3).
func TestHowTheWireIsProtected(t *testing.T) {
	for name, c := range map[string]struct {
		mode string
		want string
		says string
	}{
		"by default":            {"", "required", ""},
		"asked for":             {"require", "required", ""},
		"turned off on purpose": {"disable", "disabled", ""},
		"verified":              {"verify-full", "", "no certificate"},
		"verified to a CA":      {"verify-ca", "", "no certificate"},
		"something else":        {"encrypt-ish", "", "not a way to protect"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := wireCrypt(source.TLSConfig{Mode: c.mode})
			if c.want == "" {
				if err == nil {
					t.Fatalf("%q was accepted as %q", c.mode, got)
				}
				if !strings.Contains(hintOf(t, err), c.says) {
					t.Errorf("it says %q, which does not mention %q", hintOf(t, err), c.says)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("%q becomes %q, want %q", c.mode, got, c.want)
			}
		})
	}
}

// The default is encryption, and a connection nobody said anything about must
// not be plain text. Held against the string that reaches the library, which
// is the only thing that decides it.
func TestTheWireIsEncryptedUnlessSomebodySaidOtherwise(t *testing.T) {
	got, err := dsn(source.ConnectionConfig{Database: "x.fdb"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "wire_crypt=required") {
		t.Errorf("a connection nobody configured would dial %s", got)
	}
}

func TestAFailureToConnectIsCalledWhatItIs(t *testing.T) {
	for name, c := range map[string]struct {
		err  error
		want source.ConnectKind
	}{
		"credentials refused": {&fb.FbError{GDSCodes: []int{gdsLogin},
			Message: "Your user name and password are not defined."}, source.ConnectAuth},
		"no database there": {&fb.FbError{GDSCodes: []int{gdsIOError, gdsFileNotFound},
			Message: `I/O error during "open" operation`}, source.ConnectNoDatabase},
		// The general failure comes first in Firebird's status vector and the
		// reason after it, so the whole vector is read and not its head.
		"the reason behind a general failure": {&fb.FbError{GDSCodes: []int{335544721, gdsLogin}},
			source.ConnectAuth},
		"no such host":     {&net.OpError{Err: &net.DNSError{}}, source.ConnectUnreachable},
		"nothing there":    {&net.OpError{Err: syscall.ECONNREFUSED}, source.ConnectRefused},
		"nothing in time":  {&net.OpError{Err: timeoutError{}}, source.ConnectUnreachable},
		"will not encrypt": {errors.New("firebirdsql: wire_crypt=required but no wire encryption was established"), source.ConnectTLS},
		"something else":   {errors.New("the wheels came off"), source.ConnectUnknown},
	} {
		t.Run(name, func(t *testing.T) {
			got := classifyConnectError(c.err)
			if k := kindOf(t, got); k != c.want {
				t.Errorf("it called that %v, want %v", k, c.want)
			}
			if !errors.Is(got, c.err) {
				t.Error("it dropped what the server said")
			}
		})
	}
	// A refusal this driver already made is not classified again: the hint it
	// carries is more precise than anything read from an error's text.
	mine := &source.ConnectError{Kind: source.ConnectConfig, Hint: "Name the database."}
	if got := classifyConnectError(mine); got != error(mine) {
		t.Errorf("it rewrote its own refusal as %v", got)
	}
}

// timeoutError is a net.Error that timed out, which is the only thing about
// it the classification asks.
type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

// The tree hangs everything under a name somebody recognises: the database's
// file name, not the whole path it happens to sit at on the server.
func TestTheDatabaseIsNamedByItsFile(t *testing.T) {
	for path, want := range map[string]string{
		"/var/lib/firebird/data/ikigai.fdb": "ikigai.fdb",
		`C:\data\ikigai.fdb`:                "ikigai.fdb",
		"employee":                          "employee", // an alias the server knows
		"":                                  "database",
	} {
		s := &firebirdSource{path: path}
		if got := s.database(); got != want {
			s := s
			t.Errorf("%q shows as %q, want %q (%v)", path, got, want, s.path)
		}
	}
}

// The version decides one thing: whether a clause that arrived in Firebird 4
// may be written. An engine that did not say is treated as older, which
// refuses rather than writing SQL it cannot parse.
func TestWhatTheVersionDecides(t *testing.T) {
	for version, want := range map[string]string{
		"5.0.4":   "DEFAULT VALUES",
		"4.0.0":   "DEFAULT VALUES",
		"3.0.10":  "",
		"2.5.9":   "",
		"":        "",
		"unknown": "",
	} {
		s := &firebirdSource{version: version}
		if got := s.defaultRow(); got != want {
			t.Errorf("version %q writes a row of defaults as %q, want %q", version, got, want)
		}
	}
}

// A trigger says when it fires and on what. Firebird encodes both in one
// number, twice over: one way for a single event and another for the
// multi-event triggers of 2.1 and later.
func TestWhenATriggerFires(t *testing.T) {
	for name, c := range map[string]struct {
		typ    int64
		timing string
		events []string
	}{
		"before insert": {1, "BEFORE", []string{"INSERT"}},
		"after insert":  {2, "AFTER", []string{"INSERT"}},
		"before update": {3, "BEFORE", []string{"UPDATE"}},
		"after update":  {4, "AFTER", []string{"UPDATE"}},
		"before delete": {5, "BEFORE", []string{"DELETE"}},
		"after delete":  {6, "AFTER", []string{"DELETE"}},
		// 8192 + (1 << 1) is a BEFORE on INSERT alone, in the later encoding.
		"before insert, the new way":   {8194, "BEFORE", []string{"INSERT"}},
		"after insert, the new way":    {8195, "AFTER", []string{"INSERT"}},
		"before insert or update":      {8194 + (2 << 3), "BEFORE", []string{"INSERT", "UPDATE"}},
		"after insert, update, delete": {8195 + (2 << 3) + (3 << 5), "AFTER", []string{"INSERT", "UPDATE", "DELETE"}},
		// A database or DDL trigger, which is not on a table and never
		// reaches this: nothing is better than a guess.
		"something else": {7, "", nil},
		"nothing":        {0, "", nil},
	} {
		t.Run(name, func(t *testing.T) {
			timing, events := triggerTiming(c.typ)
			if timing != c.timing {
				t.Errorf("it fires %q, want %q", timing, c.timing)
			}
			if strings.Join(events, ",") != strings.Join(c.events, ",") {
				t.Errorf("on %v, want %v", events, c.events)
			}
		})
	}
}

// The catalogue's own objects are left out of the tree, by the flag that
// marks them and by their names.
func TestTheEnginesOwnObjectsAreLeftOut(t *testing.T) {
	got := userIn("r", "RDB$RELATION_NAME")
	for _, want := range []string{"COALESCE(r.RDB$SYSTEM_FLAG, 0) = 0",
		"r.RDB$RELATION_NAME NOT STARTING WITH 'RDB$'",
		"NOT STARTING WITH 'MON$'", "NOT STARTING WITH 'SEC$'"} {
		if !strings.Contains(got, want) {
			t.Errorf("the test reads\n%s\nwhich lacks %q", got, want)
		}
	}
}
