# ADR-0081: The Cassandra tree

**Status:** Accepted · **Date:** 2026-09-18
**Tasks:** T2.48 · **Requirements:** FR-2.1, FR-2.2, FR-2.5, FR-12.3, REQ-DB-4 · **Packages:** `internal/source/drivers/cassandra`, `internal/ui/shell`

## Context

A Cassandra cluster holds keyspaces; a keyspace holds tables, the
materialized views built over them, the indexes on them and the types they
are written in. All of it is in `system_schema`, which is the cluster's own
account of itself and needs no permission beyond reading it.

Two things about that account shape the code. Within a keyspace, the rows are
clustered by the object's own name, so the cluster hands tables, views, types
and indexes back in name order already. The keyspaces themselves are not: they
are partitioned by name, so a listing arrives in the order of the tokens of
those names, which is no order to read a list in.

## Decisions

1. **A keyspace is this paradigm's database.** It is a `KindDatabase` node,
   its children are the object classes the model already knows (Tables,
   Materialized Views, Indexes, Types), and a table's own children are its
   columns — the shape PostgreSQL presents, so the explorer, the tabs and
   session restore need to know nothing about Cassandra (REQ-DB-4).

2. **The cluster's own keyspaces are hidden.** `system`, `system_schema` and
   the rest are its account of itself rather than anybody's data, as another
   engine's system schemas are, and PostgreSQL hides those already. A
   connection may still be opened on one by name. The filtering and the
   sorting are one pure function, `userKeyspaces`, so that both are proven
   without a live cluster's ordering having to disagree with the alphabet.

3. **Only the keyspaces are sorted.** What a keyspace holds arrives in name
   order, and sorting it again would be code that cannot be wrong — and so
   cannot be tested. What the cluster gives is kept, and said to be why.

4. **A keyspace's structure is how it is replicated** (FR-12.3): the strategy,
   the factors it was made with, and whether writes are durable. That is a
   `*model.Schema`, whose `Attrs` the model already documents as the place a
   Cassandra replication strategy goes, and the structure tab gained a case
   for it: the replication by name, then a count of what the keyspace holds.
   The strategy is stored as a Java class name, and what a person reads is the
   end of it — `SimpleStrategy`, not its package.

5. **A table's structure is what addresses its rows.** The columns come in the
   order they are addressed in: the partition key, then what orders rows
   within a partition, then the static columns a partition shares, then the
   rest. The primary key is the partition key and the clustering columns, in
   that order, and it has no name because Cassandra does not give it one. A
   key column is part of a row's address, so it can never be empty; everything
   else can.

6. **Nothing is badged.** Cassandra keeps no count of a table's rows, and
   counting them is a read of every partition on every node — which is
   exactly what a badge must never be (FR-2.5). `Badge` reports that it has
   nothing rather than paying for a number.

7. **A table still offers no rows.** The tree says what is there and what it
   is made of; reading a page of it is a walk of the partitions a paging state
   names, which is T2.51. Until then no node claims to be browsable, and the
   driver would rather show nothing than offer what it cannot do.

## Consequences

The driver now joins the application's own list of drivers, which is the
condition ADR-0080 set for it: a tree worth opening.

CQL's types are read as the model's own, with the cluster's own words kept as
`Native`: a person who wrote `frozen<address>` reads `frozen<address>`.
Frozen says how a value is held rather than what it is, so it is unwrapped
before the type is classified, and a type nobody here knows is a keyspace's
own — which in CQL can only be a structure of fields.

Materialized views are experimental in Cassandra 5 and disabled unless a
cluster is told otherwise; the test container was told. Where a cluster
refuses them the driver lists none, and the live test that needs one says why
it is skipping rather than failing.

gocql writes its own account of a failed connection to standard error, beside
the one this driver turns into a `ConnectError`. Quieting it means handing
gocql a logger, which is a connection-level decision and is left to the task
that gives Cassandra its session options rather than smuggled in here.
