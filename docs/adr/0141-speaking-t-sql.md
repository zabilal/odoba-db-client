# ADR-0141: Speaking T-SQL

**Status:** Accepted · **Date:** 2026-09-23
**Tasks:** T3.30 · **Requirements:** REQ-DB-1, FR-2.1, FR-2.3, FR-3.3, FR-3.4, FR-5.4, FR-5.10, NFR-S3, NFR-S4, NFR-S6
**Packages:** `internal/source/drivers/sqlserver`

## Context

Microsoft SQL Server is the last relational engine REQUIREMENTS names, and
the first new driver since Phase 2. Eight drivers before it settled the
contracts, so what this records is only where SQL Server is not like the
others — and it is not like them in more places than its syntax.

## Decisions

1. **A pool per database.** SQL Server puts a level between the server and
   the schema that PostgreSQL and MySQL do not: a server holds databases, a
   database holds schemas, a schema holds tables. A connection is to one
   database and reaching another means another connection, so the explorer
   shows the databases a login can open and the driver keeps a pool for each
   one it is asked about, as the PostgreSQL driver does for the same reason.
   A reference is three parts deep and is written two parts deep: the
   statement is already running on a connection to the database it names,
   and naming it again would make a cross-database reference of what is not
   one — which needs a privilege the connection may not have.

2. **A URL, not a keyword string.** A password with a semicolon in it ends a
   keyword connection string early and nothing says so. The URL form escapes
   it (NFR-S6). Encryption is on and verified unless somebody chose
   otherwise (NFR-S3); the driver spells the two halves of that in two
   settings, and the second only means anything while the first is on.

3. **Classification is the only defence.** There is no session setting that
   makes a SQL Server connection read-only — no `default_transaction_read_only`,
   no `query_only` pragma — so unlike every other relational driver here,
   what this program decides is all there is (NFR-S4). That is a reason to
   be stricter rather than cleverer: a word this has not been taught is a
   write, and what `EXEC` runs is taken for the most it could be unless the
   name is one of the catalogue's own.

4. **A script is cut twice.** First at a `GO` line, which is not SQL at all:
   the server has never heard of `GO`, and what it separates is a batch, the
   unit a client sends. Then within a batch at a semicolon — except inside a
   `BEGIN … END` body, whose semicolons are the body's own, and except in a
   batch that defines a routine, a view or a schema, which the server
   requires to hold nothing else and which is therefore sent whole. That
   last rule is the server's own, and taking it means a procedure body needs
   no guessing about where it ends.

5. **A body opens at the word after `BEGIN`.** `BEGIN TRANSACTION` opens
   nothing and has no `END` to match it, and which of the two a `BEGIN` is
   cannot be told until the word after it is read. Nothing but whitespace
   and comments may sit between them, so opening a word late loses nothing.

6. **An `ELSE` is glued to the `IF` before it.** An `IF` whose arms are
   single statements ends its first arm with a semicolon, and that semicolon
   ends the arm rather than the statement. Which semicolon that is cannot be
   known until the word after it has been read, so the statements are put
   back together afterwards.

7. **The number after `GO` is not obeyed.** It tells sqlcmd to send the
   batch again. A window that ran a write five times over because a digit
   followed a word would be hard to forgive, so the batch runs once.

8. **Paging always has an order.** `OFFSET … FETCH` is the only paging SQL
   Server has and it is part of `ORDER BY`, so a browse with nothing to sort
   by writes `ORDER BY (SELECT NULL)`, which is how T-SQL says the rows may
   come in whatever order they come in. There is no `NULLS FIRST`, so where
   the NULLs go is written as a `CASE` that sorts on whether the value is
   one — which keeps them where they were asked for when the sort is
   flipped.

9. **A table with no key is ordered by every column that may be sorted on.**
   SQL Server has no row address to fall back on: `%%physloc%%` is where a
   row sits on a page and moves when it is updated. So it is the primary
   key, and failing that the first unique index whose columns cannot be
   NULL — one that can holds a single NULL row and tells no others apart —
   and failing that every column, minus the ones `ORDER BY` may not name.

10. **Stopping a statement costs its connection.** TDS has an attention
    signal and the driver sends it when the statement's context ends, which
    does stop the statement at the server — but the connection does not come
    back usable and `database/sql` retires it. A tab whose stop button left
    it unable to run anything again would be worse than one that lost a
    temporary table, so the session takes a new connection. What the old one
    held is gone, and nothing pretends otherwise. `KILL` is not used: it
    would end the session as well, and from a second connection.

11. **An error's line is counted into a character offset.** The server says
    which line of the batch an error was on and the contract asks where in
    the statement it was, so the statement is counted through to the start
    of that line (FR-5.10). A name the server could not resolve is reported
    against line one, because that is where the server says it was; what is
    claimed is only that where it says is where the editor points.

12. **The server's properties, not its banner.** `@@VERSION` is four lines
    of prose about the build and the operating system. `SERVERPROPERTY`
    gives the build, the edition and which engine answered in one round
    trip, on every edition including Azure's — and a connection to Azure SQL
    Database is named as that, because calling it a SQL Server somebody runs
    would tell a person the wrong thing about what they may do to it.

13. **A bracket is escaped in a search.** T-SQL's `LIKE` has a third
    wildcard the other engines do not: `[abc]` matches one of them. So
    `contains` escapes `[` as well as `%` and `_`, and a search for a
    bracket finds one.

## Consequences

- The read path is whole: the explorer, structure, browsing, filtering,
  paging, the picklist, the query tab, scripts and grid edits.
- Four things are deliberately not claimed yet, because claiming a
  capability with nothing behind it is worse than not claiming it: editable
  query results (SQL Server can answer where a result's columns came from,
  through `sys.dm_exec_describe_first_result_set`, at the cost of a round
  trip per statement), `SHOWPLAN_XML` plans, explicit transactions, and bulk
  loading — the shared loader writes `SAVEPOINT`, which T-SQL spells `SAVE
  TRANSACTION`, so its skip policies would fail here.
- `PRINT` output and server messages are not yet shown. Reading them means
  `sqlexp.ReturnMessage`, which turns the result loop into a message loop.
