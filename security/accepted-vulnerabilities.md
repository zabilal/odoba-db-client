# Accepted vulnerabilities

CI fails on any vulnerability `govulncheck` reports as reachable from this
application's code (NFR-S9). This file is the exception list, and it is short on
purpose: an advisory is accepted only when there is nothing to upgrade to, and
each one says what the exposure is and what would take it off this list.

The list is machine-read by the `vulnerabilities` job in
`.github/workflows/ci.yml`. Only the `GO-…` identifiers at the start of a line
count; everything else is for people.

---

GO-2026-5046 — `github.com/hamba/avro/v2`: denial of service via unbounded
allocations while decoding. No fixed version exists (latest is v2.31.0, which is
what we require). Reached where this application decodes Avro from a Schema
Registry and from Kafka records (T2.69, T2.70). The exposure is a broker or
registry serving a deliberately malformed payload to a person who chose to
connect to it: the process can be made to allocate until it is killed. Nothing
is written, and no credential is involved. Comes off this list when hamba/avro
publishes a fixed release.

GO-2026-5047 — `github.com/hamba/avro/v2`: as above, a different allocation
path. Same exposure, same condition for removal.

GO-2026-5048 — `github.com/hamba/avro/v2`: as above, unbounded map allocations.
Same exposure, same condition for removal.

---

## Reviewing this list

Check it whenever the CI job fails, and at every release. Two questions per
entry: is there a fixed version now, and is the exposure still what this says?
An entry nobody can justify in a sentence is an entry that should be a fix.
