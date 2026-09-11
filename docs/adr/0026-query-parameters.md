# ADR-0026: Query parameters

**Status:** Accepted · **Date:** 2026-09-11
**Tasks:** T1.65 · **Requirements:** FR-5.7, NFR-S6, UX principle 4 · **Packages:** `internal/source`, `internal/source/sqlscript`, the drivers, `internal/app`, `internal/store/localdb`, `internal/ui/shell`

## Context

FR-5.7 asks for query parameters, with a prompt panel and remembered
values. Half of it was built. `source.Statement` carried named values:
PostgreSQL rewrote `:name` to `$n` through the lexer, SQLite bound `:name`
itself, and MySQL refused them. But a script was run with `QueryMulti`,
which took its text and nothing else. Nothing asked for values, and
nothing remembered them. ADR-0012 left parameters undecided.

## Decisions

1. **Named parameters, written `:name` on every engine.** They are found
   through the lexer, so a `:name` inside a string or a comment, a
   comment's across lines included, is none, and neither is a PostgreSQL
   `::text` cast (`sqlscript.Names`). The engines' own positional markers,
   `?` and `$1`, pass to the server as they are written. In PostgreSQL `?`
   is a jsonb operator, so asking for them would take each engine's own
   rules, and is not done here.

2. **A script carries its values.** `QueryMulti(ctx, script,
   ScriptOptions{Confirmed, Named})` replaces the `confirmed` flag. Every
   statement is given the script's values and binds those it uses.
   PostgreSQL's and MySQL's rewrites look up only the names a statement
   holds, and SQLite passes over a value it has no parameter for. Giving
   each statement only its own values was tried first, and no test on any
   engine could tell it from giving them all, so it was taken out.

3. **Values are bound, never written into the statement (NFR-S6).**
   PostgreSQL rewrites `:name` to `$n`, one number to a name however often
   it is used. MySQL rewrites it to `?`, one value to each use, since its
   placeholders are numbered by place. SQLite binds `:name` itself. The two
   rewrites share `sqlscript.BindNamed`. A name with no value is refused
   before anything is sent.

4. **Values are sent as typed.** The server reads each as its place needs.
   The tagged suites show it on each engine: the text "7" matches a bigint
   key on PostgreSQL, "21" added to itself is 42 on MySQL, and "7" finds an
   integer key on SQLite. So nothing guesses a value's type from how it
   looks. NULL is a box beside the value, not a word typed into it.

5. **The panel.** Before a script with named parameters runs, a side panel
   asks for them (UX principle 4: beside the work, not over it). It lists
   each name with the value last given on that connection, or NULL. Run
   runs the script, as does Return in a value; Cancel and Escape close the
   panel. The values are kept in the local database's key-value table, by
   connection and name (`localdb.Param`). A production script that needs
   confirmation keeps its values when confirmed.

6. **History records the script as written**, with its `:name`, and never
   the values. A value is whatever was typed, and a password could be one.

## Consequences

- Positional markers are not asked for. A statement written with `?` or
  `$1` and run without values fails at the server, as before.
- The panel offers no picker by type, such as for a date; a value is
  typed as the server reads it.
