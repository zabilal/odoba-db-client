# ADR-0145: One connection, a whole cluster

**Status:** Accepted · **Date:** 2026-09-24
**Tasks:** T3.33 · **Requirements:** REQ-DB-1, FR-1.4, FR-2.1, FR-2.4, FR-2.5, FR-3.6, FR-3.7, FR-4.4, FR-4.5, FR-4.7, FR-5.4, NFR-S3, NFR-S4, NFR-S6, NFR-P11
**Packages:** `internal/source/drivers/cockroach`

## Context

CockroachDB speaks PostgreSQL's wire protocol, so pgx connects to it and the
statements this application writes run. That makes it tempting to treat it as
PostgreSQL with a different banner, which is what the PostgreSQL driver's
`Info` already does — it reads the word "CockroachDB" out of `version()` and
renames the product.

It is not PostgreSQL. Three of the things this application's contracts are
built around are answered differently here, and each of them changes the
driver's shape rather than one of its statements.

## Decisions

1. **One connection reads the whole cluster, so there is one pool.** A
   PostgreSQL connection is to one database, which is why that driver keeps a
   pool per database and bounds how many it will open. Here a fully qualified
   name reaches any database from any connection — `"shop".pg_catalog.pg_class`
   answers from `defaultdb`, catalogue and all. So there is one pool, and the
   database is part of an object's name rather than part of the connection:
   `QualifyRef` writes all three parts, where the PostgreSQL driver deliberately
   writes two because a three-part name there is a cross-database reference and
   is refused.

   A database name is the one thing this driver writes into statement text, and
   it goes through `QuoteIdentifier` like every other identifier (NFR-S6).
   Everything a person typed is still bound.

2. **Every row has a key, because the engine makes one.** A table declared with
   no `PRIMARY KEY` is given a hidden `rowid INT8 NOT NULL DEFAULT
   unique_rowid()`, and an index over it named `t_pkey` that the catalogue
   reports as primary. So there is no table here whose rows cannot be told
   apart, and no need for the Choose a Key… dialog that ClickHouse needed
   (ADR-0142) or the row address Oracle fell back on (ADR-0144). The identity
   is always `IdentityPrimaryKey`.

   This is the best answer of the three engines in this phase. Oracle's `ROWID`
   says where a row is and stops being true when the row moves; a MergeTree has
   nothing that stays still at all. A `rowid` here is a key: unique, stable, and
   the thing the engine itself finds the row by.

3. **The hidden key is read to address a row and is no part of the table's
   structure.** `SELECT *` does not return it and nobody declared it, so the
   structure tab and the explorer's column list leave it out —
   `pg_attribute.attishidden` says which columns those are, and
   `information_schema.columns` does not filter them, which is why the
   catalogue is read through `pg_catalog` here.

   A browse selects it anyway, last, after the columns the table's owner wrote.
   Oracle puts its `ROWID` first because an address is not one of a row's
   columns; this is a column, one the engine added to the end, and that is
   where it reads. Without it in the projection two rows identical in every
   declared column would be two rows a grid could not tell apart, and the
   contract would have to refuse to edit either.

4. **`crdb_internal` is not read at all.** As of v26.3 the server refuses it:
   *"Access to crdb_internal and system is restricted. These interfaces are
   unsupported in production. To proceed, set the session variable
   allow_unsafe_internals = true (not recommended)."* A client that set that
   variable would be asking somebody's cluster to lower a guard it raised on
   purpose, in order to read tables its own makers call unsupported. What is
   left — `pg_catalog`, `information_schema` and the `SHOW` statements —
   answers everything this driver asks. The `system` database is hidden from
   the tree for the same reason.

5. **What was declared is read from the catalogue; what renders a definition is
   asked for by name.** The catalogue tables answer across databases. The
   functions that render a definition from an OID do not: `pg_get_viewdef`,
   `pg_get_indexdef` and `pg_get_constraintdef` look their argument up in the
   database the connection is attached to and return *nothing at all* for an
   OID from another one — not an error, an empty string. A check constraint
   would quietly lose its expression, with nothing to say it ever had one.

   Rather than learn which functions happen to work, definitions are asked for
   by name, which always works: `SHOW CREATE` for a view, `SHOW INDEXES` and
   `SHOW CONSTRAINTS` for a table's. A name says which database it is in, and
   an OID does not.

6. **An index is shown as it was declared, without the key the engine appends
   to it.** Every secondary index here carries the primary key's columns at its
   end so that it can find the row, and `SHOW INDEXES` marks them `implicit`.
   An index drawn with them would be an index nobody wrote. Its `storing`
   columns are its payload, which `model.Index` calls `Include`.

7. **A badge is an estimate the engine gathered, and nothing when it has
   gathered none.** `pg_class.reltuples` is null here; the figure lives in the
   engine's own statistics, and `SHOW TABLES` carries it beside each name, so a
   folder's listing costs one round trip as it does everywhere else.

   Zero means no badge. The engine says zero both for a table it has never
   measured and for one that is empty, and there is no telling the two apart
   from the listing. An empty table losing its badge costs an absence; a table
   of a million rows badged "0" would be an untruth in the tree, which FR-2.5
   will not have.

8. **A transaction the server asks to be run again is run again.** CockroachDB
   is serialisable, and it keeps that promise by telling a transaction that
   lost a race to start over (`40001`) rather than by making it wait. That is
   the engine working as designed, not a fault. A changeset is therefore
   retried a few times before the person who made it is told.

   Retrying is safe here in a way it usually is not: the server's promise with
   that error is that the transaction left nothing behind, so a second attempt
   cannot write anything twice. Passing it on instead would be handing somebody
   the engine's internal working as though it were their problem, for a race
   they had no part in. It is bounded rather than open-ended, because a
   changeset that will not get through should say so rather than keep a window
   waiting; when the attempts run out, the message says what the collision
   means.

   A statement in the query tab is not retried. That one somebody typed and is
   watching, and running it twice without saying so is not this driver's
   decision to make.

9. **A statement is stopped by the session running it.** There is no
   `pg_cancel_backend` here — `pg_backend_pid()` exists and answers, and
   nothing takes it. What stops a statement is `CANCEL QUERY`, and what finds
   the statement to stop is `SHOW CLUSTER QUERIES`. So a session's handle is
   its `session_id`, asked for once as the session opens, and stopping it is
   one statement over a result:

   ```sql
   CANCEL QUERIES SELECT query_id FROM [SHOW CLUSTER QUERIES] WHERE session_id = $1
   ```

   The name is fixed for the session's life, so it is readable while a
   statement is running — which is the only moment anything wants it, and a
   defect that had to be fixed in two drivers before this one.

10. **Read-only mode is three defences, as it is on PostgreSQL.** The
    classifier refuses what it cannot vouch for; every statement on a read-only
    connection runs inside an explicit `READ ONLY` transaction; and
    `default_transaction_read_only` is set as the connection opens, which this
    engine honours and enforces with `25006`. The classifier learned this
    engine's own verbs: `UPSERT` writes, `USE` changes what every later name
    means, `SET CLUSTER SETTING` reaches the whole cluster, and `CANCEL`,
    `PAUSE`, `RESUME`, `BACKUP` and `EXPORT` are administration.

11. **An address is shown with only the mask it has.** pgx hands an `inet`
    over as a `netip.Prefix`, and printing one adds `/32` to an address that
    has no mask — a value the column does not hold and the server never shows
    (FR-3.8). An address whose mask covers all of it is written as the address.

12. **An exact number needs no help.** Unlike ClickHouse and Oracle, this
    engine pads a decimal back to its column's scale itself, so a column of
    money holding 1.50 arrives as `1.50`. Reading it from its text rather than
    from pgx's decoded form is still what keeps it exact.

## Consequences

- The read path is whole: the explorer across every database in the cluster,
  structure, browsing, filtering, paging, the picklist, the query tab and
  scripts. The grid edits every table, because every table has a key.
- Not claimed, for want of something behind them: bulk loading, `EXPLAIN`
  plans, editable query results, explicit transactions in the query tab, column
  statistics and schema diff. `EXPLAIN` here answers a text tree and has no
  `FORMAT JSON`, so the plan model would be parsed out of drawing characters
  rather than read; that is a change of its own, not a line in this one.
- The PostgreSQL driver still recognises CockroachDB in `version()` and renames
  the product. Somebody who connects to a cluster with that driver gets
  something that mostly works and quietly does not: no hidden key, so no
  editing a table without one, and `pg_get_viewdef` answering nothing. Pointing
  them at this driver instead is a change to the connection form, not to either
  driver.
