package commands

import (
	"errors"
	"strings"
	"testing"
)

func cmd(id, cat, title string, sc Shortcut) Command {
	return Command{ID: id, Category: cat, Title: title, Shortcut: sc, Run: func() {}}
}

func TestShortcutLabelsArePlatformCorrect(t *testing.T) {
	s := Shortcut{Key: "K", Mods: ModShortcut | ModShift}
	if got := s.Label("darwin"); got != "⇧⌘K" {
		t.Errorf("darwin: %q", got)
	}
	if got := s.Label("windows"); got != "Ctrl+Shift+K" {
		t.Errorf("windows: %q", got)
	}
	run := Shortcut{Key: "Return", Mods: ModShortcut}
	if run.Label("darwin") != "⌘↩" || run.Label("linux") != "Ctrl+Enter" {
		t.Errorf("run: %q / %q", run.Label("darwin"), run.Label("linux"))
	}
	if (Shortcut{Key: "Slash", Mods: ModShortcut}).Label("linux") != "Ctrl+/" {
		t.Error("punctuation keys should render as the character")
	}
	// macOS writes modifiers in a fixed order: ⌃ ⌥ ⇧ ⌘.
	all := Shortcut{Key: "P", Mods: ModShortcut | ModShift | ModAlt | ModControl}
	if all.Label("darwin") != "⌃⌥⇧⌘P" {
		t.Errorf("modifier order: %q", all.Label("darwin"))
	}
}

func TestRegisterRefusesMistakes(t *testing.T) {
	r := NewRegistry()
	r.MustRegister(cmd("a", "", "A", Shortcut{Key: "K", Mods: ModShortcut}))

	bad := map[string]Command{
		"no id":      {Title: "X", Run: func() {}},
		"no title":   {ID: "x", Run: func() {}},
		"no run":     {ID: "x", Title: "X"},
		"duplicate":  cmd("a", "", "Again", Shortcut{}),
		"same chord": cmd("b", "", "B", Shortcut{Key: "k", Mods: ModShortcut}), // case of key ignored
	}
	for name, c := range bad {
		if err := r.Register(c); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestConflictOnAnyPlatformIsRefusedOnEvery(t *testing.T) {
	// ⌘K and ⌃K are different keys on a Mac, but both Ctrl+K elsewhere.
	r := NewRegistry()
	r.MustRegister(cmd("a", "", "A", Shortcut{Key: "K", Mods: ModShortcut}))
	err := r.Register(cmd("b", "", "B", Shortcut{Key: "K", Mods: ModControl}))
	if err == nil || !strings.Contains(err.Error(), "linux") {
		t.Errorf("want a conflict reported for non-macOS platforms, got %v", err)
	}
	// Distinct everywhere: fine.
	if err := r.Register(cmd("c", "", "C", Shortcut{Key: "K", Mods: ModShortcut | ModShift})); err != nil {
		t.Error(err)
	}
}

func TestRunRespectsEnabled(t *testing.T) {
	r := NewRegistry()
	ran := 0
	on := false
	r.MustRegister(Command{ID: "x", Title: "X", Run: func() { ran++ }, Enabled: func() bool { return on }})
	if err := r.Run("x"); !errors.Is(err, ErrDisabled) || ran != 0 {
		t.Errorf("disabled command ran: %v", err)
	}
	on = true
	if err := r.Run("x"); err != nil || ran != 1 {
		t.Errorf("enabled command: %v, ran %d", err, ran)
	}
	if err := r.Run("nope"); !errors.Is(err, ErrUnknown) {
		t.Errorf("unknown: %v", err)
	}
}

func TestSearchRanking(t *testing.T) {
	r := NewRegistry()
	r.MustRegister(cmd("q.run", "Query", "Run Statement", Shortcut{Key: "Return", Mods: ModShortcut}))
	r.MustRegister(cmd("c.new", "Connection", "New Connection…", Shortcut{Key: "N", Mods: ModShortcut | ModShift}))
	r.MustRegister(cmd("q.fmt", "Query", "Format Script", Shortcut{}))
	r.Get("q.fmt")
	r.MustRegister(Command{ID: "q.explain", Category: "Query", Title: "Explain Plan", Run: func() {},
		Keywords: []string{"analyze", "performance"}})
	r.MustRegister(Command{ID: "c.close", Category: "Connection", Title: "Close Connection", Run: func() {},
		Enabled: func() bool { return false }})

	top := func(q string) string {
		m := r.Search(q, 0)
		if len(m) == 0 {
			return ""
		}
		return m[0].Command.ID
	}
	if top("newc") != "c.new" {
		t.Errorf("newc → %s", top("newc"))
	}
	if top("run") != "q.run" {
		t.Errorf("run → %s", top("run"))
	}
	if top("perf") != "q.explain" {
		t.Errorf("keyword search: perf → %s", top("perf"))
	}

	// Disabled commands are found but ranked after enabled ones.
	m := r.Search("conn", 0)
	if len(m) < 2 || !m[0].Enabled || m[len(m)-1].Command.ID != "c.close" || m[len(m)-1].Enabled {
		t.Errorf("disabled ordering: %+v", m)
	}

	// Empty query lists everything in registration order.
	all := r.Search("", 0)
	if len(all) != 5 || all[0].Command.ID != "q.run" {
		t.Errorf("empty query: %d results, first %s", len(all), all[0].Command.ID)
	}
	if got := r.Search("", 2); len(got) != 2 {
		t.Errorf("limit ignored: %d", len(got))
	}
}

func BenchmarkSearch100Commands(b *testing.B) {
	r := NewRegistry()
	for i := 0; i < 100; i++ {
		r.MustRegister(Command{ID: strings.Repeat("x", i+1), Category: "Category", Title: "Some Longer Command Title Number",
			Run: func() {}})
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		r.Search("comtn", 20)
	}
}
