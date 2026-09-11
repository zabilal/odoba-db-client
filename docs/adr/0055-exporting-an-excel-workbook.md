# ADR-0055: Exporting an Excel workbook

**Status:** Accepted · **Date:** 2026-09-11
**Tasks:** T2.23 · **Requirements:** FR-10.1 · **Packages:** `internal/export`, `internal/transfer`, `internal/ui/shell`

## Context

An export writes CSV, TSV, JSON, NDJSON or Markdown (T1.71). People who open
their data in a spreadsheet ask for a workbook, which keeps numbers as
numbers and dates as dates where CSV leaves them as text to be guessed at.
The import reads workbooks with the standard library alone (ADR-0044); the
export should write them the same way, and stream as the other formats do.

## Decisions

1. **`export.XLSX` writes a workbook with the standard library alone**:
   a zip of XML parts. It streams, so memory stays flat: the parts that
   name the sheet are known before any row and are written first, then the
   sheet, a row as it comes. Strings go inline in their cells rather than
   in a table of shared strings, which would hold every string until the
   end.

2. **A value is written as Excel keeps it, or as its text.** A number is a
   number where Excel keeps every digit — at most 15, leading and trailing
   zeros aside — and its text where Excel would round it, so an exact
   numeric stays exact, as in every export. A float is a number (NaN and
   the infinities are their text). A date, a time of day or a timestamp is
   Excel's count of days from 30 December 1899, shown by an ISO format
   (`yyyy-mm-dd`, `hh:mm:ss`, both); an instant is UTC's wall clock, as the
   other formats write it. A time of day with its zone, and a day before
   March 1900, where Excel counts a 29 February that was not, are their
   text. A boolean is a boolean, NULL an empty cell, and anything else its
   text as CSV writes it.

3. **What a workbook cannot hold is refused, not cut short**: more than
   16,384 columns, more than 1,048,576 rows, or a cell of more than 32,767
   characters, counted as Excel counts them. The export stops and says
   which, and to export as CSV to keep it all; its partial file is
   removed, as for any export that fails.

4. **Text is kept whole.** Beside XML's escapes, a character XML cannot hold
   — a control character, or a carriage return, which XML would read as a
   line feed — is written as Excel writes it, `_xHHHH_`, and an underscore
   that would read as the start of one as `_x005F_`. The import now reads
   these escapes, in inline and shared strings alike, so what a workbook
   from Excel says is what it reads.

5. **The header is the sheet's first row**, bold, with the pane frozen below
   it; Header is offered for a workbook as for CSV and TSV. The sheet is
   named for what is exported, made a name Excel takes (31 characters, none
   of `[]:*?/\`, no apostrophe at either end), or Sheet1.

6. **Excel is offered after TSV** in the export's formats, as `.xlsx`. The
   clipboard's Copy As names its formats and does not offer it.

## Consequences

- A value Excel would round is text in the workbook: exact, but not a
  number Excel can add up.
- An instant loses its zone in the workbook, as Excel has none.
