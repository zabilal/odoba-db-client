# ADR-0161: A second binary, and a status a build can read

**Status:** Accepted · **Date:** 2026-09-25
**Tasks:** T5.9 · **Requirements:** FR-16.1, FR-7.6, FR-7.7, FR-10.1, FR-10.3, NFR-S1, NFR-S2, NFR-S4, NFR-P8
**Packages:** `internal/cli`, `cmd/ikigai-cli`, `internal/app`

## Context

FR-16.1 asks for a command line: run a saved query, export, import, deploy a
model, diff two databases, for CI/CD. FR-7.7 asks for the deploy by name.

Everything those verbs need already existed for the window — the drivers, the
comparison, the export writers, the loader — so the question was never how to do
the work. It was what a pipeline is, and how it differs from a person.

A pipeline cannot answer a dialog. It reads exit codes rather than sentences. It
runs where there is no display. And what it does must be the same every time.

## Decisions

1. **A second binary, not a mode of the application.** A binary that draws links
   against the platform's window system whether it opens a window or not, so a
   container would have to carry those libraries to run `ikigai query`. A test
   holds `cmd/ikigai-cli` to importing nothing that draws — with a control that
   points the same rule at the window and requires it to find the toolkit there,
   so the rule cannot pass by looking for the wrong thing.

2. **The same drivers as the window, held by a test.** A pipeline that reached
   fewer sources than the application would be a surprise nobody could see the
   reason for. The rule is the one T4.3 wrote after the Kafka driver turned out
   to have never been imported: the driver directories are held against what the
   binary depends on, and a new one fails until both carry it.

3. **Three ways to say which database, and a password in none of them.** A
   connection string, explicit flags, or a connection saved in the application.
   The password comes from `IKIGAI_PASSWORD`, or `IKIGAI_URL` for a whole string,
   because a command line is readable by everything else on the machine — a
   password in one is a password in every process list and every shell history.
   Saying it two ways is a mistake rather than a preference, and is refused.

4. **A saved connection opens through the application's own `Connections`,** so
   a tunnel, a cloud token and the keychain all work without being
   reimplemented. `--read-only` there tightens the guard on the connection this
   process opens and writes nothing to the file, which needed one new method on
   `Connections`: read-only is enforced in the data layer (NFR-S4), so asking
   for it has to reach the guard rather than be checked here.

5. **A sealed vault is refused, and not asked about in the environment.** There
   is nobody to ask for a passphrase, and a passphrase in an environment
   variable would undo the sealing for every process on the machine (NFR-S7).
   The refusal says to unseal it in the application or to pass a connection
   string.

6. **Four exit statuses, and 3 is not a failure.** 0 done, 1 failed, 2 the
   arguments could not be acted on, 3 there is a difference. A gate wants to tell
   "they differ" from "it broke", and a pipeline that read 1 for a flag it got
   wrong would go looking at the database. An argument mistake is 2 whoever
   noticed it, including the ones only discoverable where the work is.

7. **Rows go to one stream and the talking to the other.** A pipeline
   redirecting rows into a file must not find "3 rows" among them, so every
   count, every warning and every refusal goes to the error stream even when
   nothing is wrong.

8. **A file is written under another name and moved into place.** A pipeline
   reading the output of a run that failed finds nothing rather than half of
   something.

9. **`deploy` prints its statements and stops.** ADR-0115 says a structural
   change is read before it runs, and in the window a person reads it. A pipeline
   has no person at the moment it runs, so the reading is done earlier, by
   whoever wrote the pipeline, and the printing is what makes that possible: the
   statements in the build log are the same statements `--apply` would run, from
   the same comparison. Anything that drops an object needs `--allow-drops` as
   well, because a model that lost a table by accident would otherwise take the
   table with it.

10. **Every difference is deployed, or none.** The window lets somebody choose
    which differences to close; a pipeline has nobody to choose. A pipeline that
    wants less than everything wants a model that says less, which is what the
    model's own ignore rules are for (FR-7.5) — and a comparison says that they
    are in force, because a rule can hide a dropped column.

11. **`save` exists although FR-16.1 does not list it.** The list does not work
    without it: deploying a model and comparing against one both need a model to
    have been written, and until this the only thing that could write one was the
    window. "Commit what the database now is" is one line, and the format is the
    one the window writes and the comparison reads (ADR-0121).

12. **An import pairs columns by name and says what it left out.** The window's
    import is a wizard — look at the file, pair the columns, see what would
    happen. A pipeline has nobody to show it to, so the pairing is the one the
    wizard suggests, and a file column with no column to go in is named rather
    than dropped quietly: a load that silently left out a column would be a file
    that looked imported and was not. `--dry-run` reads the whole file, makes
    every value the table's, writes nothing, and answers 3 where a value would
    not go in.

13. **The tests run against SQLite, which is a real database in a file.** Every
    verb but one runs for real in the ordinary test suite: a real connection, a
    real schema read, real rows in a real file, no server and nothing skipped.
    The exception is `deploy`, because SQLite renders no DDL, and that is
    asserted too — the documented limit held as a test, so the day SQLite grows
    a DDL generator the test fails and the limit comes out. Deploying for real is
    an end-to-end journey against PostgreSQL: J6, promoting a schema change, with
    a model saved, a table dropped behind its back and the deploy putting it
    right.

## Consequences

**A defect this found.** `app.Snapshot` read a database's contents by asking for
the children of a database reference. SQLite never answers that: it has one
database and no node above its class folders, so its root *is* what its database
holds. Every snapshot of a SQLite database was therefore empty — which meant two
different SQLite databases compared equal, a model saved from one held nothing,
and a comparison against a saved model reported no differences. All three looked
like success, which is the worst way for a comparison to be wrong. `classesUnder`
now reads the root where the source has one database and says nothing about it,
and a fake shaped like SQLite holds it in `internal/app`. The window had this
defect too: "Save as a Model…" and "Compare with a Saved Model" are offered on
SQLite and both were empty.

**The second binary is 69.8 MB** where the application is 86.3, so the toolkit is
about 16 and the drivers are the rest. It is measured in CI beside the
application, and the same three drivers dominate both (T4.30's open decision).
CI also fails if the command line ever grows past the application, which would
mean something that must not be imported has arrived.

**What is not here.** No `--params` file, no output to several files, no
`--format parquet` (T5.17's), and no way to run a saved query against a
connection other than the one it was saved on. The verbs are the five the
requirement lists plus `save`.

## Alternatives

**One binary with a command-line mode.** Rejected by decision 1: a GUI binary
needs a window system present on Linux, and a pipeline runs in a container
without one.

**A `--password` flag.** Rejected: it would put the password in every process
list on the machine. The environment is not private either, but it is not
readable by every other process by default, and it is what every other tool of
this kind uses.

**`deploy` applying by default, with `--dry-run` to hold it back.** Rejected: the
first time somebody ran it against the wrong database would be the last time they
trusted it. The safe direction is the default, and ADR-0115 already decided this
shape for the window.

**Letting a pipeline choose which differences to deploy.** Rejected: a selection
on a command line would be a list of names in a build script, drifting from the
model it was written against. The model is the selection.

**Reimplementing the open path for a connection string, rather than using
`Connections` for a saved one.** Rejected both ways round: a URL cannot name a
tunnel or a cloud identity, so nothing is lost by opening it directly, and a
saved connection can name both, so nothing is gained by reimplementing it.
