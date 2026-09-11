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
	list   *widget.List
	status *widget.Label
	all    []commands.Command
	shown  []commands.Command
}

func (s *Shell) showShortcuts() *shortcutsPanel {
	p := &shortcutsPanel{s: s, filter: s.newPanelEntry(), status: widget.NewLabel("")}
	p.filter.SetPlaceHolder("Filter by name or keys")
	p.status.Importance = widget.LowImportance
	for _, c := range s.reg.All() {
		if !c.Shortcut.IsZero() {
			p.all = append(p.all, c)
		}
	}
	sort.SliceStable(p.all, func(i, j int) bool {
		if p.all[i].Category != p.all[j].Category {
			return p.all[i].Category < p.all[j].Category
		}
		return p.all[i].Title < p.all[j].Title
	})
	p.list = widget.NewList(
		func() int { return len(p.shown) },
		func() fyne.CanvasObject {
			title := widget.NewLabel("")
			title.Truncation = fyne.TextTruncateEllipsis
			category := widget.NewLabel("")
			category.Importance = widget.LowImportance
			keys := widget.NewLabelWithStyle("", fyne.TextAlignTrailing, fyne.TextStyle{Monospace: true})
			return container.NewBorder(nil, nil, nil, keys, container.NewVBox(title, category))
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			if i >= len(p.shown) {
				return
			}
			c, row := p.shown[i], o.(*fyne.Container)
			box, keys := row.Objects[0].(*fyne.Container), row.Objects[1].(*widget.Label)
			box.Objects[0].(*widget.Label).SetText(c.Title)
			box.Objects[1].(*widget.Label).SetText(c.Category)
			keys.SetText(c.Shortcut.Label(s.d.GOOS))
		})
	p.list.OnSelected = func(widget.ListItemID) { p.list.UnselectAll() }
	p.filter.OnChanged = func(string) { p.apply() }
	s.openPanel(panelShortcuts, "Keyboard Shortcuts", container.NewBorder(p.filter, p.status, nil, nil, p.list), p.filter)
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
