# ADR-0179: DBF, decided before the first record

**Status:** Accepted · **Date:** 2026-10-12
**Tasks:** T5.17 · **Requirements:** FR-10.10, FR-10.3, FR-10.4
**Packages:** `internal/export`, `internal/transfer`, `internal/cli`, `internal/ui/shell`

## Context

The second of T5.17's three formats. DBF is dBase's table file, and the file of
everything since that wrote one: FoxPro, Clipper, every shapefile's attributes.
It is forty years old and small — a header, a line a column, records of a fixed
width — and is read and written by hand here, as a workbook is (ADR-0044,
ADR-0055): there is nothing in it a library would do that a page does not.

Its age is the whole of its difficulty. It was designed for a table already on
disk, so everything about a file is decided before its first record.

## Decisions

1. **Export writes the rows twice.** The header says how many records there are
   and how wide each column is, in bytes, and every record is exactly that
   wide. A stream of rows knows neither until its last. So rows are spooled to a
   temporary file as they arrive, while each column's widest value, most decimal
   places and whether it is still a number are noted; and at the end the header
   is written and the spool is replayed at the widths that turned out to be
   needed. Memory does not grow with the rows (FR-10.3). Disk does, for as long
   as the export takes, and the file is removed however it ends — finished,
   refused, the rows failing, or stopped. The row writers gained one optional
   thing for that: a writer that holds something is told when it will not be
   finished.

2. **What a column is, is decided by what it held.** A column the source calls
   a number is a DBF number while every value in it can be written as one —
   digits, a sign, a point, at most twenty characters. The first value that
   cannot be — a longer exact number, an exponent, a NaN — makes it text, with
   every digit kept. A number with some digits gone is the one thing this will
   not write. A date column is a DBF date, and a boolean one a DBF logical,
   while that is what they hold; a column of numbers that held nothing at all is
   text of width one.

   A number is fitted by its widest whole part and its most decimal places,
   which may be two different rows'.

3. **Nothing is cut to fit.** Text is at most 254 bytes in the bytes it is
   written in. Longer text is refused, naming the column, with nothing written:
   DBF's own answer is a memo in a second file, and this writes one file. Names
   are the exception that cannot be avoided — ten capital letters, digits and
   underscores — and are made to fit, and made different where fitting made
   them the same.

4. **Text is written in the code page a reader will assume, where it can be.**
   Plain ASCII says nothing about its code page and needs to say nothing. Text
   that Windows-1252 can write is written in it and the header says so, which
   is what the readers of this format expect. Text it cannot write — anything
   outside Western Europe — is written as UTF-8 with the code page left unsaid,
   which a modern reader reads and an old one does not. Which it will be is not
   known until all the text has been seen, which the two passes allow.

5. **Import refuses a table with a memo.** A memo column holds where its text
   is, in a second file. An import is given one file. Read anyway, the column
   would be empty in every row, and empty looks like data; so the table is
   refused, naming the column and saying what to do. Every other type dBase III
   and IV have is read, and FoxPro's integer, double, currency and datetime; a
   type that is not known is refused by its letter.

6. **A record is what the file says is one.** A record marked deleted is still
   in the file until somebody packs it, and is not a row; only a star marks one
   — a record marked any other way is a record. Records are read until the
   count the header gives, the mark that ends the file, or its end, whichever
   comes first: all three disagree in files that exist. A record cut short is
   an error naming its number.

7. **A blank field is NULL.** A number field left blank, or filled with the
   stars dBase writes for a number that did not fit, is no number. A text field
   of spaces is NULL, as an empty CSV field is; text keeps the space it begins
   with and loses the padding it ends with. A logical field of `?` is not yet
   said.

## What is not proven

- Files written by dBase, FoxPro or a GIS tool. The reader is tested against
  files put together by hand to the format's description and against what the
  writer writes; no file from another program is in the suite.

## Consequences

- No dependency.
- Parquet is the last of T5.17.
