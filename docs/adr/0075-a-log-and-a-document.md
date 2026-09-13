# ADR-0075: A log and a document, among a key's kinds

**Status:** Accepted · **Date:** 2026-09-13
**Tasks:** T2.42 · **Requirements:** FR-4.1, FR-4.4, FR-12.2 · **Packages:** `internal/source/drivers/redis`

## Context

FR-12.2 asks for two more editors beyond the five of ADR-0074: JSON and
stream. They are the two kinds a key holds that are not a collection of
values — a document is one structure, and a stream is an append-only log with
an id for every entry. One of them is not even a plain server's: JSON is a
module's, and a server without it holds no document at all.

## Decisions

1. **A stream is its entries, in the order they were written.** The columns are
   the entry's `id` and its `fields`. The fields are one value rather than
   columns of their own, because they are the entry's: one entry's fields are
   not another's, and a grid of every field any entry ever carried would be
   mostly empty. They are JSON, so the cell viewer shows the structure.

2. **A stream is paged by id, not by a cursor.** `XRANGE` reads from the last
   id seen, made exclusive with `(`. There is no cursor to carry, so the walk
   carries the id instead, and says there is more until a read comes back
   empty. `XLEN` is the count.

3. **An entry is written once.** A stream is a log, and a log that can be
   rewritten is not one: an entry is added (`XADD`, the server giving it the
   id that orders it) or deleted (`XDEL`), and never changed. The id is
   read-only for the same reason.

4. **A JSON document is one value, as a string is.** One row, the key beside
   the document, typed as JSON so the cell viewer reads and writes it as the
   structure it is. `JSON.GET` at the root path answers an array of what it
   matched, and the one it matched is the document, so it is unwrapped.

5. **A document is set whole** (`JSON.SET key $`), and what is set must be
   JSON — a plan that is not is refused before it reaches the server. Paths
   below the root are not edited a row at a time: a document is one value, and
   the same choice a Mongo document's editor made (ADR-0067).

6. **Neither is narrowed by the server.** A stream is read in order and a
   document is one value, so a filter on either is refused rather than applied
   to the rows already read, as for a list and a string (ADR-0073).

7. **A kind nothing here reads is said to be that, by name.** A time series, a
   bloom filter and the rest of the modules' types are named in the message
   rather than read as something they are not.

## Consequences

- Seven of Redis's kinds are now rows: string, hash, list, set, sorted set,
  stream and JSON. What is left is the modules' own, each of which would be a
  task of its own.
- The live tests want a server with the JSON module as well as a plain one:
  `ikigai-redis-json` (56380), `redis/redis-stack-server`, and CI runs one
  beside the plain server. A plain server is still the one everything else is
  tested against, so the path where a module is absent stays covered.
- A stream's entries are read from the beginning every time a page is asked
  for, as a hash's fields are: an offset is walked to, not sought to.
