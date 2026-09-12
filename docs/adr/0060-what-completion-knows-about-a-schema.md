# ADR-0060: What completion knows about a schema

**Status:** Accepted · **Date:** 2026-09-12
**Tasks:** T2.29 · **Requirements:** FR-5.2 · **Packages:** `internal/app`, `internal/ui/shell`

## Context

The completion engine answers from a catalog that holds no I/O (ADR-0057):
completion runs on the keystroke path, where NFR-P5 allows 16 ms for
everything. Something has to fill that catalog from the server, and empty it
when the server's schema changes.

## Decisions

1. **`app.SchemaCache` is the catalog, one per connection**, hung off `Live`
   and built on first use, so two tabs on one connection read one cache and a
   connection nobody types a query on never introspects for one.

2. **It answers with what it holds and fetches the rest behind.** A miss
   returns nothing and starts one background load, keyed so that twenty
   keystrokes during a load make one request. What lands is told to
   subscribers, and the popup asks again — keeping the candidate that was
   chosen, so a list growing underneath does not move what was about to be
   accepted.

3. **It reads the same lazy tree the explorer does**, level by level:
   `Root` for the databases, a database's children for its schemas, a
   schema's classes (`model.ClassRef`) for its tables, views and routines,
   and an object's children for its columns, whose type comes from the
   `Attrs["type"]` every driver sets and the explorer already shows. Nothing
   is fetched that nothing asked about: a statement mentioning one table
   costs that table's columns.

4. **The tree's shape tells the paradigm.** Where a database's children are
   schemas, they are what a name is qualified with (PostgreSQL). Where they
   are classes, the database *is* the schema and the databases are what
   qualifies a name (MySQL). Where the root itself is classes, the source has
   one database, named by the class's own path (SQLite). An unqualified name
   is looked for in the schema the source lists first — PostgreSQL sorts
   public ahead of the rest for exactly this reason — and a database name the
   source does not know means the connection's own, since SQLite's is a file
   path.

5. **Emptied, not updated.** `Invalidate` throws everything away when a
   script that alters structure has run — `Dialect.Classify` saying
   `AccessDDL`, and a source whose dialect cannot say is taken to have
   changed everything — and when the explorer is refreshed, which is a person
   saying the structure has moved. A load in flight when that happens read
   the schema as it was, so it is dropped when it lands rather than recorded.

6. **A failure is remembered as an empty answer.** A server that refuses is
   not asked again on the next keystroke; the next Invalidate lets it be
   tried again. A class a source has no notion of fails on its own and leaves
   the others' objects alone.

## Consequences

- The first keystrokes in a fresh tab offer the dialect's words alone, and
  the names appear a moment later as the loads land. Each level of the tree
  is one round trip, so the first table name costs three.
- DDL run elsewhere — another client, another tab's connection — is not
  noticed. Refreshing the explorer (⌘R) is the way to say so.
- A transient failure costs completion those names until something empties
  the cache.
