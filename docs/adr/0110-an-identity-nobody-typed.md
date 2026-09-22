# ADR-0110: An identity nobody typed

**Status:** Accepted · **Date:** 2026-09-22
**Tasks:** T2.87 · **Requirements:** FR-1.14, FR-1.5, NFR-S2
**Packages:** `internal/cloud`, `internal/app`, `internal/source`, `internal/store`

## Context

FR-1.14 asks for cloud authentication: AWS IAM, Google's application default
credentials, Microsoft Entra. All three answer the same question, which is
what a connection uses for a password when there is no password.

T2.57 drew the line this sits on the far side of. Kafka's AWS MSK IAM
mechanism signs with credentials somebody typed into the connection — an
access key id and a secret key, each kept in the keychain (FR-1.5) — and its
entry said that an ambient identity nobody typed, a profile or an instance
role, was FR-1.14's business. This is that business.

The three clouds differ in what they hand over but not in shape. Each mints
something short-lived from an identity this machine already holds, and each
expects it where a password would go: for AWS a request signed for one
endpoint and one database user, good for fifteen minutes; for Google and
Azure a bearer token for an account, good for about an hour.

## Decisions

1. **The token is the password, and no driver knows.** A driver asks for a
   password through `ConnectionConfig.Secret` and is handed one. Where a
   connection names a cloud, the app layer replaces that function with one
   that mints instead of looking up. This is ADR-0109's arrangement applied
   again: the tunnel is opened before a driver sees its configuration, and the
   token is minted before a driver asks for a secret, so both are one
   implementation instead of one per driver — and both work for a driver
   written afterwards that does nothing to deserve it.

2. **The token is minted before a tunnel rewrites the host.** An AWS token is
   signed over the endpoint it may be spent at. A tunnel replaces the host and
   port with its own local end. Minted in the wrong order, the token would be
   signed for `127.0.0.1` and refused by the database it was meant for, while
   looking from this side exactly like a token. `withCloudToken` takes the
   endpoint before `throughTunnel` runs and holds it.

3. **No cloud SDK.** The AWS, Google and Azure Go SDKs would bring something
   near forty modules between them into an application with seventeen direct
   dependencies, every one of which it needs. This project wrote its own diff
   rather than take a dependency for a single view, and refused testcontainers
   on the same grounds in T2.84. Signature Version 4 is a documented procedure
   over HMAC-SHA256; the token endpoints are documented HTTP; a JWT assertion
   is RSA and base64url. All of it is standard library.

   What an SDK would have brought is not the arithmetic but the breadth of
   the credential chains. That cost is real and is paid where decision 5 says.

4. **Nothing is cached.** Every provider here issues something that expires.
   A token kept from an earlier connection is a login failure with no visible
   cause, and the arithmetic is local for AWS and one HTTP call for the
   others. A connection mints its own; a reconnection mints another.

5. **What is not supported says so by name.** The chains here are narrower
   than an SDK's: AWS single sign-on, a role to assume and an external
   credential process are not implemented, nor is Google's workload identity
   federation or service account impersonation. Each of those is recognised
   where it is found and refused with the thing it is named after, rather
   than skipped so that the search falls through to a metadata service that
   was never going to answer. Being told "this profile signs in with single
   sign-on, which this cannot do" is a different experience from watching a
   connection time out.

6. **Only IMDSv2, no redirects, bounded everything.** The EC2 metadata service
   is asked for a session token first and never asked without one, because the
   older unauthenticated version is what made instance credentials reachable
   through a request-forgery bug in an unrelated application on the same host.
   The HTTP client refuses to follow redirects at all: these requests are
   about to receive credentials, and a metadata service answering with a
   redirect elsewhere is either broken or an attempt to have this send them
   there. Responses are read under a limit and every metadata call has a
   two-second deadline, because on a machine that is not in that cloud the
   address is a black hole rather than a closed port.

7. **The Azure CLI is run, and it is the only program that is.** On a
   developer's own machine a signed-in `az` is usually the only Azure identity
   there is, and it is how Microsoft's own library reaches the same one. It is
   tried last, looked up on the path rather than through a shell, and a
   resource that would be read as a flag is refused before it reaches a
   command line.

8. **Nothing secret is in the settings.** `Params` carries a profile, a
   region, a tenant, a scope — things written in plain sight. The Azure client
   secret is read from the environment and never from the settings file, and
   the identities behind the other two are files and services this does not
   copy. A credential somebody types still belongs in the keychain (FR-1.5).
   A minted token is a password and is redacted as one (NFR-S2); the access
   key id inside an AWS token is an identifier rather than a secret, which is
   why the token can be handed to a driver at all.

## Proof, and its limit

Nothing in this project can reach real AWS, Google or Azure, and no cloud CLI
is installed on the machine it was written on. So a signature this code likes
proves nothing on its own: it would be one implementation agreeing with
itself.

That is why AWS's published Signature Version 4 test suite matters more here
than a golden file usually would. The `get-vanilla` case's canonical request
hash and signature are digests published by the party that will later check
the signature, and this implementation reproduces both. The rest is held to
what AWS documents about the token's shape — no scheme, the port included,
`Action=connect`, `X-Amz-Expires=900`, the `rds-db` service in the credential
scope — and to the fact that the signature changes when the endpoint, the
port, the user, the region or the minute changes, which is what makes it a
signature rather than a string.

Google's assertion is verified against the public half of the key that signed
it, which is the check Google makes. The metadata and token endpoints are
proved against servers running in this process, as the SSH tunnel was against
an SSH server running in this process. The Azure CLI is proved against a
program called `az` on the path, which establishes how it is invoked and what
is made of the answer, and not that the real CLI behaves so.

This is the same position T2.57 recorded and should be read the same way: the
mechanism is written and checked as far as it can be checked here, and the
first connection to real infrastructure is still the first connection to real
infrastructure.

## Consequences

- One package serves every driver, and a driver added later gets cloud
  authentication without knowing the word.
- A cloud connection can also be carried through a tunnel; they compose, and
  the order they compose in is fixed by decision 2 rather than left to whoever
  edits `Open` next.
- The connection editor has no section for any of this, exactly as it has none
  for SSH. A cloud identity can be configured only by editing the settings
  file by hand. The half of FR-1.14 a driver can see is done and the way in
  is not.
