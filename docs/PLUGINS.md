# Writing a plugin

A plugin adds a source the application does not have (FR-16.2). It is a
**program**, not a library: it speaks JSON lines on its standard input and
output, so it can be written in any language, it cannot take the application
down when it crashes, and it can be stopped by being killed.

Nothing here is a stable promise yet. The protocol is version 1 and the
application refuses a plugin that speaks another version, which is the only
compatibility guarantee: a plugin says which version it speaks and is talked to
or not.

## Installing one

A plugin is a directory holding a program called `plugin` (`plugin.exe` on
Windows):

    ~/.local/share/ikigai-db/plugins/
      csvdir/
        plugin
        README

The folder is `plugins` inside the application's data directory unless the
settings name another. **Nothing runs until plugins are turned on**, because a
program that ran because it was in a folder is a program nobody chose:

```json
{"plugins": {"enabled": true}}
```

What loaded, and what did not and why, is in the log.

## What a plugin is trusted with

A plugin is code you are running. When you open a connection with it, it is sent
that connection's settings **and its secrets** — that is what it needs to
connect, and there is no way to give it less. Install one the way you would
install anything else that can read your passwords.

What a plugin says back is data and never instruction: an object tree, a
described object, rows, and failures. Nothing it answers becomes a statement the
application runs.

## The conversation

One JSON object per line, in both directions. The host asks; the plugin answers
with the same `id`. Several requests may be in flight at once.

    host   {"id":1,"op":"hello"}
    plugin {"id":1,"hello":{"protocol":1,"kind":"source","id":"csvdir","name":"CSV folder",
            "fields":[{"key":"database","label":"Folder","kind":"file","required":true}],
            "capabilities":{"objects":["table"]}},"done":true}

    host   {"id":2,"op":"open","config":{"database":"/data","secrets":{}}}
    plugin {"id":2,"handle":"1","done":true}

    host   {"id":3,"op":"root","handle":"1"}
    plugin {"id":3,"nodes":[{"ref":{"kind":"folder","path":["csv","table"]},
            "label":"Tables","has_children":true}],"done":true}

    host   {"id":4,"op":"browse","handle":"1",
            "ref":{"kind":"table","path":["csv","items"]},"browse":{"limit":2}}
    plugin {"id":4,"columns":[{"name":"id","class":"integer"},{"name":"name"}]}
    plugin {"id":4,"row":[1,"one"]}
    plugin {"id":4,"row":[2,"two"]}
    plugin {"id":4,"done":true}

Every request ends with exactly one line carrying `"done":true` or `"error"`.
Rows stream between the columns and the done, so a plugin's memory does not grow
with the size of what it is reading.

### The operations

| op | what it answers |
|---|---|
| `hello` | `hello`: the driver's name, its form, what it can do |
| `open` | `handle` for the connection; `config` carries the settings and the secrets |
| `close` | `ok` |
| `ping` | `ok` |
| `info` | `info`: the product, its version, the database |
| `root` | `nodes`: the top of the tree |
| `children` | `nodes`: what is under `ref` |
| `describe` | `object`: one object's columns, primary key or definition |
| `browse` | `columns`, then a `row` a line, for `ref` and `browse` |
| `cancel` | nothing; `of` names the request to abandon |

A failure is `{"id":N,"error":"what went wrong","kind":"auth"}`. The kinds are
`config`, `auth`, `network`, `tls`, `nodatabase` and `refused`, and they decide
what the application says when a connection will not open.

### What is not in version 1

**Statements.** A query editor is not a "run this" call: it is splitting a
script, classifying each statement so that a read-only connection can refuse the
ones that write, quoting identifiers and completing from the schema. A protocol
that answered only "run this" would be asking the application to trust a plugin
with the one guard that exists to be untrusting. Browsing is the whole of the
data path here, as it is the required one for every source (ADR-0005).

**Writing.** No inserts, updates, deletes or DDL. A plugin is read-only, and the
application does not offer what a plugin cannot do.

**Counts and sizes.** The badge beside a tree node is the expensive question,
asked once per visible node; it is not asked of a plugin at all.

## Writing one in Go

Implement `plugin.Source` and call `plugin.Serve`. The whole of
[`examples/csvdir`](../examples/csvdir/main.go) — a folder of CSV files browsed as
a database — is about two hundred lines, most of them reading CSV.

```go
package main

import (
    "context"
    "github.com/ikigai-db/ikigai-db/plugin"
)

func main() { plugin.Serve(&mine{}) }

type mine struct{}

func (m *mine) Hello() plugin.Hello {
    return plugin.Hello{ID: "mine", Name: "My Source",
        Capabilities: plugin.Capabilities{Objects: []string{"table"}}}
}
// ... Open, Close, Ping, Info, Root, Children, Describe, Browse
```

Build it into place:

    go build -o ~/.local/share/ikigai-db/plugins/mine/plugin ./cmd/my-plugin

## Writing one in another language

Read a line, decode it, answer with the same `id`. The protocol above is the
whole specification; the Go package is a convenience and not a requirement.

Three things to get right:

- **Flush after every line.** A host waiting for an answer sitting in your
  output buffer looks like a plugin that has hung.
- **Say `done` exactly once per request,** or `error` instead of it.
- **Write nothing else to standard output.** Anything on standard error is
  logged as your plugin's own words, which is where to put diagnostics.

## What the host does about a plugin that misbehaves

It is not a trusting host, and none of the following is hypothetical: the first
plugin written against a new protocol gets something wrong.

- A node with no path, or no kind, is dropped rather than drawn.
- A row longer than its columns is cut; a shorter one is padded.
- A row with no values at all is an error, because the line it would make cannot
  be told from a line with no row in it.
- A line that is not JSON is logged and ignored; the conversation goes on.
- An answer to a request nobody is waiting for is dropped.
- A plugin that stops reading its input fails its requests rather than hanging
  the application.
- A plugin that will not stop when its input closes is killed.
