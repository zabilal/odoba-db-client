# ADR-0146: An engine that is not a server

**Status:** Accepted · **Date:** 2026-09-24
**Tasks:** T3.34 · **Requirements:** REQ-DB-1, FR-1.4, FR-2.1, FR-2.4, FR-2.5, FR-3.6, FR-3.7, FR-3.8, FR-4.4, FR-4.5, FR-4.7, FR-5.4, NFR-S4, NFR-S6, NFR-P11
**Packages:** `internal/source/drivers/duckdb`

## Context

DuckDB is a library, not a server. There is no protocol to speak to it: a
connection is a file, opened in this process, and every query runs in it.

That is the first thing about it, and it decides the second. Every other
engine this application reaches is reached in Go alone — pgx, the MySQL
driver, `modernc.org/sqlite`, clickhouse-go, go-ora in its thin mode, gocql,
franz-go. DuckDB has no Go implementation, and the only way to it is cgo over
a copy of the engine built for each platform.

## Decisions

1. **The driver is behind a build tag, and the default build does not have
   it.** `marcboeker/go-duckdb` needs cgo and pulls in a prebuilt copy of the
   engine for each platform it might be built for — about a hundred megabytes
   of object code, and a C toolchain to link it with. That is a price the rest
   of the application should not pay for a driver most people will not use, and
   it would take away the one thing every other driver here has kept: a build
   that is Go and nothing else.

   So `go build ./...` compiles a package of one file and links no C;
   `go build -tags duckdb` builds the driver. The registration in `cmd/ikigai`
   is behind the same tag.

2. **A connection is a file, and opening never makes one.** DuckDB creates a
   database it cannot find, so a mistyped path would open an empty one that
   looks exactly like data gone missing. The file has to be there already,
   which is the rule the SQLite driver already keeps.

3. **One connection, several databases.** A file somebody has `ATTACH`ed is
   read through the same connection by naming it, so the explorer's top level
   is the databases this connection holds rather than the one file it was
   opened on, and a name is three parts long. What is left out is `system` and
   `temp`: one holds the catalogue being read to draw the tree and the other
   holds what a session made for itself.

4. **`duckdb_schemas().internal` is not read**, because it does not mean what
   its name suggests: `main` — the schema every database starts with and where
   most people's tables are — is flagged internal. A tree that believed it
   would hide the only schema most files have. What is hidden is decided a
   level up, by which databases are listed.

5. **Every row has an address.** `rowid` says where a row is, is hidden from
   `SELECT *`, and can be filtered and written by. So a table with a key is
   addressed by its key and a table without one is addressed by where its rows
   are — which is Oracle's bargain (ADR-0144) rather than CockroachDB's
   (ADR-0145): this is a position, not a key, and it is good while the grid
   holds the row it read.

6. **A document is read as its own text, and only a document needs that.**
   The library decodes a JSON value into a Go map before this driver sees it,
   which sorts the keys and rounds the numbers: `{"z":1.0,"a":2}` arrives as
   `{"a":2,"z":1}`, and it cannot be scanned as a string instead. So a browse
   casts those columns to `VARCHAR`, which is the one place the document
   survives (FR-3.8).

   Everything else the library hands over can be put back exactly: an exact
   number arrives as an integer and a scale, a UUID as its sixteen bytes, a
   `HUGEINT` as a big integer, an interval as its three parts. Those are
   converted rather than cast, so the statement stays the statement somebody
   would have written.

   A result in the query tab is not cast, there being no table to ask about
   its columns, so a document read that way is the map the library made of it.
   That is a limit of the library and it is not hidden: the grid shows what it
   has.

7. **A cast column keeps the type it was declared with.** A document read as
   text arrives as `VARCHAR`, and a grid told that would show a document as a
   string and offer to edit it as one. What the wire says is right for a
   statement somebody typed, where there is no table to ask.

8. **Read-only is two defences, not three.** The classifier refuses what it
   cannot vouch for, and the file itself is opened read-only. That second one
   is stronger than what any server engine here offers: `access_mode` is
   settled when the database is opened and DuckDB will not have it changed
   afterwards, so there is no session default for a routine to turn off
   underneath it and no need for a transaction wrapped round every statement.

9. **A file is held one way at a time, and saying so is the driver's job.**
   Within one process every connection to a file shares its configuration, so
   a connection open read-write cannot be joined by one open read-only. The
   refusal names the reason and what to do about it, because the message the
   engine gives — "Can't open a connection to same database file with a
   different configuration than existing connections" — does not.

10. **A statement is stopped by the session running it.** There is no second
    connection to send a cancel down, because there is no server: a DuckDB
    database is this process. So each session keeps the means of stopping its
    own statement and is found by the name whatever wants it stopped knows it
    by.

11. **A badge means zero when it says zero.** The catalogue answers nothing at
    all for a table it has not measured, rather than answering zero, so unlike
    the engines that cannot tell the two apart (ADR-0145) an empty table here
    is badged 0 and means it.

12. **The catalogue is DuckDB's own.** `duckdb_tables()`, `duckdb_columns()`,
    `duckdb_constraints()` and the rest, rather than the `pg_catalog` it also
    keeps — that is a compatibility layer over the same descriptors, and the
    native one answers more and answers it plainly. Every name reaches it as a
    bound parameter, which the three-part names of the other engines in this
    phase could not manage.

13. **A NOT NULL is a column's business.** DuckDB keeps one as a constraint of
    its own, beside the checks. It is left where it is: the column already says
    it cannot hold nothing, and repeating it as a rule over the row would be
    saying the same thing twice in the place people read rules.

## Consequences

- The read path is whole: the explorer over every attached file, structure,
  browsing, filtering, paging, the picklist, the query tab and scripts. The
  grid edits every table, because every table has an address for its rows.
- The default build is unchanged: no cgo database dependency, and cross-
  compilation as it was. `go.mod` gains the bindings, which `go mod tidy` keeps
  because it reads every file whatever its tags; nothing links them unless the
  tag is set.
- Not claimed, for want of something behind them: bulk loading, `EXPLAIN`
  plans, editable query results, explicit transactions in the query tab, column
  statistics and schema diff. `EXPLAIN` here answers a drawn box of box-drawing
  characters, which is a picture rather than a plan.
- A foreign key reports no action on delete or update, because DuckDB has none
  to report: it refuses a change that would break a key rather than following
  it.
