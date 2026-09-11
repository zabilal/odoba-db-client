package shell

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/ui/commands"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer/view"
	uitheme "github.com/ikigai-db/ikigai-db/internal/ui/theme"
)

// Connection folders (FR-1.6, ADR-0025). The explorer lists the folders
// above the connections in none, each holding its own. A folder is made,
// renamed and coloured from the Connection menu or its own context menu, and
// a connection is put in one from its form. Deleting a folder keeps its
// connections, which move out of it.

const (
	cmdFolderNew    = "connection.folder.new"
	cmdFolderEdit   = "connection.folder.edit"
	cmdFolderDelete = "connection.folder.delete"
)

func (s *Shell) folderCommands() []commands.Command {
	hasFolder := func() bool { _, ok := s.selectedFolder(); return ok }
	return []commands.Command{
		{ID: cmdFolderNew, Category: "Connection", Title: "New Folder…", Keywords: []string{"group", "organise", "organize"},
			Run: func() { s.showFolderForm("") }},
		{ID: cmdFolderEdit, Category: "Connection", Title: "Edit Folder…", Keywords: []string{"rename", "colour", "color"},
			Enabled: hasFolder, Run: func() {
				if id, ok := s.selectedFolder(); ok {
					s.showFolderForm(id)
				}
			}},
		{ID: cmdFolderDelete, Category: "Connection", Title: "Delete Folder…", Keywords: []string{"remove", "ungroup"},
			Enabled: hasFolder, Run: s.confirmDeleteFolder},
	}
}

// selectedFolder is the folder selected in the explorer, if one is.
func (s *Shell) selectedFolder() (string, bool) {
	id, ok := view.FolderOf(s.Explorer.Selected())
	if !ok {
		return "", false
	}
	for _, f := range s.d.Conns.Folders() {
		if f.ID == id {
			return id, true
		}
	}
	return "", false
}

// folderColours are the colours a folder can take, as the form lists them
// and as the settings file keeps them: none, then the accents.
func folderColours() (labels, values []string) {
	labels, values = []string{"None"}, []string{""}
	for _, a := range uitheme.Accents() {
		n := a.String()
		labels = append(labels, strings.ToUpper(n[:1])+n[1:])
		values = append(values, n)
	}
	return labels, values
}

// folderForm makes a folder, or edits one: its name and its colour.
type folderForm struct {
	s      *Shell
	id     string // "" for a new folder
	name   *widget.Entry
	colour *widget.Select
}

func (s *Shell) showFolderForm(id string) *folderForm {
	labels, values := folderColours()
	f := &folderForm{s: s, id: id, name: widget.NewEntry(), colour: widget.NewSelect(labels, nil)}
	f.colour.SetSelectedIndex(0)
	title, confirm := "New Folder", "Create"
	if id != "" {
		title, confirm = "Edit Folder", "Save"
		for _, fo := range s.d.Conns.Folders() {
			if fo.ID == id {
				f.name.SetText(fo.Name)
				f.colour.SetSelectedIndex(max(0, slices.Index(values, fo.Color)))
			}
		}
	}
	f.name.Validator = func(v string) error {
		if strings.TrimSpace(v) == "" {
			return errors.New("a folder needs a name")
		}
		return nil
	}
	dialog.NewForm(title, confirm, "Cancel",
		[]*widget.FormItem{widget.NewFormItem("Name", f.name), widget.NewFormItem("Colour", f.colour)},
		func(ok bool) {
			if ok {
				f.save()
			}
		}, s.win).Show()
	return f
}

// save makes or edits the folder, and shows it in the explorer.
func (f *folderForm) save() {
	_, values := folderColours()
	colour := values[max(0, f.colour.SelectedIndex())]
	name := strings.TrimSpace(f.name.Text)
	var err error
	if f.id == "" {
		_, err = f.s.d.Conns.CreateFolder(name, colour)
	} else {
		err = f.s.d.Conns.EditFolder(f.id, name, colour)
	}
	if err != nil {
		f.s.showError(err)
		return
	}
	f.s.Explorer.Refresh(explorer.RootID)
	f.s.sync()
}

// confirmDeleteFolder asks before deleting the selected folder. What it
// held is kept: its connections move out of it.
func (s *Shell) confirmDeleteFolder() {
	id, ok := s.selectedFolder()
	if !ok {
		return
	}
	name := ""
	for _, f := range s.d.Conns.Folders() {
		if f.ID == id {
			name = f.Name
		}
	}
	d := dialog.NewConfirm(fmt.Sprintf("Delete “%s”?", name),
		"Its connections are kept: they move out of it, to the top of the list.",
		func(yes bool) {
			if yes {
				s.deleteFolder(id)
			}
		}, s.win)
	d.SetConfirmText("Delete")
	d.SetDismissText("Cancel")
	d.SetConfirmImportance(widget.DangerImportance)
	d.Show()
}

func (s *Shell) deleteFolder(id string) {
	if err := s.d.Conns.DeleteFolder(id); err != nil {
		s.showError(err)
		return
	}
	s.Explorer.Refresh(explorer.RootID)
	s.sync()
}
