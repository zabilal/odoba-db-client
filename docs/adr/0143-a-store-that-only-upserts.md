# ADR-0143: A store that only upserts

**Status:** Accepted · **Date:** 2026-09-23
**Tasks:** T2.47 (extending) · **Requirements:** FR-4.4, FR-4.5, FR-4.7, FR-1.8, FR-4.9, NFR-S4
**Packages:** `internal/source/drivers/cassandra`

## Context

The Cassandra driver has reported since it was written that its rows are
known by their primary key — the partition and clustering columns are what a
row *is* there — and has had nothing to write them with. The grid therefore
offered edits it could not carry out. This closes that.

The difficulty is not addressing a row, which CQL does exactly. It is that
CQL has no writes, only upserts.

## Decisions

1. **Every statement carries a condition.** An `UPDATE` of a row that is not
   there makes one, and an `INSERT` over a row that is there overwrites it.
   Either would be the grid doing something nobody asked for: somebody
   editing a row they can see does not mean "make this row if it has gone",
   and somebody adding a row does not mean "replace whatever is under this
   key". So an update and a delete are written `IF EXISTS`, and a new row
   `IF NOT EXISTS`, and Cassandra answers whether the statement applied.

2. **A condition that did not hold says which way it did not.** A change or
   a delete that did not apply found no row, which is `sqlscript.ErrNoRow` —
   the same answer every other engine gives when its statement changed no
   row. A new row that did not apply found one already there, which is its
   own error, because "no row matched" would describe the opposite of what
   happened.

3. **The cost is a round of Paxos, and it is paid per edited row.** A
   lightweight transaction is several times the cost of an ordinary write.
   That is the price of a grid whose edits mean what they say, and nothing
   but an edited row pays it: reading, browsing and the query tab are
   untouched.

4. **A changeset is not atomic, and says so.** CQL has no transaction to
   write one in, so a plan that fails half way leaves the half that ran.
   `WritePlan.Atomic` and `Data.TransactionalWrite` are both false, which the
   contract already requires the window to warn about, and the outcome of a
   failed plan reports that nothing was undone (FR-4.5).

5. **A row of nothing but defaults is refused.** A row here *is* its key, and
   CQL has no form for a row with no columns: there would be nothing to write
   it under.

## Consequences

- The grid edits Cassandra tables by the key the driver already reported, and
  the guard holds every write on a read-only or production connection as it
  does everywhere else (FR-1.8, FR-4.9, NFR-S4).
- The driver's own guard check, which was written to fail if a write path
  ever arrived without being held to the guard, did exactly that, and now
  holds `Apply`.
- Bulk loading is still not claimed. The shared loader writes one row per
  statement inside a transaction, and there is none here to open.
