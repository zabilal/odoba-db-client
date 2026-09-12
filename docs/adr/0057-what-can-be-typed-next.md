# ADR-0057: What can be typed next

**Status:** Accepted · **Date:** 2026-09-12
**Tasks:** T2.25 · **Requirements:** FR-5.2 · **Packages:** `internal/source/sqlcomplete`

## Context

FR-5.2 asks the editor to offer keywords, schemas, tables, columns and
functions where they can go. RISK-2 staged it into Phase 2 because no
pure-Go editor was going to be had for free.

A statement being typed is not a statement: half of it is missing, and the
half that is there is often wrong. Nothing can parse it. What the source
layer already has is a lexer that resumes per line (`internal/sqllex`), and
a `source.Completer` interface declared for this and never implemented.

## Decisions

1. **One engine, not one per driver.** `sqlcomplete.Engine` takes a lexer
   dialect, a catalog and the source's `QuoteIdentifier`. What differs
   between engines is already data: the keyword list, the quote character,
   whether a schema is a database. A driver implements `source.Completer`
   by handing those three over.

2. **The grammar is the token before the cursor, and the clause it is in.**
   A name after FROM, JOIN, INTO, UPDATE, TABLE, TRUNCATE, ANALYZE or
   DESCRIBE is a table's, and a schema's is offered beside it. After
   SELECT, WHERE, ON, HAVING, BY, SET, an operator or an opening bracket,
   a column, a function or a keyword can go. After a finished thing — a
   name, a literal, a closing bracket — only a keyword carries the
   statement on, and at a statement's start only a keyword begins one.
   A comma means another of whatever its clause is a list of, so the walk
   back to that clause steps over bracketed lists: the comma in
   `generate_series(1, |)` is the call's, and the WHERE inside a subquery
   in a FROM list is not the FROM's.

3. **Nothing is offered inside a comment, a string or a parameter.**
   Completing there would write SQL into text. The lexer's state is carried
   across lines, so a cursor three lines into a block comment is seen to be
   in one.

4. **A dotted chain is read as a place.** `public.` is a schema, `orders.`
   a table, `sales.public.` a database and a schema; which a single name is,
   the catalog says, and both are asked, so tables and columns come back
   together. A name that is an alias for a table is T2.26's work.

5. **The catalog answers from memory.** No context, no error, no round
   trip: completion runs on the keystroke path, where NFR-P5 allows 16 ms
   for everything. A catalog that does not yet know a schema's tables
   returns none and the keywords still come. Filling it, and emptying it
   when DDL runs, is T2.29's. `Static` is the catalog the tests complete
   against, and what that cache will build.

6. **Ranking is what a candidate is, then how it was typed.** A column
   scores above a table above a schema above a routine above a built-in
   function above a keyword, because the position has already decided which
   kinds are offered at all; within a kind, the fuzzy score the command
   palette uses (ADR-0018) orders them, and a name the typed letters *begin*
   is lifted over one they are merely scattered through: `id` before
   `is_deleted`. Equal candidates go in name order, so the popup does not
   shuffle between keystrokes.

7. **An identifier is quoted where it must be**, through the source's own
   `QuoteIdentifier` (ARCH-2): a name that is not plain lower case, or that
   the server would read as a word, or one whose opening quote the person
   has already typed. A keyword is written in the case being typed, upper
   until a lower-case letter says otherwise.

8. **The result says what it replaces**, as rune offsets into the request's
   text, once rather than per candidate: the word being typed, with its
   opening quote, and the whole of it where the cursor is in its middle.
   `source.Completer` returns a `CompletionResult` for this, changed here
   from a bare slice while no driver had implemented it.

## Consequences

- The whole statement is re-lexed on every keystroke. It is affordable
  because the lexer is (sqllex's probe: a 5 000-line buffer in under a
  millisecond) and because completion is asked over one statement.
- Unqualified names are offered from the session's schema alone. A table in
  another schema is reached by naming that schema first, which is also how
  it would have to be written.
- The rules are a heuristic, not a grammar. `CREATE TABLE |` offers the
  tables that are there, where a new name is what is wanted; the popup is
  dismissed by typing, so the cost is a glance.
