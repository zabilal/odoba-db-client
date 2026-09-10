package palette

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/ui/commands"
)

func setup(t *testing.T) (*Palette, map[string]int) {
	t.Helper()
	test.NewTempApp(t)
	ran := map[string]int{}
	reg := commands.NewRegistry()
	add := func(id, cat, title string, sc commands.Shortcut, enabled bool) {
		reg.MustRegister(commands.Command{ID: id, Category: cat, Title: title, Shortcut: sc,
			Run: func() { ran[id]++ }, Enabled: func() bool { return enabled }})
	}
	add("q.run", "Query", "Run Statement", commands.Shortcut{Key: "Return", Mods: commands.ModShortcut}, true)
	add("c.new", "Connection", "New Connection…", commands.Shortcut{}, true)
	add("q.fmt", "Query", "Format Script", commands.Shortcut{}, true)
	add("c.close", "Connection", "Close Connection", commands.Shortcut{}, false)

	w := test.NewTempWindow(t, widget.NewLabel("content"))
	w.Resize(fyne.NewSize(900, 700))
	return New(reg, w), ran
}

func key(p *Palette, name fyne.KeyName) { p.entry.TypedKey(&fyne.KeyEvent{Name: name}) }

func TestTypingFiltersAndEnterRunsTheTopMatch(t *testing.T) {
	p, ran := setup(t)
	p.Show()
	if !p.Visible() {
		t.Fatal("palette not shown")
	}
	test.Type(p.entry, "newc")
	if len(p.matches) == 0 || p.matches[0].Command.ID != "c.new" {
		t.Fatalf("top match for newc: %+v", p.matches)
	}
	key(p, fyne.KeyReturn)
	if ran["c.new"] != 1 {
		t.Errorf("Enter did not run the top match: %v", ran)
	}
	if p.Visible() {
		t.Error("palette still open after running a command; a dialog would open behind it")
	}
}

func TestArrowKeysMoveTheSelection(t *testing.T) {
	p, ran := setup(t)
	p.Show()
	test.Type(p.entry, "query")
	if len(p.matches) < 2 {
		t.Fatalf("need two query matches, got %d", len(p.matches))
	}
	second := p.matches[1].Command.ID
	key(p, fyne.KeyDown)
	key(p, fyne.KeyReturn)
	if ran[second] != 1 {
		t.Errorf("Down then Enter should run %s: %v", second, ran)
	}
	// Wrapping: Up from the first result goes to the last.
	p.Show()
	key(p, fyne.KeyUp)
	if p.sel != len(p.matches)-1 {
		t.Errorf("Up from the top selected %d, want %d", p.sel, len(p.matches)-1)
	}
}

func TestEscapeClosesWithoutRunning(t *testing.T) {
	p, ran := setup(t)
	p.Show()
	test.Type(p.entry, "run")
	key(p, fyne.KeyEscape)
	if p.Visible() || len(ran) != 0 {
		t.Errorf("Escape: visible=%v ran=%v", p.Visible(), ran)
	}
}

func TestDisabledCommandIsListedButNotRun(t *testing.T) {
	p, ran := setup(t)
	p.Show()
	test.Type(p.entry, "close conn")
	if len(p.matches) != 1 || p.matches[0].Enabled {
		t.Fatalf("want the disabled command listed: %+v", p.matches)
	}
	key(p, fyne.KeyReturn)
	if ran["c.close"] != 0 {
		t.Error("a disabled command ran")
	}
	if !p.Visible() {
		t.Error("palette closed on a command that did nothing")
	}
}

func TestReopeningStartsFresh(t *testing.T) {
	p, _ := setup(t)
	p.Show()
	test.Type(p.entry, "format")
	p.Hide()
	p.Show()
	if p.entry.Text != "" || len(p.matches) != 4 || p.sel != 0 {
		t.Errorf("reopened with stale state: text %q, %d matches, sel %d", p.entry.Text, len(p.matches), p.sel)
	}
}

func TestRowHighlightsMatchedLetters(t *testing.T) {
	p, _ := setup(t)
	p.Show()
	test.Type(p.entry, "newc")
	r := newRow()
	r.show(p.matches[0], "darwin", true)

	bold := func() string {
		var b string
		for _, o := range r.label.Objects {
			if txt, ok := o.(*canvas.Text); ok && txt.TextStyle.Bold {
				b += txt.Text
			}
		}
		return b
	}
	// "Connection: New Connection…". Typed "newc": the run "New", then the C
	// that starts "Connection", not a c from the middle of a word.
	if got := bold(); got != "NewC" {
		t.Errorf("newc highlighted %q, want \"NewC\"", got)
	}
	p.setQuery("nc")
	r.show(p.matches[0], "darwin", true)
	if got := bold(); got != "NC" {
		t.Errorf("nc highlighted %q, want the word starts \"NC\"", got)
	}
	if r.shortcut.Text != "" {
		t.Errorf("a command without a shortcut shows %q", r.shortcut.Text)
	}
	run := p.reg.Search("run", 1)[0]
	r.show(run, "darwin", false)
	if r.shortcut.Text != "⌘↩" {
		t.Errorf("shortcut label %q, want ⌘↩", r.shortcut.Text)
	}
}
