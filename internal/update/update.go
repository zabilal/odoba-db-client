// Package update asks a release feed whether there is a newer version, and
// says what changed (FR-15.10).
//
// It does not install anything, and it does not run on its own. Two rules
// shape this package, both of them from elsewhere in the requirements:
//
// Nothing this application does by itself touches the network (NFR-D6), so
// there is no check on start and no timer. A check happens when somebody
// asks for one, or when they have said it may happen at a start — and even
// then only if a feed has been configured, which by default it has not.
//
// Nothing is downloaded and run. Replacing a running binary is the part
// that needs signing and notarisation to be safe (NFR-D4), and until those
// exist an application that installed its own updates would be an
// application asking people to trust an unsigned download. So what this
// does is tell somebody there is a newer version, show them what is in it,
// and open the page where it is published. The install is theirs.
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ErrNoFeed is a check with nowhere to ask. It is the ordinary state of a
// build nobody has pointed at a release feed.
var ErrNoFeed = errors.New("update: no release feed is configured")

// A Release is what a feed says about the latest version.
type Release struct {
	// Version is the release, as "1.4.2" or "v1.4.2".
	Version string `json:"version"`
	// Notes is what changed, as text a person reads.
	Notes string `json:"notes"`
	// URL is the page it is published on, which is what somebody is sent
	// to. No file is fetched from here.
	URL string `json:"url"`
	// Published is when, for saying how old a version is.
	Published time.Time `json:"published"`
}

// feedLimit caps what is read from a feed. A release note is prose; a
// megabyte of it is a feed that has gone wrong or is not a feed.
const feedLimit = 1 << 20

// checkTimeout bounds a check. Somebody who asked is waiting.
const checkTimeout = 10 * time.Second

// Check asks the feed at url what the latest release is, and reports
// whether it is newer than current.
//
// An empty url answers ErrNoFeed without asking anything: a build with no
// feed is a build that never reaches the network for this.
func Check(ctx context.Context, client *http.Client, url, current string) (Release, bool, error) {
	if strings.TrimSpace(url) == "" {
		return Release{}, false, ErrNoFeed
	}
	if client == nil {
		client = &http.Client{Timeout: checkTimeout}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Release{}, false, err
	}
	req.Header.Set("Accept", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return Release{}, false, fmt.Errorf("update: asking %s: %w", url, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return Release{}, false, fmt.Errorf("update: %s answered %s", url, res.Status)
	}
	var rel Release
	if err := json.NewDecoder(io.LimitReader(res.Body, feedLimit)).Decode(&rel); err != nil {
		return Release{}, false, fmt.Errorf("update: what %s answered is not a release: %w", url, err)
	}
	if strings.TrimSpace(rel.Version) == "" {
		return Release{}, false, fmt.Errorf("update: what %s answered names no version", url)
	}
	return rel, Newer(rel.Version, current), nil
}

// Newer reports whether release is a later version than current.
//
// A version this build cannot read is not newer: a development build calls
// itself "dev", and telling somebody running one that every release is an
// update would be telling them something they already know in a way they
// cannot act on.
func Newer(release, current string) bool {
	r, ok := parse(release)
	if !ok {
		return false
	}
	c, ok := parse(current)
	if !ok {
		return false
	}
	for i := range r.nums {
		switch {
		case r.nums[i] > c.nums[i]:
			return true
		case r.nums[i] < c.nums[i]:
			return false
		}
	}
	// Same numbers: a release is newer than a pre-release of it, and a
	// pre-release is never newer than the release itself.
	return r.pre == "" && c.pre != ""
}

type version struct {
	nums [3]int
	pre  string
}

// parse reads "v1.4.2", "1.4.2" and "1.4.2-rc.1". Anything else is not a
// version, which is not an error: it is a build that does not take part.
func parse(s string) (version, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	if s == "" {
		return version{}, false
	}
	var v version
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		v.pre, s = s[i+1:], s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return version{}, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return version{}, false
		}
		v.nums[i] = n
	}
	return v, true
}
