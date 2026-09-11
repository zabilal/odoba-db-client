package shell

import (
	"fmt"
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/ikigai-db/ikigai-db/internal/store"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer/view"
)

// inFolder makes a folder and shows the explorer's root with it, selected.
func inFolder(t *testing.T, fx *fixture, name, colour string) store.Folder {
	t.Helper()
	fo, err := fx.conns.CreateFolder(name, colour)
	if err != nil {
		t.Fatal(err)
	}
	fx.s.Explorer.Refresh(explorer.RootID)
	loaded(t, fx, explorer.RootID)
	fx.s.Explorer.Tree.Select(view.FolderID(fo.ID))
	return fo
}

func TestANewFolderIsMadeAndShown(t *testing.T) {
	fx := newFixture(t)
	var menu []string
	for _, m := range fx.s.menu.Items {
		if m.Label == "Connection" {
			for _, it := range m.Items {
				menu = append(menu, it.Label)
			}
		}
	}
	for _, want := range []string{"New Folder…", "Edit Folder…", "Delete Folder…"} {
		if !has(menu, want) {
			t.Errorf("the Connection menu %q has no %q", menu, want)
		}
	}
	f := fx.s.showFolderForm("")
	f.name.SetText("  Live  ")
	f.colour.SetSelected("Red")
	test.Tap(findButton(fx.s.win.Canvas().Overlays().Top(), "Create"))
	got := fx.conns.Folders()
	if len(got) != 1 || got[0].Name != "Live" || got[0].Color != "red" {
		t.Fatalf("folders %+v; the name is kept without the spaces around it", got)
	}
	loaded(t, fx, explorer.RootID)
	if kids := fx.s.Explorer.Model.Children(explorer.RootID); len(kids) == 0 || kids[0] != view.FolderID(got[0].ID) {
		t.Errorf("the explorer lists %v; the new folder should head it", kids)
	}
}

func TestEditingAFolderRenamesAndRecoloursIt(t *testing.T) {
	fx := newFixture(t)
	if !fx.s.menuItems[cmdFolderEdit].Disabled || !fx.s.menuItems[cmdFolderDelete].Disabled {
		t.Error("with no folder selected, there is none to edit or delete")
	}
	fo := inFolder(t, fx, "Staging", "")
	if fx.s.menuItems[cmdFolderEdit].Disabled || fx.s.menuItems[cmdFolderDelete].Disabled {
		t.Fatal("a folder is selected: it can be edited and deleted")
	}
	f := fx.s.showFolderForm(fo.ID)
	if f.name.Text != "Staging" || f.colour.Selected != "None" {
		t.Fatalf("the form shows %q in %q", f.name.Text, f.colour.Selected)
	}
	f.name.SetText("Live")
	f.colour.SetSelected("Orange")
	test.Tap(findButton(fx.s.win.Canvas().Overlays().Top(), "Save"))
	if got := fx.conns.Folders(); len(got) != 1 || got[0].Name != "Live" || got[0].Color != "orange" {
		t.Errorf("folders %+v", got)
	}
	if f := fx.s.showFolderForm(fo.ID); f.colour.Selected != "Orange" {
		t.Errorf("the form shows the colour %q, want the one saved", f.colour.Selected)
	}
}

func TestDeletingAFolderKeepsItsConnections(t *testing.T) {
	fx := newFixture(t)
	c := fx.create(t, "db1", nil)
	fo := inFolder(t, fx, "Live", "")
	if err := fx.conns.SetFolder(c.ID, fo.ID); err != nil {
		t.Fatal(err)
	}
	fx.s.run(cmdFolderDelete)
	top := fx.s.win.Canvas().Overlays().Top()
	if top == nil || len(fx.conns.Folders()) != 1 {
		t.Fatal("deleting a folder should ask first, and delete nothing until told")
	}
	test.Tap(findButton(top, "Delete"))
	if len(fx.conns.Folders()) != 0 {
		t.Error("the folder is still there")
	}
	if !fx.s.menuItems[cmdFolderEdit].Disabled || !fx.s.menuItems[cmdFolderDelete].Disabled {
		t.Error("with the folder gone, there is none to edit or delete")
	}
	if cur, ok := fx.conns.Get(c.ID); !ok || cur.Folder != "" {
		t.Errorf("its connection %+v should be kept, out of the folder", cur)
	}
	loaded(t, fx, explorer.RootID)
	if kids := fx.s.Explorer.Model.Children(explorer.RootID); len(kids) != 1 || kids[0] != view.ConnectionID(c.ID) {
		t.Errorf("the explorer lists %v; the connection should be at the top", kids)
	}
}

func TestAConnectionIsPutInAFolderFromItsForm(t *testing.T) {
	fx := newFixture(t)
	if f := fx.s.showConnectionForm(""); formHas(f, "Folder") {
		t.Error("with no folders, the form offers none")
	}
	fx.s.win.Canvas().Overlays().Top().Hide()
	fo, _ := fx.conns.CreateFolder("Live", "")
	f := fx.s.showConnectionForm("")
	if !formHas(f, "Folder") || f.folder.Selected != "None" {
		t.Fatalf("the form offers a folder %v, chosen %q", formHas(f, "Folder"), f.folder.Selected)
	}
	f.driver.SetSelected("PG Fake")
	test.Type(f.inputs["host"].entry, "db1")
	f.folder.SetSelected("Live")
	test.Tap(f.saveBtn)
	list := fx.conns.List()
	if len(list) != 1 || list[0].Folder != fo.ID {
		t.Fatalf("saved %+v, want it in the folder", list)
	}
	f = fx.s.showConnectionForm(list[0].ID)
	if f.folder.Selected != "Live" {
		t.Errorf("editing shows it in %q", f.folder.Selected)
	}
	f.folder.SetSelected("None")
	test.Tap(f.saveBtn)
	if cur, _ := fx.conns.Get(list[0].ID); cur.Folder != "" {
		t.Errorf("moved out, it is still in %q", cur.Folder)
	}
}

func TestANewConnectionGoesInTheFolderSelected(t *testing.T) {
	fx := newFixture(t)
	inFolder(t, fx, "Live", "")
	if f := fx.s.showConnectionForm(""); f.folder.Selected != "Live" {
		t.Errorf("a new connection starts in %q, want the folder selected", f.folder.Selected)
	}
}

func TestAFolderHasItsOwnMenu(t *testing.T) {
	fx := newFixture(t)
	c := fx.create(t, "db1", nil)
	fo := inFolder(t, fx, "Live", "")
	fx.s.Explorer.Tree.Select(view.ConnectionID(c.ID)) // the selection is elsewhere when the menu is asked for
	m := fx.s.explorerMenu(view.FolderID(fo.ID))
	if m == nil {
		t.Fatal("a folder has no menu")
	}
	var got []string
	for _, it := range m.Items {
		if !it.IsSeparator {
			got = append(got, it.Label)
		}
	}
	if want := "[New Connection… Edit Folder… Delete Folder…]"; fmt.Sprint(got) != want {
		t.Errorf("menu %v, want %s", got, want)
	}
	if id, ok := fx.s.selectedFolder(); !ok || id != fo.ID {
		t.Error("the menu's folder should be selected, so its commands act on it")
	}
}

func TestAFolderLeftOpenComesBackOpen(t *testing.T) {
	fx := newFixture(t)
	fo := inFolder(t, fx, "Live", "")
	fx.s.reexpand([]string{view.FolderID(fo.ID)})
	if !fx.s.Explorer.Tree.IsBranchOpen(view.FolderID(fo.ID)) {
		t.Error("a folder opens again at the next start: it connects to nothing")
	}
}

// formHas reports whether the connection form has an item labelled label.
func formHas(f *connForm, label string) bool {
	for _, it := range f.form.Items {
		if it.Text == label {
			return true
		}
	}
	return false
}
