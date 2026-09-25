// Command testplug is a plugin that behaves badly on purpose, for the tests
// about running somebody else's program.
//
// Under testdata so that it is not part of the application's build; the tests
// build it themselves. What it does is chosen by IKIGAI_TEST_PLUGIN:
//
//	id:NAME   a plugin that works, calling itself NAME
//	exit      a program that ends before saying anything
//	garbage   a program that writes something that is not the protocol
//	deaf      a program that says hello and then ignores everything, including
//	          being told to stop
//	bye       a program that says hello and then ends
//	bydir     a program that works, unless it was installed somewhere with
//	          "bad" in the path, where it writes nonsense instead
//	rowfirst  a program that writes a row before saying what the columns are
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ikigai-db/ikigai-db/plugin"
)

func main() {
	mode := os.Getenv("IKIGAI_TEST_PLUGIN")
	switch {
	case mode == "exit":
		os.Exit(3)
	case mode == "garbage":
		fmt.Println("I am not a plugin, I am a free program")
		time.Sleep(time.Minute)
	case mode == "deaf":
		// Says hello once, by hand, and then reads nothing ever again. A
		// process like this is why closing one has a kill behind it.
		//
		// Sleeping rather than blocking on a channel: a program with nothing
		// but a blocked goroutine is one Go's own runtime ends, and a process
		// that helpfully dies is not the process being tested for.
		fmt.Printf(`{"id":1,"hello":{"protocol":%d,"kind":"source","id":"deaf","name":"Deaf"},"done":true}`+"\n",
			plugin.Protocol)
		for {
			time.Sleep(time.Hour)
		}
	case mode == "bye":
		// Says hello and then ends, which is a plugin that has gone.
		fmt.Printf(`{"id":1,"hello":{"protocol":%d,"kind":"source","id":"bye","name":"Bye"},"done":true}`+"\n",
			plugin.Protocol)
		os.Exit(0)
	case mode == "bydir":
		// What it does depends on where it was installed, so that one program
		// can be a plugin that loads and a plugin that does not in the same
		// directory of plugins.
		if where, _ := os.Executable(); strings.Contains(where, "bad") {
			fmt.Println("I am not a plugin, I am a free program")
			for {
				time.Sleep(time.Hour)
			}
		}
		plugin.Serve(&named{id: "bydir"})
		return
	case mode == "rowfirst":
		// Writes a row before it has said what the columns are, which is a
		// plugin author's mistake and has to be reported as one.
		plugin.Serve(&wrongOrder{})
		return
	}
	id := strings.TrimPrefix(mode, "id:")
	if id == "" || id == mode {
		id = "testplug"
	}
	plugin.Serve(&named{id: id})
}

// named is a plugin that works and calls itself whatever it was told to.
type named struct{ id string }

func (n *named) Hello() plugin.Hello {
	return plugin.Hello{ID: n.id, Name: "Test " + n.id,
		Capabilities: plugin.Capabilities{Objects: []string{"table"}}}
}

func (n *named) Open(context.Context, plugin.Config) (string, error) { return "h", nil }
func (n *named) Close(string) error                                  { return nil }
func (n *named) Ping(context.Context, string) error                  { return nil }

func (n *named) Info(context.Context, string) (plugin.Info, error) {
	return plugin.Info{Product: "Test"}, nil
}

func (n *named) Root(context.Context, string) ([]plugin.Node, error) { return nil, nil }

func (n *named) Children(context.Context, string, plugin.Ref) ([]plugin.Node, error) {
	return nil, nil
}

func (n *named) Describe(context.Context, string, plugin.Ref) (plugin.Object, error) {
	return plugin.Object{}, nil
}

func (n *named) Browse(context.Context, string, plugin.Ref, plugin.BrowseOptions, plugin.RowWriter) error {
	return nil
}

// wrongOrder writes a row before its columns, which the protocol refuses.
type wrongOrder struct{ named }

func (w *wrongOrder) Hello() plugin.Hello {
	return plugin.Hello{ID: "rowfirst", Name: "Row first",
		Capabilities: plugin.Capabilities{Objects: []string{"table"}}}
}

func (w *wrongOrder) Browse(_ context.Context, _ string, _ plugin.Ref,
	_ plugin.BrowseOptions, rows plugin.RowWriter) error {
	return rows.Row("a value with no column")
}
