# ADR-0085: The level a read is answered at

**Status:** Accepted · **Date:** 2026-09-18
**Tasks:** T2.52 · **Requirements:** FR-1.4, FR-12.3 · **Packages:** `internal/source/drivers/cassandra`

## Context

Cassandra holds no single copy of a row. A keyspace says how many replicas of
it exist, and every read and every write says how many of those replicas must
answer before the statement is: one of them, a majority, all of them, a
majority within the local data centre. That number is the caller's, per
statement, and it is the whole of the trade the database offers — an answer
sooner, from fewer nodes, which may be behind; or an answer that waited for
enough nodes that it cannot be.

Every other server this application connects to decides such a thing for
itself. Cassandra hands it to the person, so the application has to ask.

## Decisions

1. **The level is chosen on the connection.** A select on the connection form,
   beside the data centre — the same data-driven field the Redis mode uses, so
   nothing in the form knows what a consistency level is.

   Per statement was the alternative, and CQL has no way to say it: the
   `CONSISTENCY` a person may know is a command of the cqlsh shell, not a
   statement the cluster parses. Asking per statement would mean inventing a
   syntax the language has not got, in an editor whose job is to send CQL as
   typed. A level is also not really a property of one query; it is how
   somebody means to work with this data for as long as they are looking at
   it, which is what a connection is.

2. **ANY is not offered.** It is a write's level alone — a write that reached
   any node at all, even as a hint nobody has replayed yet — and a read at ANY
   is refused by the cluster. A list that included it would be offering a
   setting that stops reading working, which is worse than a list that is
   shorter than the enum behind it.

3. **It is set on the cluster, and reaches everything.** gocql gives a query
   the session's level, so one setting covers a browse, a console statement,
   and the driver's own reads of `system_schema` that the tree is drawn from.

   Pinning the schema reads to a cheap level was considered — the tree would
   then keep working on a cluster whose replicas are down, whatever a person
   chose. It is rejected: a level that silently applies to some of what the
   application sends and not the rest is a lie about a setting whose entire
   purpose is to say how much agreement is enough. Somebody who chooses ALL
   means it about all of it, and a tree that fails at ALL is telling them
   something true about their cluster.

4. **A level nobody offers is refused before a node is dialled.** Unknown text
   in the setting is a `ConnectConfig` failure naming what there is to choose
   from, rather than a silent fall back to the default: a person who typed
   EACH_QUORM meant something, and reading at QUORUM instead would answer a
   question they did not ask (FR-1.4).

5. **Nothing chosen is a majority.** QUORUM is what gocql itself would have
   used, so the default is not a weakening of the library's behind anybody's
   back. cqlsh's own default is ONE; a tool that shows rows a person will act
   on should not read more weakly than the driver underneath it does.

6. **Serial consistency is not offered here.** It is a different question —
   how a *conditional* write's paxos round agrees, SERIAL or LOCAL_SERIAL —
   asked only of a statement with an IF clause. Nothing in this driver writes
   yet, conditionally or otherwise. It is the row writer's to ask, and a
   selector offered before then would be a setting that changes nothing.

## Consequences

Every statement a Cassandra connection sends carries the level a person chose,
and a level their cluster cannot reach is the cluster's to refuse, in its own
words: asking a keyspace of one replica for three is answered by Cassandra
saying so, which is also how somebody discovers how their keyspace is
replicated.

Two levels at once means two connections, which is the same shape as asking
two questions of two servers, and is how a person would compare them.

The form gained a field and nothing else changed: no statement is rewritten,
no capability is claimed or dropped, and the levels offered are a list in this
driver rather than gocql's enum, which is what lets a level that reads be told
from one that does not.
