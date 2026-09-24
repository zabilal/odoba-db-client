package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// --- paths -------------------------------------------------------------------

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestPathsFollowEachPlatformsConvention(t *testing.T) {
	home := filepath.Join("/", "home", "ada")

	mac := pathsFor("darwin", home, env(nil))
	if want := filepath.Join(home, "Library", "Application Support", "Ikigai DB"); mac.Config != want || mac.Data != want {
		t.Errorf("darwin config/data = %s / %s", mac.Config, mac.Data)
	}
	if mac.Logs != filepath.Join(home, "Library", "Logs", "Ikigai DB") {
		t.Errorf("darwin logs = %s", mac.Logs)
	}

	win := pathsFor("windows", home, env(map[string]string{"APPDATA": "R", "LOCALAPPDATA": "L"}))
	if win.Config != filepath.Join("R", "Ikigai DB") {
		t.Errorf("windows config should roam: %s", win.Config)
	}
	if win.Data != filepath.Join("L", "Ikigai DB") || win.Logs != filepath.Join("L", "Ikigai DB", "Logs") {
		t.Errorf("windows data/logs should be machine-local: %s / %s", win.Data, win.Logs)
	}

	lin := pathsFor("linux", home, env(nil))
	if lin.Config != filepath.Join(home, ".config", "ikigai-db") ||
		lin.Data != filepath.Join(home, ".local", "share", "ikigai-db") ||
		lin.Logs != filepath.Join(home, ".local", "state", "ikigai-db", "logs") {
		t.Errorf("linux defaults = %+v", lin)
	}
}

func TestXDGOverridesAreHonouredOnlyWhenAbsolute(t *testing.T) {
	home := filepath.Join("/", "home", "ada")
	abs := filepath.Join("/", "xdg", "config")
	p := pathsFor("linux", home, env(map[string]string{"XDG_CONFIG_HOME": abs, "XDG_DATA_HOME": "relative/data"}))
	if p.Config != filepath.Join(abs, "ikigai-db") {
		t.Errorf("absolute XDG_CONFIG_HOME ignored: %s", p.Config)
	}
	// The spec says a relative value is invalid and must be ignored.
	if p.Data != filepath.Join(home, ".local", "share", "ikigai-db") {
		t.Errorf("relative XDG_DATA_HOME was used: %s", p.Data)
	}
}

func TestPortablePathsStayBesideTheExecutable(t *testing.T) {
	p := portablePaths(filepath.Join("/", "usb", "ikigai"))
	for _, d := range []string{p.Config, p.Data, p.Logs, p.Cache} {
		if !strings.HasPrefix(d, filepath.Join("/", "usb", "ikigai")) {
			t.Errorf("portable path escapes the executable's directory: %s", d)
		}
	}
	if !p.Portable {
		t.Error("Portable not set")
	}
}

func TestEnsureCreatesPrivateDirectories(t *testing.T) {
	root := t.TempDir()
	p := portablePaths(root)
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	for _, d := range []string{p.Config, p.Data, p.Logs, p.Cache} {
		st, err := os.Stat(d)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != 0o700 {
			t.Errorf("%s has mode %v, want 0700", d, st.Mode().Perm())
		}
	}
}

// --- settings ----------------------------------------------------------------

func conn(id, name string) SavedConnection {
	return SavedConnection{ID: id, Name: name, Driver: "postgres", Host: "db.local", Port: 5432,
		User: "ada", Environment: "dev", Secrets: []string{"password"}}
}

func openAt(t *testing.T) (*SettingsFile, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.json")
	f, _, err := OpenSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	return f, path
}

func TestMissingFileGivesDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	f, notice, err := OpenSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	if !notice.Created || f.Get().Version != CurrentSettingsVersion {
		t.Errorf("notice %+v, settings %+v", notice, f.Get())
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Error("opening must not create the file; it is written on the first change")
	}
}

func TestRoundTrip(t *testing.T) {
	f, path := openAt(t)
	if err := f.Update(func(s *Settings) error {
		s.Appearance = "dark"
		s.Connections = append(s.Connections, conn("c1", "Orders DB"))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	g, _, err := OpenSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	s := g.Get()
	if s.Appearance != "dark" || len(s.Connections) != 1 || s.Connections[0].Name != "Orders DB" {
		t.Errorf("round trip lost data: %+v", s)
	}
}

func TestSettingsFileIsPrivateAndLeavesNoTempFiles(t *testing.T) {
	f, path := openAt(t)
	for i := 0; i < 5; i++ {
		if err := f.Update(func(s *Settings) error { s.Editor.TabSize = i + 1; return nil }); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("atomic writes left debris: %v", names)
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
			t.Errorf("settings mode %v, want 0600", st.Mode().Perm())
		}
	}
}

func TestCorruptFileIsMovedAsideNotOverwritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	original := []byte(`{"version": 1, "connections": [ {"id": "c1", "name": "Pro`) // truncated
	os.WriteFile(path, original, 0o600)

	f, notice, err := OpenSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	if notice.Recovered == "" {
		t.Fatal("corrupt file was not reported as recovered")
	}
	kept, err := os.ReadFile(notice.Recovered)
	if err != nil || string(kept) != string(original) {
		t.Fatalf("the corrupt file was not preserved byte for byte: %v", err)
	}
	// The application carries on, and saving writes a fresh file without
	// touching the preserved one.
	if err := f.Update(func(s *Settings) error {
		s.Connections = append(s.Connections, conn("c2", "New"))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if again, _ := os.ReadFile(notice.Recovered); string(again) != string(original) {
		t.Error("the preserved corrupt file changed")
	}
}

func TestNewerVersionIsNeverOverwritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	future := []byte(`{"version": 99, "connections": [], "hologram_mode": true}`)
	os.WriteFile(path, future, 0o600)

	f, notice, err := OpenSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	if notice.Frozen == "" {
		t.Fatal("a newer file should open frozen")
	}
	if err := f.Update(func(s *Settings) error { s.Appearance = "dark"; return nil }); !errors.Is(err, ErrSettingsFrozen) {
		t.Errorf("Update = %v, want ErrSettingsFrozen", err)
	}
	if got, _ := os.ReadFile(path); string(got) != string(future) {
		t.Error("a newer version's settings were overwritten, losing fields this build does not know")
	}
}

func TestSecretsCannotBeSaved(t *testing.T) {
	f, path := openAt(t)
	for _, key := range []string{"password", "sslpassword", "api_key", "token"} {
		err := f.Update(func(s *Settings) error {
			c := conn("c-"+key, "X")
			c.Params = map[string]string{key: "hunter2"}
			s.Connections = append(s.Connections, c)
			return nil
		})
		if err == nil {
			t.Errorf("param %q was accepted", key)
		}
	}
	if data, err := os.ReadFile(path); err == nil && strings.Contains(string(data), "hunter2") {
		t.Fatal("a secret reached the settings file")
	}
	if len(f.Get().Connections) != 0 {
		t.Error("a rejected update changed the in-memory settings")
	}
}

func TestHandEditedSecretFreezesRatherThanSilentlyStripping(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	os.WriteFile(path, []byte(`{"version":1,"connections":[{"id":"a","name":"A","driver":"postgres",
		"params":{"password":"hunter2"},"tls":{}}]}`), 0o600)
	f, notice, err := OpenSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	if notice.Frozen == "" || !strings.Contains(notice.Frozen, "keychain") {
		t.Errorf("want a frozen notice pointing at the keychain, got %q", notice.Frozen)
	}
	if err := f.Update(func(*Settings) error { return nil }); !errors.Is(err, ErrSettingsFrozen) {
		t.Errorf("want frozen, got %v", err)
	}
}

func TestValidationRejectsBrokenConnections(t *testing.T) {
	cases := map[string]func(*Settings){
		"duplicate id": func(s *Settings) { s.Connections = []SavedConnection{conn("x", "A"), conn("x", "B")} },
		"no name":      func(s *Settings) { s.Connections = []SavedConnection{conn("x", " ")} },
		"no driver":    func(s *Settings) { c := conn("x", "A"); c.Driver = ""; s.Connections = []SavedConnection{c} },
		"bad port":     func(s *Settings) { c := conn("x", "A"); c.Port = 70000; s.Connections = []SavedConnection{c} },
		"bad environment": func(s *Settings) {
			c := conn("x", "A")
			c.Environment = "prod-ish"
			s.Connections = []SavedConnection{c}
		},
		"missing folder": func(s *Settings) { c := conn("x", "A"); c.Folder = "nope"; s.Connections = []SavedConnection{c} },
		"secret value in Secrets": func(s *Settings) {
			c := conn("x", "A")
			c.Secrets = []string{"Hunter2!"}
			s.Connections = []SavedConnection{c}
		},
	}
	for name, mut := range cases {
		f, _ := openAt(t)
		if err := f.Update(func(s *Settings) error { mut(s); return nil }); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestFailedUpdateChangesNothing(t *testing.T) {
	f, path := openAt(t)
	f.Update(func(s *Settings) error { s.Appearance = "light"; return nil })
	before, _ := os.ReadFile(path)

	boom := errors.New("boom")
	if err := f.Update(func(s *Settings) error { s.Appearance = "dark"; return boom }); !errors.Is(err, boom) {
		t.Fatalf("want the callback's error, got %v", err)
	}
	if f.Get().Appearance != "light" {
		t.Error("memory changed after a failed update")
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Error("disk changed after a failed update")
	}
}

func TestGetReturnsACopy(t *testing.T) {
	f, _ := openAt(t)
	f.Update(func(s *Settings) error { s.Connections = []SavedConnection{conn("a", "A")}; return nil })
	s := f.Get()
	s.Connections[0].Name = "mutated"
	s.Connections[0].Secrets[0] = "mutated"
	if f.Get().Connections[0].Name != "A" || f.Get().Connections[0].Secrets[0] != "password" {
		t.Error("Get shares memory with the store")
	}
}

func TestConcurrentUpdatesAreAllKept(t *testing.T) {
	f, path := openAt(t)
	var wg sync.WaitGroup
	for i := 0; i < 25; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := NewID()
			if err := f.Update(func(s *Settings) error {
				s.Connections = append(s.Connections, conn(id, "C "+id))
				return nil
			}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	var onDisk Settings
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, &onDisk); err != nil {
		t.Fatalf("file corrupted by concurrent updates: %v", err)
	}
	if len(onDisk.Connections) != 25 || len(f.Get().Connections) != 25 {
		t.Errorf("lost updates: %d on disk, %d in memory", len(onDisk.Connections), len(f.Get().Connections))
	}
}

func TestConnectionConfigResolvesSecretsLazily(t *testing.T) {
	c := conn("conn-42", "Prod")
	c.Environment, c.ReadOnly = "production", true
	c.TLS = TLS{Mode: "verify-full", CAFile: "/ca.pem"}

	var asked []string
	cfg := c.ConnectionConfig(func(id, key string) (string, error) {
		asked = append(asked, id+"/"+key)
		return "s3cret", nil
	})
	if len(asked) != 0 {
		t.Fatal("a secret was read before the driver asked for it")
	}
	if pw, _ := cfg.Secret("password"); pw != "s3cret" || asked[0] != "conn-42/password" {
		t.Errorf("secret lookup: %q via %v", pw, asked)
	}
	if !cfg.Guard.ReadOnly || cfg.Guard.Environment != source.EnvProduction {
		t.Errorf("guard not carried: %+v", cfg.Guard)
	}
	if cfg.TLS.Mode != "verify-full" || cfg.TLS.CAFile != "/ca.pem" {
		t.Errorf("TLS not carried: %+v", cfg.TLS)
	}
}

func TestNewIDIsUniqueAndOpaque(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		id := NewID()
		if seen[id] || len(id) != 24 {
			t.Fatalf("bad id %q", id)
		}
		seen[id] = true
	}
}

// A marker beside the executable is what makes a copy portable (FR-17.6).
func TestTheMarkerBesideTheExecutableMakesItPortable(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Skipf("no executable path on this platform: %v", err)
	}
	beside := filepath.Join(filepath.Dir(exe), portableMarker)
	if _, err := os.Stat(beside); err == nil {
		t.Skip("something already put the marker there")
	}
	// Without it, the platform's own directories.
	p, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if p.Portable {
		t.Fatalf("portable without the marker: %+v", p)
	}
	if err := os.WriteFile(beside, nil, 0o600); err != nil {
		t.Skipf("the executable's directory cannot be written to: %v", err)
	}
	defer os.Remove(beside)

	p, err = Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if !p.Portable {
		t.Fatalf("the marker was ignored: %+v", p)
	}
	for _, d := range []string{p.Config, p.Data, p.Logs, p.Cache} {
		if !strings.HasPrefix(d, filepath.Dir(exe)) {
			t.Errorf("%s is not beside the executable", d)
		}
	}
}
