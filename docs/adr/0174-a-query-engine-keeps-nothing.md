# ADR-0174: A query engine keeps nothing

**Status:** Accepted · **Date:** 2026-10-12
**Tasks:** T5.15 · **Requirements:** §4.3, FR-1.4, FR-2.1, FR-2.5, FR-3.3, FR-3.4, FR-3.6, FR-4.7, FR-4.9, FR-5.1–5.5, FR-5.10, REQ-DRV-3, NFR-S3, NFR-S4, NFR-S6, NFR-P11
**Packages:** `internal/source/drivers/trino`, `internal/sqllex`, `cmd/ikigai`, `cmd/ikigai-cli`, `.github/workflows`

## Context

The second of T5.15's four sources. Trino is not a database: it is a query
engine in front of other people's. A catalog is somebody's warehouse, lake or
operational store; a schema is a namespace in it; a table is whatever that store
calls one. Trino holds none of the data and makes few of the promises a database
makes about it. Most of what follows is what that takes away.

It speaks SQL, so unlike the search engine (ADR-0173) it is a relational source
with a dialect, a query editor and everything the conformance suite asks of
one. It is built over `trinodb/trino-go-client`, a `database/sql` driver.

## Decisions

1. **The client is pinned at v1.1.0.** The current release, v1.3.0, declares Go
   1.27, and taking it moved this module's own `go` line and fetched a new
   toolchain — a change to what builds this application, made as a side effect
   of adding a driver. v1.1.0 declares 1.25.5 and has everything used here. The
   Go version is the owner's to raise, on purpose, and then this can follow.

   *It was raised the same day, by the owner: the module is on Go 1.27.2 and
   the client on v1.3.0, with the live suite and every mutation's test passing
   unchanged. Raising it took one thing with it. golangci-lint 2.6.2 cannot
   read Go 1.27's standard library — every package comes back as one failed
   import — so CI builds 2.14.0, which found nine things the older one had not
   been asked about, five of them in the tests of the two drivers above.*

2. **A catalog is shown as a database, and a table is named in full.** The tree
   is catalog, schema, class, object, column. Each catalog has an
   `information_schema` of its own, so everything below the root is read from
   the catalog it is about, and the catalog's name is the one name in those
   queries that a parameter cannot stand for. It is quoted; everything else is
   bound. A statement this writes always says `"catalog"."schema"."table"`,
   because what a bare name means depends on a session the statement may not be
   running in.

3. **The grid reads and does not edit.** No row has an address: there is no key
   a query engine over other people's stores could vouch for. The rows say so
   (`model.IdentityNone`), the source implements no `Writer`, and the write
   checks of the conformance suite have nothing to run. INSERT, UPDATE and
   DELETE typed in the editor run, behind the same guard as anywhere.

4. **Every page is ordered by every column that has an order.** Trino promises
   no order it was not asked for — a table is read by many workers and its rows
   come back as they finish — so the second page of an unordered read is not
   what follows the first. A browse ends its ORDER BY with every column Trino
   will sort by, after whatever sort was asked for. What has no order (a map, a
   document, a shape, an array, a row) is left out, so a table of such things is
   still read.

   That is a sort and is not free, but it is not what makes a page cost what it
   costs: an OFFSET is a read of everything before it either way, with no index
   to skip by, and the sort adds keeping the first offset-and-limit rows in
   order as they pass. Deep pages of a large table are slow here and a filter is
   the way through; that is said in `docs/KNOWN-DEFECTS.md`.

5. **An estimate, and never a count.** `SHOW STATS` gives the connector's own
   figure for a table's rows — exact for some connectors, days old for others,
   absent for many. That is the badge, marked an estimate, and nothing where
   there is none. An exact count is a read of the whole table in an engine built
   for tables too large to read, so `ExactCount` is not claimed and `Countable`
   is not implemented.

   `SHOW STATS` cannot be selected from, which the first attempt assumed. It is
   run as it is and the line with no column named is the table's.

6. **The session is in UTC.** A timestamp with no zone is compared with one that
   has a zone by reading it in the session's zone. The client writes a Go time
   as a zoned literal, and the grid hands a filter the value it was shown. In a
   session in somebody's own zone those two facts make a filter on a timestamp
   select a different hour from the one picked. With the session in UTC a
   zone-less value, a zoned one and a date all filter by themselves, and the
   live suite holds each. The cost is that `current_timestamp` typed in the
   editor answers in UTC.

   A time of day is the exception that is not solved: it is shown as text, and
   a filter on a `time` column is refused by the server as a comparison of a
   time with a varchar.

7. **The first question is asked once.** The client retries a request it could
   not send twenty times over two minutes. That is right for a query that has
   run for an hour when a coordinator restarts, and is two minutes of nothing
   for somebody who typed the wrong port and pressed Test — which is how the
   live suite found it, a refused connection reported as a timeout after the
   test's own deadline. So `Open` asks what the server is on a connection of its
   own with retries off, and a refusal is a refusal at once. The pool that runs
   queries keeps the client's patience.

8. **A statement somebody stops is stopped at the server, and no more is
   claimed.** Ending a statement's context makes the client tell the coordinator
   to drop the query, and the live suite proves it: a query over a table that
   waits is stopped, and the coordinator's own list shows it failed rather than
   running on. What is not claimed is `Query.Cancel`, which means stopping a
   statement from another connection by its name; this keeps no name.

   The table that waits is in the `blackhole` catalog, which is why the test
   server has one: it is the only thing in Trino that can wait without working.
   A wide scan would prove the same thing and has twice taken a container, and
   Docker with it, off this machine.

9. **A plan is read; an analysed plan is what it runs.** `EXPLAIN` is a read.
   `EXPLAIN ANALYZE` runs the statement it explains, so it is classified as the
   worst of the words in it — a column called `comment` costs a prompt, and an
   INSERT is never taken for a question. `EXECUTE` runs a statement the
   classifier cannot see and is a write. It matters more here than it looks:
   Trino may hold nothing, but a DELETE through it is a DELETE in somebody's
   warehouse.

10. **A certificate is checked against the host, or the connection is refused.**
    Verification is on unless a connection was told otherwise (NFR-S3). The
    client offers no other name to check a certificate against, so a connection
    that gives one is refused before anything is dialled, rather than verified
    against something else. A password over an unencrypted connection is refused
    in the same place, in terms of the setting: Trino would refuse it too.

11. **An exact number is passed as one.** The client writes each argument as a
    literal of the type Go gave it. A `model.Decimal` is text in Go, and passed
    as text Trino is asked to compare a decimal with a varchar and refuses. It
    is passed as the client's numeric type, in a browse's filters and in a
    statement's parameters alike.

## What is not proven

- **A coordinator that asks who is calling, or is reached over TLS.** The test
  server has neither. What the settings become is tested; the connection is
  not.
- **Any connector but tpch, memory and blackhole.** Statistics, comments and
  views are each a connector's to provide, and each is read as possibly absent.

## Consequences

- `sqllex` has a Trino dialect, which is the standard's with the words of a
  distributed query added.
- `gate.sh` and the nightly conformance run require the Trino server. The image
  is pinned at 483 in CI.
- T5.15 stays open for Snowflake and BigQuery, which need accounts.
- A plan view, stopping by name, materialized views as their own class and
  explicit transactions are each unclaimed.
