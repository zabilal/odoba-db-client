# ADR-0083: Running CQL

**Status:** Accepted · **Date:** 2026-09-18
**Tasks:** T2.50 · **Requirements:** FR-5.1, FR-5.2, FR-5.4, FR-5.6, FR-12.3, NFR-S4, REQ-DRV-1 · **Packages:** `internal/source/drivers/cassandra`

## Context

The editor is already built: it takes its colours from the lexer dialect the
source names (`Capabilities.Query.Language`), and its completion from the
connection's schema cache, which fills itself from the driver's own tree. A
driver that claims a language and can run a statement gets both, and Cassandra
had the language (ADR-0082) but nothing to run one with.

What was left was the driver's side: a session, statements, and what comes
back.

## Decisions

1. **The language is claimed now that something runs it.** `Query.Supported`
   with `cql`, which the lexer knows, and `MultiStatement`, because a script
   runs a statement at a time. The conformance suite holds a claimed language
   to having a `Queryer` behind it (REQ-DRV-1), which is why the claim waited
   for this task rather than arriving with the dialect.

2. **USE opens a session of the console's own.** gocql pins a keyspace at the
   session rather than taking it per statement, so moving keyspaces means
   another session — opened when USE asks for it, and not before. A console
   that moves leaves every other tab where it was, and never moves the
   connection's own session, which the tree is read through. The name is taken
   as it was written: CQL folds an unquoted name to lower case, and a quoted
   one keeps its case.

3. **Every statement of a script is classified and put to the guard before the
   first one runs** (NFR-S4), as MongoDB's console does and for the same
   reason: refusing the fourth after three have run leaves a person somewhere
   they did not choose. A script then stops where it failed, because what
   follows it was written to run after what did not.

4. **Cassandra counts nothing, and the result says so.** There is no affected
   count in CQL — a write reports nothing — so `Affected` is -1 rather than a
   fiction. Server warnings come back as messages, in the server's own words
   (FR-5.6).

5. **A value is narrowed to the set the model holds**, as every other driver
   narrows its engine's: a UUID and a CQL duration are rendered as Cassandra
   writes them, a decimal and a varint keep their digits exactly as
   `model.Decimal`, and a collection — a list, a set, a map, a tuple, a
   keyspace's own type — becomes the JSON the cell viewer reads down into.
   A CQL `time` is a reading of the clock rather than a length of anything, so
   it is written as one; a CQL `duration` is months, days and nanoseconds,
   because a month is not a number of days, and is written the way it would be
   typed.

6. **A row's destinations are fresh every row.** gocql scans into pointers it
   is handed; reusing them would make a row change as the next one was read.

7. **A column says where it came from** when the cluster names its keyspace
   and table, which is what a result needs before anything can edit one
   (FR-4.8) — though nothing does yet.

## Consequences

A Cassandra connection now opens a query tab that colours CQL, completes
against the keyspaces and tables the tree found, runs statements and scripts,
and refuses what the guard refuses.

Reading a table through the grid is still T2.51: that needs paging by the
state the cluster hands back, and until it exists no node offers rows. A
console can of course `SELECT` from a table, which is how a person reads one
today.
