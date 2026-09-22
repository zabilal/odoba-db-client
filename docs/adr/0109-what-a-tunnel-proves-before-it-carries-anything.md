# ADR-0109: What a tunnel proves before it carries anything

**Status:** Accepted · **Date:** 2026-09-22
**Tasks:** T2.85, T2.86 · **Requirements:** FR-1.9, FR-1.5, NFR-S2, NFR-S3
**Packages:** `internal/tunnel`, `internal/app`

## Context

FR-1.9 asks for connections through an SSH tunnel. A tunnel is a connection to
one machine that then carries the connection to another, and it raises two
questions that have to be answered before a byte moves: what this proves to
the SSH server, and what the SSH server proves to this.

The second is the one worth being careful about. Everything the database
connection carries — the credentials it authenticates with, every row it
reads, every statement it runs — passes through the tunnel. A tunnel that
accepted whatever host key it was offered would hand all of that to anything
able to answer on that address, and it would do so while looking exactly like
a tunnel that worked.

That is a worse failure than an unverified TLS connection to a database,
because it is upstream of the database's own authentication rather than
beside it.

## Decisions

1. **The host key is verified, and an unknown host is refused.** This follows
   the rule TLS already follows here (NFR-S3): verification is on, and
   anything else is an explicit per-connection choice rather than a default
   nobody noticed.

2. **`known_hosts` is where the answer comes from.** The user's own file is
   where this question has already been answered for every other SSH client
   on the machine. A store of this application's own would drift from it, and
   a disagreement between the two would be discovered at the worst moment.

3. **An unknown host is refused with what to do about it, not with an offer
   to trust it.** Trust-on-first-use is a decision made at exactly the moment
   somebody is least able to make it — mid-connection, with a task in mind.
   The refusal names the host and says to add it the way every other SSH
   client adds it. A key that has *changed* is said differently from one that
   was never known: the first is what an interception looks like.

4. **Secrets are secrets.** A password and a key's passphrase go to the
   keychain like every other credential (FR-1.5), reached through the same
   `Secret` function a driver uses. A path to a key file is not a secret and
   is kept with the connection.

5. **No driver knows what a tunnel is.** It is opened before the driver sees
   the configuration, and the driver is handed a host and port that point at
   the local end. That is what keeps this one implementation rather than one
   per driver, and it is why a tunnel works for a driver written afterwards
   without that driver doing anything.

6. **The tunnel lives exactly as long as the connection it carries.** Closing
   the source closes the tunnel. A tunnel outliving its connection is a
   listener on a local port that nobody remembers opening.

## Consequences

A connection through a tunnel fails in SSH's terms before it fails in the
database's, which is the right order: "that host's key is not one you know" is
a different problem from "that password is wrong", and reporting the second
when the first is true sends somebody looking in the wrong place.

What this does not do is offer a way to skip verification. If a connection to
a host nobody can add to `known_hosts` turns out to be a real need, that is a
per-connection choice to be designed and named, the way disabling TLS
verification was — not a flag that arrives quietly because something was
awkward once.

Agent authentication and jump hosts are the next task, and both fit this
shape: an agent is another way to prove who this is, and a jump host is
another hop whose key is verified exactly like the last one's.
