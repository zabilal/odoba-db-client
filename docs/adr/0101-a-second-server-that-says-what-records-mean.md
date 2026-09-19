# ADR-0101: A second server that says what records mean

**Status:** Accepted · **Date:** 2026-09-19
**Tasks:** T2.69 · **Requirements:** FR-13.7, FR-13.14, NFR-S2, NFR-S3 · **Packages:** `internal/source/drivers/kafka`

## Context

A broker hands over bytes and knows nothing about what they mean. A schema
registry knows what they mean and nothing about the broker. The only thing
tying them together is five bytes at the front of a record: a zero, then the
four-byte id of the schema that wrote it.

That shape decides most of this. A registry is a second server, reached over
HTTP, named separately, authenticated separately, and failing separately.

## Decisions

1. **A registry is named per connection and is optional.** Most clusters are
   read without one. Naming none is not a fault and must not read as one;
   every registry call then refuses in those words rather than returning
   nothing, so that "no subjects" and "no registry" stay different answers.

2. **The capability is claimed only where a registry is named.** Claiming
   `Stream.SchemaRegistry` unconditionally would promise subjects a connection
   can never list — a claim is a promise (REQ-DRV-1), and this one is true of
   the connection rather than of the driver.

3. **A registry that was named and cannot be reached fails at connect.** A
   person who typed an address wants to hear about it then, not when they open
   a record an hour later. One that was not named fails nothing.

4. **It refuses in the registry's own terms.** Unauthorised, a certificate
   that would not verify, a host that does not resolve and a refused
   connection are four different problems with four different fixes, and a
   registry can fail in ways the broker never does.

5. **Listing subjects does not read their versions.** A registry may hold
   thousands, and reading every version of each to draw a list is the shape
   refused for topics in ADR-0092. Versions are read when a subject is opened.

6. **Credentials live in the keychain and never in the address.** A registry
   password is a `Secret` field like every other password here (FR-1.5), and
   an address that carries userinfo is redacted wherever it is written
   (NFR-S2). Over HTTPS it verifies by the same rules the broker's own
   connection follows (NFR-S3).

7. **A decoder says which schema wrote a record, and says what it cannot do.**
   Reading the five-byte header is all this task claims: it names the schema,
   refuses a record written by a different one rather than reading it with the
   wrong schema, and refuses bytes with no header at all. Avro, Protobuf and
   JSON Schema are their own languages, written next — returning a guess in
   the meantime would be worse than an honest refusal, which the decoder
   contract already expects to show beside the bytes rather than instead of
   them.

## Consequences

The test registry needed a broker of its own. Both existing brokers advertise
`localhost` and sit on the default bridge network, which has no DNS, so a
registry container that bootstrapped from either would be handed the address
"localhost" and try to reach the broker at itself. Rather than recreate a
working broker the gate depends on, the registry has its own pair on a
user-defined network: a broker advertising one listener for containers beside
it and another for tests on this machine.

T2.70 onwards add decoders for the languages themselves. They join the list
ADR-0100 established, so what grows is which decoders apply to a subject —
not how a record is read, or where the choice is remembered.
