# ADR-0018: The explorer's filter matches paths, over what is loaded

**Status:** Accepted · **Date:** 2026-09-11
**Tasks:** T1.43 · **Requirements:** FR-2.3, FR-15.4 · **Packages:** `internal/fuzzy`, `internal/ui/explorer`, `internal/ui/explorer/view`, `internal/ui/shell`

## Context

FR-2.3 asks for a fuzzy filter across the whole tree, matching on the path
and not just the leaf's name: two tables called `orders` in two schemas are
told apart only by where they are. The tree loads lazily. A connection's
objects are fetched only when it is expanded, and expanding a connection
connects to its server (ADR-0011 §10).

## Decisions

1. **Only what has been loaded is searched.** A filter that connected to
   every saved server to search it would be slow, would ask for passwords,
   and would reach production servers no one asked about. The filter says
   what it did not search instead: "Not searched: 2 connections not
   opened." As more of the tree loads, the matches follow it, in the same
   single refresh that redraws the tree (the coalescing of ADR-0002 holds).

2. **It matches the path.** A node's labels from its connection down are
   joined with " / " and scored with the palette's fuzzy matcher, so
   "sales cust" finds the customers table in the sales database. The best
   come first: letters at the starts of words, unbroken runs, then shorter
   paths. The matcher moved out of the command registry into its own
   package, `internal/fuzzy`, since the explorer's model should not depend
   on the command registry to rank paths.

3. **Matches are listed in the tree's place.** Each row names the object
   and, under it, where it is. The tree's open branches are left alone
   while a filter is on, so a search neither rearranges the tree nor
   changes the open nodes the session keeps. Choosing a table opens it, as
   a double-click does; choosing anything else clears the filter and shows
   it in the tree, with the branches above it opened and it selected.
   Enter chooses the first match; Escape clears the filter.

4. **⇧⌘F (View › Filter Objects) puts the keyboard in the filter**, showing
   the sidebar first if it is hidden.

Filtering the tree in place was rejected: it needs every branch above a
match open while the filter is on, which fights the open state the session
keeps, and restoring that state afterwards is fragile.

## Verification

`internal/fuzzy` keeps the palette's matcher tests. The model's search is
tested to match on the path, not the leaf, to look inside nothing unloaded,
to load nothing and to count the unopened connections. The view is tested
to list matches in the tree's place, to open a table, to show a schema in
the tree with its branches open, to follow the tree as it loads, to open
the best match on Enter, to clear on Escape and to say what it did not
search; the shell, that ⇧⌘F shows the sidebar and focuses the filter.

## Not decided here

Highlighting the matched letters in each row. Searching an unopened
connection on request, for example from a row that offers to open it.
