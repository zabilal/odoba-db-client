# ADR-0056: SQL, HTML and XML exports

**Status:** Accepted · **Date:** 2026-09-11
**Tasks:** T2.24 · **Requirements:** FR-10.1, FR-10.3 · **Packages:** `internal/export`, `internal/ui/shell`

## Context

FR-10.1 asks for SQL INSERT, HTML and XML beside the formats an export
writes already (CSV, TSV, JSON, NDJSON, Markdown, Excel). They must stream
as the others do (FR-10.3). INSERT statements are dialect text, which only a
source's dialect may write (ARCH-2), and the export package knows no
dialect.

## Decisions

1. **SQL INSERT is written by the table's own source.** The shell hands the
   export `Options.Inserts`, the tab's `app.BrowseSource.InsertRows`: each
   driver writes its statements through `sqlscript.Inserts` and its own
   literals, as Copy as INSERT does. The writer asks for a row at a time,
   so memory stays flat and it keeps no row a stream might reuse. Without
   `Inserts` the format refuses to start.

2. **It is offered where Copy as INSERT is**: for a table's rows, and for a
   selection of them, from a source whose dialect writes rows. A query's
   result has no one table to insert into, and is not offered it.

3. **HTML is a page of one table**: a head naming the columns, a row a line,
   each value escaped and its line breaks kept as `<br>`. NULL is written
   `<i>NULL</i>`, so it reads apart from the text "NULL" with no style at
   all (UX principle 7); numbers align right in figures of one width. The
   page's little style follows the reader's light or dark appearance, and
   its greys keep WCAG AA's contrast on either. It runs no script and
   fetches nothing. Its title is the name of what is exported.

4. **XML is a `<row>` of `<field name="…">`s**: a column's name is not
   always an XML name, so it goes in an attribute. NULL is `null="true"`.
   Bytes, and text holding a character XML 1.0 cannot hold at all, go as
   base64 marked `encoding="base64"`, so nothing is lost and the document
   stays well-formed. A carriage return is written `&#13;`, which XML would
   otherwise read as a line feed. Every other value is written as CSV
   writes it.

5. **`Options.Sheet` becomes `Options.Name`**, what is exported: an Excel
   workbook's sheet and an HTML page's title.

6. **The formats are offered in the order** CSV, TSV, Excel, JSON, NDJSON,
   XML, HTML, Markdown, SQL INSERT; Header applies to CSV, TSV and Excel.

## Consequences

- A value an INSERT's dialect cannot write stops the export with the
  dialect's error, as Copy as INSERT does.
- FR-10.2's "multiple tables in one batch" is not yet: an export is of one
  tab's rows.
