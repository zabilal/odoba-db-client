# ADR-0022: One copy to a data directory

**Status:** Accepted · **Date:** 2026-09-11
**Tasks:** T1.1 · **Requirements:** NFR-R2, NFR-R3 · **Packages:** `internal/single`, `cmd/ikigai`

## Context

Two copies of the application on one machine share one settings file and
one local database. Each would save its own session and its own unsaved
query text there (ADR-0011 §9, §10), and each would write over the other's.
Opening the application a second time should bring the first forward, as a
Mac application does.

## Decisions

1. **One copy to a data directory.** The first copy listens on a local
   socket in its data directory. A second, starting, connects, asks the
   first to come forward, and gives way before it touches the settings or
   the database. Keyed to the data directory, a portable copy with its own
   data runs beside an installed one, since they share nothing. On
   Windows the settings roam with the profile while the data stays on the
   machine; one copy is a machine's concern, so the data directory is the
   key.

2. **A socket nobody answers on was left by a copy that crashed**, and is
   taken over. Two copies starting at the same moment settle it by one
   asking again: the other has the socket by then. The socket is the
   user's alone.

3. **A deep data directory gets a short socket.** A socket's path is
   limited to 104 bytes on macOS; a data directory whose socket would be
   longer, as a portable copy's can be, has its socket in the temporary
   directory, named from the data directory, so it is still one to each.

4. **If the check cannot be made, the application still starts**, and says
   so in its log. Refusing to open would be worse than the risk the check
   guards against.

## Verification

A second copy gives way and the first is asked forward; the directory is
free again once released; a socket left by a crash is taken over;
separate data directories run side by side; a deep directory's socket is
short and stable; the socket is the user's alone.

## Not decided here

Passing a file to the running copy, such as a script to open, when one is
given on the command line.
