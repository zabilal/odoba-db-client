# ADR-0063: The MongoDB tree

**Status:** Accepted · **Date:** 2026-09-12
**Tasks:** T2.31 · **Requirements:** FR-2.1, FR-2.4, FR-2.5 · **Packages:** `internal/source/drivers/mongo`, `internal/ui/shell`

## Context

The explorer, the tabs and session restore address every object by a kind and
a path (`model.ObjectRef`), and know nothing about engines (REQ-DB-4). A
document store has to reach them through the same tree: databases, the
classes each holds, and the objects in those classes.

## Decisions

1. **The shape is the relational one**: a database holds a class of
   collections; a collection holds a class of indexes. A collection has no
   columns to list in a class's place, and its indexes are what it has.
   Nothing in the explorer changes.

2. **A view is a collection, marked as one.** MongoDB's views are read the
   same way as collections and appear beside them rather than in a class of
   their own; the node says `view`, and so does the structure tab. Separating
   them would say the difference matters more than it does.

3. **The server's own collections are hidden.** `system.` is MongoDB's
   reserved prefix — `system.views` holds the definitions the tree already
   shows as views — as every other driver hides a server's own schemas.

4. **Collections come in name order and `_id_` comes first.** The server
   answers in no order of its own, and a tree that reordered itself between
   two refreshes would be unreadable. `_id_` is the index every collection
   has, so it leads the rest.

5. **A refusal is not a failure.** A view has no indexes of its own and
   sometimes no count, and a collection that has gone has neither; the server
   says so with `CommandNotSupportedOnView` or `NamespaceNotFound`, and the
   tree shows what there is rather than an error.

6. **A badge is the count the server holds as metadata** (FR-2.5), said to be
   an estimate because it is one. Nothing counts documents to paint a tree
   node.

7. **The structure tab reads a `*model.Collection`**: its indexes — keys in
   order, with the kind of index where a key names one, unique, sparse, and
   what expires when — its document estimate, and, once inference has run
   (T2.32), the fields it found and how often each was seen. A shape is
   always said to be sampled, never declared (FR-12.4).

## Consequences

- An index's keys carry the kind of index in the key's expression, where the
  server writes `text` or `2dsphere` in place of a direction. A relational
  index has no such thing, so the two render apart.
- Listing a database's collections costs one round trip, and is made again
  for a collection's own type when it is described. Both are cheap listings;
  neither is cached, so a refresh sees the server as it is.
- A collection's fields are not in the tree yet: shape inference is T2.32,
  and until then a collection's only class is its indexes.
