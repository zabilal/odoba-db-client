# ADR-0080: The Cassandra connection

**Status:** Accepted · **Date:** 2026-09-18
**Tasks:** T2.47 · **Requirements:** FR-1.2, FR-1.3, FR-1.4, FR-12.3, NFR-S2 · **Packages:** `internal/source/drivers/cassandra`

## Context

Cassandra is the fourth engine family this application speaks to, and the
first wide-column one. A cluster holds keyspaces, keyspaces hold tables,
tables hold rows of declared columns, and CQL reads like SQL. Underneath it
is unlike PostgreSQL in every way that matters to a query planner: rows live
in partitions spread over a ring, there are no joins, no foreign keys and no
transactions, and a page of rows is a walk the server hands back a token for.

A connection is also plural in a way the others are not. A node is dialled,
and what comes back is a session to every node that one named.

## Decisions

1. **Cassandra is of the relational paradigm.** Keyspaces, tables, columns
   and a SQL-family language are what the explorer, the grid and the editor
   are built on, and a paradigm of its own would buy nothing they need. What
   Cassandra cannot do shows in the capabilities the driver claims rather
   than in a paradigm: no joins to diagram, no transaction to promise, and
   paging that is a token rather than an offset (T2.51).

2. **A form of fields, not a URI.** Any node and its port, an optional
   keyspace, a role and its password, further nodes, and the data centre
   whose nodes are asked first. `cassandra://` is claimed as a scheme so a
   pasted address fills the form (FR-1.3), but nothing is ever written back
   into one: the credentials are settings gocql takes on their own, so no
   error can quote a URL with a password in it (NFR-S2).

3. **A local data centre means token awareness over it.** Given one, the
   pool asks that data centre's nodes first, and within it the node that
   holds the partition. Left empty, gocql's round robin stands. Nothing here
   guesses a data centre's name: guessing wrong sends every query across a
   region link.

4. **Encryption is the settings' to say.** Cassandra speaks its own protocol
   in the clear unless told otherwise, so an unset mode is a connection in
   the clear and nothing downgrades silently (ADR-0008). gocql reads
   `SslOptions.EnableHostVerification` rather than the `tls.Config` it is
   given, so the mode is said twice: once to `tlsconf`, once to gocql.

5. **A connection given up is raced, not waited on.** `CreateSession` takes
   no context — it dials, authenticates and reads the cluster's own view of
   itself on its own clock. It runs in a goroutine, the caller's context
   races it, and a session that arrives after the caller has given up is
   closed rather than leaked.

6. **A keyspace that is not there is asked about, not guessed at.** gocql
   folds every node's refusal into one sentence — "no connections were made
   when creating the session" — which hides the commonest mistake of all.
   Where a keyspace was named, the cluster is dialled again without it and
   asked whether it has that keyspace; only then is the failure called
   `ConnectNoDatabase`. It costs one extra dial, and only on a failure, and
   it is the difference between "fix your network" and "fix your keyspace"
   (FR-1.4).

7. **Nothing is claimed that is not written.** The driver claims the
   relational paradigm and the two object kinds a tree will hold, and
   nothing else: no query language until CQL has a dialect (T2.49), no rows
   until paging is written (T2.51), and an empty tree until T2.48. `Browse`
   refuses rather than answering with nothing. A claim is a promise the
   conformance suite holds a driver to (REQ-DRV-1), so each waits for the
   thing it promises — and the driver stays out of the application's own
   list of drivers until its tree is worth opening.

## Consequences

A Cassandra connection can be made and tested, and says what is wrong when it
cannot. What it cannot yet do, it says it cannot.

The test cluster is a container of its own, `ikigai-cassandra` on 59042, from
`public.ecr.aws/docker/library/cassandra:5`: Docker Hub refuses an
unauthenticated pull on this machine. It authenticates every connection with
`AllowAllAuthenticator`, which is why the live tests prove what a cluster can
show — a port with nothing on it, a name that resolves to nothing, a keyspace
that is not there — and the credentials' own refusals are proven against the
words a cluster answers with instead.
