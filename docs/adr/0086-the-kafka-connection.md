# ADR-0086: The Kafka connection

**Status:** Accepted · **Date:** 2026-09-18
**Tasks:** T2.54 · **Requirements:** FR-1.2, FR-1.4, FR-13.1, REQ-DRV-1 · **Packages:** `internal/source/drivers/kafka`

## Context

Kafka is the first source here that holds no rows. A topic is an append-only
log cut into partitions: nothing declares what a record looks like, a record
is addressed by the offset it was written at rather than by a key of its own,
and reading is consuming — the same record read again by somebody else, and
gone when the log ages out rather than when anyone deletes it. The model
already has a name for that shape, `ParadigmStream`, and everything this
application comes to show of Kafka follows from it.

This ADR is the connection alone: what a person fills in, what is dialled, and
what is said when it fails.

## Decisions

1. **`twmb/franz-go`**, as TASKS.md settled. It is pure Go with no cgo, which
   the toolkit decision requires, it speaks the protocol directly rather than
   wrapping librdkafka, and its `kadm` and `kversion` packages answer the
   questions the cluster overview will ask (FR-13.1).

2. **Bootstrap servers are a way in, not the cluster.** The form asks for one
   broker and allows more, because a seed is only asked who the brokers are;
   every later request goes to the broker that holds what is being asked
   about. More than one may be named so that a broker being down is not a
   cluster being unreachable, and each address is given a port if it was
   written without one — the first field's port, since a cluster is usually
   configured alike throughout.

3. **Opening dials, because the library will not.** `kgo.NewClient` only reads
   its options: connections happen when a request needs one. A driver that
   returned that client as a live connection would report success for a
   cluster that is not there, and fail later somewhere the person cannot
   connect to a cause. So `Open` pings — a broker-only metadata request, the
   cheapest question there is, tried against each seed until one answers —
   and a failure is classified into what to fix (FR-1.4).

4. **Nothing is claimed that is not written.** The capabilities say the
   paradigm and nothing else: no object kinds, because the tree is empty until
   T2.59 onward; no stream operations, because consuming, producing, groups
   and topic administration each wait for the task that writes them. A claim
   is a promise the conformance suite holds a driver to (REQ-DRV-1), and the
   browse says plainly that it reads no records yet rather than half-doing it:
   consuming means assigning partitions and choosing where in each log to
   start, without joining a consumer group or committing anybody's offsets
   (FR-13.19), and none of that exists yet.

   `Query` stays zeroed for good, not for now. Kafka has no query language,
   which is the case `capability.Query` was written to leave empty.

5. **The version is a guess, and says so.** A broker will not say what release
   it is; it says which versions of each API it speaks, and a release is read
   back out of that. franz-go returns "v4.1" where that is exact and "at least
   v4.0" where it is not, and this driver passes on whichever it gets rather
   than dressing the second up as the first.

6. **The driver stays out of `cmd/ikigai` until it has a tree**, as the
   Cassandra driver did between ADR-0080 and ADR-0081. It registers itself, so
   the connection form offers it and a connection can be tested; adding it to
   the application's own list would put a source in the explorer that opens
   onto nothing.

## Consequences

A Kafka connection can be made and tested, and says what is wrong when it
cannot be: a port nothing listens on, a host that does not resolve, a
certificate that does not verify, credentials the cluster would not take —
that last one ahead of the SASL mechanisms themselves (T2.55), because the
classification is about what a person must fix rather than about how they
prove who they are.

What the connection is for comes next, and in the order the tasks give: the
cluster overview, then topics and partitions in the tree, then records in the
grid. Each will add its claim as it lands.
