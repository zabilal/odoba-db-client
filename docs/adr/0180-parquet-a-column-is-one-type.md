# ADR-0180: Parquet, where a column is one type

**Status:** Accepted · **Date:** 2026-10-12
**Tasks:** T5.17 · **Requirements:** FR-10.10, FR-10.3, FR-10.4
**Packages:** `internal/export`, `internal/transfer`, `internal/cli`, `internal/ui/shell`

## Context

The last of T5.17's three formats, and the opposite of the other two. YAML and
DBF are old or loose and were written by hand (ADR-0178, ADR-0179). Parquet is
neither: columns kept apart, each in one of a dozen encodings under one of half
a dozen compressions, described at the end of the file in a serialisation of
its own. A reader of that written by hand would be a library, less tested.

It is also the first format here in which the file says what it holds. Every
other import guesses a column's type from its values, and every other export
writes a value as whatever it happens to be.

## Decisions

1. **Through `parquet-go/parquet-go`, not Arrow.** It is pure Go, adds four
   modules that were not already here, and asks for no newer Go. The Arrow
   library reads the same files and brings sixty-eight modules with it. The
   stripped binary went from 96.9 MB to 100.8, under a ceiling of 120.

2. **Export gives each column the type its source says it has.** A whole number
   is `INT64`, a float `DOUBLE`, a date `DATE`, bytes `BYTE_ARRAY`, JSON and
   arrays `JSON`. A timestamp says whether it is a moment or a wall clock, as
   the column does, and is counted in microseconds — which reach every year a
   database keeps — unless the column keeps more than six places, when it is
   counted in nanoseconds. An exact decimal whose digits and places are known is
   a `DECIMAL` of exactly those, in the fewest bytes that hold them. Everything
   else is text.

3. **A decimal of unknown precision is text, not a float.** PostgreSQL's bare
   `numeric` says nothing of how many digits it holds, and Parquet's decimal
   must. The choices are a float, which loses digits silently; a guess at the
   precision, which refuses a value later; or text, which keeps every digit and
   loses only the column's being a number. Text.

4. **A value that is not its column's type is refused.** This is the cost of
   the format, and it is new here: in CSV a column of integers that holds one
   word holds one word. A Parquet column's type is said before its first value
   and binds every one after. A row holding something else — text in an integer
   column, as SQLite allows; a moment finer than its column counts; a decimal
   with more places than its column has — stops the export, naming the row and
   the column and saying what the two were. It is not written as something it
   is not, and not left out. DBF answered the same question by spooling every
   row and deciding at the end (ADR-0179); that buys a file where this gives an
   error, at the price of writing everything twice, and Parquet's types are
   worth more kept than DBF's were.

5. **Columns stay in the order they came in.** The library's group of columns
   is a map and sorts them by name, which would hand a person back their table
   in the alphabet's order. The schema is built from a group that keeps a list,
   and rows are written as values already placed, not looked up by name.

6. **Every column may be absent.** A result's column is not known never to be
   NULL: the table's column may be, and an outer join makes it so. Declaring a
   column required and then meeting a NULL would be decision 4's refusal for no
   reason.

7. **Rows are written in groups, by count and by size.** A group is written and
   let go of at a hundred thousand rows or thirty-two megabytes of values,
   whichever comes first, so memory holds one group and not the file (FR-10.3)
   and a few very wide rows do not wait for a hundred thousand.

8. **Import reads each value as what its column says it is.** The library will
   read a row into a Go map, and in that reading an exact decimal becomes a
   float. So rows are read as the file's own values and each is converted by
   its column's logical type: a decimal from its integer or its bytes to its
   digits with the point where the column says; a date, a time of day and a
   timestamp in whichever unit; a UUID to its text; an unsigned integer past
   what `int64` holds to an exact number; the twelve-byte timestamp older
   writers used. A time of day that is not within a day is refused.

9. **A column with parts is JSON.** A list, a map or a group is not one value,
   and a row has JSON for that. This one path does go through the library's
   reading of a row, so an exact decimal *inside* such a column becomes a
   float; a decimal that is a column of its own does not.

10. **Nothing about a Parquet file is asked of a person.** It is found by its
    first four bytes, or its name. The import window offers no encoding, no
    delimiter, no header and no sheet for it, because the file says all of it.

11. **The offline rule names what it lets through.** NFR-D6 is held by a test
    that the packages which read and write files cannot reach `net/http`, by
    their import graph. The library's Brotli codec imports it: one file that
    offers a web server a writer that compresses a response the server is
    already writing. It dials nothing, and nothing here calls it, but an
    import graph cannot tell that from a package that does. The rule was not
    loosened to "may reach it". It now finds who imports `net/http` and
    fails unless that is the one package read and named, for that one import —
    so a second way in, or a different library's, fails as before. The
    stronger rule beside it is untouched: no file of this project's outside
    the few listed imports `net/http` at all.

## What is proven

- Against another implementation, both ways. A file ClickHouse 26.9 wrote
  through the Arrow library, compressed with Zstandard — integers of three
  widths, two decimals, a date, timestamps in two units, a list, a map, a
  tuple, a UUID, a NULL — is in the suite and read value for value. A file
  this wrote was read back by ClickHouse with every type and value as it went
  out, a thirty-digit decimal and a nanosecond among them; that was done once
  by hand and is not in the suite, which has no ClickHouse in its unit half.

## What is not proven

- That a value the writer refuses is rare in practice. The drivers' rows were
  not surveyed for columns whose values disagree with their declared class;
  SQLite's are known to be able to.
- Compressions other than Snappy and Zstandard on the way in. The library
  reads gzip, Brotli and LZ4 too; no file of those is in the suite.
- Files from Spark, pandas or DuckDB.

## Consequences

- One new direct dependency and three indirect.
- T5.17 is done.
