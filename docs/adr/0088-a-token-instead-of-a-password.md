# ADR-0088: A token instead of a password

**Status:** Accepted · **Date:** 2026-09-18
**Tasks:** T2.56 · **Requirements:** FR-1.5, FR-1.11 · **Packages:** `internal/source/drivers/kafka`

## Context

OAUTHBEARER is unlike the mechanisms beside it. PLAIN and SCRAM prove that
somebody knows a password; OAUTHBEARER presents a token that somebody else
issued, which already says who the bearer is and how long that stays true.
There is no password, and often no user name either.

## Decisions

1. **A token is a credential, so it lives where credentials live.** A token
   field of its own, kept in the keychain like a password and never in the
   settings file (FR-1.5). It is not a user name typed into a box that happens
   to be long.

2. **OAUTHBEARER needs no user, and a user given means something else.** The
   token names its subject. Where a person does fill in a user name, it is
   passed as the authorization id — the identity to act as — which is what
   that field means for this mechanism.

3. **A token and a password together are refused.** One of them would be
   ignored, and nothing on the screen would say which. The same reasoning
   refuses a token typed under PLAIN or SCRAM, and a token with no mechanism
   chosen at all: a credential that would be sent nowhere is a lie about what
   is happening (ADR-0087).

4. **The driver takes a token as given, and neither fetches nor refreshes
   one.** franz-go's mechanism can be built around a function that returns a
   token, which is exactly where refreshing would go, and that function needs
   to know an issuer, a client id, a secret, and a flow. None of that is asked
   for here yet, so nothing pretends to do it: a token that expires ends the
   connection, and the fix is a new token. A token source is its own decision,
   and belongs with whatever comes to know the issuer.

5. **GSSAPI is not in this task.** franz-go ships no Kerberos mechanism at
   all, so speaking it means a Kerberos library of this project's own, plus a
   KDC and a keytab to prove it against — a different piece of work from
   naming a mechanism franz-go already implements. It stays out of the
   offered list until the driver can actually speak it, which is the rule the
   list has followed since it existed.

## Consequences

A Kafka connection can authenticate with a bearer token, proven against a real
broker that validates one: the test rig enables Kafka's unsecured-JWS
validator, and the tests mint their own token rather than carrying a written
one that would expire. That validator is a test facility and nothing like a
production issuer — what it proves is that this driver sends a token the
broker's own machinery accepts, and that a token which has run out, or which
is not a token at all, comes back as an authentication failure rather than as
a cluster that could not be found.

T2.56 is therefore half done, and says so: OAUTHBEARER lands, GSSAPI waits for
a Kerberos library and a KDC.
