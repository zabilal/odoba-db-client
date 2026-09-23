# ADR-0140: What is selected, added up

**Status:** Accepted · **Date:** 2026-09-23
**Tasks:** T3.29 · **Requirements:** FR-3.15, FR-3.14, NFR-P11
**Packages:** `internal/app`, `internal/source`, `internal/ui/shell`

## Context

FR-3.15 asks for an aggregate footer per column — sum, average, count,
smallest and largest — over the selection or the full set. A spreadsheet
answers this the moment a selection changes, and that is the behaviour
people bring with them.

## Decisions

1. **The selection's figures are arithmetic over what is on the screen.**
   They ask the server nothing, which is what lets them follow a selection
   down a column without anybody waiting.

2. **Only the rows the grid already has.** A bar that asked for a row it did
   not hold would queue a fetch for every row of a selection somebody
   dragged to the end of a large table, which is the unbounded read NFR-P11
   forbids. It looks at no more than fifty thousand rows either, because
   walking to the last row of a table two thousand million long is not
   following a selection.

3. **It says how many rows it looked at when it did not look at them all.**
   A total over half a column is not the total, and a number presented as
   one would be worse than no number.

4. **Cells, values and numbers are three counts, and they are told apart.**
   A column of dates has cells and no numbers; a total over half the cells
   of a column is not the column's total. What is not a number is counted
   and not added.

5. **Only what applies is written.** A column of words has a count and no
   total: saying "sum 0" of one would be a number nobody's rows put there.

6. **The whole column is a separate asking.** It is the same question the
   statistics panel answers (ADR-0139), over the same rows, exactly — and
   it is a count on the server, which on a large table is a wait. So it is
   a button rather than something that happens, and the answer says it came
   from the server and how long it took.

7. **The total joined the figures a column can be measured for.** A column
   with an average has a total and a column without has neither, so the two
   are asked for together and read together.

8. **Neither an infinity nor a not-a-number is added up.** A total that came
   back as "NaN" because one row held one would say nothing about the rest.
   A decimal is read from the text it travels in, so a column of money adds
   up.

## Consequences

- The bar is shown when it is asked for and stays until it is dismissed,
  like the detail panel. It follows the selection while it is there.
- Nothing is remembered between tabs, and nothing is aggregated across
  columns: a selection spanning two columns adds up every selected cell
  together, which is what a spreadsheet does and what somebody selecting two
  columns of numbers means.
- The exact answer needs a connection that measures columns. Where one does
  not, the selection's own figures are there as ever and the whole column is
  not offered.
