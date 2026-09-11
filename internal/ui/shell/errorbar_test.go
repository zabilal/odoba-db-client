package shell

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	uitheme "github.com/ikigai-db/ikigai-db/internal/ui/theme"
)

func TestAnErrorIsSaidAcrossTheTopAndCanBeCopied(t *testing.T) {
	fx := newFixture(t)
	b := fx.s.errors
	if b.shown() {
		t.Fatal("with nothing wrong there is no bar")
	}
	fx.s.showError(errors.New("the export failed: disk full"))
	if !b.shown() || b.message.Text != "the export failed: disk full" || b.action.Visible() {
		t.Fatalf("bar shown %v saying %q, action %v", b.shown(), b.message.Text, b.action.Visible())
	}
	if fx.s.win.Canvas().Overlays().Top() != nil {
		t.Error("an error must not open a dialog over the data")
	}
	b.copyAll()
	if got := fx.s.app.Clipboard().Content(); got != "the export failed: disk full" {
		t.Errorf("copied %q", got)
	}
	acted := false
	fx.s.showErrorWith(errors.New("the server could not be reached"), "Edit Connection…", func() { acted = true })
	if b.message.Text != "the server could not be reached" || !b.action.Visible() || b.action.Text != "Edit Connection…" {
		t.Errorf("a later error replaces the first: %q, action %q", b.message.Text, b.action.Text)
	}
	test.Tap(b.action)
	if !acted || b.shown() {
		t.Errorf("the action ran %v; the bar should go as it runs", acted)
	}
	fx.s.showError(errors.New("line one\nline two"))
	if b.message.Text != "line one" || !b.more.Visible() {
		t.Errorf("a long error shows its first line with Details: %q", b.message.Text)
	}
	b.toggleDetails()
	if !b.detailBox.Visible() || b.details.Text != "line one\nline two" {
		t.Error("Details should show all of it")
	}
	b.dismiss()
	if b.shown() {
		t.Error("the bar should go when dismissed")
	}
}

func TestTheErrorBarGetsItsRoom(t *testing.T) {
	fx := newFixture(t)
	fx.s.showError(errors.New("something failed"))
	b := fx.s.errors
	if got, need := b.box.Size().Height, b.box.MinSize().Height; got < need {
		t.Errorf("the bar has %v of height and needs %v", got, need)
	}
	d := fyne.CurrentApp().Driver()
	if bottom, top := d.AbsolutePositionForObject(b.box).Y+b.box.Size().Height, d.AbsolutePositionForObject(fx.s.split).Y; bottom > top+0.5 {
		t.Errorf("the bar ends at %v, below the top of what it sits over, at %v", bottom, top)
	}
}

// FR-15.7 and UX principle 4: an error is a band across the top of the
// window, not a modal dialog. This keeps the modal one from coming back.
func TestNoErrorOpensAModalDialog(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(b, []byte("dialog.ShowError(")) {
			t.Errorf("%s opens an error as a modal dialog; use showError", f)
		}
	}
}

// The band is drawn, not themed. The first version kept its tint across an
// appearance change, and in light mode its text could not be read.
func TestTheErrorBarFollowsTheAppearance(t *testing.T) {
	fx := newFixture(t)
	fx.s.showError(errors.New("something failed"))
	fx.s.setAppearance(uitheme.AppearanceDark)
	if fx.s.errors.bg.FillColor != uitheme.Dark.DangerSubtle {
		t.Error("in dark mode the band should wear the dark tint")
	}
	fx.s.setAppearance(uitheme.AppearanceLight)
	if fx.s.errors.bg.FillColor != uitheme.Light.DangerSubtle {
		t.Error("back in light mode the band should wear the light tint")
	}
}
