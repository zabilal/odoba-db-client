# ADR-0073: The key browser

**Status:** Accepted · **Date:** 2026-09-13
**Tasks:** T2.40 · **Requirements:** FR-2.5, FR-3.4, FR-12.2, NFR-P9, NFR-P11 · **Packages:** `internal/source/drivers/redis`

## Context

FR-12.2 asks for a key browser with a pattern and a type filter. Redis has no
rows, no query language and no order: `SCAN` walks a hash table a piece at a
time, matching a glob and a type as it goes, and answers with a cursor to
carry on from. Between two calls the keyspace can change, and a key found on
one call can be gone by the next.

The question this decides is what a key *is* to the rest of the application —
a node in the explorer's tree, or a row in the grid.

## Decisions

1. **A database's keys are rows, not a tree.** The database node is
   `Browsable` and has no children: a keyspace of millions is a tree nobody
   could read, and everything the explorer would do with it — narrowing,
   paging, sorting the view — the grid already does. `Children` answers with
   nothing rather than an error, because there is genuinely nothing under a
   database.

2. **A key shows its name, its kind and what is left of it.** Not its value:
   reading every value to draw a page would be a command for each, and the
   kinds differ — a hash is not a string. The editors (T2.41) are where a
   value is read. The columns are `key`, `type` and `ttl`, and a browse asked
   for fewer sends fewer commands: `type` and `ttl` are a command each.

3. **The pattern and the type are the server's to match, and everything else
   is refused.** A filter on `key` becomes `MATCH` — a name outright, a name
   within another, or the grid's own `%`/`_` pattern turned into the server's
   `*`/`?`, with anything in the text the server would read as a pattern
   written out. A filter on `type` becomes `SCAN TYPE`. An order, a typed
   condition, a filter on the time left, a second pattern, a kind no key is:
   each is refused rather than applied to the page already read, which would
   answer about part of the keyspace as though it were the whole (REQ-DRV-3).

4. **A page is walked, not sought.** `SCAN` has a cursor, not an offset, so a
   page that begins at 200 walks past the first 200 keys. `COUNT` is how much
   of the keyspace the server walks before answering, not how many keys come
   back, so it is a page's worth, never below a hundred (a round trip for
   every handful) and never above a thousand (the server held in one call for
   too long, NFR-P9).

5. **A key that went between the walk and the question is no row.** `TYPE`
   answers `none` and `TTL` answers -2 for a key that has expired or been
   deleted since the walk found it. Drawing it would be a lie about now
   rather than a fact about then, so it is left out.

6. **The database is chosen on the connection that walks it, and put back.**
   A `SELECT` on a pool changes whichever connection answered and leaves the
   others where they were, so a walk takes a connection of its own, selects
   on it, and selects back before letting it go: a connection returned to the
   pool on another database would answer the next question about the wrong
   keyspace.

7. **A cluster is walked shard by shard.** Its keyspace is shared out between
   its masters, and each answers only for its own slots — so the walk asks
   each master in turn, in the order of their addresses, and asks about a key
   on the shard that found it. A cluster has one keyspace and no numbered
   databases, and a browse of one is refused.

8. **A count is exact, and a badge is a count.** `DBSIZE` is the number of
   keys in a database and the server holds it, so an unfiltered count is a
   single command and the tree's badge is exact (FR-2.5). A filtered count
   walks — that is what `SCAN` is for, and the cursor is what makes it
   interruptible.

## Consequences

- Nothing in the UI knows what Redis is. A database opens into the grid as a
  table does, the pattern is the grid's own filter on a column, and the type
  filter is a filter on another (REQ-DB-1).
- A filtered count over a large keyspace walks the whole of it. It is
  cancellable and incremental, but it is not free; an unfiltered one is.
- Offsets are honest about a keyspace that moves. Two pages read a moment
  apart are two walks, and a key written between them can fall either side.
  Nothing in Redis offers better, and a stable view would mean holding the
  whole keyspace.
- The live tests want a cluster as well as a single server: `ikigai-redis`
  (56379) and `ikigai-redis-cluster`, three shards on 7001–7003 published as
  themselves, because a shard must be reachable at the address it announces
  to the others. CI has the single server, and the cluster test skips there.
