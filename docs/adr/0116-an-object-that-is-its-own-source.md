# ADR-0116: An object that is its own source

**Status:** Accepted · **Date:** 2026-09-22
**Tasks:** T3.5 · **Requirements:** FR-6.5, FR-6.4
**Packages:** `internal/app`, `internal/ui/shell`, `internal/source/drivers/postgres`

## Context

FR-6.5 asks for editors for views, procedures, functions, triggers and
sequences. The designer ADR-0114 built does not fit any of them. A view is
not a list of columns; a function's body is a language this program does not
parse — PL/pgSQL, or SQL, or Python, or whatever else an engine has been
taught. A grid of fields would have to leave the body in a box anyway, and
then the grid is decoration around the only part that matters.

What every one of these objects has in common is that the engine keeps the
text somebody wrote and gives it back on request. `pg_get_functiondef`,
`pg_get_triggerdef` and `pg_get_viewdef` exist precisely because that is the
form the object has.

## Decisions

1. **The editor is the text, in the editor a query is typed in.** Read what
   the engine kept, show it, send back what somebody made of it. Same
   `view.Editor`, same highlighting, same dialect. Nothing here is a form.

2. **What is sent is what the engine printed, not something rebuilt from
   parts.** A routine is re-sent as `pg_get_functiondef` printed it, a
   trigger as `pg_get_triggerdef` printed it. Rebuilding would mean parsing
   a body this program cannot parse, and a round trip that loses something
   loses it silently — the thing ADR-0115 exists to prevent one step later.

3. **The preview is the same preview.** Editing a view runs nothing directly:
   it renders statements, shows them, and runs the list that was shown
   (ADR-0115). A source editor is a structural change like any other, so it
   is read before it runs and it is guarded like the rest.

4. **A materialized view goes and is made again, and says so.** PostgreSQL
   has no `CREATE OR REPLACE MATERIALIZED VIEW`. That is a real difference —
   the data is rebuilt — so it renders as two statements somebody can read
   rather than one that hides it. A plain view is `CREATE OR REPLACE`, which
   is what it means.

5. **A trigger is dropped `IF EXISTS` and made again**, because
   `CREATE OR REPLACE TRIGGER` arrived in PostgreSQL 14 and this does not ask
   the server its version to decide what to send.

6. **A sequence is the exception and is treated as one.** It has no source;
   it is six numbers. So what is shown is the statement that would set them,
   and editing that statement is editing the sequence. It is the only object
   here whose editor holds statements rather than a definition, so it is the
   only one that reaches the preview by splitting a script instead of
   rendering an object — `app.SplitStatements`, using the connection's own
   rules about where a statement ends.

   The alternative was a form of six boxes. It would be a second way to say
   the same thing, and it would render the statement anyway at the end.

7. **A routine whose arguments were edited makes a second routine.** That is
   what PostgreSQL does — the identity of a function includes its arguments —
   and the preview shows the `CREATE OR REPLACE` that will do it. Hiding it
   behind a drop nobody asked for would delete a routine somebody else may
   be calling.

   For the same reason a routine is described by `proname` plus
   `pg_get_function_identity_arguments(oid)`, parameter names included, which
   is exactly the key the tree lists routines under: a node from the tree
   always resolves to the routine that node named.

8. **Refusing beats sending nothing.** An object described with an empty
   definition renders an error, not an empty statement. An engine that
   answered with nothing is a question to ask, not a script to run.

## Consequences

- Only PostgreSQL implements `DDLGenerator`, so only PostgreSQL offers these
  editors. On MySQL, SQLite and Cassandra the menu item is unavailable for
  the same reason the designer previews nothing there — one missing
  implementation, not five.
- Opening the same object twice brings the first tab forward. Two editors on
  one view could disagree, and only one of them could run.
- After something runs, the tab reads the object again rather than trusting
  what was typed: what PostgreSQL stores is normalised — a view comes back
  with its columns expanded and its keywords its own way — and the editor
  should show what is there now, not what was sent.
