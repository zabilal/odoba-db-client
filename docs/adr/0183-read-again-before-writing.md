# ADR-0183: Read again before writing

**Status:** Accepted · **Date:** 2026-10-12
**Tasks:** T5.20 · **Requirements:** FR-4.12
**Packages:** `internal/app`, `internal/ui/shell`

## Context

An edit in the grid is made to a row as it was read, which may be minutes
ago. The statement that writes it finds the row by its key and sets only the
columns that were changed (ADR-0027), so somebody else's change to another
column is not written over. Their change to the same column is; and a deletion
deletes whatever the row has since become. FR-4.12 asks that a person be
warned.

## Decisions

1. **Each row a change touches is read again by its key, when Commit is
   pressed.** Not when the review opens: a review can sit open, and the point
   is to look as late as can be. The row is held against what was read, column
   by column — bytes as bytes, a moment as a moment whatever zone it is said
   in, not-a-number as itself — and a row that differs, or is gone, is said
   before anything is written. New rows are not read: nobody had them to
   change.

2. **It is read by its key alone.** The tab's own filter, typed condition and
   order are left off, so a row that has been changed out of the filter is
   still found, and found changed, rather than reported gone. The tab's choice
   of columns is kept, so the two rows are the same shape.

3. **The warning says what committing would do, row by row.** A column the
   edit writes: *was X when read and is Y now; committing writes over that.* A
   column beside the edit: *is not one of your changes, and is left as it is* —
   said, because knowing somebody else is in the row is the thing asked for. A
   row to be deleted that has changed: *deleted as it is now.* A row that is
   gone: *the change to it will write nothing*, or, for a deletion, *deleted
   already*. Then *Commit Anyway*, or Cancel, which keeps the changes and says
   to refresh.

4. **A check that could not be made is asked about, not passed.** If the rows
   cannot be read again — the connection dropped — the same question is
   asked, saying so. A failed read is not a row that is unchanged.

5. **It is a warning, not a lock, and not a guarantee.** Nothing is held while
   a person thinks, which is what makes it optimistic. Between the reading and
   the writing there is still a moment in which a row can change. Closing it
   would mean every statement's condition carrying the row's old values, in
   every driver, and deciding what an update that then matches nothing means
   in each; that is a change to the writers' contract and is not made here.

6. **At most two hundred rows are read again.** Each is a read by key, and a
   commit of thousands of rows should not become thousands of reads before it
   begins. Past the limit rows are not checked; a warning that is shown says
   how many, and a commit with nothing found among the first two hundred is
   written without saying so.

7. **A key that finds two rows is not a key.** Which of them was read cannot
   be said, and nothing is claimed about it.

## What is proven

- The journey that filters, edits, reviews and commits a row runs through the
  check against SQLite, MySQL, MariaDB and PostgreSQL, each on its own types,
  and none is warned of a row nobody changed. On PostgreSQL the journey then
  changes the row behind the application and is warned of it by value, and
  told to all the same, writes over it.

## What is not done

- **Edits to a query's result** are written through the query's session, which
  does not read a table's row by key, and are committed unchecked.
- **Sources that do not filter on the server** — a key-value store, a search
  index read whole — are committed unchecked.
- A column the tab did not read is not compared.
- Every engine but the four above is unproven against its own types: a driver
  whose filtered read returns a value differently from its paged read would
  warn of a row nobody changed.

## Consequences

- One read a changed row before a commit, and one more question when
  something is found. No change to any driver.
