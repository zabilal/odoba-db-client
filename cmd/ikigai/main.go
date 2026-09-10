// Command ikigai is the Ikigai DB desktop client.
//
// The shell is built in Phase 1 (TASKS.md T1.1). Phase 0 delivers the core
// contracts and the four UI spikes; this entrypoint exists so the module
// builds and CI has something to compile on every platform.
package main

import (
	"fmt"
	"os"

	"github.com/ikigai-db/ikigai-db/internal/source"

	// Drivers register themselves on import (REQ-DB-1). Adding a source to the
	// application is a blank import here, and nothing else.
	_ "github.com/ikigai-db/ikigai-db/internal/source/drivers/postgres"
)

// version is set at build time via -ldflags.
var version = "dev"

func main() {
	fmt.Fprintf(os.Stderr, "ikigai %s\n", version)

	drivers := source.Drivers()
	if len(drivers) == 0 {
		fmt.Fprintln(os.Stderr, "no drivers registered")
		return
	}
	for _, d := range drivers {
		fmt.Fprintf(os.Stderr, "  %s (%s)\n", d.Name, d.Paradigm)
	}
}
