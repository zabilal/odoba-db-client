# ADR-0061: Snippets

**Status:** Accepted · **Date:** 2026-09-12
**Tasks:** T2.28 · **Requirements:** FR-5.2 · **Packages:** `internal/source/sqlcomplete`, `internal/ui/editor/view`

## Context

FR-5.2 asks for snippets beside the keywords, tables and columns. A snippet
is the last part of completion that writes more than a name: several lines
from two or three letters, with places in them for a person to fill in.

## Decisions

1. **The engine offers them, the editor fills them in.** A snippet reaches
   the editor as an ordinary candidate of kind `CompletionSnippet` whose
   `Insert` carries the places to fill in — `${1:name}` in the order they are
   stepped through, `$0` where the caret ends. No other kind's `Insert` is
   read that way, so a table called `${1:x}` is written as it is named.

2. **A dozen snippets, shared by every dialect.** A list long enough to need
   searching is slower than typing. Each is a shape typed often and got wrong
   once: the order of INSERT's columns and values, CASE's END, a CTE's
   brackets. They are written in the case being typed, as a keyword is.

3. **They go where a keyword goes, under it.** A statement or a clause can
   only begin where a keyword can, and the same letters usually meant the
   keyword: `sel` offers SELECT first and the snippet under it. Nothing is
   offered after a dot or where a table's name goes.

4. **Tab steps through the places, selecting each** so that typing replaces
   it. The last place is where the caret is left and ends the filling in, so
   Tab goes back to indenting. Where the completion popup is open it takes
   Tab first — it is on top, and accepting is what Tab there means.

5. **A place written twice is filled in once**, at the first, which is how a
   CTE's name reaches its own FROM clause.

6. **The places that follow what is being typed move with it**, by what each
   edit added or removed, so the next place is still a place and not the
   middle of a word. Moving the caret, clicking and losing the focus all end
   the filling in, because from there the offsets are a guess.

## Consequences

- A snippet is written as one undoable edit: undo takes back the whole
  statement, not a line of it.
- The places move by the length of each edit, not by where the text actually
  went, so an edit made elsewhere in the document while a snippet is being
  filled in would leave them wrong. Everything that moves the caret out of a
  place ends the run first, which leaves only typing into the place itself.
- The set is the same on every engine. A dialect's own shapes — PostgreSQL's
  DO block, MySQL's DELIMITER — are not there, and a person's own snippets
  are not either; both are additive, and neither is in FR-5.2.
