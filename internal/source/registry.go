package source

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// The registry is how REQ-DB-1 is satisfied: a driver package registers itself
// at init time, and the UI discovers it through Drivers(). Adding a source
// requires no edit to any UI file.

var (
	registryMu sync.RWMutex
	registry   = map[string]Driver{}
)

// Register makes a driver available. It is called from a driver package's init
// function and panics on a duplicate ID, since that is a programming error
// discoverable at startup.
func Register(d Driver) {
	desc := d.Describe()
	if desc.ID == "" {
		panic("source: driver registered with an empty ID")
	}

	registryMu.Lock()
	defer registryMu.Unlock()

	if _, dup := registry[desc.ID]; dup {
		panic(fmt.Sprintf("source: driver %q registered twice", desc.ID))
	}
	registry[desc.ID] = d
}

// Lookup returns the driver with the given ID.
func Lookup(id string) (Driver, error) {
	registryMu.RLock()
	defer registryMu.RUnlock()

	d, ok := registry[id]
	if !ok {
		return nil, fmt.Errorf("source: no driver registered for %q", id)
	}
	return d, nil
}

// Drivers returns every registered driver's descriptor, ordered by name, for
// the new-connection picker.
func Drivers() []Descriptor {
	registryMu.RLock()
	defer registryMu.RUnlock()

	out := make([]Descriptor, 0, len(registry))
	for _, d := range registry {
		out = append(out, d.Describe())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// LookupScheme finds the driver claiming a connection-string scheme, which is
// what makes pasting a URL into the connection form work (FR-1.3).
func LookupScheme(scheme string) (Descriptor, bool) {
	scheme = strings.ToLower(strings.TrimSuffix(scheme, ":"))

	registryMu.RLock()
	defer registryMu.RUnlock()

	for _, d := range registry {
		desc := d.Describe()
		for _, s := range desc.URLSchemes {
			if strings.EqualFold(s, scheme) {
				return desc, true
			}
		}
	}
	return Descriptor{}, false
}

// reset clears the registry. Test-only.
func reset() {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry = map[string]Driver{}
}
