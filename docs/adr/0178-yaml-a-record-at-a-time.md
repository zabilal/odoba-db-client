# ADR-0178: YAML, a record at a time

**Status:** Accepted · **Date:** 2026-10-12
**Tasks:** T5.17 · **Requirements:** FR-10.10, FR-10.3, FR-10.4
**Packages:** `internal/export`, `internal/transfer`, `internal/cli`, `internal/ui/shell`

## Context

T5.17 names three file formats: Parquet, DBF and YAML. This is the first, in
both directions, and the one whose difficulty is not where it looks. Writing
YAML that reads back is a matter of care. Reading it without holding the whole
file is a matter of YAML not having been designed to be read that way.

## Decisions

1. **Export writes YAML by hand, and only the forms that cannot be misread.** A
   list of mappings, a row to a mapping. By hand for the reason every writer in
   `export` is: a row is written and forgotten, and a library that builds a
   document builds all of it (FR-10.3).

   What keeps a hand-written emitter honest is refusing to be clever. A string
   is always in double quotes, written as JSON writes one, which is YAML's own
   double-quoted form — so `no`, `1.0` and `2026-03-04` stay text whoever reads
   the file, rather than a boolean, a number and a date. A key is plain only
   when it is a word that is not one YAML reads as something else. A number is
   plain, an exact one with every digit, and a whole float keeps a point so it
   does not come back an integer. What is made of other values is written as
   JSON, which YAML reads as a flow collection. Bytes are `!!binary`. And the
   three numbers JSON cannot write are written as YAML's own: `.nan`, `.inf`,
   `-.inf` — the one place this format says more than JSON does.

2. **Import cuts the file into records and parses each alone.** A YAML parser
   reads a document, and a list of a million records is one document. So the
   file is cut at the lines where a record begins — a dash in the first column —
   and each piece is handed to the parser by itself. That is exactly right for a
   list in block style, which is how every tool writes one and the only style in
   which a record can begin a line: what a record holds is indented under it, so
   nothing else puts a dash there. A stream of documents that are each a mapping
   is cut at its marks the same way.

   Two things follow, and both are said in `docs/KNOWN-DEFECTS.md`. A list in
   flow style, in brackets, has no such lines and is read whole. And an alias
   reaches no further than its own record: one that points into another record
   is refused, naming the anchor.

3. **The rules for values are JSON's.** The two are read into the same rows, so
   a number keeps its digits (`model.Decimal`), a nested mapping or sequence is
   JSON, null is NULL, and the columns are the keys of the first thousand
   records in the order first seen, with a later unknown key an error rather
   than a value dropped. What YAML has that JSON has not is kept as what it is:
   bytes as bytes, infinity as itself. A number written another way — in
   hexadecimal, say — is read through an integer where it is one, so that it
   keeps digits a float would lose.

4. **A key twice is refused.** A parser may take the last or the first. A row
   has one value to a column, and which is not this reader's to choose.

5. **YAML is told by its name or by how it begins, not by a dash.** `.yaml`,
   `.yml`, a document mark or a directive. A first line beginning with a dash is
   not enough: a CSV file may begin with one.

## Consequences

- `go.yaml.in/yaml/v3` is a direct dependency. It was already in the build.
- The mutation runner treats a mutation that hangs a test as one the tests
  noticed. It had no way to: one of these — a record cut before it begins —
  loops for ever, and the run died with it.
- DBF and Parquet are the rest of T5.17.
