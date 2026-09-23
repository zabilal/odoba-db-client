# ADR-0139: What a column holds

**Status:** Accepted · **Date:** 2026-09-23
**Tasks:** T3.28 · **Requirements:** FR-3.14, NFR-S6
**Packages:** `internal/source`, `internal/source/drivers/{postgres,mysql,sqlite}`, `internal/app`, `internal/ui/shell`

## Context

FR-3.14 asks for column statistics: a distinct count, a null proportion,
min, max, average and a histogram. A page of rows is what somebody can
already see; the question a statistics panel answers is about the rest.

## Decisions

1. **Nothing is estimated.** Every engine keeps its own statistics — PostgreSQL's
   `pg_stats`, MySQL's index cardinality — and each is a sample taken whenever
   the table was last analysed. A distinct count from there can be out by a
   factor of ten, and PostgreSQL's is negative when it means a proportion. A
   figure somebody reads as "how many" and gets wrong is worse than the wait
   for the real one, so this counts.

2. **What can be measured is decided once and rendered by each driver.** An
   average of text is nothing, the smallest and largest of a JSON document are
   an order nobody meant, and the distinct values of a column of pictures are a
   long way to no answer. That judgement is the same on every engine, so it
   lives in one place and each driver quotes the column its own way.

3. **A figure nobody measured says so rather than saying none.** Not measured
   is −1, or false beside the value it would have qualified. A panel showing
   "0 different values" where none were counted would be a lie in a table.

4. **The figures are about the rows the grid's filters select.** Somebody who
   has narrowed the grid is asking about what is left. Sorting and paging are
   not part of that: how a page is ordered says nothing about the whole of it.

5. **How the values are spread is a sample, and the counts are not.** Drawing
   a hundred thousand values says no more than drawing ten thousand and takes
   ten times as long, while a count of the distinct values is the answer
   itself and cannot be sampled. The sample is the first rows the source
   gives rather than a random draw, because a random one would cost a sort of
   the whole table and what this is for is a shape.

6. **The spread is drawn by the chart.** A histogram of a column is a
   histogram, so it is the one the charts already draw (ADR-0132): the bins
   are round numbers, the bars touch, and nothing is invented.

7. **A typed WHERE reaches the statement the same way it reaches every other
   one**, through the same reader and the same refusal: one condition, no
   semicolon, and nothing the classifier calls a write (NFR-S6).

8. **The panel says how long the server took.** Measuring means counting,
   which on a large table is a wait; how long it took last time is the other
   half of deciding whether to ask again.

9. **A reading that arrives after a later one is dropped.** Somebody clicking
   down a row of headers asks several times over, and the panel should say
   what they asked for last.

## Consequences

- PostgreSQL, MySQL, MariaDB and SQLite measure columns. Cassandra has no
  `count(DISTINCT)` and no `min`/`max` over a partition it has not been given;
  Mongo could through an aggregation and does not yet; Redis and Kafka have no
  columns to measure. The window offers nothing where a connection says so.
- Measuring a column of a very large table is a full scan and will be slow.
  The panel is asked for rather than kept up to date, and nothing measures
  anything unasked.
- A column's figures are read from one row of one statement, which is why the
  aggregates and their order are decided in one place: a panel showing figures
  from the wrong columns would be worse than one showing none, so a row of the
  wrong width is an error rather than a datum.
- Nothing measures several columns at once, and nothing remembers what it
  measured. Both would be worth having; neither is claimed by a task.
