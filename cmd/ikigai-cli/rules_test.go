package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// What this binary is, held rather than remembered.
//
// Two claims are made for it, and both are easy to lose by accident. It carries
// every driver the application does, so a pipeline is not quietly narrower than
// the window. And it draws nothing, so it runs where there is no display — one
// import of a package that draws, anywhere below it, and a container image needs
// the platform's window libraries to run "ikigai query".

// modulePath is what a package of ours is called from anywhere in the module.
const modulePath = "github.com/ikigai-db/ikigai-db/internal/"

// unbuilt are the driver packages this binary deliberately does not carry, each
// behind a build tag, each with its reason written where the tag is. The same
// list the application's own rule holds, for the same reasons.
var unbuilt = map[string]string{
	"duckdb": "cgo and a copy of the engine per platform, behind the duckdb tag (ADR-0146)",
}

func TestTheCommandLineCarriesEveryDriver(t *testing.T) {
	root := moduleRoot(t)
	dir := filepath.Join(root, "internal", "source", "drivers")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	deps := listDeps(t, ".")
	found := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pkg := modulePath + "source/drivers/" + e.Name()
		carried := slices.Contains(deps, pkg)
		if why, ok := unbuilt[e.Name()]; ok {
			if carried {
				t.Errorf("%s is in this binary, and is listed here as not built: %s", e.Name(), why)
			}
			continue
		}
		if !carried {
			t.Errorf("internal/source/drivers/%s is not imported here, so the command line "+
				"cannot open it while the application can", e.Name())
		}
		found++
	}
	if found < 10 {
		t.Errorf("only %d driver packages were found under %s, which is fewer than this "+
			"application has: the directory is not being read as expected", found, dir)
	}
}

// drawing is the toolkit and everything that comes with it. A binary that must
// run without a display must not import any of it — not for a colour, not for a
// type, not for a constant.
var drawing = []string{"fyne.io/", "github.com/go-gl/", "github.com/go-text/"}

func TestTheCommandLineDrawsNothing(t *testing.T) {
	// A control first: a rule of this shape passes when it looks for the wrong
	// thing, so it is pointed at the binary that does draw and has to find it.
	if !reachesAny(listDeps(t, modulePath+"ui/shell"), drawing) {
		t.Fatalf("none of %v was found in internal/ui/shell, which draws: "+
			"the rule below would pass whatever the answer", drawing)
	}
	for _, dep := range listDeps(t, ".") {
		for _, prefix := range drawing {
			if strings.HasPrefix(dep, prefix) {
				t.Errorf("%s reaches %s, so this binary needs a window system to run", dep, prefix)
			}
		}
	}
}

func reachesAny(deps, prefixes []string) bool {
	for _, dep := range deps {
		for _, p := range prefixes {
			if strings.HasPrefix(dep, p) {
				return true
			}
		}
	}
	return false
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
