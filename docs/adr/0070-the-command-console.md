# ADR-0070: The command console

**Status:** Accepted · **Date:** 2026-09-12
**Tasks:** T2.37 · **Requirements:** FR-12.1, FR-5.1, FR-5.4, FR-3.6 · **Packages:** `internal/sqllex`, `internal/source/drivers/mongo`, `internal/source/conformance`

## Context

FR-12.1 asks for a `mongosh`-compatible command console. mongosh is
JavaScript with a driver bound into it; a JavaScript engine is not something
this application will carry. What a person actually types at a database
prompt is a much smaller language, and that is what this reads.

## Decisions

1. **The console reads commands, not JavaScript.** `db.<collection>.<method>(…)`,
   `db.<method>(…)`, `show collections`, `show dbs`, `use <database>`. Anything
   else is refused by name, with what the console does know beside it. One
   call a line: `find({}).limit(5)` is said to be more than the console reads,
   rather than half-run.

2. **Arguments are extended JSON, read as a person writes them.** Single
   quotes are JavaScript's, so they are read as quotes; a comma inside a
   document is not a comma between arguments; a bracket inside a string is
   text. `db["my.people"]` names a collection whose name has a dot in it.

3. **MongoDB's query language is this console's** (`capability.Query.Language`
   = "mongosh"), and the lexer knows it: a dialect whose strings are
   double-quoted, whose comments begin with `//`, and whose words are the
   driver's methods. The lexer gained two flags for it, and a string now
   remembers which quote opened it, so a `'` inside `"…"` does not end it.

4. **Every command is classified and put to the guard before any of them
   runs** (NFR-S4): a find reads, an insert writes, a drop is structural, and
   an aggregate whose stages write is a write whatever it looks like. A
   command the console cannot read is taken to change everything, which is
   the safe direction. A script is refused whole rather than half-applied.

5. **A session holds the database the commands are on**, which `use` changes
   and the next command sees. Nothing else is session state: MongoDB has no
   temporary tables to lose.

6. **A condition typed in the grid is a filter document** now that the
   language exists (FR-3.6): `{"score": {"$gt": 10}}`, ANDed with the grid's
   own filters. It must be one document — extended JSON reads the first and
   ignores what follows, which would be the document store's version of a
   second statement smuggled in behind a semicolon.

7. **The conformance suite takes a source's conditions in its own language.**
   It asked every driver for `1 = 1`; it now asks the target what a condition
   matching everything looks like, what one matching nothing looks like, and
   which forms must be refused. SQL's are the default, so no other driver
   changed.

## Consequences

- A console command reads at most two hundred documents. It answers a person
  reading a screen; a collection is paged by the grid.
- `db.people.find({}).sort({name: 1})` is not read. Sorting from the console
  is `aggregate([{"$sort": …}])`, which is one call.
- Unquoted keys — `{name: "Ada"}`, which mongosh accepts — are not read
  either: they are not JSON. The console says what it could not read, and the
  quotes are a small thing to add.
- The query tab, its history, its statement-at-the-caret and its error
  positions all work for MongoDB now, because they ask the dialect and the
  dialect answers in mongosh.
