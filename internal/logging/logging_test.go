package logging

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func readAll(t *testing.T, dir string) string {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	var sb strings.Builder
	for _, e := range entries {
		b, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		sb.Write(b)
	}
	return sb.String()
}

type dbConfig struct {
	Host     string
	Password string
}

func TestSecretsNeverReachTheLogFile(t *testing.T) {
	dir := t.TempDir()
	log, closer, err := New(dir, Options{Level: slog.LevelDebug})
	if err != nil {
		t.Fatal(err)
	}
	const pw = "hunter2-xyzzy"

	log.Info("connecting to postgres://ada:" + pw + "@db.local/sales")
	log.Info("connect", "dsn", "postgres://ada:"+pw+"@db.local/sales")
	log.Info("connect", "password", pw)
	log.Info("connect", "api_key", pw)
	log.Warn("failed", "err", errors.New("dial failed: password="+pw))
	log.Info("cfg", "config", dbConfig{Host: "db.local", Password: pw})
	log.Info("cfg", "config", &dbConfig{Host: "db.local", Password: pw})
	log.Info("grouped", slog.Group("conn", slog.String("dsn", "mysql://root:"+pw+"@h/d")))
	log.With("dsn", "postgres://ada:"+pw+"@db.local/x").Info("later line")
	log.Info("stringer", "value", stringer("token="+pw))
	closer.Close()

	out := readAll(t, dir)
	if strings.Contains(out, pw) {
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, pw) {
				t.Errorf("secret leaked: %s", line)
			}
		}
	}
	// Redaction must not erase what makes a log useful.
	for _, keep := range []string{"db.local", "connecting to", "dial failed", "later line"} {
		if !strings.Contains(out, keep) {
			t.Errorf("over-redacted: %q missing", keep)
		}
	}
}

type stringer string

func (s stringer) String() string { return string(s) }

func TestRotationBoundsDiskUse(t *testing.T) {
	dir := t.TempDir()
	log, closer, err := New(dir, Options{MaxBytes: 1024, Keep: 2})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 500; i++ {
		log.Info("line", "n", i, "pad", strings.Repeat("x", 40))
	}
	closer.Close()

	entries, _ := os.ReadDir(dir)
	var names []string
	var total int64
	for _, e := range entries {
		names = append(names, e.Name())
		info, _ := e.Info()
		total += info.Size()
	}
	if len(entries) != 3 { // ikigai.log, .1, .2
		t.Errorf("want 3 files with Keep=2, got %v", names)
	}
	if total > 3*(1024+200) {
		t.Errorf("logs use %d bytes; rotation is not bounding them", total)
	}
	// The newest line is in the current file.
	cur, _ := os.ReadFile(filepath.Join(dir, FileName))
	if !strings.Contains(string(cur), `"n":499`) {
		t.Error("the newest line is not in the current log file")
	}
}

func TestReopeningAppends(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 2; i++ {
		log, closer, err := New(dir, Options{})
		if err != nil {
			t.Fatal(err)
		}
		log.Info(fmt.Sprintf("run %d", i))
		closer.Close()
	}
	out, _ := os.ReadFile(filepath.Join(dir, FileName))
	if !strings.Contains(string(out), "run 0") || !strings.Contains(string(out), "run 1") {
		t.Error("a restart truncated the log instead of appending")
	}
}

func TestLevelFilters(t *testing.T) {
	dir := t.TempDir()
	log, closer, _ := New(dir, Options{Level: slog.LevelWarn})
	log.Info("quiet")
	log.Warn("loud")
	closer.Close()
	out := readAll(t, dir)
	if strings.Contains(out, "quiet") || !strings.Contains(out, "loud") {
		t.Errorf("level filtering wrong: %s", out)
	}
}

func TestLogFileIsPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	dir := t.TempDir()
	log, closer, _ := New(dir, Options{})
	log.Info("x")
	closer.Close()
	st, _ := os.Stat(filepath.Join(dir, FileName))
	if st.Mode().Perm() != 0o600 {
		t.Errorf("log mode %v, want 0600", st.Mode().Perm())
	}
}

func TestWriteAfterCloseFailsCleanly(t *testing.T) {
	dir := t.TempDir()
	_, closer, _ := New(dir, Options{})
	closer.Close()
	if err := closer.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}
