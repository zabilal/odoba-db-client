# ADR-0005: Browse, not Query, is the required data path

**Status:** Accepted · **Date:** 2026-09-10

## Context

Task T0.29 required validating the driver contract against Kafka *before* any
driver was written, on the theory that a log is the paradigm most likely to
break an abstraction designed around relational data.

The contract as originally sketched (REQUIREMENTS §10.2) listed `Queryer` among
the interfaces every source implements, with `StreamConsumer` as an optional
addition for logs. Working through Kafka concretely showed this was wrong.

The grid needs rows. For a relational source those come from a query. But when
a user opens a table, they do not write SQL — the application generates it. For
Kafka, Redis and document stores there is no statement to generate at all.
Requiring `Queryer` would have forced every non-relational driver to invent a
fake query language purely to satisfy the interface, and would have pushed
paradigm knowledge into the grid.

## Decision

**`Browser` is required of every source; `Queryer` and `Dialect` are optional
and paired.**

```go
Browse(ctx, ref ObjectRef, opt BrowseOptions) (RowStream, error)
```

Relational drivers implement `Browse` by generating SQL through their
`Dialect`. Kafka implements it by assigning partitions and consuming. The UI
calls it identically and never learns which happened.

Two further simplifications followed:

- **`StreamConsumer` was removed entirely.** Seeking is `BrowseOptions.Seek`
  and live tailing is `BrowseOptions.Follow` — a following stream simply blocks
  in `Next` instead of returning `io.EOF`. The same mechanism serves Mongo
  change streams and Redis pub-sub (FR-12.5), so one concept covers three
  paradigms.
- **Row identity became a stream property**, not a relational one.
  `IdentityLogOffset` makes a record addressable for selection and export while
  reporting `Mutable() == false`, so the grid refuses to edit it with no
  Kafka-specific logic.

## Consequences

- REQ-DRV-2 in `REQUIREMENTS.md` is updated: the required set is `Connector`,
  `Introspector`, `Capabilities`, `Browser`.
- `internal/source/contract_kafka_test.go` encodes this as a compile-time
  proof. A Kafka-shaped source implementing only the required interfaces is
  asserted to satisfy `source.Source`. If the required contract ever grows
  something a log cannot provide, that file stops compiling.
- `Browse` must *refuse* options it cannot honour rather than ignore them.
  Silently dropping a sort would show unsorted rows under a sort indicator,
  which is worse than an error. The conformance suite checks this.

## Note

This is what T0.29 was for. The finding cost an afternoon before any driver
existed; discovering it in Phase 2, with four drivers already built against the
wrong shape, would have cost far more.
