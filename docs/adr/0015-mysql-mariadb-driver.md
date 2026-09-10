# ADR-0015: The MySQL and MariaDB driver

**Status:** Accepted · **Date:** 2026-09-10
**Tasks:** T1.35–T1.37, T1.39, T1.40 · **Packages:** `internal/source/drivers/mysql`, `internal/source/tlsconf`, `internal/source/sqlscript`, `internal/e2e`

## Context

MySQL and MariaDB are the last of Phase 1's engines. MariaDB began as MySQL
and still speaks its protocol, but the two have drifted, and a driver that
guesses which one it is talking to will guess wrong.

## Decisions

**One driver, and the server says which it is.** go-sql-driver/mysql
serves both. `Open` asks `VERSION()` and keeps the answer. Where the two
servers differ, the driver consults it rather than trusting what the
connection was called (REQ-DB-2). The query language is reported as `mysql`
or `mariadb`, and both lex with the MySQL dialect.

**Read-only is refused twice, and the second defence diverges by name.**
The guard refuses writes by classification. A read-only connection also sets
its sessions' transactions read-only on every pooled connection:
`transaction_read_only` on MySQL, `tx_read_only` on MariaDB. That is why the
classifier treats `SET` of a session variable as admin: setting that one
back would switch the second defence off. `SET @user_var` and `SET NAMES`
stay harmless.

**Cancel keeps the session.** go-sql-driver closes a connection whose
context is cancelled, and a query tab's session — its variables, temporary
tables and open transaction — would go with it. So a session's statements
run under a context that ignores cancellation, and a watcher sends
`KILL QUERY <id>` from another connection when the user stops. PostgreSQL
behaves the same way (ADR-0008). A test sets a variable, runs a statement
long enough to cancel, cancels it, and reads the variable back.

**A stopped statement is reported as stopped.** Some statements return
normally when killed: an interrupted `SLEEP()` or `BENCHMARK()` yields a
row. The first version showed that row, passing a stopped query off as a
finished one. The test caught it: the statement ended at the cancel, but
with no error. If a statement's context ended while it ran, the outcome is
now a cancellation, whatever the server sent. A stream that ends early
because of a stop reports the stop, not the end of the data.

**Closing a half-read result stops the query first.** go-sql-driver reads a
result to its end before closing it. Superseding a large result would have
waited for every row, so the session kills the query first.

**Paging needs a key, and MySQL has no row address.** Tables are ordered
last by the primary key, or else by the first unique index whose columns are
all `NOT NULL`. A unique index over nullable columns does not tell NULL rows
apart. A table with neither is ordered by every column, which is
deterministic for distinct rows and the best there is.

**Small dialect choices, each for a reason.**

- `NULLS FIRST/LAST` does not exist in MySQL, so sorting on `col IS NULL`
  first emulates it.
- `LIKE` escapes with `!`, not a backslash: what a backslash means inside a
  string literal depends on `sql_mode`.
- `REGEXP` serves regex filters.
- Counts are estimates (`TABLE_ROWS`), shown as ones, because InnoDB counts
  by scanning.
- Sessions run in UTC, so `TIMESTAMP` values arrive as instants and
  `DATETIME` values as wall-clock times.

**Routine bodies need `DELIMITER`.** The splitter honours the client's
`DELIMITER` directive (`sqlscript.SplitDelimited`), which is how MySQL
scripts keep a `BEGIN … END` body in one piece. Guessing at `BEGIN … END`
nesting instead would fail: `END IF` and the `IF()` function both look like
block boundaries.

**TLS is shared.** `internal/source/tlsconf` gives every network driver the
same four modes, with verification on by default. PostgreSQL moved onto it
unchanged.

## Known divergences (REQ-DB-2)

| | MySQL 8.4 | MariaDB 11.8 |
|---|---|---|
| Read-only session variable | `transaction_read_only` | `tx_read_only` |
| JSON columns | a JSON type; arrive as JSON | an alias for LONGTEXT; arrive as text |
| `STATISTICS.EXPRESSION` | present | absent; the driver falls back |
| `ANALYZE <statement>` | not a statement | executes the statement; classified by what it runs |

## Testing

Two containers, `ikigai-mysql` (mysql:8.4, port 53306) and `ikigai-mariadb`
(mariadb:11, port 53307), run the conformance suite, the driver's own
integration tests, and journeys J1 and J3. All of it carries the
`conformance` tag, and `IKIGAI_REQUIRE_MYSQL=1` makes a missing server a
failure.

## Not decided here

Stored routines, events and sequences in the tree, several result sets from
one `CALL` (the first is shown), named parameters, and keyset paging for
large tables (shared with ADR-0013).
