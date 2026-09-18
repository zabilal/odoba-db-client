# ADR-0089: Signing in to a managed cluster

**Status:** Accepted · **Date:** 2026-09-18
**Tasks:** T2.57 · **Requirements:** FR-1.5, FR-1.11, FR-1.14 · **Packages:** `internal/source/drivers/kafka`

## Context

AWS MSK IAM is unlike every mechanism beside it. PLAIN sends a password, SCRAM
proves one is known, OAUTHBEARER presents a token somebody else issued — MSK
IAM sends none of those. The client signs a request with AWS credentials and
the broker asks IAM whether that signature belongs to somebody allowed in. The
secret never crosses the wire at all.

## Decisions

1. **The fields already there carry it.** The access key id goes in User, the
   secret access key in Password, and the session token in Token, with each
   field's help saying what it means for this mechanism. Three more
   always-visible fields, useful to one mechanism and empty for every other
   connection, would cost more clarity than the reused labels do — and the
   form is per-driver data, not per-mechanism.

2. **No region is asked for.** franz-go reads the region out of the broker's
   own address, and falls back to AWS_REGION and AWS_DEFAULT_REGION. A field
   here would be deciding a second time what is already decided correctly,
   which is the redundancy the TLS guard was cut for (ADR-0086).

   That failure is deferred, though: it arrives during authentication rather
   than when the settings are read, and franz-go words it as not being able to
   determine a region. Left alone it would reach a person as a cluster nobody
   could reach, so it is classified as a fault in the settings that names
   AWS_REGION — the broker is answering perfectly well.

3. **Nothing reads the environment for the keys themselves.** Every credential
   in this application is one somebody typed, kept in the keychain (FR-1.5).
   An ambient identity nobody typed — a profile, an instance role, a
   credentials file — would mean connecting as whoever the machine happens to
   be, which is a different promise. FR-1.14 asks for exactly that, separately
   and deliberately, and it is where a credential chain belongs.

4. **Both keys are wanted before anything is dialled; a session token is
   optional**, because temporary credentials have one and permanent ones do
   not. A key pair half given is refused in the same breath as every other
   half-given credential here.

## Consequences

MSK IAM lands with unit tests and no live proof, and the task says so rather
than implying coverage it has not got: the mechanism authenticates against
real AWS, and there is no local broker that speaks it — the same honest
position GSSAPI is in, for a different reason.

What is proven is still worth having: the mechanism this driver builds is the
one franz-go signs with, both keys are required before a connection is
attempted, a session token is accepted where the password mechanisms refuse
one, and a region nobody named reads as a setting to fix rather than as a
broker that is not there.

With this, FR-1.11 is complete for Kafka but for GSSAPI, which waits on a
Kerberos library and a KDC.
