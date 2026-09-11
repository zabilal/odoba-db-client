# Architecture

Companion to [REQUIREMENTS.md](REQUIREMENTS.md) §10. This file is the working
summary; the requirements document is authoritative.

## Layering

Dependencies point downward only.

```
cmd/ikigai              entrypoint (and, later, the CLI)
internal/ui             Fyne widgets, layouts, theme
  theme/                custom fyne.Theme — Phase 0 deliverable
  grid/                 W1 — data grid layer
  editor/               W2 — query editor widget
  canvas/               W3 — node canvas (ER + designer)
  chart/                W4 — charting
internal/app            use cases: connections, live health, sessions, tabs, tasks
  connstr/              pasted URLs, JDBC, key=value, .pgpass, ~/.my.cnf
internal/source         data-source abstraction, pooling, streaming
  drivers/              one package per source (incl. kafka/)
  introspect/           schema reading → canonical model
  sqlgen/               per-dialect SQL/CQL/DDL generation, quoting
  capability/           per-source feature descriptors
  conformance/          the one battery every driver runs (REQ-DRV-1)
internal/model          canonical model (relational · document · kv · stream)
internal/value          a value as text, read as its column's type (editors, imports)
internal/diff           schema comparison & sync-script synthesis
internal/transfer       import/export pipelines (streaming)
internal/store          paths and the settings file (JSON, atomic, never clobbered)
  secrets/              OS keychain; no file fallback, ever
  localdb/              SQLite: history (redacted, searchable), saved queries, session state
internal/logging        slog JSON, redacted at the handler, rotated by size
internal/redact         secret removal for logs and errors
internal/sqllex         SQL tokeniser shared by the editor and the drivers
```

## The four rules that matter

**ARCH-1 — the core imports no UI.** `internal/model`, `internal/source`,
`internal/diff`, `internal/transfer`, `internal/store` and `internal/redact`
must be usable as a library, testable headlessly, with no Fyne anywhere. This
is enforced mechanically by a `depguard` rule in `.golangci.yml`, not by
convention. It is what keeps the valuable core portable if ADR-0001 is ever
revisited.

**ARCH-2 — the UI never constructs SQL.** All statement text originates in a
`Dialect`. Identifiers reach statements only through `QuoteIdentifier`, and
user values only as bound parameters (NFR-S6).

**ARCH-4 — everything takes a context.** Every driver call is cancellable, and
cancellation must take effect promptly (NFR-P9). The conformance suite checks
this rather than trusting it.

**ARCH-6 — one goroutine boundary.** UI mutation happens on the Fyne main
goroutine; driver work happens on workers. Results cross through an explicit
channel. No shared mutable state between the two.

## The driver contract

A source is added by implementing four things and calling `source.Register`.
No UI file changes (REQ-DB-1).

**Required**

| Interface | Purpose |
|---|---|
| `Driver` | static metadata and `Open` |
| `Introspector` | enumerate objects lazily into `model.Node`s |
| `Browser` | open an object as a `model.RowStream` |
| `Capabilities()` | declare, as data, what the source supports |

**Optional, discovered by type assertion**

`Queryer` · `Sessioner` · `Dialect` · `Writer` · `Explainer` · `Transactor` · `Killer` ·
`Snapshotter` · `Searcher` · `Countable` · `DistinctLister` · `BulkLoader` ·
`Scriptable` · `Completer` · `StreamAdmin` · `StreamProducer` · `SchemaRegistry`

Each optional interface has a corresponding capability field. The conformance
suite fails a driver that *claims* a capability without implementing the
interface behind it — a claimed-but-absent capability is worse than an
unclaimed one, because the UI offers an affordance that fails at runtime.

### Why Browse and not Query

See [ADR-0005](docs/adr/0005-browser-as-required-data-path.md). Briefly: the
grid needs rows, not statements. Relational sources produce them from generated
SQL; Kafka produces them by consuming partitions; Redis by scanning keys. Making
`Browse` the required path is what lets a log be a first-class source instead of
a special case, and `internal/source/contract_kafka_test.go` proves it by
compilation.

## Four paradigms, one surface

`internal/model` represents relational, document, key-value and stream
structure. Three things unify them so that UI code never branches on source
type (REQ-DB-4):

- **`ObjectRef`** — path-based addressing that works for
  `table:sales.public.orders` and `topic:orders` alike.
- **`RowStream`** — the single path any paradigm's data takes to the grid.
- **`Capabilities`** — every "can this source do X?" question, answered as data.

The test for whether something belongs in `Capabilities`: if UI code would
otherwise need to know which engine it is talking to, it belongs there.

## Safety

`source.Guard` is the single chokepoint for read-only mode and production
confirmation (NFR-S4, FR-4.9). Every operation declares an `Access`
(read/write/DDL/admin) and passes through it. Enforcement is in the data layer
because a disabled button is a courtesy, not a control.

`Dialect.Classify` is deliberately conservative: anything not confidently
read-only is reported as mutating. A misclassified write on a read-only
production connection is the failure this exists to prevent.

`internal/redact` is the one implementation of secret removal (NFR-S2), applied
indiscriminately at the logging boundary rather than selectively at call sites —
selective redaction is redaction that eventually gets forgotten.
