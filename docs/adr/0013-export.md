# ADR-0013: Export

**Status:** Accepted · **Date:** 2026-09-10
**Tasks:** T1.69–T1.72 · **Packages:** `internal/export`, `internal/app` (`rows.go`)

## Context

FR-10.1–10.3 ask for CSV, TSV, JSON and NDJSON export, streaming, with
memory flat in the row count (NFR-P11). A data client's export is judged on
whether the file holds exactly what the database holds.

## Decisions

**One streaming engine, in core.** `export.Copy` reads a `model.RowStream`
and writes one format through a buffered writer. It holds one row at a time,
reports progress at most every 100 ms, and stops on cancellation. It has no
UI dependencies (ARCH-1). A test streams 2 million rows and fails if the heap
grows by more than 16 MB.

**Values leave exactly as they arrived:**

| Value | CSV / TSV | JSON / NDJSON |
|---|---|---|
| Decimal | the server's text | a number literal with every digit, never via float64 |
| Float | the shortest text that reads back identically | the same |
| NaN, ±Infinity | `NaN`, `Infinity`, `-Infinity` | the same, as strings — never `null`, which would read as missing |
| Bytes | `\x` hex, PostgreSQL's form | base64 |
| Date | `2006-01-02` | the same, as a string |
| Instant (timestamptz) | RFC 3339 in UTC | the same |
| Wall-clock timestamp | no offset, since there is none to state | the same |
| JSON | its text | embedded, and compacted so NDJSON stays one record a line |
| Arrays, maps | compact JSON, by the rules above | nested, by the rules above |
| NULL | empty, or a chosen marker such as `\N` | `null` |

Duplicate column names, as from a join, become distinct JSON keys (`id`,
`id_2`). HTML characters are not escaped: exported text reads as it is.

**CSV and TSV share RFC 4180 quoting.** TSV is the same writer with a tab
delimiter, quoting a field only when it needs it. That is what spreadsheet
applications read back.

**Two sources.**

- A browse exports the whole table, or the filtered result when the browse
  is filtered. It pages through 5 000 rows at a time and lets each page go
  before fetching the next. Paging is deterministic because the driver
  orders by a unique key (ADR-0008's tiebreak).
- A query result exports the rows already buffered for it (ADR-0012).

**Known limit: deep OFFSET pages cost a scan.** Exporting a table of many
millions of rows makes the server skip ever more rows on each page. Keyset
paging (`BrowseOptions.Seek` for relational sources) is the fix, and is
open.

**The export UI.** Export… (⇧⌘E) works on the active tab: a table (whole or
filtered), or the query result on show. A format sheet leads to the save
dialog, then to a progress sheet. It shows rows, rows per second and, when
the total is known, a bar and an estimate of the time left. PostgreSQL gives
no cheap total, so a table shows rows and rate only.

**Cancel stops the export and removes the partial file.** So does closing
the tab, and so does a failure: a truncated export left behind looks like a
complete one.

## Not decided here

The other FR-10.1 formats (xlsx, SQL INSERT, Markdown, HTML, XML), exporting
a selection (it needs grid selection, FR-3.7), multi-table batches, and
import (FR-10.4).
