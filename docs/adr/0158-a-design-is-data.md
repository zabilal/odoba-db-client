# ADR-0158: A design is data

**Status:** Accepted · **Date:** 2026-09-25
**Tasks:** T5.1 · **Requirements:** FR-9.1, FR-9.3, FR-3.6, ARCH-2, NFR-S6, NFR-A1, REQ-DB-2, REQ-DB-4
**Packages:** `internal/query`, `internal/ui/erd`, `internal/ui/diagram`, `internal/ui/shell`

## Context

FR-9 asks for a visual query designer: tables on a canvas, joins inferred from
foreign keys and editable, columns and conditions chosen without SQL, and a live
view of the SQL. It was downgraded to `C` in v0.2 with a note that it "shares
the canvas infrastructure with the ER diagram but adds a large amount of bespoke
interaction".

The canvas was written for that: `internal/ui/canvas` has no Fyne in it and its
`Node` doc already says "a table, view, or query-designer source", its `Edge`
doc "for an ER diagram it is a foreign key; for the query designer, a join", and
its `-1` port "is what the query designer uses before a join condition is
chosen". `internal/ui/erd` is the half that knows about databases, and says so.

## Decisions

1. **A design is data, and rendering one is a function of it and a dialect.**
   `internal/query` holds a `Design` — tables, joins, outputs, conditions,
   grouping, ordering, a limit — with no Fyne and no connection in it. That is
   what lets the whole of the SQL be tested on every engine without a server,
   and it is what keeps ARCH-2: statement text comes from a dialect and from
   code that goes through one, never from the window.

2. **A table is referred to by its place, not its name.** Two tables on one
   canvas may be the same table twice — a self-join is the commonest thing a
   designer is used for — so every column, condition, grouping and ordering
   names a table by its index, and the alias is what the SQL calls it. An alias
   that is taken gets a number after it.

3. **A condition's right-hand side is text the person typed.** Exactly as the
   grid's typed WHERE is (FR-3.6). A designer whose values were quoted strings
   could not express `CURRENT_DATE - 7`, which is half of what somebody
   designing a query means by a value; and there is no method on `Dialect` for
   rendering one value as a literal — only four of thirteen drivers can write
   an INSERT's literals at all. So the value is theirs, and what guards it is
   the last thing `Render` does: it asks the dialect what the statement it has
   just written would do, and refuses to hand back one that could change
   anything. A designer is for reading.

4. **Every table must be reached by a join.** A table nothing joins is a cross
   product with everything before it, which is never what somebody designed and
   on two large tables is a server busy for a very long time. It is refused,
   naming the table, rather than rendered.

5. **Joins are written in the order the tables are reached, not the order they
   were made in.** A join whose left-hand table has not been named yet is not
   legal SQL, so the tables are walked outward from the first and each join is
   written when the table it brings in is reached — and a join drawn backwards,
   from the new table to one already on the canvas, is written the other way
   round. A join between two tables both already reached is a further condition
   and is written last.

6. **Only declared keys are inferred.** A foreign key is a join the database
   has declared, so making somebody draw it again would be asking them to retype
   the schema. Two columns of the same name in two tables are two columns, and
   joining on them would be this program's opinion presented as the catalogue's.
   One join per pair of tables: a pair with two keys between them is a choice
   somebody has to make.

7. **A suggestion is offered, not applied.** `Suggest` finds them and `Apply`
   takes them, and the canvas marks an inferred join as one — so what the schema
   said can be told from what a person decided, which is what "editable" means
   in FR-9.1. Changing a join clears the mark: it is theirs now.

8. **The canvas shows the query and the panels edit it.** Not joins made by
   dragging a line from one column to another. Everything here has to be doable
   from the keyboard (NFR-A1), and a join made by picking two columns from two
   lists is a join somebody using a screen reader can make. The lines are the
   picture of what they chose.

9. **The limit clause is read out of a browse.** It is the one part of a SELECT
   the engines do not agree on — `LIMIT n`, `FETCH NEXT n ROWS ONLY`, `TOP n`
   before the select list — so rather than a table of engine names in a package
   that has no business knowing them (REQ-DB-4), a browse of the first table is
   rendered and the bound is read from its tail. An engine that bounds some
   other way is not recognised, and the designer says so rather than writing a
   clause that does nothing.

10. **A port is emphasised when its column is selected.** In an ER diagram
    `Port.Key` marks a primary key; here it marks a chosen column, because what
    somebody is doing on this canvas is choosing columns and that is the thing
    worth seeing at a glance. Nothing chosen marks everything, which is what a
    canvas with tables on it and nothing picked means.

11. **`diagram.Widget` gained `Select`.** The designer rebuilds the canvas on
    every change, and a selection that could not be put back would be lost each
    time somebody added a table, with the controls that depend on it going dead
    while the box was still highlighted. It does not call `OnSelect`: that is the
    owner saying what is chosen, and telling it what it has just said would be a
    loop.

## Consequences

The model covers the whole of FR-9 — conditions, grouping, aggregates and
ordering are in it and tested — and what T5.2 adds is the panels that edit them.
T5.3's live SQL view is `Render` called on every change, which is already a pure
function of the design.

A design is not saved anywhere yet. It lives in the tab, and closing the tab
loses it. Saving one is a store change no task claims.

## Alternatives

**A `ValueScripter` refinement on `Dialect`,** so values could be rendered as
literals. Rejected for now: it is nine drivers' worth of contract change for
something a person designing a query does not want, since half their values are
expressions. If the designer ever grows a value picker that knows the column's
type, this is what it would need.

**Joins by dragging between columns.** Rejected as the only way in: see
decision 8. It could be added on top of the lists, which is the right order.

**Parsing SQL back into a design,** to make FR-9.3's "bidirectional" literal.
Not decided here; T5.3 will say what it does.
