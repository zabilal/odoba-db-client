# ADR-0105: Comparing two versions of a schema

**Status:** Accepted · **Date:** 2026-09-19
**Tasks:** T2.74 · **Requirements:** FR-13.14 · **Packages:** `internal/app/textdiff`, `internal/ui/shell`

## Context

A subject in a schema registry is one thing that has been revised: version 1,
then version 2, each with the text it was registered with. FR-13.14 asks for
the versions, the text, the compatibility mode — and a diff between two
versions.

Nothing in this application compares two texts. There is no diff anywhere in
it, and no dependency that offers one. So the diff is not a matter of calling
something already here; it has to be decided.

## Decisions

1. **The comparison is written here.** A line comparison is a hundred lines of
   code with no ambiguity in it, and it is wanted in exactly one view. Taking
   a module for that would add something to keep, to audit and to update for
   the rest of the project's life in exchange for code that can be read in one
   sitting and proved without a window.

2. **Longest common subsequence, not Myers.** Myers is what a version control
   system uses because it compares files of tens of thousands of lines. A
   schema is tens of lines. The straightforward table is easier to write
   correctly, easier to test, and for these sizes indistinguishable in speed —
   and being sure the answer is right matters more here than being fast at a
   size that will not occur.

   Lines shared at the start and the end of both texts are matched directly
   and kept out of the table. That is exact rather than a shortcut, and it is
   what keeps the usual change — a field added to a schema otherwise untouched
   — down to almost nothing.

   Past a cap the answer becomes coarse: everything in the first text went and
   everything in the second arrived. It is a true statement about two texts
   with nothing in common, and it is there so that a schema nobody anticipated
   degrades into a blunt answer rather than into a pause.

3. **JSON is laid out before it is compared, and this is the decision the
   feature rests on.** A registry keeps a schema as the single string it was
   registered with, and for Avro and JSON Schema that is one line no matter
   how large the schema is. Comparing two such texts line by line can only
   ever report that the one line changed — true, and useless. Laying them out
   first is what gives the comparison something to work with. Protobuf arrives
   with its own lines and is left alone, as is anything that does not parse.

4. **It compares text, not meaning.** Two schemas that say the same thing with
   their fields in a different order read here as a change, because that is
   what was registered and what the next version will be checked against. A
   comparison that understood Avro, Protobuf and JSON Schema well enough to
   say "nothing that matters changed" would be three comparisons, each able to
   be wrong in its own way, and none of them asked for.

5. **The versions to compare are chosen, and default to the last two.** The
   comparison somebody opens this view wanting is the newest against the one
   before it. Any two can be picked, because a subject revised eight times has
   seven other questions in it.

6. **What changed is marked by a sign as well as by colour.** A line that
   arrived reads `+` and one that went reads `-`. Colour alone would fail
   anybody who cannot separate the two, which is the rule the whole interface
   is held to.

## Consequences

`internal/app/textdiff` is a pure package: two texts in, a list of lines out,
no Fyne and no driver. It can be tested exhaustively without a window, and
anything else that needs to compare two texts — two versions of a view's
definition, two saved queries — now has somewhere to go that already works.

The subject view is the first structure view whose body is driven by a choice
made inside it. The shape existed already: a collection's shape is sampled by
a button that redraws the same view. This one keeps its state in its own
widgets rather than going back to the shell, because nothing outside the view
needs to know which two versions somebody is looking at.

What this does not do is say whether a change was safe. The registry already
answers that question, in the compatibility mode shown beside the versions,
and it answers it authoritatively because it is the thing that enforces it.
