package view

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	fynetheme "fyne.io/fyne/v2/theme"

	"github.com/ikigai-db/ikigai-db/internal/ui/explorer"
	uitheme "github.com/ikigai-db/ikigai-db/internal/ui/theme"
	"github.com/ikigai-db/ikigai-db/internal/ui/uithread"
)

func TestFoldersComeFirstAndHoldTheirConnections(t *testing.T) {
	newApp(t)
	l, saved := setup(t, "a", "b", "c")
	live, err := l.Conns.CreateFolder("Live", "red")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Conns.CreateFolder("Empty", ""); err != nil {
		t.Fatal(err)
	}
	if err := l.Conns.SetFolder(saved[1].ID, live.ID); err != nil {
		t.Fatal(err)
	}
	root := load(t, l, explorer.Item{})
	var got []string
	for _, it := range root {
		got = append(got, fmt.Sprintf("%s %v %v %v", it.Label, it.HasChildren, it.Eager, it.Connects))
	}
	want := []string{"Live true true false", "Empty false true false", "a true false true", "c true false true"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("root %q, want %q: folders first, an empty one with nothing to open, then the connections in none", got, want)
	}
	if root[0].ID != FolderID(live.ID) || root[2].ID != ConnectionID(saved[0].ID) {
		t.Errorf("IDs %s, %s", debugID(root[0].ID), debugID(root[2].ID))
	}
	inside := load(t, l, root[0])
	if len(inside) != 1 || inside[0].ID != ConnectionID(saved[1].ID) || !inside[0].Connects {
		t.Errorf("the folder holds %+v, want b", inside)
	}
}

func TestTheFilterFindsAConnectionInAFolderNotOpened(t *testing.T) {
	newApp(t)
	l, saved := setup(t, "primary", "secondary")
	live, _ := l.Conns.CreateFolder("Live", "")
	if err := l.Conns.SetFolder(saved[1].ID, live.ID); err != nil {
		t.Fatal(err)
	}
	q := &uithread.Queue{}
	e := New(l, q.Run, 0)
	e.Model.Children(explorer.RootID)
	waitReal(t, e.Model, explorer.RootID)
	deadline := time.Now().Add(3 * time.Second) // nobody asks for the folder's children
	for _, st, _ := e.Model.Item(FolderID(live.ID)); st != explorer.Loaded; _, st, _ = e.Model.Item(FolderID(live.ID)) {
		if time.Now().After(deadline) {
			t.Fatal("the folder's connections never loaded")
		}
		time.Sleep(3 * time.Millisecond)
	}
	e.Filter.SetText("live second")
	q.Flush()
	if len(e.hits) != 1 || strings.Join(e.hits[0].Path, "/") != "Live/secondary" {
		t.Errorf("hits %+v; the connection in the folder should be found by the folder's name too", e.hits)
	}
	if want := "Not searched: 2 connections not opened."; !strings.Contains(e.note.Text, want) {
		t.Errorf("note %q, want %q: the folder's connection counts too", e.note.Text, want)
	}
}

func TestAFolderRowShowsItsColourAsADot(t *testing.T) {
	newApp(t)
	l, _ := setup(t, "a")
	l.Conns.CreateFolder("Live", "red")
	l.Conns.CreateFolder("Plain", "")
	e := New(l, (&uithread.Queue{}).Run, 0)
	e.Model.Children(explorer.RootID)
	root := waitReal(t, e.Model, explorer.RootID)
	r := newNodeRow()
	e.update(root[0], r)
	red, _ := uitheme.FolderColor("red", false)
	if r.label.Text != "Live" || r.badge.Text != "●" || r.badge.Color != red {
		t.Errorf("row %q with badge %q in %v, want Live with a red dot", r.label.Text, r.badge.Text, r.badge.Color)
	}
	if want := fyne.CurrentApp().Settings().Theme().Icon(fynetheme.IconNameFolder); r.icon.Resource != want {
		t.Error("a folder row should have the folder icon")
	}
	fyne.CurrentApp().Settings().Theme().(*uitheme.Theme).Appearance = uitheme.AppearanceDark
	e.update(root[0], r)
	if dark, _ := uitheme.FolderColor("red", true); r.badge.Color != dark {
		t.Errorf("in dark, the dot is %v, want dark red", r.badge.Color)
	}
	e.update(root[1], r)
	if r.label.Text != "Plain" || r.badge.Text != "" {
		t.Errorf("a folder with no colour shows %q beside %q", r.badge.Text, r.label.Text)
	}
}

func TestFolderOf(t *testing.T) {
	if id, ok := FolderOf(FolderID("x")); id != "x" || !ok {
		t.Errorf("FolderOf(FolderID) = %q, %v", id, ok)
	}
	for _, id := range []string{ConnectionID("x"), FolderID("x") + "\x00loading", FolderID(""), ""} {
		if f, ok := FolderOf(id); ok {
			t.Errorf("FolderOf(%s) = %q; it is no folder", debugID(id), f)
		}
	}
	if c, ok := ConnectionOf(FolderID("x")); ok {
		t.Errorf("a folder is taken for connection %q", c)
	}
}
