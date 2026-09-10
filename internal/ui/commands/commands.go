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
	"unicode"
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
	mu    sync.RWMutex
	byID  map[string]*Command
	order []string
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{byID: map[string]*Command{}} }

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
	if !c.Shortcut.IsZero() {
		for _, goos := range []string{"darwin", "linux"} {
			want := c.Shortcut.effective(goos)
			for _, id := range r.order {
				o := r.byID[id]
				if !o.Shortcut.IsZero() && o.Shortcut.effective(goos) == want {
					return fmt.Errorf("commands: %s and %s both use %s on %s",
						id, c.ID, c.Shortcut.Label(goos), goos)
				}
			}
		}
	}
	cp := c
	cp.Keywords = append([]string(nil), c.Keywords...)
	r.byID[c.ID] = &cp
	r.order = append(r.order, c.ID)
	return nil
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
		score, pos, ok := Fuzzy(q, label)
		if !ok {
			for _, kw := range c.Keywords {
				if s, _, found := Fuzzy(q, kw); found {
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

const (
	scoreMatch     = 10
	bonusWordStart = 12
	bonusFirst     = 6
	bonusConsec    = 8
	penaltyGap     = 1
)

const impossible = -1 << 30

// Fuzzy reports whether query's runes appear in order in target,
// case-insensitively, and scores the best such placement.
//
// The best placement is found by dynamic programming, not taken greedily. A
// greedy match of "nc" in "Connection: New Connection" takes the first n and
// the next c, both mid-word, and scores badly. The placement on the word
// starts, New Connection, is the one a person means. A running maximum over
// earlier positions keeps it O(len(query) × len(target)).
func Fuzzy(query, target string) (int, []int, bool) {
	qs := []rune(strings.ToLower(query))
	ts := []rune(target)
	lt := make([]rune, len(ts))
	for i, r := range ts {
		lt[i] = unicode.ToLower(r)
	}
	nq, nt := len(qs), len(ts)
	if nq == 0 {
		return 0, nil, true
	}
	if nq > nt {
		return 0, nil, false
	}

	start := make([]int, nt)
	for j := range ts {
		start[j] = scoreMatch
		if isWordStart(ts, j) {
			start[j] += bonusWordStart
		}
		if j == 0 {
			start[j] += bonusFirst
		}
	}

	best := make([][]int, nq)
	back := make([][]int, nq)
	for i := range best {
		best[i] = make([]int, nt)
		back[i] = make([]int, nt)
		for j := range best[i] {
			best[i][j] = impossible
			back[i][j] = -1
		}
	}
	for j := 0; j < nt; j++ {
		if lt[j] == qs[0] {
			best[0][j] = start[j] - j/4 // a gentle preference for early matches
		}
	}
	for i := 1; i < nq; i++ {
		runMax, runArg := impossible, -1 // max over k <= j-2 of best[i-1][k] + k
		for j := i; j < nt; j++ {
			if k := j - 2; k >= 0 && best[i-1][k] != impossible && best[i-1][k]+k > runMax {
				runMax, runArg = best[i-1][k]+k, k
			}
			if lt[j] != qs[i] {
				continue
			}
			cand, arg := impossible, -1
			if p := best[i-1][j-1]; p != impossible {
				cand, arg = p+bonusConsec, j-1
			}
			if runMax != impossible {
				if gap := runMax + 1 - j*penaltyGap; gap > cand {
					cand, arg = gap, runArg
				}
			}
			if cand != impossible {
				best[i][j], back[i][j] = cand+start[j], arg
			}
		}
	}

	endScore, end := impossible, -1
	for j := 0; j < nt; j++ {
		if best[nq-1][j] > endScore {
			endScore, end = best[nq-1][j], j
		}
	}
	if end < 0 {
		return 0, nil, false
	}
	pos := make([]int, nq)
	for i, j := nq-1, end; i >= 0; i-- {
		pos[i] = j
		j = back[i][j]
	}
	return endScore - nt/8, pos, true // on a tie, the shorter label wins
}

func isWordStart(ts []rune, j int) bool {
	if j == 0 {
		return true
	}
	prev, cur := ts[j-1], ts[j]
	if unicode.IsSpace(prev) || strings.ContainsRune(":_-./()…", prev) {
		return true
	}
	return unicode.IsLower(prev) && unicode.IsUpper(cur) // camelCase
}
