# ADR-0138: Laying a statement out without changing it

**Status:** Accepted · **Date:** 2026-09-23
**Tasks:** T3.27 · **Requirements:** FR-5.12
**Packages:** `internal/sqlfmt`, `internal/ui/shell`

## Context

FR-5.12 asks for a formatter per dialect. A formatter rewrites somebody's
SQL, which is the only thing in this program that changes a statement
without being asked what it should become. The risk is not that it lays a
statement out badly — that is annoying — but that it lays one out
differently from what was written, which is data loss with a tidy face on
it.

## Decisions

1. **It works from the lexer's tokens and writes nothing else.** The
   formatter may move whitespace and change the case of SQL's own words, and
   may do nothing else. It cannot drop a comment, reorder a clause or touch
   a string, because it never writes anything but the tokens it was given.

2. **It checks its own work.** Before answering, the laid-out text is lexed
   again and its tokens compared with the ones that went in. Where they
   differ the statement is handed back exactly as it was written. The check
   costs one more pass over a statement somebody is looking at, and it turns
   the promise from something tested into something enforced.

3. **Statement by statement.** A script where one statement cannot be laid
   out safely — a function body with a string across three lines — still has
   the rest of it laid out, and the error says how many were left. One
   awkward statement should not stop a script being readable.

4. **A word the lexer flags as an error is a statement left alone.**
   Something it cannot read is something this must not rewrite.

5. **One rule decides every line break: it fits, or it breaks.** A clause of
   three short columns reads better on one line than on three, and a clause
   of twenty does not fit on any line. What breaks, and where, is in the
   order a reader looks for the next thing: a condition at its AND and OR, a
   CASE at its WHEN and ELSE, and anything else at the first bracket worth
   opening. A bracket with nothing in it is not one: two lines saying
   nothing are worse than one saying something.

6. **It knows no grammar.** It reads the clause words as they go past, which
   is enough to lay out what somebody wrote and cannot mislay what it does
   not recognise: a word it has never heard of is written where it stood.
   Where a guess is needed — is this LEFT a join or a function? — the wrong
   answer costs a space or a line break and never a meaning.

7. **SQL's own words are capitals and nobody else's are touched.** An
   unquoted identifier is folded by the server in its own direction and a
   quoted one means exactly what it says, so neither is a formatter's to
   change. The case of keywords can be left alone by asking.

8. **A fuzz target is the proof.** Forty-seven million inputs across five
   dialects, each checked for saying the same thing afterwards and for
   laying out the same way twice. It found one defect: a statement handed
   back untouched was being trimmed, and the whitespace trimmed was inside
   the token at its edge.

9. **What is laid out is what is pointed at**: the selection, or else the
   whole script. Laying out what is already laid out changes nothing and
   costs nobody an undo.

## Consequences

- PostgreSQL, MySQL, MariaDB, SQLite, SQL Server and Cassandra are all laid
  out, because the dialects are the lexer's and the layout is not dialect
  specific. What differs between them is which words are SQL's own and what
  quotes an identifier, which is exactly what the lexer already knows.
- A statement with a string or a dollar-quoted body across several lines is
  handed back as written. The formatter would have to keep the line breaks
  inside such a token to do otherwise, and a statement it will not touch is
  better than one it might change.
- Nothing aligns anything. The river style — every clause's body starting at
  one column — reads beautifully and needs the whole statement measured
  before a single line can be written; this lays out from left to right and
  can stop anywhere.
- The layout is not configurable beyond the indent, the width and the case.
  A formatter with a page of settings is one nobody agrees about; this one
  has the three that change how a statement reads on a particular screen.
