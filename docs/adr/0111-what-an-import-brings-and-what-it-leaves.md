# ADR-0111: What an import brings, and what it leaves

**Status:** Accepted · **Date:** 2026-09-22
**Tasks:** T2.88 · **Requirements:** FR-1.12, FR-1.5, NFR-S2, NFR-S4
**Packages:** `internal/importer`, `internal/app`

## Context

FR-1.12 asks for connections to be imported from DBeaver, DBGate, TablePlus,
DataGrip, `.pgpass` and `~/.my.cnf`. Somebody arriving with forty connections
already set up somewhere else should not have to type them again.

These six are not one problem. Four of them are another program's saved
settings; two of them are files whose entire purpose is to hold a password.
The difference decides nearly everything below.

## Decisions

1. **An import brings connections, not secrets.** DBeaver keeps its passwords
   in an encrypted store of its own; DataGrip keeps them in the operating
   system's password store or in KeePass. Neither is opened. A connection
   arrives with a note saying where its password stayed, which is better than
   arriving silently unable to connect.

   This is not only politeness toward another program's secret store. Reading
   it would mean this application had a way to decrypt somebody's credentials
   in bulk, which is a thing worth not having.

2. **Except the two files that exist to hold a password.** `.pgpass` and
   `~/.my.cnf` are password files, and ADR-0009 already decided they would be
   read explicitly when somebody asks — deliberately unlike the driver, which
   refuses to pick them up implicitly (ADR-0008). Their passwords go to the
   keychain (FR-1.5), and whether they come at all is a choice at the moment
   of import rather than a default to discover afterwards.

3. **A password found in a JDBC URL is noticed and dropped.** DataGrip records
   that a URL-only connection may carry its password in the URL, and DBeaver's
   configuration can too. Saving that connection as written would put a
   plaintext password in this application's settings file, which is the exact
   thing FR-1.5 exists to prevent. The user survives the import; the password
   does not, and the note says so.

4. **A `.pgpass` that others can read is refused.** libpq ignores such a file.
   Importing from it would be taking a password out of somewhere PostgreSQL
   has already judged unsafe, and doing it quietly. The refusal says to run
   `chmod 0600` on it.

5. **Nothing is dropped in silence.** An entry that cannot be brought — a
   database there is no driver for, a `.pgpass` line matching any host, a line
   of the wrong shape — comes back as an entry with no connection and a reason.
   Somebody who imports forty and gets thirty-one needs to know which nine and
   why, not to count them.

6. **Connection type and read-only come across.** DBeaver marks a data source
   dev, test or prod and can mark it read-only. Those map to this project's
   environments and to its read-only flag, because they are not labels: they
   decide whether this application asks before it writes (NFR-S4). Losing them
   in an import would quietly remove a guardrail somebody had already set.

7. **The same place and the same account is the same connection.** Importing
   the same file twice is something people do, usually because they are not
   sure the first one worked. A connection matching one already saved on
   driver, host, port, database and user is not saved again. The name is not
   part of the comparison: the same database under two names is one database.

8. **Scanning reads nothing.** `Scan` reports which of the documented files
   exist and stops there. Reading is a separate step, because these files
   belong to other programs and two of them hold credentials.

## What is not done, and why

**DBGate.** Its documentation says where its data directory is and does not
say how connections are stored inside it. The only other way to learn the
format is to read its source, which this project does not do: DBGate is
GPL-3.0 and this is a clean-room implementation of the same idea. The
remaining honest route is to install DBGate, let it write a connection, and
read the file it produced — observing behaviour rather than copying
expression. That needs the program installed, which is the owner's call.

There is an irony in the one tool this project was measured against being the
one whose settings it will not guess at, and it is the right way round.

**TablePlus.** Its connections live in a binary property list. Reading one
means either a new dependency for a single importer, in a project that wrote
its own diff rather than take one for a single view, or a binary plist reader
of this project's own — and there is no sample on the machine this was written
on to hold either to. It is worth doing deliberately rather than guessing, so
it is not done here.

Both are recorded against T2.88, which stays open.

## Consequences

- Four of the six sources work, and the two that do not say exactly what is
  needed to finish them.
- The importer answers a list; saving it is `Connections.Import`, so the
  decision of what to keep belongs to whatever asks — including a UI that does
  not exist yet.
- There is no way to invoke any of this from the window. That is the same gap
  the SSH tunnel and cloud authentication have, and it now spans three
  features.
