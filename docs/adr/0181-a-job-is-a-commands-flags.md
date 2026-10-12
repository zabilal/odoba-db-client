# ADR-0181: A job is a command's flags

**Status:** Accepted · **Date:** 2026-10-12
**Tasks:** T5.18 · **Requirements:** FR-10.11, FR-16.1
**Packages:** `internal/job`, `internal/cli`, `internal/ui/shell`

## Context

FR-10.11 asks for import and export definitions that are saved and can be run
again. The command line already runs both (ADR-0161), told what to do by
flags. The question is what a saved one is.

## Decisions

1. **A job is the flags, in a file.** Not a second description of an export —
   a schema of its own with a table, a format and a destination — which would
   have to be kept in step with the first for ever. The flags a command takes
   are the only definition there is of what it can be told. A job is those, by
   their names, and running one is running the command with them. A flag added
   to `export` tomorrow is a thing a job can say tomorrow, with nothing
   written here.

2. **`--save-job FILE` writes one instead of running; `ikigai run FILE` runs
   it.** Saving does not run the command, and says so. Only the flags that
   were given are kept, so a job says what somebody said and a default that
   changes later changes for the job too.

3. **Flags after the job win.** `ikigai run nightly.json --out other.csv
   --dry-run` is the job with those said last. That is how a pipeline gives a
   job its connection and its consent, and how a job is tried before it is
   trusted.

4. **A job never holds a connection string.** `--url` may hold a password, and
   a job is a file made to be committed. Saving with `--url` is refused,
   saying what to do instead: name a saved connection, or give the driver and
   database, or pass `--url` — or set `IKIGAI_URL` — when the job is run. A
   job file that says `url` by hand is refused when run, so the rule cannot be
   edited around.

5. **A job never holds consent.** `--confirm` is what somebody says about one
   run. Written into the file it would be said by nobody, every night. It is
   left out when saving, and that it was left out is said, so that it is not
   found missing later.

6. **Files are found from the job.** What a job reads and writes is kept as a
   path from the job's own directory, with forward slashes, and resolved from
   there when it runs. A job and its files can be moved or checked out
   elsewhere together, and a job run from a scheduler's working directory
   writes where it says and not where the scheduler happened to be.

7. **JSON, sorted, typed.** Keys in the alphabet's order so a change is a line
   in a diff; a yes-or-no as one and a count as a number, text otherwise. A
   key nobody reads, a flag the command does not have, a version this does not
   know and a command that is not an export or an import are each refused by
   name before anything is opened, with the status that means "the arguments".

8. **What the import window can be told, the command can be told.** A job
   saved from the window has to do what the window did, and the window is told
   three things no flag said: how a text file's characters are written, what
   parts its lines, and which of its columns fills which of the table's. So
   `import` gained `--encoding`, `--delimiter` (by name — comma, tab,
   semicolon, pipe — or as the one character it is) and `--map "file
   column=table column"`, said once for each pair. With `--map` the pairs are
   the ones said and no others: a name that is neither the file's nor the
   table's, and a table column filled twice, are refused before a row is read.
   A flag said more than once is a list in a job.

9. **The import window saves what is on show.** *Save as Job…* beside *Dry
   Run* and *Import* writes the import as it stands — the connection by its
   name, the table, the file, the format with its encoding, delimiter, header
   and sheet, every pairing, the mode, the rows a transaction and what a row
   that would not go in does. Everything is said and nothing is left to be
   detected again, so that a file that has changed since is read the way this
   one was. Saving imports nothing, and says so, with the command that runs
   it.

10. **The words are in one place.** The file, what is never in it, and the
    words for formats, encodings and delimiters are `internal/job`'s, which
    the command line and the window both use. The window does not spell a
    flag: it describes an import and is handed the flags, and a test holds
    each of them against the command's own.

## What is not done

- **Saving an export from the window.** The window's export is of what a tab
  shows: filters built in its own panel, each a column, a comparison and a
  value; a sort; a choice of columns; a selection; a topic's decoded records;
  or one result of a script of several. `export` can be told a table, a
  condition as text and a limit. Until it can be told the rest, a job saved
  there would not write what the window wrote. T5.18 stays open for it.
- A table whose name has a dot in it, and a table column with an equals sign
  in its name, cannot be said in a job, and saving says so.
- A list of jobs kept by the application. A job is a file wherever somebody
  put it.

## Consequences

- No dependency. The export command is unchanged: its flags are read, not
  restated.
