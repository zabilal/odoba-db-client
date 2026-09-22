# ADR-0124: What a half-finished change leaves on screen

**Status:** Accepted · **Date:** 2026-09-22
**Tasks:** T3.13 · **Requirements:** FR-7.4, UX-6
**Packages:** `internal/ui/shell`

## Context

A sync script is written from a comparison. FR-7.4 asks for it to be applied,
or saved to a file.

Applying is the first thing in this program that runs a script somebody
assembled out of a screenful of decisions, and DDL on several engines cannot
be rolled back: `ApplyDDL` already reports how far it got and which statement
stopped it (ADR-0115). What was never settled is what the screen should show
afterwards.

## Decisions

1. **Applying goes through the preview and the guard, like every other
   structural change.** The statements are shown, the list shown is the list
   sent, read-only refuses it and production asks with the connection's name
   typed. None of that is written again here: the sync script reaches the
   same `previewDDL` the designer and the source editors reach.

2. **A change that ran part-way is still read again.** This is the new
   decision. Until now, a failure left the screen as it was; that was right
   for a change that never started and wrong for one that half-landed. A
   designer showing the table as it was before three of five statements ran,
   or a comparison of a database that has since moved, is showing something
   nobody can act on — and the next thing somebody does with it will be based
   on it.

   So the re-read happens whenever anything ran, and not otherwise. Nothing
   ran means nothing to read again: a connection that refused before the
   first statement leaves the screen exactly as it was, which is the truth.

   This corrects the designer and the source editors as well, where the same
   gap was open and nothing had noticed.

3. **The comparison is made again rather than adjusted.** Adjusting it would
   mean this program deciding what the server did with a script that failed
   halfway. The server is right there to ask.

4. **Three verbs, three buttons: write it, keep it, run it.** They are three
   different acts — read it in a query tab, save it for a repository or a
   change window, or run it now — and folding any two together would make one
   of them a surprise. All three appear together when something is chosen and
   go together when nothing is.

5. **A cancelled save writes nothing and reports nothing.** Cancelling is an
   answer, not a failure, and an error band after it would be this program
   arguing with a decision.

## Consequences

- The saved file carries the same header as the query tab: how many
  differences it holds and that nothing has run. A file that outlives the
  window should say what it is.
- A name a filesystem would read as a path does not become one: the suggested
  file name has its separators replaced.
- Applying re-reads both sides, which on a large schema is two snapshots
  again. It is the only honest answer, and a comparison somebody has just
  changed is the one moment they most want re-read.
- Comparing two live databases still has no way in from the window, so
  applying always writes to the connection and reads from the saved model.
