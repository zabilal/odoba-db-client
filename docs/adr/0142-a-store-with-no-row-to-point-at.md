# ADR-0142: A store with no row to point at

**Status:** Accepted · **Date:** 2026-09-23
**Tasks:** T3.31 · **Requirements:** REQ-DB-1, FR-2.1, FR-2.3, FR-3.3, FR-3.4, FR-4.7, FR-5.4, FR-5.5, NFR-S3, NFR-S4, NFR-S6
**Packages:** `internal/source/drivers/clickhouse`, `internal/sqllex`

## Context

ClickHouse is the first column store this application speaks to. The
contracts were settled by nine row stores before it, and most of them fit;
where they do not, it is because a column store is arranged around
answering questions over many rows rather than around finding one.

## Decisions

1. **No row has an address, so the grid reads and does not edit.** A
   MergeTree's `ORDER BY` is the order its parts are written in, and nothing
   enforces that it is unique. The virtual columns that say where a row sits
   on disk — `_part`, `_part_offset` — move when parts merge. There is no
   third option, so a browse reports `IdentityNone`, and `Data.Insert`,
   `Update` and `Delete` are not claimed. A capability with nothing behind
   it is worse than none (FR-4.7).

2. **Paging orders by the sorting key.** That is the order the data is
   already in, so it is the cheap one. Two rows equal on the key may change
   places between one page and the next, which is what a store without a row
   address can offer; ordering by every column instead would be a total
   order and a sort of the whole table, which on the tables this engine is
   for is not a page of rows. A table with no sorting key at all — Memory,
   Log, a view — is small by what it is for, and is ordered by every column
   that may be ordered by.

3. **Only the key's plain column names are written into the statement.** A
   sorting key may be an expression, `toYYYYMM(d)`, and an expression is not
   a name this program may write (NFR-S6). Dropping it costs only the
   ordering it would have added.

   A table with no sorting key at all is ordered by every column it has.
   ClickHouse compares almost everything — a map, a tuple, a point and a
   JSON document all have an order — and the one type it will not order by,
   an aggregate state, is one this driver cannot read at all, so a table
   holding one is unreadable long before it is unsortable.

4. **Which statements answer with rows is decided before they are sent.**
   Asking clickhouse-go for rows that a statement does not have ends the
   connection, not just the statement, and a query tab would lose everything
   it had left behind. So a short list of the words that certainly answer —
   `SELECT`, `WITH`, `SHOW`, `DESCRIBE`, `EXPLAIN`, `EXISTS`, `CHECK` —
   decides it, and everything else is sent as a statement with no result.
   Being wrong the other way costs a result nobody sees, which is the milder
   of the two.

5. **Stopping a statement costs its connection, and not the tab.** The
   driver tells the server to cancel and then has nothing usable left, and
   `database/sql` retires the connection. The session takes a new one and
   keeps its name, which is what anything stopping it knows it by; what the
   old connection held is gone, and nothing pretends otherwise. This is the
   same bargain the SQL Server driver strikes (ADR-0141), for a different
   reason.

6. **Every statement is named after its session and numbered within it.**
   That is what `KILL QUERY` stops, from a second connection, by matching
   the session's name as a prefix. Numbered rather than named once, because
   a statement somebody has stopped is still running at the server for a
   moment afterwards and ClickHouse refuses a name a running statement
   already has.

7. **One pool, not one per database.** A ClickHouse connection reads every
   database it has rights to by naming it, so there is nothing a second
   connection would be for — unlike SQL Server, where a database is what a
   connection is to. The tree is two levels deep above its objects: there is
   no schema between a database and its tables.

8. **A type is a small language, not a word.** `Nullable(Decimal(30, 10))`,
   `Array(LowCardinality(String))`, `Map(String, UInt64)`. `Nullable` says
   the column may hold nothing and `LowCardinality` says how it is stored;
   what it holds is inside either. What is shown beside the column is the
   whole declaration, because that is what somebody wrote.

9. **An exact number is padded back to its column's places.** The driver's
   decimal drops the zeros at the end of one, so a column of money would
   show 1.5 where the row holds 1.50. The column says how many places there
   are, and a number shown with fewer is a different number to read.

10. **Classification is the defence.** ClickHouse has a `readonly` setting,
    but it belongs to the server's user profile and not to this connection,
    so what is decided here is what stands between somebody and their data
    (NFR-S4). `ALTER TABLE … UPDATE` and `… DELETE` are writes wearing a
    definition's clothes, and are classified as writes.

11. **The lexer learned ClickHouse, and a second way of writing a name.**
    ClickHouse reads both `` `name` `` and `"name"`, so the lexer gained an
    `AltQuoteIdent`. Without it a semicolon inside a double-quoted name
    would end a statement that has not ended.

12. **What bounds a typed condition is the parser, not the classifier.** On
    SQL Server a `SELECT` can be made to write, and a filter's typed WHERE
    is weighed for it (ADR-0141). Here it cannot, so a rule refusing a
    condition the classifier called mutating would be one that could never
    fire; what keeps the condition to one condition, with its brackets
    closed and its comments ended, is `sqlscript.Predicate` (FR-3.6).

13. **A session's name is readable while its statement is running.** That is
    the only moment anything wants it: what asks is whatever is about to
    stop that statement. The name is therefore held apart from the lock the
    statement is run under — here it never changes at all, and in the SQL
    Server driver, where a stopped statement costs the connection and its
    number, it is held as an atomic.

14. **A search is a search and a pattern is a pattern.** ClickHouse has
    `match()`, so `regex` is a real regular expression here rather than
    something refused (as on SQL Server) or approximated. `contains` is
    `ILIKE` over the column's text, with the text escaped so that a per cent
    somebody typed is a per cent.

## Consequences

- The read path is whole: the explorer, structure, browsing, filtering,
  paging, the picklist, the query tab and scripts.
- A statement that changes rows reports no count. ClickHouse says what it
  wrote as progress rather than as a number of rows, and an `INSERT … SELECT`
  writes rows nobody counted; saying nothing is better than saying zero.
- A table with an `AggregateFunction` column cannot be read: clickhouse-go
  has no way to decode one, so the whole result fails rather than that
  column. Nothing here can mend it, and nothing pretends the column is
  simply unsupported.
- Not claimed, for want of something behind them: writing from the grid,
  bulk loading, `EXPLAIN` plans, editable query results and transactions.
  Transactions are experimental in the engine itself.
