# ADR-0058: The tables a statement reads

**Status:** Accepted · **Date:** 2026-09-12
**Tasks:** T2.26 · **Requirements:** FR-5.2 · **Packages:** `internal/source/sqlcomplete`

## Context

ADR-0057 offers a table's columns where the table is named before the dot.
That is not how SQL is written. `select o.| from orders o` names the table
once, far from the cursor and after it, and the rest of the statement calls
it `o`. FR-5.2 asks for alias-resolved columns for exactly this reason.

## Decisions

1. **`Scope` lists the tables a statement reads**, each under the name the
   statement calls it by: the alias where there is one, the table's own name
   where there is not. It reads the whole statement, not the part before the
   cursor, because a column is typed before the FROM clause that says where
   it comes from.

2. **It reads the lists, not the statement.** After FROM, JOIN, INTO or
   UPDATE comes a comma-separated list of names, each optionally followed by
   AS and a name, and a keyword is never that name — it is the next clause.
   That is the whole grammar. `ANALYZE` and `TRUNCATE` are left out: their
   table is the statement's object, not something columns are read from.

3. **A subquery's tables are in scope only inside its brackets**, and the
   statement's own are in scope within them, which is what a correlated
   subquery reads. Each table is recorded with the bracket group that holds
   it, and the groups open at the cursor are the ones it can name. A
   statement the cursor is not in — before a semicolon, or after one —
   contributes nothing.

4. **A derived table is recorded under its name with no table.** Its columns
   are the subquery's, which this does not read, and a table elsewhere in
   the catalog that happens to share the name is not read in its place.

5. **A qualifier is a name the statement reads a table under, first**, and a
   path (`public.orders.`) only when it is not. So an alias wins over a
   table of the same name, and the match is made without regard to case, as
   every engine here resolves unquoted names.

6. **Unqualified, every table in scope offers its columns**, and the names
   the tables are read under are offered beside them, so the qualifier can
   be typed with help. Where more than one table is read, a column says
   which it is from; where more than one *has* a column of that name, the
   name is written qualified, because unqualified the server would refuse it
   as ambiguous.

## Consequences

- A CTE (`with x as (…) select … from x`) is in scope as a table named `x`
  with no columns known, and the catalog is asked for a table of that name.
  Naming a CTE after a real table offers that table's columns.
- The scope is read on every keystroke, over the whole statement. It is the
  same lexing pass' cost as ADR-0057 and no round trip.
- A column offered from scope is not checked against what the server would
  allow — a column of a table joined later in the statement is offered while
  the FROM clause is still half-typed. Offering too much while typing is the
  right side to err on; the alternative is offering nothing until the
  statement is finished.
