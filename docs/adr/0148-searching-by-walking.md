# ADR-0148: Searching by walking

**Status:** Accepted · **Date:** 2026-09-24
**Tasks:** T4.13 · **Requirements:** FR-2.7, FR-2.1, FR-2.2
**Packages:** `internal/app`, `internal/ui/shell`

## Context

FR-2.7 asks for a full-text search across object definitions: a column
somewhere among two hundred tables, a table named in a procedure's body, a
default nobody can account for. The explorer's filter already narrows what is
loaded by its name, and that is the other question — what is inside.

Every engine can answer it in its own way. PostgreSQL has `pg_proc.prosrc` and
`pg_views.definition`, MySQL has `information_schema.routines`, SQL Server has
`sys.sql_modules`, and each of them is one query where a walk is hundreds. A
driver interface for it would be the fastest thing to build on, and eleven
drivers would have to implement it before it worked everywhere.

## Decisions

1. **The search walks the tree and describes each object, through the calls
   the explorer already makes.** `Children` to find them, `Describe` to read
   them. It works on every engine that can be browsed, today, with nothing
   written for any of them — the same bargain the schema comparison struck for
   a driver with no `Snapshot` (ADR-0119).

   The cost is a round trip per object, and it is paid where somebody can see
   it: the scope is the node picked in the tree, the matches arrive as they are
   found, and it is a task, so it says how it is getting on and can be stopped.

2. **A hit carries the tree's own node, not a path to be turned back into
   one.** Opening a match is then exactly what the explorer would have done
   with that node — its rows if it has rows, its structure if it has not — and
   there is no second opinion about what a ref for an object looks like on an
   engine with no schemas.

3. **Where a search looks is fixed and complete for the relational model**:
   names; a column's name, type, default, generation expression and comment;
   an index's columns, expressions, included columns and predicate; a check's
   expression; a foreign key's columns and what it points at; a trigger's
   condition and body; a view's columns, comment and definition; a routine's
   parameters, comment and body. Every one of those is a place the tests prove
   it looks, because a place nobody proves is a place that quietly stops being
   looked at.

4. **Five hundred matches is where it stops.** Past that a list is not read,
   and walking a whole database to fill one nobody wants is worse than saying
   so. The panel says it stopped and asks for something narrower.

5. **Cancelling is the only way to stop it, and everything that ends the
   search cancels**: a second search, closing the panel, closing the window.
   A search nobody can see the results of should not go on asking the server.

## Consequences

- No engine-native search is claimed, and none is needed for the feature to
  work anywhere. If one is written later it is a `Searcher` optional interface
  with this as the fallback, exactly as `Snapshotter` sits over the walk.
- The search is as good as `Describe` is on each driver. An engine whose
  `Describe` does not carry routine bodies cannot be searched for what is in
  them, and says nothing about it — which is the same limit its structure view
  and its DDL script already have.
- An object a driver lists twice — a trigger under its table and again under a
  triggers folder — can be reported twice, once by name and once by body. The
  two rows say different things about where the text is, and neither is wrong.
- Nothing is cached. A second search asks again, which is right for a thing
  whose whole purpose is to say what is there now.
