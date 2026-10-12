# ADR-0175: The same driver, asked twice

**Status:** Accepted · **Date:** 2026-10-12
**Tasks:** T5.16 · **Requirements:** §4.3, FR-13.1, FR-13.15, REQ-DRV-3
**Packages:** `internal/source/drivers/kafka`, `.github/workflows`

## Context

The requirements say of Redpanda: "Kafka-compatible, same driver". That is a
claim about somebody else's product and about this driver, and neither had been
tested. T5.16's first source is finding out whether it is true.

## Decisions

1. **The Kafka driver's whole live suite runs against Redpanda as well.** Not a
   Redpanda suite: the same one, pointed at another port, with
   `IKIGAI_KAFKA_IS=redpanda` for the few tests that have to know. "The same
   driver" is a statement about everything the driver does, and the only way to
   hold it is to ask it everything twice. `gate.sh` and the nightly run both do.

   On its first run two tests of the suite failed, and both were Redpanda
   answering a question differently from Kafka. A third difference was not
   failing a test, because the test was not looking.

2. **A group that is not there is said to be missing, whoever is asked.** Kafka
   answers a group nobody has heard of with an error. Redpanda describes it as
   a dead group with no members and no error, which is the protocol's older
   answer. The driver took the absence of an error for a group and reported no
   progress — which reads as a group that is up to date. A group that is dead,
   has nobody in it and has committed nothing is now missing on either.

3. **Redpanda says what it is, and is given no Kafka version.** A broker does
   not say what version it is; a release is read back from which API versions
   it speaks, against the table of which Kafka release spoke which. Redpanda is
   in no row of that table and came out as "between v0.11.0 and v1.0", under
   the product name "Kafka". It is told apart by the one thing it says of
   itself over this protocol — its cluster id begins `redpanda.` — and is named
   Redpanda with no version, which is better than that one.

4. **What Redpanda cannot say about its permissions is said for it.** A Kafka
   broker with no authorizer refuses to list permissions, and the driver turns
   that into what it means: nothing is kept, so everything is allowed
   (ADR-0166). Redpanda has no such state. It keeps permissions whether or not
   it enforces them, answers a list either way, and of the nine settings it
   shows over Kafka's protocol none says which. So an empty list there may
   mean nobody is allowed or everybody is, and the driver cannot know.

   It does not pretend to. The cluster's description carries a line saying
   that permissions are kept whether or not they are enforced and that the
   cluster does not say which. That is weaker than what Kafka gets, and is the
   whole of what is true.

5. **The connection form says Redpanda.** The driver is listed as
   "Kafka / Redpanda" and takes `redpanda://` as well as `kafka://`: somebody
   with a Redpanda cluster looks for its name.

## What is not proven

- Redpanda with SASL, with TLS, and its own schema registry. Those suites run
  against Kafka only.
- More than one Redpanda version: v26.2.4.

## Consequences

- T5.16 stays open for NATS JetStream and Pulsar, which are drivers of their
  own.
- The nightly conformance run had never required the Kafka broker it starts —
  no `IKIGAI_REQUIRE_KAFKA` is set there, so the Kafka suite skips in CI and is
  proved only by `gate.sh`. The Redpanda step does require its broker, so for
  now CI proves the Kafka driver against Redpanda and not against Kafka. That
  is written down here rather than fixed in passing: Kafka's CI listeners need
  looking at on their own.
