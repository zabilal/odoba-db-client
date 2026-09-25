package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// A driver nobody can choose is not a driver. Registering one is a blank
// import in main.go and nothing else, which is easy to write and just as
// easy to forget: Kafka was written, given a tree, an admin menu, a record
// viewer and a produce dialog, and was never imported here — so none of it
// could be reached from the application at all. Nothing said so, because
// every one of its own tests imports its package directly.
//
// So the driver packages are held against what this binary actually depends
// on, both of them read rather than written down. A new directory under
// internal/source/drivers fails this until the application can open it.
//
// The import graph is what is checked rather than the registry, because a
// package's directory name and the ID it registers are not always the same
// word (mongo registers "mongodb"), and a name is not the thing being
// asserted: whether the binary carries the code is.

// unbuilt are the driver packages this build deliberately does not carry,
// each behind a build tag, each with its reason written where the tag is.
var unbuilt = map[string]string{
	"duckdb": "cgo and a copy of the engine per platform, behind the duckdb tag (ADR-0146)",
}

func TestEveryDriverPackageIsInTheBinary(t *testing.T) {
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
				t.Errorf("%s is in the binary, and is listed here as not built: %s", e.Name(), why)
			}
			continue
		}
		if !carried {
			t.Errorf("internal/source/drivers/%s is not imported by main.go, so nothing "+
				"in the application can open it", e.Name())
		}
		found++
	}
	// A rule of this shape passes when it finds nothing to check, so it says
	// how much it found. The number is a floor and not a count: it fails when
	// the directory stops being readable in the way this assumes, not when a
	// driver is added.
	if found < 10 {
		t.Errorf("only %d driver packages were found under %s, which is fewer than "+
			"this application has: the directory is not being read as expected", found, dir)
	}
}
