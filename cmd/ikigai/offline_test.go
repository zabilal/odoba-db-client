package main

import (
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// NFR-D6: the application needs no network for any core function, and
// nothing it does on its own touches the network at all.
//
// "Offline-capable" is easy to claim and easy to lose: one convenience —
// a telemetry ping, an update check on start, a font fetched the first
// time — and a person on a plane with a SQLite file has an application
// that hangs. So it is held here rather than remembered.
//
// The network is used in exactly four places, each of them something a
// person asked for: a driver connecting to a server they configured, an
// SSH tunnel they configured, a cloud IAM token a connection asked for,
// and the assistant, which is off until somebody turns it on and is
// contacted only when they ask it a question (FR-14.4). Nothing contacts
// any host belonging to this project.

// netFree are the packages that must not be able to reach the network at
// all, whatever a future change does above them: what is kept, what is
// read and written, and what text is made of.
//
// The packages that draw are not here, and cannot be: fyne.io/fyne/v2
// itself reaches net/http for its own URI handling, so the import graph
// says nothing about a package that draws anything. What covers those is
// the file rule below — no file of ours outside internal/cloud imports
// net/http at all — which is the stronger statement anyway.
var netFree = []string{
	"store/...",
	"export",
	"transfer",
	"model",
	"diff",
	"sqlfmt",
	"sqllex",
	// The plugin host runs a program somebody installed. It does not fetch
	// one: there is no plugin directory to download from and no plugin
	// manager, and a host that could reach the network would be the beginning
	// of both (FR-16.2).
	"plugin",
}

// modulePath is what a package of ours is called from anywhere in the
// module, which is what lets this test run from its own directory.
const modulePath = "github.com/ikigai-db/ikigai-db/internal/"

// forbidden are the ways of reaching a network that none of those packages
// may have.
var forbidden = []string{"net/http", "net/smtp", "net/rpc"}

func TestTheseLayersCannotReachTheNetwork(t *testing.T) {
	// A control first. A rule of this shape passes when it looks for the
	// wrong thing, so it is pointed at a package that does reach the
	// network — the cloud token fetchers — and has to find it there.
	control := listDeps(t, modulePath+"cloud")
	if !reachesAny(control, forbidden) {
		t.Fatalf("none of %v was found in internal/cloud, which reaches the network: "+
			"the rule below would pass whatever the answer", forbidden)
	}
	if len(importers(t, modulePath+"cloud", "net/http")) == 0 {
		t.Fatal("nothing in internal/cloud was found to import net/http, which its files do: " +
			"the rule below would name nobody whoever it was")
	}
	for _, pkg := range netFree {
		deps := listDeps(t, modulePath+pkg)
		for _, bad := range forbidden {
			if !slices.Contains(deps, bad) {
				continue
			}
			// It is there. Whoever brought it is named, and has to be one
			// of the few known to carry it without using it.
			by := importers(t, modulePath+pkg, bad)
			if len(by) == 0 {
				t.Errorf("%s reaches %s, through the standard library alone", pkg, bad)
			}
			for _, who := range by {
				if carried[who] != bad {
					t.Errorf("%s reaches %s, through %s", pkg, bad, who)
				}
			}
		}
	}
}

// carried are the packages of other people's that import a way of reaching
// the network and do not reach it, each with the one thing it imports. A
// package here was read before it was written here.
//
// The Brotli codec is one file-format library's compression (Parquet,
// ADR-0180; ClickHouse's driver links it too). Its http.go offers a server a
// writer that compresses a response it is already writing: it takes an
// http.ResponseWriter and an *http.Request, sets a header and returns a
// writer. Nothing in it dials, listens or holds a client, and nothing here
// calls it. The import graph cannot tell that from a package that does, so it
// is said here, for this package and this import and no other.
var carried = map[string]string{
	"github.com/andybalholm/brotli": "net/http",
}

// importers are the packages outside the standard library, among everything a
// package of ours is built from, that import bad themselves.
func importers(t *testing.T, pkg, bad string) []string {
	t.Helper()
	out, err := exec.Command("go", "list", "-deps", "-f",
		`{{if not .Standard}}{{.ImportPath}} {{join .Imports ","}}{{end}}`, pkg).Output()
	if err != nil {
		t.Fatalf("go list -deps %s: %v", pkg, err)
	}
	var who []string
	for _, line := range strings.Split(string(out), "\n") {
		path, imports, _ := strings.Cut(line, " ")
		if slices.Contains(strings.Split(imports, ","), bad) {
			who = append(who, path)
		}
	}
	return who
}

func reachesAny(deps, any []string) bool {
	for _, a := range any {
		if slices.Contains(deps, a) {
			return true
		}
	}
	return false
}

// TestOnlyTheseFilesSpeakHTTP holds the other half: the places that do
// reach the network are the ones this application means to have, and a new
// one has to be added here on purpose.
func TestOnlyTheseFilesSpeakHTTP(t *testing.T) {
	// The cloud token fetchers, and little else of ours. Drivers reach the
	// network through their own libraries rather than net/http, and each is
	// a server somebody configured.
	allowed := []string{
		filepath.Join("internal", "cloud", "aws.go"),
		filepath.Join("internal", "cloud", "azure.go"),
		filepath.Join("internal", "cloud", "cloud.go"),
		filepath.Join("internal", "cloud", "gcp.go"),
		// The release feed, asked only when somebody asks and only by a
		// build that was given a feed to ask — which one made from source
		// is not (FR-15.10).
		filepath.Join("internal", "update", "update.go"),
		// The assistant, which asks a language model a question (FR-14). It is
		// off by default and off per connection, and the consent that turns it
		// on is enforced before anything is sent rather than in the window
		// (internal/assistant/consent.go). Nothing here is contacted unless
		// somebody asks it something, and the endpoint is one they gave: there
		// is no provider compiled in that would be reached by default.
		filepath.Join("internal", "assistant", "provider.go"),
		// The one driver that is its own library: Elasticsearch and
		// OpenSearch are spoken to directly, because the official client of
		// one refuses the other (ADR-0173). It is a server somebody
		// configured, like every other driver's, and this is the one file of
		// it that speaks.
		filepath.Join("internal", "source", "drivers", "elasticsearch", "wire.go"),
	}
	root := moduleRoot(t)
	out, err := exec.Command("grep", "-rl", "net/http",
		filepath.Join(root, "internal"), filepath.Join(root, "cmd")).CombinedOutput()
	if err != nil && len(out) == 0 {
		t.Fatalf("looking for net/http: %v", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" || strings.HasSuffix(line, "_test.go") {
			continue
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(filepath.Clean(line), root), string(filepath.Separator))
		if !slices.Contains(allowed, rel) {
			t.Errorf("%s imports net/http; if that is meant, say so here and say why", rel)
		}
	}
}

// moduleRoot is where this module's files are.
func moduleRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}").Output()
	if err != nil {
		t.Fatalf("finding the module root: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// listDeps is every package a package depends on, as the toolchain sees it.
func listDeps(t *testing.T, pkg string) []string {
	t.Helper()
	out, err := exec.Command("go", "list", "-deps", pkg).Output()
	if err != nil {
		t.Fatalf("go list -deps %s: %v", pkg, err)
	}
	return strings.Split(strings.TrimSpace(string(out)), "\n")
}
