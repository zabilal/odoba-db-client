package shell

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/store/localdb"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer"
)

// Workspaces (FR-15.9): a named piece of work — the connections it is
// over, the tabs it was left at, and the saved queries that belong to it.
//
// A workspace holds nothing of its own. Its connections are the ones
// already saved, named by ID; its tabs are a session, the same shape the
// window already keeps and puts back; and a saved query is in it because
// its connection is. So there is nothing here to fall out of step with
// anything there, and forgetting a workspace loses no work at all.

// workspaceTimeout bounds reading or writing the local database, which is
// on this machine and should never be the thing somebody waits for.
const workspaceTimeout = 5 * time.Second

// canUseWorkspaces reports whether there is anywhere to keep one.
func (s *Shell) canUseWorkspaces() bool { return s.d.Workspaces != nil }

// scopeToWorkspace narrows the explorer to the connections the workspace
// is over, and widens it again when there is none.
func (s *Shell) scopeToWorkspace() {
	if s.workspace.ID == "" {
		s.loader.Shows = nil
	} else {
		s.loader.Shows = s.workspace.Holds
	}
	s.Explorer.Refresh(explorer.RootID)
	// Two windows in two workspaces are otherwise the same window.
	if s.workspace.Name == "" {
		s.win.SetTitle(windowTitle)
	} else {
		s.win.SetTitle(windowTitle + " — " + s.workspace.Name)
	}
	if s.panelIs(panelSaved) {
		s.savedView.load() // the saved queries are narrowed with them
	}
	s.sync()
}

// saveWorkspace asks what to call this piece of work, and over which
// connections, and keeps it with the tabs open now.
func (s *Shell) saveWorkspace() {
	if !s.canUseWorkspaces() {
		return
	}
	name := widget.NewEntry()
	name.SetPlaceHolder("Billing")
	name.SetText(s.workspace.Name)
	name.Validator = func(text string) error {
		if strings.TrimSpace(text) == "" {
			return errors.New("a workspace needs a name")
		}
		return nil
	}
	// The connections an open tab is on are ticked to begin with: they are
	// what this piece of work has turned out to be over.
	using := map[string]bool{}
	for _, t := range s.open {
		using[t.connID] = true
	}
	conns := s.d.Conns.List()
	ticks := make([]*widget.Check, len(conns))
	box := container.NewVBox()
	for i, c := range conns {
		tick := widget.NewCheck(c.Name, nil)
		tick.SetChecked(using[c.ID] || slices.Contains(s.workspace.Connections, c.ID))
		ticks[i] = tick
		box.Add(tick)
	}
	over := container.NewVScroll(box)
	over.SetMinSize(fyne.NewSize(0, 160))

	d := dialog.NewForm("Save this workspace", "Save", "Cancel", []*widget.FormItem{
		{Text: "Name", Widget: name},
		{Text: "Over", Widget: over},
		{Widget: quiet("Ticking none is over them all. " +
			nounCount(len(s.open), "open tab") + " will be kept with it.")},
	}, func(ok bool) {
		if !ok {
			return
		}
		var over []string
		for i, tick := range ticks {
			if tick.Checked {
				over = append(over, conns[i].ID)
			}
		}
		s.keepWorkspace(strings.TrimSpace(name.Text), over)
	}, s.win)
	d.Resize(fyne.NewSize(480, d.MinSize().Height))
	d.Show()
}

// keepWorkspace writes the workspace and takes it up. The tabs stay where
// they are: saving is naming what is already here, not going anywhere.
func (s *Shell) keepWorkspace(name string, over []string) {
	w := localdb.Workspace{ID: newWorkspaceID(), Name: name, Connections: over,
		Tabs: s.sessionOf(), Used: time.Now().UTC()}
	s.workspace = w
	s.scopeToWorkspace()
	s.putWorkspace(w, "Saved the workspace “"+name+"”.")
	s.sessionChanged()
}

// newWorkspaceID names one. Two saved under the same name are two
// workspaces, because somebody replacing one picks it from the list.
func newWorkspaceID() string { return fmt.Sprintf("w%d", time.Now().UTC().UnixNano()) }

// putWorkspace writes one, and says so if there is anything to say.
func (s *Shell) putWorkspace(w localdb.Workspace, said string) {
	st := s.d.Workspaces
	ctx, cancel := context.WithTimeout(s.ctx, workspaceTimeout)
	go func() {
		defer cancel()
		err := st.PutWorkspace(ctx, w)
		s.d.Run(func() {
			if err != nil {
				s.showError(fmt.Errorf("could not save the workspace: %w", err))
				return
			}
			if said != "" {
				s.status.SetText(said)
			}
		})
	}()
}

// rememberWorkspace writes the tabs into the workspace in use, so that
// coming back to it comes back to these. It is called on the way out of
// one: on a switch, and as the window closes. It goes through the writer
// with the session and the unsaved text, so that a quit waits for it.
func (s *Shell) rememberWorkspace() {
	if s.writer == nil || !s.canUseWorkspaces() || s.workspace.ID == "" {
		return
	}
	w := s.workspace
	w.Tabs, w.Used = s.sessionOf(), time.Now().UTC()
	s.workspace = w
	st := s.d.Workspaces
	s.writer.queue("workspace/"+w.ID, func(ctx context.Context) error {
		if err := st.PutWorkspace(ctx, w); err != nil {
			return fmt.Errorf("the workspace's tabs could not be remembered for the next time: %w", err)
		}
		return nil
	})
}

// showWorkspaces lists what is saved, to go to or to forget.
func (s *Shell) showWorkspaces() {
	if !s.canUseWorkspaces() {
		return
	}
	st := s.d.Workspaces
	ctx, cancel := context.WithTimeout(s.ctx, workspaceTimeout)
	go func() {
		defer cancel()
		saved, err := st.Workspaces(ctx)
		s.d.Run(func() {
			if err != nil && len(saved) == 0 {
				s.showError(fmt.Errorf("could not read the workspaces: %w", err))
				return
			}
			s.offerWorkspaces(saved)
		})
	}()
}

// offerWorkspaces puts up the list. Going to one closes what is open and
// opens what it was left at; forgetting one leaves everything it grouped
// exactly where it is.
func (s *Shell) offerWorkspaces(saved []localdb.Workspace) {
	if len(saved) == 0 {
		dialog.ShowInformation("Workspaces",
			"None yet. Open what a piece of work needs and save it as a workspace to have it again.", s.win)
		return
	}
	rows := container.NewVBox()
	var d dialog.Dialog
	redraw := func() {}
	redraw = func() {
		rows.Objects = nil
		if s.workspace.ID != "" {
			out := widget.NewButton("Everything", func() {
				d.Hide()
				s.switchWorkspace(localdb.Workspace{})
			})
			out.Alignment = widget.ButtonAlignLeading
			rows.Add(container.NewVBox(out, quiet("Leave “"+s.workspace.Name+"” and see every connection again.")))
		}
		for _, w := range saved {
			go2 := widget.NewButton(w.Name, nil)
			go2.Alignment = widget.ButtonAlignLeading
			go2.Importance = widget.MediumImportance
			if w.ID == s.workspace.ID {
				go2.Importance = widget.HighImportance
			}
			forget := widget.NewButtonWithIcon("", theme.DeleteIcon(), nil)
			forget.Importance = widget.LowImportance
			go2.OnTapped = func() {
				d.Hide()
				s.switchWorkspace(w)
			}
			forget.OnTapped = func() {
				s.forgetWorkspace(w)
				saved = withoutWorkspace(saved, w)
				if len(saved) == 0 {
					d.Hide()
					return
				}
				redraw()
			}
			rows.Add(container.NewBorder(nil, nil, nil, forget,
				container.NewVBox(go2, quiet(describeWorkspace(s, w)))))
		}
		rows.Refresh()
	}
	redraw()
	d = dialog.NewCustom("Workspaces", "Close", container.NewVScroll(rows), s.win)
	d.Resize(fyne.NewSize(460, 380))
	d.Show()
}

// describeWorkspace says what a workspace is over and what it was left at,
// by the names somebody would look for rather than by IDs.
func describeWorkspace(s *Shell, w localdb.Workspace) string {
	var has []string
	if n := len(w.Connections); n == 0 {
		has = append(has, "every connection")
	} else {
		var names []string
		for _, id := range w.Connections {
			if c, ok := s.d.Conns.Get(id); ok {
				names = append(names, c.Name)
			}
		}
		if len(names) == 0 { // every connection it was over has been deleted
			has = append(has, nounCount(n, "connection")+", none of them still saved")
		} else {
			has = append(has, strings.Join(names, ", "))
		}
	}
	if n := len(w.Tabs.Tabs); n > 0 {
		has = append(has, nounCount(n, "tab"))
	}
	return strings.Join(has, " · ")
}

func withoutWorkspace(ws []localdb.Workspace, drop localdb.Workspace) []localdb.Workspace {
	out := ws[:0:0]
	for _, w := range ws {
		if w.ID != drop.ID {
			out = append(out, w)
		}
	}
	return out
}

// switchWorkspace leaves the workspace in use and takes up another, or
// none. What is open is written into the one being left, so that coming
// back comes back to this; the tabs then close, keeping their unsaved
// text, because the application is closing them and not the user.
func (s *Shell) switchWorkspace(w localdb.Workspace) {
	if w.ID == s.workspace.ID {
		return
	}
	s.rememberWorkspace()
	for _, t := range append([]*tab(nil), s.open...) {
		s.removeTab(t.item, true)
	}
	s.workspace = w
	s.scopeToWorkspace()
	s.openWorkspaceTabs(w)
	s.sessionChanged()
	if w.Name == "" {
		s.status.SetText("Out of that workspace: every connection again.")
		return
	}
	s.status.SetText("In the workspace “" + w.Name + "”.")
}

// openWorkspaceTabs puts back what a workspace was left at.
func (s *Shell) openWorkspaceTabs(w localdb.Workspace) {
	ctx, cancel := context.WithTimeout(s.ctx, workspaceTimeout)
	go func() {
		defer cancel()
		scratches, saved, err := s.tabMaterials(ctx, w.Tabs)
		s.d.Run(func() {
			if err != nil {
				s.showError(err)
			}
			s.reopen(w.Tabs, scratches, saved)
		})
	}()
}

// tabMaterials reads what a session's tabs are made of: the unsaved text
// they name, and the saved queries they were opened from. Only the text
// these tabs name is returned, so that switching does not reopen every
// buffer a crash left behind.
func (s *Shell) tabMaterials(ctx context.Context, ss localdb.Session) ([]localdb.Scratch, map[string]localdb.SavedQuery, error) {
	wanted := map[string]bool{}
	for _, st := range ss.Tabs {
		wanted[st.ScratchID] = true
	}
	var (
		out      []localdb.Scratch
		saved    = map[string]localdb.SavedQuery{}
		problems []error
	)
	if s.d.Scratch != nil {
		all, err := s.d.Scratch.Scratches(ctx)
		if err != nil {
			problems = append(problems, fmt.Errorf(
				"some unsaved query text could not be read back; it is still in the local database: %w", err))
		}
		for _, sc := range all {
			if wanted[sc.ID] {
				out = append(out, sc)
			}
		}
	}
	if s.d.Saved != nil {
		all, err := s.d.Saved.SavedQueries(ctx)
		if err != nil {
			problems = append(problems, fmt.Errorf("saved queries could not be read: %w", err))
		}
		for _, sq := range all {
			saved[sq.ID] = sq
		}
	}
	return out, saved, errors.Join(problems...)
}

// forgetWorkspace takes one off the list. Its connections, its saved
// queries and its unsaved text are all left where they are: a workspace is
// a way of looking at them, and forgetting one is not throwing any away.
func (s *Shell) forgetWorkspace(w localdb.Workspace) {
	st := s.d.Workspaces
	if w.ID == s.workspace.ID {
		s.workspace = localdb.Workspace{}
		s.scopeToWorkspace()
		s.sessionChanged()
	}
	ctx, cancel := context.WithTimeout(s.ctx, workspaceTimeout)
	go func() {
		defer cancel()
		if err := st.DeleteWorkspace(ctx, w.ID); err != nil {
			s.d.Run(func() { s.showError(fmt.Errorf("could not forget the workspace: %w", err)) })
		}
	}()
}

// joinWorkspace puts a newly saved connection into the workspace in use.
// Without this it would be made and then not be there, because the
// workspace it was made in is not over it.
//
// A workspace over no connection in particular holds this one already,
// and so does no workspace at all: naming the new connection in either
// would narrow it to that one connection. Neither needs saying, because
// both already hold it.
func (s *Shell) joinWorkspace(connID string) {
	if s.workspace.Holds(connID) {
		return
	}
	w := s.workspace
	w.Connections = append(slices.Clone(w.Connections), connID)
	s.workspace = w
	s.scopeToWorkspace()
	s.putWorkspace(w, "")
}
