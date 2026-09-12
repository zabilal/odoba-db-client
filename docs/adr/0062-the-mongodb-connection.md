# ADR-0062: The MongoDB connection

**Status:** Accepted · **Date:** 2026-09-12
**Tasks:** T2.30 · **Requirements:** FR-1.3, FR-1.4, FR-12.1 · **Packages:** `internal/source/drivers/mongo`, `internal/app/connstr`, `internal/redact`

## Context

MongoDB is the first source of the document paradigm and the first real test
of REQ-DB-4: a database with no rows, no columns and no server-held schema
has to reach the same explorer, grid and tabs as PostgreSQL, through the same
contract, with no engine knowledge in the UI.

This is the connection alone. Listing collections is T2.31, reading documents
T2.33, and the conformance suite T2.38.

## Decisions

1. **The official driver**, `go.mongodb.org/mongo-driver/v2`. It is the only
   maintained Go driver, it is pure Go, and it carries SRV resolution, SCRAM
   and the wire protocol this would otherwise have to write.

2. **The connection form is fields, not a URI.** Host, port, database, user,
   password, and three of Mongo's own: a DNS seed list, the database a user
   is defined in, and a replica set's name. A URI is what a person pastes,
   not what is stored (FR-1.2).

3. **The credentials are set apart from the URI the driver takes.** A driver
   error quotes the URI it failed with; a password in one would reach a log
   or a dialog through an error nobody redacted (NFR-S2). What is built is an
   address and its options, and `SetAuth` carries the rest.

4. **A seed list carries no port** — one DNS record names the servers and
   theirs — and turns encryption on by itself, so a connection that refuses
   encryption says `tls=false` outright rather than leaving it to a default
   (ADR-0008). Verified TLS is carried as a `tls.Config` from `tlsconf`,
   which is where every driver's TLS settings already come from.

5. **A failure says what to fix** (FR-1.4). The driver reports most failures
   as a server-selection error wrapping what it actually met, so the wrapped
   text is what is read: a rejected credential, a certificate that will not
   verify, a name that does not resolve, a refused port, a server that did
   not answer.

6. **`+srv` is a scheme suffix, not a Mongo special case.** A pasted URL
   whose scheme ends `+srv` sets the connection's `srv` setting, and the
   driver reads it. Nothing in `connstr` knows what MongoDB is.

7. **`authSource` and `authMechanism` are not secrets.** The redaction rule
   catches every key holding "auth", which would have sent a database's name
   to the keychain and lost it on the way in. They are named as what they
   are, and the rule keeps catching `auth_token`.

8. **A server that refuses to list its databases still shows the one the
   connection names.** An Atlas user given one database and no more can read
   it and not the list it is in; a tree that showed nothing there would be
   wrong about the server.

9. **What is not written yet says so.** `Children`, `Describe` and `Browse`
   return an error naming the task, rather than answering with nothing, which
   would read as an empty server.

## Consequences

- The module gains the driver and its dependencies (compression, SCRAM,
  PKCS#8). It is the first source dependency that is not a SQL driver.
- A connection string naming several hosts is still refused by `connstr`, as
  it is for every driver. A replica set reached that way is named by its
  seed list or its set name instead.
- `mongodb+srv` resolves DNS while the connection options are built, so a
  seed list that does not resolve fails at connect time, where FR-1.4 wants
  it, but the URI itself cannot be checked without the network.
- The integration tests want a MongoDB on port 57017 (`ikigai-mongo`), as the
  relational drivers want theirs.
