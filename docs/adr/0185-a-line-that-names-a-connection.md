# ADR-0185: A line that names a connection

**Status:** Accepted · **Date:** 2026-10-12
**Tasks:** T5.22 · **Requirements:** FR-5.17, FR-4.9, FR-5.10
**Packages:** `internal/app`, `internal/ui/shell`

## Context

FR-5.17 asks for one script that runs on more than one connection, by
directives: count the orders on staging, then on production, and read the two
answers side by side without two tabs and a copy between them.

## Decisions

1. **A directive is a line that is an at sign and a name, and nothing else.**

       SELECT count(*) FROM orders;
       @staging
       SELECT count(*) FROM orders;
       @"Production (read replica)"
       SELECT count(*) FROM orders;

   What follows it is for the saved connection of that name, until the next.
   What comes before the first is for the connection the tab is on. A name of
   letters, digits, hyphens, underscores and dots is written as it is; any
   other between double quotes. Names are matched without regard to case.

2. **It is honoured only between statements.** A line of a string that reads
   `@home`, and a variable alone on a line in the middle of a statement, are
   part of the statement they are in. "Between" is decided by the text alone —
   nothing written since a line that ended with a semicolon — and not by a
   dialect, because the script is for more than one. A semicolon inside a
   string that ends its line is the case this gets wrong.

3. **The parts run in the order written, one after another, and a failure
   stops the script.** The second does not start until the first has answered.
   A statement meant to follow another on another server must not run when the
   first did not happen, so what follows a failed part is not run, and that is
   said.

4. **A name nobody has is refused before anything runs.** So is a name two
   saved connections share. Every part's connection is found first; nothing
   is sent anywhere until all of them are.

5. **Every safeguard is the connection's own.** A part goes through the guard
   of the connection it names — read-only, production, a statement that
   reaches every row — not the tab's. A part that would change data on a
   connection marked Production is asked about when the script reaches it, by
   that connection's name, which is the name to type; and the question says
   that what came before has already run, because it has. The consent is for
   that part and no other. Declined, the rest is not run.

6. **A result from another connection is shown and not edited.** The tab's
   editing writes through the tab's connection; a row changed in such a result
   would be written to a different database's table of the same name. The
   tab's own results are edited as they were.

7. **Run, with nothing selected, runs the statement the caret is in on the
   connection its part is for.** A selection runs on the connection it is in,
   and on those its own directives name. The alternative — the tab's
   connection whatever the script says above the caret — is the mistake the
   feature exists to prevent, made easy.

8. **A plan is asked only of the tab's own statements.** Explain on a
   statement that is for another connection is not offered: it would be
   planned by the wrong server.

9. **One session a connection, kept while the tab is open.** A part's session
   is made the first time the script reaches that connection and closed with
   the tab, rather than connected again for every run.

10. **Results are numbered through the script, and each part is said under
    its connection's name.** An error is pointed at its place in the whole
    script (FR-5.10).

## What is proven

- One script across a real SQLite and a real PostgreSQL: a function only
  SQLite has, then one only PostgreSQL has, each answering only if it was
  asked of the right server.

## What is not done

- **Nothing moves between connections.** The parts are run side by side; a
  value read on one is not carried to the next, and there is no statement that
  joins across them.
- **It is not one transaction.** A failure on the second connection does not
  undo what the first committed. The script stops; that is all.
- **The editor does not know.** Completion and highlighting are the tab's own
  connection's throughout, the statement at the caret is found by the tab's
  dialect, and a directive is not coloured as one.
- **Named parameters** are found by the tab's dialect in every part, and the
  same values are given to every connection.
- The transaction indicator, and Begin, Commit and Roll Back, are the tab's
  own connection's.
- Only the query tab reads directives. The command line's `--sql` and
  `--file` do not.
