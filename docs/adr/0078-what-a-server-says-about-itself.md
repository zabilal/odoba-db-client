# ADR-0078: What a server says about itself

**Status:** Accepted · **Date:** 2026-09-13
**Tasks:** T2.45 · **Requirements:** FR-2.4, FR-12.2 · **Packages:** `internal/model`, `internal/source/drivers/redis`, `internal/ui/shell`

## Context

FR-12.2 asks for a memory and keyspace info panel. The application already has
a place for what an object is made of — the structure tab — and it shows a
table's columns, a view's definition, a collection's inferred fields. A Redis
database has none of those: it has a count of keys, and a server behind it
that is spending memory on them.

## Decisions

1. **A keyspace and a key are model types of their own.** `model.Keyspace` and
   `model.StoredKey`, beside `Table`, `View` and `Collection`. Neither has
   columns, so neither is a `Table` with the columns left out; what there is
   to say about them is what the server reports.

2. **The figures keep the server's own names.** `used_memory_human`,
   `keyspace_hits`, `mem_fragmentation_ratio`: these are what a person
   searches the Redis documentation for, and a friendlier name would be one
   more thing to translate. They are grouped under INFO's own headings —
   Server, Memory, Clients, Statistics, Persistence, Replication — and listed
   in the order this application chose rather than the order the server
   happened to print, so that the same panel reads the same way on every
   server.

3. **A figure the server did not report is left out**, and a heading with
   nothing under it is not shown. A managed Redis reports a subset, and a
   panel of empty rows would say less than a shorter one.

4. **A server that will not say is not a failure.** If `INFO` is refused — as
   it is on some managed services — the panel still shows the keys the
   database holds. What is not answered is left unsaid rather than turned into
   an error over a read-only panel.

5. **A cluster's shards are a group of their own**, since no single server's
   `INFO` covers them: each master's address, how many keys it holds, and what
   it is using.

6. **A key is described by asking after it**: `TYPE`, `TTL`, `MEMORY USAGE`,
   `OBJECT ENCODING`, and the length its kind is counted in — characters,
   fields, elements, members, entries. `MEMORY` and `OBJECT` are refused on
   some servers, and what they do not answer is left unsaid.

7. **The structure shown is of whatever a person is looking at.** Open
   Structure took the explorer's selection; now, where there is none, it takes
   the object the tab in front is on. A Redis key is never in the tree — keys
   are rows (ADR-0073) — so this is the only way to reach one, and it is a
   better rule for every other source too.

## Consequences

- The panel is read-only, as the structure tab is. Changing what it shows is
  done where it is done: a key's expiry in the keyspace grid (ADR-0076),
  everything else in the console.
- The list of figures will age as Redis adds them. It ages harmlessly: a
  figure nobody listed is simply not shown.
- `model.Keyspace` and `model.StoredKey` are the shapes any key-value source
  would report, so Valkey, KeyDB and Dragonfly need nothing new here.
