package shell

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// The side panel (UX principle 4, T1.77): Query History and Saved Queries
// open beside the tabs, not over them, so the data they are about stays in
// view. One panel is open at a time, in a split the person can widen.

const (
	panelHistory   = "history"
	panelSaved     = "saved"
	panelShortcuts = "shortcuts"
)

// panelShare is a new panel's share of the room beside the sidebar.
const panelShare = 0.34

type sidePanel struct {
	kind  string
	box   *fyne.Container
	split *container.Split
}

// openPanel shows body in the side panel under title, replacing any panel
// open, and puts the keyboard in focus. A width the person gave the panel
// it replaces is kept.
func (s *Shell) openPanel(kind, title string, body fyne.CanvasObject, focus fyne.Focusable) {
	heading := widget.NewLabelWithStyle(title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	// Words, not a bare ×: a button with no text has no name to be read out.
	closeIt := widget.NewButton("Close", s.closePanel)
	closeIt.Importance = widget.LowImportance
	top := container.NewVBox(container.NewBorder(nil, nil, nil, closeIt, heading), widget.NewSeparator())
	box := container.NewBorder(top, nil, nil, nil, body)
	split := container.NewHSplit(s.work, box)
	split.Offset = 1 - panelShare
	if s.panel != nil {
		split.Offset = s.panel.split.Offset
	}
	s.panel = &sidePanel{kind: kind, box: box, split: split}
	s.right.Objects = []fyne.CanvasObject{split}
	s.right.Refresh()
	if focus != nil {
		s.win.Canvas().Focus(focus)
	}
	s.sync()
}

// closePanel closes the side panel, if one is open, and gives the tabs their
// room back.
func (s *Shell) closePanel() {
	if s.panel == nil {
		return
	}
	s.panel = nil
	s.right.Objects = []fyne.CanvasObject{s.work}
	s.right.Refresh()
	s.sync()
}

func (s *Shell) panelIs(kind string) bool { return s.panel != nil && s.panel.kind == kind }

// togglePanel closes a kind of panel if it is open and opens it if not, as a
// menu item that shows a panel does.
func (s *Shell) togglePanel(kind string, open func()) {
	if s.panelIs(kind) {
		s.closePanel()
		return
	}
	open()
}

// Panel is the open side panel, or nil. Tests and screenshots use it: the
// panel is part of the window, not an overlay they could find on its own.
func (s *Shell) Panel() fyne.CanvasObject {
	if s.panel == nil {
		return nil
	}
	return s.panel.box
}

// panelEntry is a panel's search field: Escape closes the panel.
type panelEntry struct {
	widget.Entry
	s *Shell
}

func (s *Shell) newPanelEntry() *panelEntry {
	e := &panelEntry{s: s}
	e.ExtendBaseWidget(e)
	return e
}

func (e *panelEntry) TypedKey(k *fyne.KeyEvent) {
	if k.Name == fyne.KeyEscape {
		e.s.closePanel()
		return
	}
	e.Entry.TypedKey(k)
}
