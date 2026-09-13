# ADR-0077: The Redis console

**Status:** Accepted · **Date:** 2026-09-13
**Tasks:** T2.44 · **Requirements:** FR-3.6, FR-5.4, FR-5.10, FR-12.2, NFR-S4 · **Packages:** `internal/source/drivers/redis`, `internal/sqllex`

## Context

FR-12.2 asks for a raw command console. Redis has no query language: it has
commands, and redis-cli is where they are typed. The application already has a
place for a language — the query tab, its editor, its script runner and its
guard — and the question is whether Redis's commands can be that language.

They can, and mongosh already showed the shape (ADR-0070): a source's query
language is whatever it answers to, and the tab does not care that it is not
SQL.

## Decisions

1. **A command is a line.** `SplitScript` divides a script by lines, and a
   line beginning with `#` is a comment — redis-cli has none, and a script
   nobody can annotate is worse than one whose comments the server never sees.
   Arguments are split as redis-cli splits them: words apart, `"…"` with the
   escapes it knows (`\n`, `\t`, `\xHH`, `\"`), `'…'` where only the quote
   itself is escaped, and a quote left open is an error rather than a guess.

2. **A console holds a connection of its own.** `SELECT` moves this console
   and nothing else, and `MULTI`, `WATCH` and `CLIENT SETNAME` are that
   connection's own state for as long as the tab is open. It is what a person
   typing at a prompt means by "the connection", and it is what redis-cli is.
   A cluster has no connection to hold, so its console is the cluster, and
   each command goes to the shard its key names.

3. **What a command does is read from its name, and the list is read the safe
   direction.** Redis has no grammar to classify by: a command is a name. So
   the names that only read are listed, and the names that change the server
   rather than what it holds are listed, and everything else is a write. A
   name nobody listed costs a person a confirmation; the other way round would
   cost them their data. A container command — `CONFIG`, `CLIENT`, `ACL`,
   `CLUSTER`, `SCRIPT` — is read with its subcommand, and one of those whose
   subcommand is not among the reading ones administers the server.

4. **Every command of a script is put to the guard before the first one
   runs** (NFR-S4), as a SQL script's statements are: refusing the fourth
   after three have run would leave a person somewhere they did not choose.

5. **Two commands are refused outright**: `SUBSCRIBE` and its kin, and
   `MONITOR`. Each turns the connection into something that no longer answers
   commands, and a console that stopped answering would look like one that had
   hung. A live tail is FR-12.5.

6. **A reply is drawn as what it is.** A word, a number, a list of them, a
   list of lists, or the pairs of a map: each becomes rows of its own shape,
   with the column typed as its values are and text where they are not all
   one. What is nested is JSON, so the cell viewer shows the structure; what
   is not text is bytes. A reply of nothing is an answer — `(nil)` — and not a
   failure, and a number is what a write touched.

7. **Redis is a lexer dialect of its own**, registered as `redis` (and
   `valkey`): its commands as words, `"…"` as a string rather than a name in
   quotes, and `#` as a comment. The conformance suite checks that a driver's
   declared language is one the lexer knows, so the console is coloured and
   its history redacted by Redis's rules rather than PostgreSQL's.

8. **A condition typed in the grid is a pattern.** Implementing `Dialect`
   makes the grid offer the box FR-3.6 describes, and the only condition Redis
   understands about a name is a glob. So a typed condition is the pattern the
   names — or a hash's fields, a set's members — are matched by, refused
   beside a filter that is already one, and refused altogether where the
   server cannot narrow at all (a list, a string, a stream, a document).

9. **The grid is shown the command a browse sends** (`BuildBrowse`): the
   `SCAN` of a keyspace with its pattern, its count and its type, or the
   `LRANGE`, `HSCAN` or `JSON.GET` of one key. What a key holds is only known
   once something has read it, so the source remembers the kind each key was
   last seen with and shows no command for one nothing has read, rather than
   one it does not send.

## Consequences

- A Redis connection now claims a query language, so the query tab, the script
  runner, the history and the editor's colouring all work on it with no change
  to any of them (REQ-DB-1).
- The classification table is the one thing here that will age: a Redis
  release adding a read-only command makes this application ask for a
  confirmation it need not. That is the direction to age in.
- `CONFIG SET` and the rest are guarded as structural changes, which means a
  production connection asks twice before one runs.
