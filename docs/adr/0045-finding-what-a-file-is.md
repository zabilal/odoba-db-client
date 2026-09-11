# ADR-0045: Finding what a file is

**Status:** Accepted · **Date:** 2026-09-11
**Tasks:** T2.18 · **Requirements:** FR-10.4 · **Packages:** `internal/transfer`

## Context

FR-10.4 asks that an import find a file's delimiter, encoding and header.
The readers take them as given (ADR-0044). None of the module's
dependencies decodes a text encoding other than UTF-8.

## Decisions

1. **`Detect` reads a file's name and its first 64 KiB** and returns the
   options to read it with, for an import to start from and a person to
   correct. It only reads; nothing is imported on a guess alone.

2. **The format:** a zip's signature, or a name ending `.xlsx`, is a
   workbook; text that opens with `[` is JSON, with `{` NDJSON, and
   anything else is delimited.

3. **The encoding:** a byte-order mark's, where the text has one; else
   UTF-16 where nearly every other byte is a zero, as ASCII is in UTF-16,
   and the other bytes' zeros are far fewer; else
   UTF-8 where the text reads as UTF-8, its last character allowed to be
   cut by the end of what was read; else Windows-1252, what a text that is
   not UTF-8 most often is: Excel's CSV on Windows. UTF-16 is decoded with
   the standard library, and Windows-1252 by a table of its 32 characters
   that Latin-1 does not have; no dependency is added. Every text format
   is read as UTF-8 with its byte-order mark dropped, JSON's too.

4. **The delimiter:** of tab, comma, semicolon and pipe, the one whose first
   20 records most often share a width of more than one field, ties going
   in that order. A text that none splits is one column. A semicolon or a
   pipe is carried in `Options.Comma`.

5. **The header:** each column votes. A first value of another kind than
   all the rest of its column (a word over numbers or dates), or of another
   length where two or more of the rest are words all of one length, votes
   for a header; one of the rest's kind and length votes against; a mixed
   column does not vote, and one value has no length to share. Where the
   votes are even, none cast among them, a first record of words alone,
   none repeated, is a header. A workbook's first rows are weighed the
   same way.

## Consequences

- A guess can be wrong: a file of names over names reads its first row as
  data. The options are there to be corrected.
- A text in another encoding (Shift-JIS, a Latin-1 other than
  Windows-1252's) reads as Windows-1252, and shows wrong characters.
