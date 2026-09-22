# ADR-0131: A diagram narrowed to what is near

**Status:** Accepted · **Date:** 2026-09-22
**Tasks:** T3.20 · **Requirements:** FR-8.5, FR-8.1
**Packages:** `internal/ui/shell`, `internal/ui/erd`

## Context

A diagram of two hundred tables is a ball of string. FR-8.5 asks for a filter
to the tables within N relationships of a starting point, which is what turns
one into a picture somebody can read.

`erd.Neighbourhood` has answered that set since T3.16 and nothing asked it.

## Decisions

1. **The starting point is the table somebody is already looking at.** A box
   chosen on the diagram becomes the focus, so the control names it: "Focus
   on public.orders". Asking for a starting point in a list would be asking
   somebody to find twice a thing they have in front of them.

2. **How far out is said in words.** "1 relationship away" says what it does;
   a bare 1 does not, and "degree" is the graph's word rather than anybody
   else's. "Just that table" is the nearest end of it, which is how somebody
   sees one table with nothing else in the way.

3. **Narrowing redraws from the schema already read, not from the server.**
   Focusing is instant, and — more to the point — it is the same schema. A
   diagram redrawn from a second reading could differ from the one somebody
   was looking at when they chose.

4. **One arrangement serves every focus of a diagram.** A table keeps its
   place when the picture narrows around it, which is what makes narrowing
   feel like moving closer rather than like opening something else.

5. **A narrowed diagram is fitted, not shown at the view that was kept.**
   That view was of a larger picture, and a corner of a picture that no
   longer exists looks like a failure.

6. **It says what it is showing and what leads out of it.** "Around
   public.orders, 1 relationship away" above the count, and the count of
   relationships that leave what is drawn — which is the line written in
   T3.18 that could not fire until now.

## Consequences

- This is also FR-8.1's other half: the window can now ask for a subset of
  tables, which `erd` has supported since it was written.
- The fake schema the window is tested against grew a shape worth narrowing —
  four tables in a chain, one connected to nothing, and a second schema — so
  that drawing one schema is not the same as drawing the database. The
  comparison tests now build their saved model from that same fake rather
  than by hand, because a model written out separately is one that drifts.
- `TestCopyAsWritesWholeRowsWithTheirHeader` waited on the shared status
  line, which anything redrawing the window overwrites, so a copy that
  succeeded and was then talked over was a copy it waited for for ever. It
  waits on the clipboard now, which is what a copy is about and what nothing
  else touches. That is the same family as the flaky tests already recorded:
  waiting on state that something else may change.
