# ADR-0072: The Redis connection

**Status:** Accepted · **Date:** 2026-09-12
**Tasks:** T2.39 · **Requirements:** FR-1.2, FR-1.3, FR-1.4, FR-12.2, NFR-S2, NFR-S3 · **Packages:** `internal/source`, `internal/source/drivers/redis`, `internal/app/connstr`

## Context

FR-12.2 asks for Redis: a key browser, editors for each type it holds, TTLs,
a command console and its memory figures. Before any of that there has to be a
connection, and a Redis connection is three connections wearing one form — a
single server, a set watched by sentinels, and a cluster of shards. They
differ in how an address is found, not in what is said once it is.

Redis is also the first source of the key-value paradigm (REQ-DB-3): a flat
keyspace of typed values, with no schema, no rows and no query language. It
has to reach the same explorer and the same tabs as PostgreSQL does, through
the same contract (REQ-DB-4).

## Decisions

1. **`github.com/redis/go-redis/v9`.** It is the maintained Go client, pure Go
   (NFR-T1), and it carries all three topologies, RESP3, ACL users and TLS in
   one library. The alternative, `rueidis`, is faster at the pipelining this
   application does not do.

2. **The mode is a setting, not three drivers.** One descriptor, one form, and
   a `mode` field of standalone, sentinel or cluster. The client is built by
   name for the mode rather than through go-redis's universal client, so that
   a setting the mode has not got is refused rather than quietly dropped: a
   cluster has one keyspace and no numbered databases, a sentinel set is known
   by the name its sentinels use, and a single server has one address.

3. **A database is a number.** Redis names its databases `db0`…`db15`, so the
   field every other driver fills with a name holds a number here, and the
   tree's nodes are `KindDatabase` all the same. The count comes from the
   server's own configuration; where the server refuses `CONFIG` — managed
   Redis usually does — the databases that hold keys are listed instead, with
   the one this connection is on among them whether it holds anything or not.

4. **The credentials are settings, never part of an address.** go-redis takes
   the user and password as options, so no error can quote an address with a
   password in it (NFR-S2). The sentinels have a password of their own, read
   from the keychain under its own name.

5. **`rediss://` means encryption, and the scheme says so rather than a
   parameter.** `Descriptor.TLSSchemes` names the schemes that mean an
   encrypted connection, and `connstr` reads a pasted address's scheme
   through it — so the parser still knows nothing about Redis, and the next
   driver with a `+tls` scheme needs no code there either. A scheme that means
   encryption is read as the verified kind (NFR-S3), and a query parameter
   after it can still turn it down, which is the explicit choice ADR-0008 asks
   for.

6. **An unset TLS mode is a connection in the clear.** `tlsconf` reads an
   empty mode as verify-full, because a driver that speaks TLS by default must
   verify it. Redis speaks its own protocol in the clear unless the address
   says otherwise, so here the driver turns the configuration off rather than
   dialling TLS at a server that is not expecting it. Nothing is downgraded
   silently: the choice is in the scheme or in the form.

7. **A failure says what to fix, by the server's own code.** `WRONGPASS`,
   `NOAUTH`, `NOPERM` and the answer a server gives when it wants no password
   at all are read as credentials; a certificate that will not verify, a name
   that does not resolve, a refused port and a server that did not answer are
   each said to be what they are. Two are Redis's own: a single server asked
   to be a cluster says cluster support is disabled, and a master name no
   sentinel knows is a settings error rather than an unreachable server.

8. **What is not written yet says so.** `Children`, `Describe` and `Browse`
   return an error naming the task — the key browser is T2.40 — rather than
   answering with nothing, which would read as an empty server.

9. **Nothing is claimed that is not written.** The capabilities say the
   key-value paradigm, databases and keys, and multiple databases only where
   there are any. No query language, no writes, no indexes: the conformance
   suite checks a claim against the server, and T2.46 is where they are made
   good.

## Consequences

- The module gains go-redis and its hashing dependency. It is the second
  source dependency that is not a SQL driver.
- A cluster's capabilities differ from a single server's on the same driver,
  which is what `Capabilities` being a method on the live connection is for.
- `Descriptor.TLSSchemes` is new, and empty for every existing driver: a
  PostgreSQL URL says its TLS in `sslmode`, and a MongoDB seed list in the
  driver's own URI.
- The integration tests want a Redis on port 56379 (`ikigai-redis`), as the
  other drivers want theirs, and CI now names the Mongo and Redis services it
  had been starting without pointing the tests at them.
