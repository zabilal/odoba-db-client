# ADR-0076: A key itself: how long it has left, and what it is called

**Status:** Accepted · **Date:** 2026-09-13
**Tasks:** T2.43 · **Requirements:** FR-3.8, FR-4.1, FR-4.4, FR-12.2 · **Packages:** `internal/source/drivers/redis`

## Context

FR-12.2 asks for a key's time to live to be shown and edited. It is shown
already: the keyspace browses with a `ttl` column beside the name and the kind
(ADR-0073). What is missing is editing it — and once a row of the keyspace can
be edited at all, the question is what else a change to one of those rows
means.

## Decisions

1. **The keyspace's rows are edited where they are shown.** A change to a row
   of a database's keyspace is a change to the key itself rather than to what
   it holds, so the writer takes a `KindDatabase` target as well as a
   `KindKey` one, and the two paths are separate: one sends `EXPIRE` and
   `RENAMENX`, the other `HSET` and its kin.

2. **A time to live is read however it is written.** The grid holds it as a
   length of time and shows it as one; typed back, a bare number is seconds,
   as the server counts them, and anything else is a length of time as Go
   writes one — `30m`, `12h`, `1h30m`. Nothing at all — an empty cell, a value
   taken away — is a key that never expires, and so is a negative one, which
   is what the server itself answers for a key with no expiry.

3. **Never expiring is `PERSIST`, and it is no failure to do it twice.**
   `PERSIST` answers 0 both for a key that is not there and for one with no
   expiry to take away, so the key is asked after first: a key that never
   expires, told not to expire, has not changed since it was read.

4. **An expiry of nothing does not delete the key.** `EXPIRE` with a
   non-positive time deletes it, which nobody typing into a cell is asking
   for; a key is deleted by deleting its row, which says what it does.

5. **A key is renamed with `RENAMENX`.** `RENAME` would delete whatever was
   under the new name, and a person typing a name into a grid is not asking to
   overwrite another key; a name that is taken is refused and says so.

6. **A key is deleted from the keyspace, and never added there.** This is the
   only place a whole key can be deleted. A key comes into being when
   something is written to it, so a new row of a keyspace is refused and says
   how a key is really made.

7. **A key's kind is not something to type over**, being the kind of what it
   holds, and two changes at once — a rename and an expiry — are refused
   rather than sent as two commands under one line of description.

8. **A cluster answers these itself.** Each of these commands names the key it
   is about, so the client sends it to the shard that holds it: where a walk
   of the keyspace visits every shard in turn, a change to one key has one
   place to go.

## Consequences

- The keyspace grid is now a key manager: a TTL set or cleared, a key
  renamed, a key deleted — each shown as the command it will send, and
  guarded like every other write.
- Deleting a key is a row delete, so the grid's own "delete row" affordance is
  what does it, with the same review before it runs.
- `value.Parse` still hands a `TypeInterval` cell to the driver as the text
  that was typed. Reading it as a `time.Duration` there would reach every
  driver, and PostgreSQL's intervals go to the server as text.
