package shell

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/ikigai-db/ikigai-db/internal/ui/commands"
)

func editorOn(t *testing.T, fx *fixture, id string) *shortcutEditor {
	t.Helper()
	p := fx.s.showShortcuts()
	c, ok := fx.s.reg.Get(id)
	if !ok {
		t.Fatalf("no command %s", id)
	}
	p.editor.edit(c)
	if !p.editor.box.Visible() {
		t.Fatal("the editor should open in the panel")
	}
	return p.editor
}

func TestAShortcutIsChangedInThePanel(t *testing.T) {
	fx := newFixture(t)
	e := editorOn(t, fx, cmdFind)
	e.command.SetChecked(true)
	e.control.SetChecked(false)
	e.option.SetChecked(true)
	e.shift.SetChecked(false)
	e.key.SetSelected("J")
	e.save()
	want := commands.Shortcut{Key: "J", Mods: commands.ModShortcut | commands.ModAlt}
	if c, _ := fx.s.reg.Get(cmdFind); c.Shortcut != want {
		t.Fatalf("Find is on %+v, want %+v; note %q", c.Shortcut, want, e.note.Text)
	}
	if e.box.Visible() {
		t.Error("the editor should close once the shortcut is changed")
	}
	if got := fx.settings.Get().Bindings[cmdFind]; got != want.String() {
		t.Errorf("kept as %q", got)
	}
}

func TestAClashIsNamedByTheCommandThatHasIt(t *testing.T) {
	fx := newFixture(t)
	save, _ := fx.s.reg.Get(cmdQuerySave)
	e := editorOn(t, fx, cmdFind)
	e.command.SetChecked(true)
	e.control.SetChecked(false)
	e.option.SetChecked(false)
	e.shift.SetChecked(false)
	e.key.SetSelected(save.Shortcut.Key)
	e.save()
	if !strings.Contains(e.note.Text, save.Title) || !e.box.Visible() {
		t.Errorf("note %q; a clash should be said by the command that has the chord, the editor left open", e.note.Text)
	}
	if len(fx.settings.Get().Bindings) != 0 {
		t.Error("a refused change should keep nothing")
	}
}

func TestShiftAloneWouldOnlyType(t *testing.T) {
	fx := newFixture(t)
	e := editorOn(t, fx, cmdFind)
	e.command.SetChecked(false)
	e.control.SetChecked(false)
	e.option.SetChecked(false)
	e.shift.SetChecked(true)
	e.key.SetSelected("J")
	e.save()
	if !strings.Contains(e.note.Text, "would only type") {
		t.Errorf("note %q", e.note.Text)
	}
	if c, _ := fx.s.reg.Get(cmdFind); c.Shortcut.Key == "J" {
		t.Error("a chord that would only type should not be taken")
	}
}

func TestDefaultAndNoShortcut(t *testing.T) {
	fx := newFixture(t)
	def := fx.s.reg.Default(cmdFind)
	e := editorOn(t, fx, cmdFind)
	e.clear()
	if c, _ := fx.s.reg.Get(cmdFind); !c.Shortcut.IsZero() {
		t.Fatalf("No Shortcut left %+v", c.Shortcut)
	}
	p := fx.s.showShortcuts()
	if slices := shownIDs(p); containsID(slices, cmdFind) {
		t.Error("a command with no shortcut should not be listed until the box is ticked")
	}
	p.unset.SetChecked(true)
	if !containsID(shownIDs(p), cmdFind) {
		t.Error("with the box ticked, commands without a shortcut should be listed, to give them one")
	}
	e = editorOn(t, fx, cmdFind)
	e.reset()
	if c, _ := fx.s.reg.Get(cmdFind); c.Shortcut != def {
		t.Errorf("Default left %+v, want %+v", c.Shortcut, def)
	}
	if _, ok := fx.settings.Get().Bindings[cmdFind]; ok {
		t.Error("back on its default, the command should leave the file")
	}
}

func containsID(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

func TestSavingACommandsOwnChordIsNoClash(t *testing.T) {
	fx := newFixture(t)
	e := editorOn(t, fx, cmdFind) // filled in with Find's own chord
	e.save()
	if e.note.Text != "" || e.box.Visible() {
		t.Errorf("saving Find's own chord should be no clash: note %q", e.note.Text)
	}
}

func TestSaveNeedsAKey(t *testing.T) {
	fx := newFixture(t)
	e := editorOn(t, fx, cmdFind)
	e.key.ClearSelected()
	e.save()
	if !strings.Contains(e.note.Text, "Choose a key") {
		t.Errorf("note %q; Save with no key should ask for one", e.note.Text)
	}
}

func TestARowsChangeButtonOpensItsCommand(t *testing.T) {
	fx := newFixture(t)
	p := fx.s.showShortcuts()
	at := -1
	for i, c := range p.shown {
		if c.ID == cmdFind {
			at = i
		}
	}
	if at < 0 {
		t.Fatal("Find should be listed")
	}
	row := p.list.CreateItem()
	p.list.UpdateItem(at, row)
	b := findButton(row, "Change…")
	if b == nil {
		t.Fatal("each row should have a Change… button")
	}
	test.Tap(b)
	if !p.editor.box.Visible() || !strings.Contains(p.editor.title.Text, "Find") {
		t.Errorf("the row's button should open the editor on its command: %q", p.editor.title.Text)
	}
}
