package shell

import (
	"fmt"
	"sort"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/ui/commands"
)

// shortcutsPanel is the searchable reference of keyboard shortcuts
// (FR-15.4, T1.9): every command that has one, whether or not it can run
// just now, with the keys as this platform writes them. It is for reading;
// running a command is the palette's job.
type shortcutsPanel struct {
	s      *Shell
	filter *panelEntry
	unset  *widget.Check // show commands with no shortcut, to give them one
	list   *widget.List
	status *widget.Label
	editor *shortcutEditor
	all    []commands.Command
	shown  []commands.Command
}

func (s *Shell) showShortcuts() *shortcutsPanel {
	p := &shortcutsPanel{s: s, filter: s.newPanelEntry(), status: widget.NewLabel("")}
	p.filter.SetPlaceHolder("Filter by name or keys")
	p.status.Importance = widget.LowImportance
	p.unset = widget.NewCheck("Show commands without a shortcut", func(bool) { p.reload() })
	p.editor = p.newEditor()
	p.gather()
	p.list = widget.NewList(
		func() int { return len(p.shown) },
		func() fyne.CanvasObject {
			title := widget.NewLabel("")
			title.Truncation = fyne.TextTruncateEllipsis
			category := widget.NewLabel("")
			category.Importance = widget.LowImportance
			keys := widget.NewLabelWithStyle("", fyne.TextAlignTrailing, fyne.TextStyle{Monospace: true})
			change := widget.NewButton("Change…", nil)
			change.Importance = widget.LowImportance
			return container.NewBorder(nil, nil, nil, container.NewHBox(keys, change), container.NewVBox(title, category))
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			if i >= len(p.shown) {
				return
			}
			c, row := p.shown[i], o.(*fyne.Container)
			box, right := row.Objects[0].(*fyne.Container), row.Objects[1].(*fyne.Container)
			keys, change := right.Objects[0].(*widget.Label), right.Objects[1].(*widget.Button)
			box.Objects[0].(*widget.Label).SetText(c.Title)
			box.Objects[1].(*widget.Label).SetText(c.Category)
			keys.SetText(c.Shortcut.Label(s.d.GOOS))
			change.OnTapped = func() { p.editor.edit(c) }
		})
	p.list.OnSelected = func(widget.ListItemID) { p.list.UnselectAll() }
	p.filter.OnChanged = func(string) { p.apply() }
	top := container.NewVBox(p.filter, p.unset)
	bottom := container.NewVBox(p.editor.box, p.status)
	s.openPanel(panelShortcuts, "Keyboard Shortcuts", container.NewBorder(top, bottom, nil, nil, p.list), p.filter)
	p.apply()
	return p
}

// apply keeps the commands that match every word typed. A word matches a
// command's title, category or keywords, or its keys, so that "save" and
// "⌘S" both find Save Query.
func (p *shortcutsPanel) apply() {
	words := strings.Fields(strings.ToLower(p.filter.Text))
	p.shown = p.shown[:0]
	for _, c := range p.all {
		hay := strings.ToLower(strings.Join(append([]string{c.Title, c.Category, c.Shortcut.Label(p.s.d.GOOS)}, c.Keywords...), " "))
		match := true
		for _, w := range words {
			match = match && strings.Contains(hay, w)
		}
		if match {
			p.shown = append(p.shown, c)
		}
	}
	p.list.Refresh()
	switch len(p.shown) {
	case 0:
		p.status.SetText("No shortcuts match.")
	case 1:
		p.status.SetText("1 shortcut")
	default:
		p.status.SetText(fmt.Sprintf("%d shortcuts", len(p.shown)))
	}
}

// gather lists the commands the panel shows: those with a shortcut, and
// with the box ticked those without, by category and title.
func (p *shortcutsPanel) gather() {
	p.all = p.all[:0]
	for _, c := range p.s.reg.All() {
		if !c.Shortcut.IsZero() || p.unset.Checked {
			p.all = append(p.all, c)
		}
	}
	sort.SliceStable(p.all, func(i, j int) bool {
		if p.all[i].Category != p.all[j].Category {
			return p.all[i].Category < p.all[j].Category
		}
		return p.all[i].Title < p.all[j].Title
	})
}

// reload lists the commands afresh, after a change or the box.
func (p *shortcutsPanel) reload() {
	p.gather()
	p.apply()
}

// shortcutEditor changes one command's shortcut, in the panel rather than
// over it (UX principle 4). Modifiers are ticked and the key chosen, rather
// than pressed: on a Mac the menu bar takes a chord it holds before the
// panel sees it, and a choice made by ticking can be read out.
type shortcutEditor struct {
	p       *shortcutsPanel
	box     *fyne.Container
	title   *widget.Label
	command *widget.Check // Command on a Mac, Control elsewhere
	control *widget.Check // Control on a Mac; not shown elsewhere, where it is the same key
	option  *widget.Check
	shift   *widget.Check
	key     *widget.Select
	note    *widget.Label
	id      string
}

// shortcutKeys are the keys Fyne delivers as a shortcut, in the order the
// editor offers them.
var shortcutKeys = func() []string {
	var out []string
	for c := 'A'; c <= 'Z'; c++ {
		out = append(out, string(c))
	}
	for c := '0'; c <= '9'; c++ {
		out = append(out, string(c))
	}
	for i := 1; i <= 12; i++ {
		out = append(out, fmt.Sprintf("F%d", i))
	}
	return append(out, "Return", "Space", "Tab", "Escape", "BackSpace", "Delete",
		"Up", "Down", "Left", "Right", "Home", "End", "Prior", "Next",
		"/", ".", ",", ";", "'", "-", "=", "+", "*", "[", "]", "\\", "`")
}()

func (p *shortcutsPanel) newEditor() *shortcutEditor {
	mac := p.s.d.GOOS == "darwin"
	e := &shortcutEditor{p: p, title: widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		note: widget.NewLabel(""), key: widget.NewSelect(shortcutKeys, nil)}
	e.note.Wrapping = fyne.TextWrapWord
	e.note.Importance = widget.DangerImportance
	if mac {
		e.command, e.control = widget.NewCheck("⌘ Command", nil), widget.NewCheck("⌃ Control", nil)
		e.option, e.shift = widget.NewCheck("⌥ Option", nil), widget.NewCheck("⇧ Shift", nil)
	} else {
		e.command, e.control = widget.NewCheck("Ctrl", nil), widget.NewCheck("Control", nil)
		e.option, e.shift = widget.NewCheck("Alt", nil), widget.NewCheck("Shift", nil)
		e.control.Hide()
	}
	e.key.PlaceHolder = "Key"
	buttons := container.NewHBox(widget.NewButton("Save", e.save), widget.NewButton("Default", e.reset),
		widget.NewButton("No Shortcut", e.clear), widget.NewButton("Cancel", e.close))
	e.box = container.NewVBox(widget.NewSeparator(), e.title,
		container.NewHBox(e.command, e.control, e.option, e.shift), e.key, e.note, buttons)
	e.box.Hide()
	return e
}

// edit opens the editor on a command, filled in with its shortcut.
func (e *shortcutEditor) edit(c commands.Command) {
	e.id = c.ID
	e.title.SetText("Shortcut for " + c.Title)
	sc := c.Shortcut
	e.command.SetChecked(sc.Mods&commands.ModShortcut != 0)
	e.control.SetChecked(sc.Mods&commands.ModControl != 0)
	e.option.SetChecked(sc.Mods&commands.ModAlt != 0)
	e.shift.SetChecked(sc.Mods&commands.ModShift != 0)
	e.key.ClearSelected()
	if k := sc.Key; k != "" {
		if len(k) == 1 {
			k = strings.ToUpper(k) // a letter is offered as a capital; a named key as it is
		}
		e.key.SetSelected(k)
	}
	e.note.SetText("")
	e.box.Show()
	e.p.s.win.Canvas().Refresh(e.box)
}

// chosen is the shortcut the editor shows.
func (e *shortcutEditor) chosen() commands.Shortcut {
	var sc commands.Shortcut
	if e.command.Checked {
		sc.Mods |= commands.ModShortcut
	}
	if e.control.Checked && e.control.Visible() {
		sc.Mods |= commands.ModControl
	}
	if e.option.Checked {
		sc.Mods |= commands.ModAlt
	}
	if e.shift.Checked {
		sc.Mods |= commands.ModShift
	}
	sc.Key = e.key.Selected
	return sc
}

// typed reports whether a key types a character: one that needs ⌘, ⌃ or
// ⌥ with it, or pressing it would only type.
func typed(key string) bool { return len([]rune(key)) == 1 }

func (e *shortcutEditor) save() {
	sc := e.chosen()
	switch {
	case sc.Key == "":
		e.note.SetText("Choose a key, or No Shortcut to have none.")
		return
	case typed(sc.Key) && sc.Mods&(commands.ModShortcut|commands.ModControl|commands.ModAlt) == 0:
		e.note.SetText(sc.Label(e.p.s.d.GOOS) + " would only type: add " + e.command.Text + ", " + e.option.Text + " or Control.")
		return
	}
	if o, taken := e.p.s.reg.Holder(e.id, sc); taken {
		e.note.SetText(fmt.Sprintf("%s is %s's shortcut.", sc.Label(e.p.s.d.GOOS), o.Title))
		return
	}
	e.apply(sc)
}

func (e *shortcutEditor) reset() {
	sc := e.p.s.reg.Default(e.id)
	if o, taken := e.p.s.reg.Holder(e.id, sc); taken {
		e.note.SetText(fmt.Sprintf("%s, its default, is now %s's shortcut.", sc.Label(e.p.s.d.GOOS), o.Title))
		return
	}
	e.apply(sc)
}

func (e *shortcutEditor) clear() { e.apply(commands.Shortcut{}) }

func (e *shortcutEditor) apply(sc commands.Shortcut) {
	if err := e.p.s.setBinding(e.id, sc); err != nil {
		e.note.SetText(err.Error())
		return
	}
	e.close()
	e.p.reload()
}

func (e *shortcutEditor) close() {
	e.box.Hide()
	e.p.s.win.Canvas().Refresh(e.box)
}
