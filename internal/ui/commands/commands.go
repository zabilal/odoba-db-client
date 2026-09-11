// Package commands is the registry every user action goes through (T1.7,
// FR-15.1, FR-15.4, UX principle 3).
//
// An action registered here appears in the menu, in the command palette and
// under its keyboard shortcut. Registering it once is what guarantees it can
// never be missing from any of the three, and never listed under two
// different shortcuts.
//
// Deliberately free of Fyne: the shell translates Shortcut into Fyne's types.
package commands

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/ikigai-db/ikigai-db/internal/fuzzy"
)

// Mod is a set of modifier keys.
type Mod uint8

const (
	// ModShortcut is the platform's primary modifier: Command on macOS,
	// Control elsewhere. Using it instead of a fixed key is what makes a
	// shortcut platform-correct (UX principle 11).
	ModShortcut Mod = 1 << iota
	ModShift
	ModAlt
	// ModControl is the Control key itself. On macOS it is distinct from
	// ModShortcut; elsewhere the two are the same key.
	ModControl
)

// Shortcut is a key chord. Key is a Fyne key name: "K", "Return", "F5".
type Shortcut struct {
	Key  string
	Mods Mod
}

// IsZero reports whether no shortcut is set.
func (s Shortcut) IsZero() bool { return s.Key == "" }

// modNames are the modifiers as String writes them, in the order it does.
var modNames = []struct {
	mod  Mod
	name string
}{{ModControl, "Control"}, {ModAlt, "Alt"}, {ModShift, "Shift"}, {ModShortcut, "Shortcut"}}

// String writes the chord in a form that reads back the same on every
// platform, for keeping in the settings file (T1.8): "Shift+Shortcut+F".
// It is not for showing; Label is.
func (s Shortcut) String() string {
	if s.IsZero() {
		return ""
	}
	var parts []string
	for _, m := range modNames {
		if s.Mods&m.mod != 0 {
			parts = append(parts, m.name)
		}
	}
	return strings.Join(append(parts, s.Key), "+")
}

// ParseShortcut reads a chord String wrote. The empty string is no shortcut.
func ParseShortcut(text string) (Shortcut, error) {
	if text == "" {
		return Shortcut{}, nil
	}
	parts := strings.Split(text, "+")
	key := parts[len(parts)-1]
	if key == "" || strings.ContainsAny(key, " \t") {
		return Shortcut{}, fmt.Errorf("commands: %q names no key", text)
	}
	var s Shortcut
	for _, p := range parts[:len(parts)-1] {
		var m Mod
		for _, n := range modNames {
			if n.name == p {
				m = n.mod
			}
		}
		switch {
		case m == 0:
			return Shortcut{}, fmt.Errorf("commands: %q: %q is not a modifier", text, p)
		case s.Mods&m != 0:
			return Shortcut{}, fmt.Errorf("commands: %q names %s twice", text, p)
		}
		s.Mods |= m
	}
	s.Key = key
	return s, nil
}

// effective is the chord as a given platform actually receives it: off macOS,
// Control and the shortcut modifier are one key.
func (s Shortcut) effective(goos string) Shortcut {
	if goos != "darwin" && s.Mods&ModControl != 0 {
		s.Mods = s.Mods&^ModControl | ModShortcut
	}
	if len(s.Key) == 1 {
		s.Key = strings.ToUpper(s.Key)
	}
	return s
}

// Label renders the chord as the platform writes it: ⌃⌥⇧⌘K on macOS,
// Ctrl+Alt+Shift+K elsewhere.
func (s Shortcut) Label(goos string) string {
	if s.IsZero() {
		return ""
	}
	key := keyLabel(s.Key, goos)
	if goos == "darwin" {
		var b strings.Builder
		for _, m := range []struct {
			mod Mod
			sym string
		}{{ModControl, "⌃"}, {ModAlt, "⌥"}, {ModShift, "⇧"}, {ModShortcut, "⌘"}} {
			if s.Mods&m.mod != 0 {
				b.WriteString(m.sym)
			}
		}
		b.WriteString(key)
		return b.String()
	}
	var parts []string
	if s.Mods&(ModShortcut|ModControl) != 0 {
		parts = append(parts, "Ctrl")
	}
	if s.Mods&ModAlt != 0 {
		parts = append(parts, "Alt")
	}
	if s.Mods&ModShift != 0 {
		parts = append(parts, "Shift")
	}
	return strings.Join(append(parts, key), "+")
}

var macKeys = map[string]string{
	"Return": "↩", "Enter": "⌤", "Escape": "⎋", "BackSpace": "⌫", "Delete": "⌦",
	"Tab": "⇥", "Up": "↑", "Down": "↓", "Left": "←", "Right": "→", "Space": "Space",
}
var otherKeys = map[string]string{
	"Return": "Enter", "Enter": "Enter", "Escape": "Esc", "BackSpace": "Backspace",
	"Up": "Up", "Down": "Down", "Left": "Left", "Right": "Right",
}
var punctuation = map[string]string{
	"Slash": "/", "BackSlash": "\\", "Comma": ",", "Period": ".", "Semicolon": ";",
	"Apostrophe": "'", "Minus": "-", "Equal": "=", "LeftBracket": "[", "RightBracket": "]",
}

func keyLabel(key, goos string) string {
	if p, ok := punctuation[key]; ok {
		return p
	}
	table := otherKeys
	if goos == "darwin" {
		table = macKeys
	}
	if l, ok := table[key]; ok {
		return l
	}
	return key
}

// Command is one user action.
type Command struct {
	ID       string // stable, e.g. "connection.new"
	Title    string // "New Connection…"
	Category string // "Connection"; the palette shows "Connection: New Connection…"
	// Keywords are extra search terms, so "sql" can find "Format Script".
	Keywords []string
	Shortcut Shortcut
	Run      func()
	// Enabled reports whether the command can run now. Nil means always.
	Enabled func() bool
}

// Label is how the palette lists the command.
func (c Command) Label() string {
	if c.Category == "" {
		return c.Title
	}
	return c.Category + ": " + c.Title
}

var (
	ErrUnknown  = errors.New("commands: no such command")
	ErrDisabled = errors.New("commands: this command is not available right now")
)

// Registry holds every command. Safe for concurrent use.
type Registry struct {
	mu       sync.RWMutex
	byID     map[string]*Command
	order    []string
	defaults map[string]Shortcut // the shortcuts commands were registered with
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{byID: map[string]*Command{}, defaults: map[string]Shortcut{}}
}

// Register adds a command.
//
// A shortcut already taken is refused here, at startup, rather than
// discovered by a user pressing a key that does the wrong thing. Conflicts
// are checked as both macOS and other platforms see the chord: ⌘K and ⌃K
// are distinct on a Mac but both Ctrl+K on Windows, and a conflict on either
// is a bug on both.
func (r *Registry) Register(c Command) error {
	switch {
	case c.ID == "":
		return errors.New("commands: a command needs an ID")
	case c.Title == "":
		return fmt.Errorf("commands: %s needs a title", c.ID)
	case c.Run == nil:
		return fmt.Errorf("commands: %s has nothing to run", c.ID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.byID[c.ID]; dup {
		return fmt.Errorf("commands: %s is registered twice", c.ID)
	}
	if err := r.clash(c.ID, c.Shortcut); err != nil {
		return err
	}
	r.defaults[c.ID] = c.Shortcut
	cp := c
	cp.Keywords = append([]string(nil), c.Keywords...)
	r.byID[c.ID] = &cp
	r.order = append(r.order, c.ID)
	return nil
}

// clash reports another command already on a chord, as macOS or another
// platform sees it. Called with mu held.
func (r *Registry) clash(id string, sc Shortcut) error {
	if sc.IsZero() {
		return nil
	}
	for _, goos := range []string{"darwin", "linux"} {
		want := sc.effective(goos)
		for _, oid := range r.order {
			if oid == id {
				continue
			}
			if o := r.byID[oid]; !o.Shortcut.IsZero() && o.Shortcut.effective(goos) == want {
				return fmt.Errorf("commands: %s and %s both use %s on %s", oid, id, sc.Label(goos), goos)
			}
		}
	}
	return nil
}

// Rebind gives a command another shortcut, or none with the zero Shortcut
// (T1.8). A chord another command has is refused, as Register refuses it.
func (r *Registry) Rebind(id string, sc Shortcut) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.byID[id]
	if !ok {
		return ErrUnknown
	}
	if err := r.clash(id, sc); err != nil {
		return err
	}
	c.Shortcut = sc
	return nil
}

// Default is the shortcut a command was registered with.
func (r *Registry) Default(id string) Shortcut {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.defaults[id]
}

// MustRegister is Register for startup wiring, where failure is a bug.
func (r *Registry) MustRegister(c Command) {
	if err := r.Register(c); err != nil {
		panic(err)
	}
}

// Get returns a command by ID.
func (r *Registry) Get(id string) (Command, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.byID[id]
	if !ok {
		return Command{}, false
	}
	return *c, true
}

// All returns every command in registration order.
func (r *Registry) All() []Command {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Command, len(r.order))
	for i, id := range r.order {
		out[i] = *r.byID[id]
	}
	return out
}

// Run executes a command if it is enabled.
func (r *Registry) Run(id string) error {
	c, ok := r.Get(id)
	if !ok {
		return ErrUnknown
	}
	if c.Enabled != nil && !c.Enabled() {
		return ErrDisabled
	}
	c.Run()
	return nil
}

// Match is one palette result.
type Match struct {
	Command Command
	Label   string
	// Positions are the rune indexes in Label to highlight.
	Positions []int
	Score     int
	Enabled   bool
}

// Search ranks commands against what the user typed (FR-15.1).
//
// Disabled commands are still listed, after the enabled ones: finding a
// command and learning it is unavailable right now teaches the user it exists,
// which is the palette's other job.
func (r *Registry) Search(query string, limit int) []Match {
	q := strings.TrimSpace(query)
	var out []Match
	for i, c := range r.All() {
		label := c.Label()
		enabled := c.Enabled == nil || c.Enabled()
		if q == "" {
			out = append(out, Match{Command: c, Label: label, Score: -i, Enabled: enabled})
			continue
		}
		score, pos, ok := fuzzy.Match(q, label)
		if !ok {
			for _, kw := range c.Keywords {
				if s, _, found := fuzzy.Match(q, kw); found {
					// Found through a keyword, not the visible title: rank it
					// below matches the user can see, and highlight nothing.
					score, pos, ok = s-60, nil, true
					break
				}
			}
		}
		if ok {
			out = append(out, Match{Command: c, Label: label, Positions: pos, Score: score, Enabled: enabled})
		}
	}
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].Enabled != out[b].Enabled {
			return out[a].Enabled
		}
		return out[a].Score > out[b].Score
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}
