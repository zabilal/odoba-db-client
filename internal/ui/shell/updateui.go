package shell

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/update"
)

// Asking whether there is a newer version, and saying what is in it
// (FR-15.10).
//
// The check happens when somebody asks for it. There is no timer and no
// check on start unless they have said there may be, because nothing this
// application does by itself touches the network (NFR-D6) — and a build
// nobody has pointed at a release feed never reaches it at all.
//
// Nothing is installed. Replacing a running binary is what needs signing
// and notarisation to be safe, and until those exist an application that
// installed its own updates would be asking people to trust an unsigned
// download. So this says what is available, shows what changed, and opens
// the page it is published on. The install is theirs (FR-15.10's "user
// controlled" read as written).

// updateTimeout bounds a check. Somebody asked and is waiting.
const updateTimeout = 15 * time.Second

// canCheckForUpdates reports whether there is a feed to ask.
func (s *Shell) canCheckForUpdates() bool { return s.d.UpdateFeed != "" }

// checkForUpdates asks the feed, off the UI goroutine, and says what it
// answered.
func (s *Shell) checkForUpdates() {
	if !s.canCheckForUpdates() {
		return
	}
	s.status.SetText("Asking about newer versions…")
	feed, now := s.d.UpdateFeed, s.d.Version
	go func() {
		ctx, cancel := context.WithTimeout(s.ctx, updateTimeout)
		defer cancel()
		rel, newer, err := update.Check(ctx, nil, feed, now)
		s.d.Run(func() {
			if s.ctx.Err() != nil {
				return
			}
			s.saidAboutUpdates(rel, newer, err)
		})
	}()
}

// saidAboutUpdates puts the answer where somebody can act on it.
func (s *Shell) saidAboutUpdates(rel update.Release, newer bool, err error) {
	switch {
	case errors.Is(err, update.ErrNoFeed):
		s.status.SetText("This build has no release feed to ask.")
	case err != nil:
		s.status.SetText("")
		s.showError(fmt.Errorf("could not ask about newer versions: %w", err))
	case !newer:
		s.status.SetText("This is the newest version there is: " + s.d.Version + ".")
	default:
		s.offerUpdate(rel)
	}
}

// offerUpdate says what is available and what is in it, and offers the page
// it is published on. Nothing is downloaded here.
func (s *Shell) offerUpdate(rel update.Release) {
	s.status.SetText("Version " + rel.Version + " is available.")
	notes := widget.NewLabel(rel.Notes)
	notes.Wrapping = fyne.TextWrapWord
	when := ""
	if !rel.Published.IsZero() {
		when = " · " + rel.Published.Local().Format("2 January 2006")
	}
	body := container.NewVBox(
		widget.NewLabelWithStyle("Version "+rel.Version+when, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		notes,
		quiet("Nothing has been downloaded. Opening the page takes you to where this version is published, "+
			"and installing it is yours to do."))
	var d dialog.Dialog
	open := widget.NewButton("Open the Release Page", func() {
		d.Hide()
		s.openReleasePage(rel.URL)
	})
	open.Importance = widget.HighImportance
	if rel.URL == "" {
		open.Disable()
	}
	later := widget.NewButton("Later", func() { d.Hide() })
	cd := dialog.NewCustomWithoutButtons("A newer version is available", container.NewVScroll(body), s.win)
	cd.SetButtons([]fyne.CanvasObject{later, open})
	cd.Resize(fyne.NewSize(520, 320))
	d = cd
	d.Show()
}

// openReleasePage hands the page to the browser. Only http and https are
// opened: a feed is a thing on a network, and a feed that answered with
// another scheme is a feed asking this application to open something else.
func (s *Shell) openReleasePage(raw string) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		s.showError(formError("That release page is not a web address, so it has not been opened."))
		return
	}
	if err := s.app.OpenURL(u); err != nil {
		s.showError(fmt.Errorf("could not open %s: %w", u.Redacted(), err))
	}
}
