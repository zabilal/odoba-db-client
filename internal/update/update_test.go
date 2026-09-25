package update

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Asking a release feed whether there is a newer version (FR-15.10).

// A feed nobody configured is not asked anything.
func TestNoFeedAsksNothing(t *testing.T) {
	asked := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = true
	}))
	defer srv.Close()
	for _, url := range []string{"", "   "} {
		if _, _, err := Check(context.Background(), srv.Client(), url, "1.0.0"); !errors.Is(err, ErrNoFeed) {
			t.Errorf("%q said %v", url, err)
		}
	}
	if asked {
		t.Error("it asked something with no feed configured")
	}
}

// A feed that says there is a newer version says what is in it and where.
func TestAFeedWithANewerVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("it asked for %q", got)
		}
		w.Write([]byte(`{"version":"1.4.2","notes":"Faster grids.","url":"https://example.test/r/1.4.2",
			"published":"2026-09-01T00:00:00Z"}`))
	}))
	defer srv.Close()
	rel, newer, err := Check(context.Background(), srv.Client(), srv.URL, "1.4.1")
	if err != nil {
		t.Fatal(err)
	}
	if !newer {
		t.Error("1.4.2 is not newer than 1.4.1")
	}
	if rel.Version != "1.4.2" || rel.Notes != "Faster grids." || rel.URL == "" || rel.Published.IsZero() {
		t.Errorf("it read %+v", rel)
	}
	// And the same feed against the same version is not an update.
	if _, newer, err := Check(context.Background(), srv.Client(), srv.URL, "1.4.2"); err != nil || newer {
		t.Errorf("1.4.2 against 1.4.2 said newer=%v (%v)", newer, err)
	}
}

// What a feed answers is checked before it is believed.
func TestAFeedThatAnswersNonsense(t *testing.T) {
	for _, c := range []struct {
		name, body string
		code       int
	}{
		{"not json", "hello", 200},
		{"no version", `{"notes":"something"}`, 200},
		{"an error", `{"version":"2.0.0"}`, 500},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(c.code)
			w.Write([]byte(c.body))
		}))
		_, newer, err := Check(context.Background(), srv.Client(), srv.URL, "1.0.0")
		srv.Close()
		if err == nil {
			t.Errorf("%s was believed", c.name)
		}
		if newer {
			t.Errorf("%s was taken for an update", c.name)
		}
	}
}

// A feed longer than a release note could be is refused rather than read:
// a megabyte of prose is a feed that has gone wrong, or is not a feed.
func TestAFeedLongerThanTheLimitIsRefused(t *testing.T) {
	// Valid JSON throughout, and too long: what must stop it is the limit
	// rather than the end of the bytes.
	long := `{"version":"2.0.0","notes":"` + strings.Repeat("a", feedLimit+1024) + `"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(long))
	}))
	defer srv.Close()
	if _, _, err := Check(context.Background(), srv.Client(), srv.URL, "1.0.0"); err == nil {
		t.Error("a feed longer than the limit was read to the end")
	}
	// And one just inside it is read.
	short := `{"version":"2.0.0","notes":"` + strings.Repeat("a", 1024) + `"}`
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(short))
	}))
	defer srv2.Close()
	if _, newer, err := Check(context.Background(), srv2.Client(), srv2.URL, "1.0.0"); err != nil || !newer {
		t.Errorf("a feed inside the limit said newer=%v (%v)", newer, err)
	}
}

// Cancelling stops it.
func TestCancellingStopsACheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(10 * time.Millisecond); cancel() }()
	if _, _, err := Check(ctx, srv.Client(), srv.URL, "1.0.0"); err == nil {
		t.Error("a cancelled check answered")
	}
}

// Which version is newer, including the cases a build actually meets.
func TestWhichVersionIsNewer(t *testing.T) {
	for _, c := range []struct {
		release, current string
		want             bool
	}{
		{"1.4.2", "1.4.1", true},
		{"1.5.0", "1.4.9", true},
		{"2.0.0", "1.9.9", true},
		{"v1.4.2", "1.4.1", true},
		{"1.4.2", "v1.4.2", false},
		{"1.4.1", "1.4.2", false},
		{"1.4", "1.3.9", true},
		{"1.4.2", "1.4.2-rc.1", true}, // the release beats its own pre-release
		{"1.4.2-rc.1", "1.4.2", false},
		{"1.4.2-rc.2", "1.4.2-rc.1", false}, // pre-releases are not ordered here
		// A development build takes no part: it knows it is not a release.
		{"1.4.2", "dev", false},
		{"dev", "1.4.2", false},
		// A version nobody can read takes no part, whatever it is next
		// to: not even next to a pre-release of nothing, where the
		// numbers it does not have would otherwise match.
		{"latest", "0.0.0-rc.1", false},
		{"", "1.4.2", false},
		{"1.4.2.1", "1.4.2", false},
		{"one.four.two", "1.4.2", false},
	} {
		if got := Newer(c.release, c.current); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.release, c.current, got, c.want)
		}
	}
}
