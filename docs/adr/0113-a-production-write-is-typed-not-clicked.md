# ADR-0113: A production write is typed, not clicked

**Status:** Accepted · **Date:** 2026-09-22
**Tasks:** T2.90 · **Requirements:** FR-4.9, NFR-S4
**Packages:** `internal/ui/shell`

## Context

FR-4.9 asks that writes on a connection marked `production` require typed
confirmation. The guard in `internal/source` has enforced the refusal from the
beginning: a mutating operation on a production connection comes back
`ErrConfirmationRequired`, and every driver consults it.

What the window did with that refusal was put up a dialog with a Yes button.
That satisfies "confirm" and not "typed", and the difference is the whole
point. By the time somebody is running statements they have dismissed a great
many dialogs, and a button in a familiar position is pressed by the hand
before it is read by the eye.

## Decisions

1. **The confirmation is typed, and what is typed is the connection's name.**
   Not a fixed word, which would become as automatic as the button. The name,
   because the mistake this guards against is almost never the wrong
   statement — it is the right statement on the wrong connection, and the name
   is the only thing that tells one production database from another. Typing
   it is the moment somebody notices they are on `orders-prod` and meant
   `orders-staging`.

2. **The match is exact, after trimming space around it.** A name is often
   pasted and a pasted name brings whitespace with it, which is not the
   carelessness this is for. Everything else must be right: `PROD` does not
   confirm `prod`, and neither does `production`.

3. **A connection that cannot be found asks for the word `production`.** The
   store refuses to save a connection with no name, so this is how that arises
   — a connection deleted while an operation was in flight. Without a
   fallback, the confirmation would be given by typing nothing at all.

4. **One dialog serves every site.** Running a script, committing pending
   changes, saving a document, running a pipeline that writes, changing an
   index, producing a record, and changing a Kafka cluster all ask the same
   way. Six near-identical dialogs was how the click-through survived this
   long; one means the guardrail cannot be half-applied, and a seventh caller
   gets it by calling it.

5. **Nothing has happened when the question is asked.** Every caller arrives
   here because a driver refused before it did anything, so asking and then
   running it confirmed is safe rather than a second attempt. The consent
   belongs to that one operation and is never cached — the guard's own comment
   has said so since it was written.

## A note on the toolkit

Fyne's `ConfirmDialog` keeps its confirm button unexported, so it cannot be
disabled. Its `FormDialog` disables the confirm button whenever any item fails
validation and re-enables it when validation passes, which is exactly the
mechanism needed, so the confirmation is a form dialog with one entry whose
validator demands the name.

The cost is that a form dialog exposes no way to set the confirm button's
importance, so the button is no longer danger-red. The typing is the friction
now, and it is a better one than a colour.

A second quirk worth recording: a `Form` renders its item labels through
`RichText` rather than `Label`, so the helper that collects label text cannot
see them — the same trap as a `Select` in T2.74. The tests read them through a
`RichText` walk instead.

## Consequences

- Eight existing tests had to be changed, because they encoded the
  click-through. They now type the name, which is what a person does.
- `DELETE` and `UPDATE` with no `WHERE` still confirm only on production
  connections. FR-4.9 asks for them to confirm everywhere, which is T2.91 and
  is not done here.
