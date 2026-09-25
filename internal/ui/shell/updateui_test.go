package shell

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

// Asking whether there is a newer version (FR-15.10).

// opened records where the window sent the browser.
type opened struct {
	fyne.App
	url *url.URL
	err error
}

func (o *opened) OpenURL(u *url.URL) error {
	o.url = u
	return o.err
}

// updating is a window over a feed, with the browser watched.
func updating(t *testing.T, body string, code int) (*fixture, *opened) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(code)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	fx := newFixture(t)
	d := fx.deps
	d.Version, d.UpdateFeed = "1.4.1", srv.URL
	browser := &opened{App: fx.s.app}
	s := New(browser, d)
	t.Cleanup(s.shutdown)
	fx.s = s
	return fx, browser
}

// A build with no feed never asks, and is not offered the command.
func TestABuildWithNoFeedNeverAsks(t *testing.T) {
	fx := newFixture(t)
	fx.s.sync()
	if fx.s.canCheckForUpdates() {
		t.Error("a build with no feed says it can ask")
	}
	if !fx.s.menuItems[cmdCheckUpdates].Disabled {
		t.Error("a build with no feed is offered the check")
	}
}

// A newer version is said, with what is in it, and the page is offered
// rather than fetched.
func TestANewerVersionIsOffered(t *testing.T) {
	fx, browser := updating(t, `{"version":"1.4.2","notes":"Faster grids.","url":"https://example.test/r/1.4.2"}`, 200)
	fx.s.sync()
	if fx.s.menuItems[cmdCheckUpdates].Disabled {
		t.Fatal("a build with a feed is not offered the check")
	}
	fx.s.run(cmdCheckUpdates)
	pump(t, fx.q, func() bool { return fx.s.win.Canvas().Overlays().Top() != nil })
	top := fx.s.win.Canvas().Overlays().Top()
	said := strings.Join(drawnSkipping(top, nil), " ")
	for _, want := range []string{"1.4.2", "Faster grids.", "Nothing has been downloaded"} {
		if !strings.Contains(said, want) {
			t.Errorf("it does not say %q: %s", want, said)
		}
	}
	if browser.url != nil {
		t.Errorf("it opened %v before being asked to", browser.url)
	}
	test.Tap(findButton(top, "Open the Release Page"))
	fx.q.Flush()
	if browser.url == nil || browser.url.String() != "https://example.test/r/1.4.2" {
		t.Errorf("it opened %v", browser.url)
	}
}

// The newest version there is says so, in the status line, without a sheet
// to dismiss.
func TestTheNewestVersionSaysSo(t *testing.T) {
	fx, _ := updating(t, `{"version":"1.4.1","notes":"x"}`, 200)
	fx.s.run(cmdCheckUpdates)
	pump(t, fx.q, func() bool { return strings.Contains(fx.s.status.Text, "newest version") })
	if top := fx.s.win.Canvas().Overlays().Top(); top != nil {
		t.Errorf("it put %T up to say there was nothing", top)
	}
}

// A feed that is only whitespace is a build with no feed, said quietly in
// the status line rather than as something that went wrong.
func TestAFeedOfWhitespaceIsNoFeed(t *testing.T) {
	fx, _ := updating(t, `{"version":"9.9.9"}`, 200)
	fx.s.d.UpdateFeed = "   "
	fx.s.checkForUpdates()
	pump(t, fx.q, func() bool { return strings.Contains(fx.s.status.Text, "no release feed") })
	if said := strings.Join(drawnSkipping(fx.s.errors.slot, nil), " "); said != "" {
		t.Errorf("it treated having no feed as a failure: %q", said)
	}
}

// A feed that will not answer is said and not swallowed.
func TestAFeedThatWillNotAnswer(t *testing.T) {
	fx, _ := updating(t, "not a release", 500)
	fx.s.run(cmdCheckUpdates)
	pump(t, fx.q, func() bool {
		return strings.Contains(strings.Join(drawnSkipping(fx.s.errors.slot, nil), " "), "newer versions")
	})
}

// A release page that is not a web address is not opened: a feed that
// answered with another scheme is a feed asking this application to open
// something else.
func TestAReleasePageThatIsNotAWebAddress(t *testing.T) {
	fx, browser := updating(t, `{"version":"1.4.2","notes":"x","url":"file:///etc/passwd"}`, 200)
	fx.s.run(cmdCheckUpdates)
	pump(t, fx.q, func() bool { return fx.s.win.Canvas().Overlays().Top() != nil })
	fx.s.openReleasePage("file:///etc/passwd")
	if browser.url != nil {
		t.Errorf("it opened %v", browser.url)
	}
	if said := strings.Join(drawnSkipping(fx.s.errors.slot, nil), " "); !strings.Contains(said, "not a web address") {
		t.Errorf("the window says %q", said)
	}
}
