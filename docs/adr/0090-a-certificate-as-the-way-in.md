# ADR-0090: A certificate as the way in

**Status:** Accepted · **Date:** 2026-09-18
**Tasks:** T2.58 · **Requirements:** FR-1.10, NFR-S3 · **Packages:** `internal/source/tlsconf`, `internal/source/drivers/kafka`

## Context

FR-1.10 asks for TLS with a CA, a client certificate and key, and four modes
that mean different things. The shared `tlsconf` has built all of that since
before this application spoke to Kafka at all: it loads a CA, loads a client
certificate with `tls.LoadX509KeyPair`, and makes `require` skip verification,
`verify-ca` check the chain without the host name, and `verify-full` check
both. The Kafka driver hands whatever that builds straight to its client
(ADR-0086).

So mutual TLS was already written. What had never happened was proving it
against a server that actually demands a client certificate — and until that
happens, "it is written" and "it works" are different claims.

## Decisions

1. **No new driver code.** Where a task turns out to be already done, the
   honest work is proving it rather than writing something so that the task
   has a diff. What this adds is a broker that asks for a certificate, and
   tests that connect to it.

2. **A certificate is transport, not a credential.** A connection with a
   client certificate and no SASL mechanism chosen is legal, and must stay
   legal: the rule that refuses credentials given with no mechanism (ADR-0087)
   is about passwords and tokens, which a mechanism would carry. A certificate
   is presented by the transport before any mechanism exists, and Kafka can
   authenticate on it alone. A test says so, so that nobody tidies the two
   rules into one.

3. **The tests prove the shared helper, not one driver's wiring.** Every
   driver here builds TLS the same way, so what is checked is what they all
   depend on: a certificate is enough to be let in, a listener that requires
   one refuses a connection without it, `verify-full` refuses a certificate
   issued for another name, `require` encrypts without verifying, and
   `disable` does not quietly speak TLS to a listener that only speaks TLS.

4. **The certificates live outside the repository**, in a fixed directory
   rather than a session's own: a private key does not belong in a repository
   even briefly, and the broker mounts the same files the tests read.

## Consequences

FR-1.10 is proven end to end for the first time, and the proof travels: the
helper under test is the one PostgreSQL, MySQL, MongoDB, Redis, Cassandra and
Kafka all use, so a mutation that breaks client certificates or weakens a mode
now fails a test instead of shipping.

The rig cost two lessons worth keeping. The broker image will not start a
listener whose name contains SSL unless it is given keystores in the shape it
expects — a keystore file and a truststore file under its own secrets
directory, with their passwords in files beside them — and it fails in its
setup script, before Kafka runs, with no Java error to read. PEM material
configured per listener is ignored at that stage, however correct it is for
Kafka itself.

The second cost longer. A truststore has to carry a trust anchor, not merely a
certificate: a PKCS12 file exported by openssl holds the CA but marks nothing
as trusted, so the broker builds its PKIX parameters with an empty set of
anchors and can verify no client at all. keytool writes the entry Java looks
for. What made it slow to see is where the failure appears: under TLS 1.3 the
server rejects the client's certificate after the handshake already looks
finished to the client, so the connection dies at the first request and the
client library reports it as the broker probably wanting TLS — advice pointing
exactly away from the fault, which was the server's trust in the client. The
server's own log said it plainly, and reading it sooner would have been
quicker than reasoning about the client.
