package commands

import (
	"errors"
	"testing"
)

func TestAShortcutReadsBackAsWritten(t *testing.T) {
	for _, sc := range []Shortcut{
		{Key: "K", Mods: ModShortcut}, {Key: "Slash", Mods: ModShortcut},
		{Key: "F", Mods: ModShortcut | ModShift | ModAlt | ModControl}, {Key: "F5"}, {},
	} {
		if got, err := ParseShortcut(sc.String()); err != nil || got != sc {
			t.Errorf("%+v wrote %q, read back %+v, %v", sc, sc.String(), got, err)
		}
	}
	if got := (Shortcut{Key: "F", Mods: ModShift | ModShortcut}).String(); got != "Shift+Shortcut+F" {
		t.Errorf("wrote %q", got)
	}
	for _, bad := range []string{"Shift+", "+", "Hyper+K", "Shift+Shift+K", "Shift+Page Up"} {
		if _, err := ParseShortcut(bad); err == nil {
			t.Errorf("%q should not read as a shortcut", bad)
		}
	}
}

func TestRebindRefusesAClashAndKeepsTheDefault(t *testing.T) {
	r := NewRegistry()
	r.MustRegister(Command{ID: "a", Title: "A", Run: func() {}, Shortcut: Shortcut{Key: "K", Mods: ModShortcut}})
	r.MustRegister(Command{ID: "b", Title: "B", Run: func() {}, Shortcut: Shortcut{Key: "J", Mods: ModShortcut}})
	if err := r.Rebind("b", Shortcut{Key: "K", Mods: ModControl}); err == nil {
		t.Error("⌃K is Ctrl+K off the Mac, which a has")
	}
	if c, _ := r.Get("b"); c.Shortcut.Key != "J" {
		t.Error("a refused rebinding should change nothing")
	}
	if err := r.Rebind("b", Shortcut{Key: "L", Mods: ModShortcut}); err != nil {
		t.Fatal(err)
	}
	if c, _ := r.Get("b"); c.Shortcut.Key != "L" || r.Default("b").Key != "J" {
		t.Errorf("rebound to %+v with default %+v", c.Shortcut, r.Default("b"))
	}
	if err := r.Rebind("a", Shortcut{Key: "K", Mods: ModShortcut}); err != nil {
		t.Errorf("a command keeps its own chord: %v", err)
	}
	if err := r.Rebind("b", Shortcut{}); err != nil {
		t.Errorf("a command can have no shortcut: %v", err)
	}
	if err := r.Rebind("nope", Shortcut{}); !errors.Is(err, ErrUnknown) {
		t.Errorf("an unknown command: %v", err)
	}
}
