# ADR-0095: Reading a log as it stands

**Status:** Accepted · **Date:** 2026-09-19
**Tasks:** T2.63 · **Requirements:** FR-13.4, FR-13.19, NFR-P9, NFR-P11, REQ-DRV-1 · **Packages:** `internal/source/drivers/kafka`

## Context

The grid reads rows. A log has records, and the difference runs through every
decision here: there is no order to ask for, because a partition is ordered by
itself and nothing orders across partitions; there is nothing to filter on,
because a broker hands over bytes and asks no questions about them; and there
is no language to write a condition in.

What a log has instead is a position and a bound.

## Decisions

1. **A browse reads the log as it stands.** Where each partition ends is asked
   before any record is read, so a read that has caught up ends because it is
   known to have caught up — not because nothing arrived within some timeout.
   Records written after the read began are not in it; that is what "as it
   stands" means, and it is the only reading that can end at all.

2. **Partitions are assigned explicitly, and no group is ever joined.** The
   reader names the partitions and the offsets it wants and commits nothing, so
   looking at a topic cannot move anybody else's place in it (FR-13.19). This
   is not an option a caller may set: there is no way to ask this driver for
   the other thing, because the guarantee is worth more than the flexibility.

3. **Every read is bounded.** The grid always names a bound and the read stops
   there; where it does not, a default applies. An unbounded read of a log is
   an unbounded read of a disk (NFR-P11).

4. **What a log cannot be asked, it refuses in its own words.** A condition, a
   filter, an order, a row offset, a position to seek to, a log to follow: each
   is refused with what it is, rather than ignored. Filtering records is
   something this application will do to what it has read (T2.67), not
   something a broker does; seeking and following are written next (T2.64,
   T2.65) and say so meanwhile.

5. **A key and a value are bytes.** What they mean is a decoder's business
   (T2.68), and nothing here pretends to know: a value that happens to be JSON
   is still bytes until somebody says otherwise.

6. **Headers are a list, not a map.** Kafka lets a header name repeat, and a
   map would quietly keep one of them.

7. **A partition that fails is not a log that ended.** Fetch errors are
   reported rather than read as the end of the data — the same distinction the
   watermarks make between not knowing and holding nothing.

## Consequences

A topic opens in the standard data grid with a column for the partition, the
offset, the timestamp, the key, the value and the headers (FR-13.4), and every
one of those columns is read-only: a record is what was written, and writing
is producing (T2.77).

This is also the first stream source the shared conformance suite has met, and
it passes what every driver passes — an addressable tree, a browse that
returns columns and rows and closes twice without complaint, a read that stops
promptly when it is given up on. What it does not claim, it is not asked for:
the suite's write, count, distinct and query-language checks skip, and the
WHERE check skips of its own accord because this driver implements no dialect.
