# ADR-0087: Proving who you are to a broker

**Status:** Accepted · **Date:** 2026-09-18
**Tasks:** T2.55 · **Requirements:** FR-1.5, FR-1.11, NFR-S3 · **Packages:** `internal/source/drivers/kafka`

## Context

Kafka does not carry credentials in an address. A connection is made first and
authentication is negotiated over it: the client names a SASL mechanism, the
broker agrees or does not, and a challenge and response follow. FR-1.11 asks
for five mechanisms across Kafka and Cassandra; this ADR covers the three a
broker most often asks for — PLAIN, SCRAM-SHA-256 and SCRAM-SHA-512 — with
OAUTHBEARER and GSSAPI in T2.56 and AWS MSK IAM in T2.57.

## Decisions

1. **The mechanism is chosen, and the list holds only what is spoken.** A
   select on the connection form, defaulting to None, because most brokers
   somebody runs for themselves ask for nothing. OAUTHBEARER and GSSAPI are
   real mechanisms and are deliberately absent until the driver speaks them: a
   setting that nothing implements is a promise broken at the broker, which is
   the same rule that keeps ANY out of the Cassandra consistency list
   (ADR-0085).

2. **Credentials with no mechanism are refused.** A user name or password
   typed while None is chosen would be sent nowhere, and the connection would
   be made as nobody — which looks like success. Somebody who typed a password
   is entitled to think it was used, so this is said at connect time instead.

3. **A mechanism with nobody to authenticate is refused before dialling**,
   naming what is missing. The broker would answer too, less clearly and a
   round trip later.

4. **A mechanism nobody here speaks is refused, naming the choices** — never
   quietly downgraded to None, which would connect unauthenticated to a
   cluster the person believed they had authenticated to.

5. **A keychain that will not answer is a fault in the settings, not a refusal
   by the broker.** It is classified `ConnectConfig`, not `ConnectAuth`,
   because the two need different fixes: one is a locked or emptied keychain,
   the other a wrong password (FR-1.5).

6. **PLAIN's clear text is said, not forbidden.** PLAIN sends the password as
   text, so it wants TLS; the field's help says so. It is not refused, because
   a broker on a person's own machine is a legitimate thing to connect to, and
   a refusal here would only push people towards worse ways round it. TLS
   stays available with verification on by default (NFR-S3), which is where
   that protection belongs.

## Consequences

A Kafka connection can authenticate the three ways brokers usually ask for,
and every way of getting it wrong is answered before a broker has to: nothing
chosen but credentials given, a mechanism with no user, a mechanism nobody
speaks, a keychain that will not open. What the broker alone can judge — a
password it will not take — comes back as an authentication failure, which is
the one thing the person must fix by knowing something this application does
not.

The mechanisms still to come slot into the same list, and the same refusals
hold them: each appears when the driver can speak it, and not before.
